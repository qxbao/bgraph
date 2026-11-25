package dart

import "strings"

// Kind is a token kind.
type Kind uint8

const (
	Word   Kind = iota + 1 // identifier or keyword
	String                 // a string literal (one part of a run of adjacent literals)
	Punct                  // any other character (one per token)
	Number
)

// Token is one lexical token. For a String, Text is the literal's value with interpolations removed, Interp says whether it
// had any, Head is the value before the first interpolation and Tail the value after the last one (both equal Text when
// Interp is false).
type Token struct {
	Kind   Kind
	Text   string
	Line   int
	Start  int // byte offset of the token in the source
	Interp bool
	Head   string
	Tail   string
}

// maxDepth bounds the nesting of string interpolations and block comments.
const maxDepth = 200

// MaxFileSize: larger files are not lexed (uncertain).
const MaxFileSize = 4 << 20

// Lexed is the result of lexing one file: the top-level tokens and, separately, the tokens of every `${...}` interpolation
// (each its own sequence, so that an expression inside a string does not interleave with the enclosing argument list).
type Lexed struct {
	Segments [][]Token
	// InterpolatedNames are the identifiers of $name interpolations (unlike ${...}, they have no token segment).
	InterpolatedNames []string
	// Problem is non-empty when the lexer could not finish (unterminated string, comment or interpolation, too deep).
	Problem string
}

type lexer struct {
	src     []byte
	i       int
	line    int
	out     *Lexed
	problem string
}

// Lex splits Dart source into tokens.
func Lex(src []byte) Lexed {
	out := Lexed{}
	if len(src) > MaxFileSize {
		out.Problem = "file_too_large"
		out.Segments = [][]Token{nil}
		return out
	}
	lx := &lexer{src: src, line: 1, out: &out}
	out.Segments = append(out.Segments, nil)
	// BOM and script tag.
	if strings.HasPrefix(string(src), "\xef\xbb\xbf") {
		lx.i = 3
	}
	if strings.HasPrefix(string(src[lx.i:]), "#!") {
		for lx.i < len(src) && src[lx.i] != '\n' {
			lx.i++
		}
	}
	lx.tokens(0, false, 0)
	out.Problem = lx.problem
	return out
}

func (lx *lexer) fail(why string) {
	if lx.problem == "" {
		lx.problem = why
	}
}

func identStart(c byte) bool {
	return c == '_' || c == '$' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 0x80
}

func identPart(c byte) bool { return identStart(c) || c >= '0' && c <= '9' }

// tokens lexes into segment seg until EOF or, when inInterp, the '}' that closes the interpolation (consumed).
func (lx *lexer) tokens(seg int, inInterp bool, depth int) {
	if depth > maxDepth {
		lx.fail("nesting_too_deep")
		lx.i = len(lx.src)
		return
	}
	braces := 0
	src := lx.src
	for lx.i < len(src) && lx.problem == "" {
		c := src[lx.i]
		switch {
		case c == '\n':
			lx.line++
			lx.i++
		case c == ' ' || c == '\t' || c == '\r' || c == '\f' || c == '\v':
			lx.i++
		case c == '/' && lx.i+1 < len(src) && src[lx.i+1] == '/':
			for lx.i < len(src) && src[lx.i] != '\n' {
				lx.i++
			}
		case c == '/' && lx.i+1 < len(src) && src[lx.i+1] == '*':
			lx.blockComment()
		case c == '\'' || c == '"':
			lx.str(seg, false, depth)
		case (c == 'r' || c == 'R') && lx.i+1 < len(src) && (src[lx.i+1] == '\'' || src[lx.i+1] == '"') && !lx.prevIdentChar():
			lx.i++
			lx.str(seg, true, depth)
		case identStart(c):
			start := lx.i
			for lx.i < len(src) && identPart(src[lx.i]) {
				lx.i++
			}
			lx.emit(seg, Token{Kind: Word, Text: string(src[start:lx.i]), Line: lx.line, Start: start})
		case c >= '0' && c <= '9' || c == '.' && lx.i+1 < len(src) && src[lx.i+1] >= '0' && src[lx.i+1] <= '9':
			start := lx.i
			lx.i++
			for lx.i < len(src) {
				d := src[lx.i]
				if identPart(d) && d != '$' || d == '.' && lx.i+1 < len(src) && src[lx.i+1] >= '0' && src[lx.i+1] <= '9' {
					lx.i++
					continue
				}
				if (d == '+' || d == '-') && (src[lx.i-1] == 'e' || src[lx.i-1] == 'E') && !strings.HasPrefix(string(src[start:lx.i]), "0x") &&
					!strings.HasPrefix(string(src[start:lx.i]), "0X") {
					lx.i++
					continue
				}
				break
			}
			lx.emit(seg, Token{Kind: Number, Text: string(src[start:lx.i]), Line: lx.line, Start: start})
		default:
			if inInterp {
				if c == '{' {
					braces++
				} else if c == '}' {
					if braces == 0 {
						lx.i++
						return
					}
					braces--
				}
			}
			lx.emit(seg, Token{Kind: Punct, Text: string(c), Line: lx.line, Start: lx.i})
			lx.i++
		}
	}
	if inInterp && lx.problem == "" {
		lx.fail("unterminated_interpolation")
	}
}

// prevIdentChar: the byte before the current position continues an identifier (so an 'r' here is part of a word, as in
// `bar'x'`, which is not valid Dart anyway but must not start a raw string).
func (lx *lexer) prevIdentChar() bool {
	return lx.i > 0 && identPart(lx.src[lx.i-1])
}

func (lx *lexer) emit(seg int, t Token) {
	lx.out.Segments[seg] = append(lx.out.Segments[seg], t)
}

// blockComment skips a (nestable) block comment starting at lx.i.
func (lx *lexer) blockComment() {
	src := lx.src
	depth := 0
	for lx.i < len(src) {
		switch {
		case src[lx.i] == '/' && lx.i+1 < len(src) && src[lx.i+1] == '*':
			depth++
			if depth > maxDepth {
				lx.fail("nesting_too_deep")
				lx.i = len(src)
				return
			}
			lx.i += 2
		case src[lx.i] == '*' && lx.i+1 < len(src) && src[lx.i+1] == '/':
			depth--
			lx.i += 2
			if depth == 0 {
				return
			}
		default:
			if src[lx.i] == '\n' {
				lx.line++
			}
			lx.i++
		}
	}
	lx.fail("unterminated_comment")
}

// str lexes a string literal whose opening quote is at lx.i.
func (lx *lexer) str(seg int, raw bool, depth int) {
	src := lx.src
	start := lx.i
	if raw {
		start--
	}
	line := lx.line
	q := src[lx.i]
	triple := lx.i+2 < len(src) && src[lx.i+1] == q && src[lx.i+2] == q
	if triple {
		lx.i += 3
	} else {
		lx.i++
	}
	var val, head strings.Builder
	interp := false
	tailStart := 0 // index in val where the text after the last interpolation starts
	for {
		if lx.i >= len(src) {
			lx.fail("unterminated_string")
			return
		}
		c := src[lx.i]
		switch {
		case c == q && (!triple || lx.i+2 < len(src) && src[lx.i+1] == q && src[lx.i+2] == q):
			if triple {
				lx.i += 3
			} else {
				lx.i++
			}
			v := val.String()
			t := Token{Kind: String, Text: v, Line: line, Start: start, Interp: interp, Head: v, Tail: v}
			if interp {
				t.Head, t.Tail = head.String(), v[tailStart:]
			}
			lx.emit(seg, t)
			return
		case c == '\n' && !triple:
			lx.fail("unterminated_string")
			return
		case c == '\\' && !raw:
			if lx.i+1 >= len(src) {
				lx.fail("unterminated_string")
				return
			}
			e := src[lx.i+1]
			lx.i += 2
			switch e {
			case 'n':
				val.WriteByte('\n')
			case 't':
				val.WriteByte('\t')
			case 'r':
				val.WriteByte('\r')
			case 'b', 'f', 'v':
				val.WriteByte(' ')
			case 'x', 'u':
				// The code point is not decoded: a path does not contain an escape, and an unknown character keeps the literal
				// from naming a file.
				val.WriteByte(0)
			case '\n':
				lx.line++
				val.WriteByte('\n')
			default:
				val.WriteByte(e)
			}
		case c == '$' && !raw && lx.i+1 < len(src) && src[lx.i+1] == '{':
			if !interp {
				head.WriteString(val.String())
			}
			interp = true
			lx.i += 2
			lx.out.Segments = append(lx.out.Segments, nil)
			lx.tokens(len(lx.out.Segments)-1, true, depth+1)
			if lx.problem != "" {
				return
			}
			tailStart = val.Len()
		case c == '$' && !raw && lx.i+1 < len(src) && identStart(src[lx.i+1]) && src[lx.i+1] != '$':
			if !interp {
				head.WriteString(val.String())
			}
			interp = true
			lx.i++
			name := lx.i
			for lx.i < len(src) && identPart(src[lx.i]) && src[lx.i] != '$' {
				lx.i++
			}
			lx.out.InterpolatedNames = append(lx.out.InterpolatedNames, string(src[name:lx.i]))
			tailStart = val.Len()
		default:
			if c == '\n' {
				lx.line++
			}
			val.WriteByte(c)
			lx.i++
		}
	}
}
