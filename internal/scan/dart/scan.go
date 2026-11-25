// Package dart is bgraph's Dart scanner (docs/ecosystems/dart.md). It is a lexer (nested block comments, every string form:
// single, double and triple quotes, raw r'...' strings, $name and nested ${...} interpolation, adjacent literals) plus a
// token-level extractor, not a parser. The result does not depend on the file's path, so it is cached by content.
//
// Every reference is a model.ImportRef:
//
//   - import / export / part directives → dart_import / dart_export / dart_part with Spec = the URI as written. Every URI of a
//     conditional directive (`import 'a.dart' if (dart.library.io) 'b.dart'`) is its own ref with the same line. A deferred
//     import is an ordinary dart_import. `import augment 'x.dart'` → dart_part with Suffix "augment". `part of` is not reported.
//   - Isolate.spawnUri / loadUri (dart:mirrors) / spawnHybridUri → dart_load. Spec is the URI when the first argument is a string literal or
//     Uri.parse / Uri.file / Uri.directory of one (Suffix "file" for the last two: a file path, not a URI); otherwise the ref
//     is Dynamic with Spec = the static head of an interpolated literal (often empty). A tear-off without a call is Dynamic.
//     spawnHybridCode always loads dynamically because its string argument holds Dart code.
//   - Process.run / runSync / start and TestProcess.start → dart_process (Spec empty): the file runs a program.
//   - String literals that may name a path → dart_string: a literal without interpolation containing '/' or '.' (Spec its
//     value; "dart:" URIs, URLs and literals with control characters are dropped); an interpolated literal → Dynamic with Spec
//     = the text before the first interpolation and Suffix = the text after the last one, kept when either contains '/'.
//     A join(...) call (package:path's p.join, path.url.join...) whose leading arguments are literals adds the joined path
//     ("a/b", or Dynamic "a/b/" when a computed argument follows); trailing literal arguments after a computed one add a
//     Dynamic ref with an empty Spec and Suffix = "/" + the joined tail.
//
// Uncertain=true when the token stream cannot be trusted: an unterminated string, block comment or interpolation, nesting
// deeper than the lexer's limit, a directive whose URI is interpolated, or a file larger than MaxFileSize.
package dart

import (
	"strings"

	"github.com/qxbao/bgraph/internal/model"
)

// tok is a token after adjacent string literals have been merged into one.
type tok struct {
	Token
	used bool // a string consumed by a directive
}

// Scan scans one Dart file.
func Scan(src []byte) model.ScanResult {
	lx := Lex(src)
	out := model.ScanResult{Imports: []model.ImportRef{}}
	if lx.Problem != "" {
		out.Uncertain, out.UncertainReason = true, lx.Problem
	}
	for _, seg := range lx.Segments {
		ts := merge(seg)
		extract(ts, &out)
	}
	if !out.Uncertain {
		out.Dart = namespace(src, lx, out.Imports)
	}
	return out
}

// merge joins runs of adjacent string literals ('a' 'b' is the string "ab").
func merge(seg []Token) []tok {
	out := make([]tok, 0, len(seg))
	for i := 0; i < len(seg); i++ {
		t := seg[i]
		if t.Kind != String || i+1 >= len(seg) || seg[i+1].Kind != String {
			out = append(out, tok{Token: t})
			continue
		}
		m := t
		for i+1 < len(seg) && seg[i+1].Kind == String {
			n := seg[i+1]
			i++
			switch {
			case !m.Interp && !n.Interp:
				m.Head, m.Tail = m.Text+n.Text, m.Text+n.Text
			case !m.Interp:
				m.Head, m.Tail = m.Text+n.Head, n.Tail
			case !n.Interp:
				m.Tail += n.Text
			default:
				m.Tail = n.Tail
			}
			m.Text += n.Text
			m.Interp = m.Interp || n.Interp
		}
		out = append(out, tok{Token: m})
	}
	return out
}

func isWord(ts []tok, i int, w string) bool {
	return i >= 0 && i < len(ts) && ts[i].Kind == Word && ts[i].Text == w
}

func isPunct(ts []tok, i int, p string) bool {
	return i >= 0 && i < len(ts) && ts[i].Kind == Punct && ts[i].Text == p
}

func isString(ts []tok, i int) bool { return i >= 0 && i < len(ts) && ts[i].Kind == String }

// closing returns the index of the bracket closing the one at open (or len(ts) when unbalanced).
func closing(ts []tok, open int) int {
	depth := 0
	for k := open; k < len(ts); k++ {
		if ts[k].Kind != Punct {
			continue
		}
		switch ts[k].Text {
		case "(", "[", "{":
			depth++
		case ")", "]", "}":
			depth--
			if depth == 0 {
				return k
			}
		}
	}
	return len(ts)
}

// args splits the argument list of the call whose '(' is at open into argument token ranges [start, end).
func args(ts []tok, open int) [][2]int {
	end := closing(ts, open)
	var out [][2]int
	depth := 0
	start := open + 1
	for k := open + 1; k < end; k++ {
		if ts[k].Kind != Punct {
			continue
		}
		switch ts[k].Text {
		case "(", "[", "{":
			depth++
		case ")", "]", "}":
			depth--
		case ",":
			if depth == 0 {
				out = append(out, [2]int{start, k})
				start = k + 1
			}
		}
	}
	if start < end {
		out = append(out, [2]int{start, end})
	}
	return out
}

func extract(ts []tok, out *model.ScanResult) {
	add := func(r model.ImportRef) { out.Imports = append(out.Imports, r) }
	uncertain := func(why string) {
		if !out.Uncertain {
			out.Uncertain, out.UncertainReason = true, why
		}
	}
	for i := range ts {
		t := &ts[i]
		if t.Kind != Word {
			continue
		}
		switch t.Text {
		case "import", "export", "part":
			kind := model.ImportDartImport
			switch t.Text {
			case "export":
				kind = model.ImportDartExport
			case "part":
				kind = model.ImportDartPart
			}
			j := i + 1
			suffix := ""
			if t.Text == "import" && isWord(ts, j, "augment") && isString(ts, j+1) {
				kind, suffix = model.ImportDartPart, "augment"
				j++
			}
			if t.Text == "part" && isWord(ts, j, "of") {
				// The library a part belongs to depends on the part, not the other way round.
				if isString(ts, j+1) {
					ts[j+1].used = true
				}
				continue
			}
			if !isString(ts, j) {
				continue
			}
			uris := []int{j}
			for k := j + 1; isWord(ts, k, "if") && isPunct(ts, k+1, "("); {
				c := closing(ts, k+1)
				if !isString(ts, c+1) {
					break
				}
				uris = append(uris, c+1)
				k = c + 2
			}
			for _, u := range uris {
				ts[u].used = true
				if ts[u].Interp {
					uncertain("interpolated_uri")
					continue
				}
				add(model.ImportRef{Kind: kind, Spec: ts[u].Text, Suffix: suffix, Line: t.Line})
			}
		case "spawnUri", "loadUri", "spawnHybridUri":
			add(loadRef(ts, i))
		case "spawnHybridCode":
			add(model.ImportRef{Kind: model.ImportDartLoad, Line: t.Line, Dynamic: true})
		case "run", "runSync", "start":
			if isPunct(ts, i-1, ".") && (isWord(ts, i-2, "Process") || t.Text == "start" && isWord(ts, i-2, "TestProcess")) {
				add(model.ImportRef{Kind: model.ImportDartProcess, Line: t.Line})
			}
		case "join":
			if isPunct(ts, i-1, ".") && isPunct(ts, i+1, "(") {
				joinRefs(ts, i, add)
			}
		}
	}
	for i := range ts {
		t := ts[i]
		if t.Kind != String || t.used {
			continue
		}
		if !t.Interp {
			if pathLike(t.Text) {
				add(model.ImportRef{Kind: model.ImportDartString, Spec: t.Text, Line: t.Line})
			}
			continue
		}
		if clean(t.Head) && clean(t.Tail) && (strings.Contains(t.Head, "/") || strings.Contains(strings.TrimPrefix(t.Tail, "/"), "/")) {
			add(model.ImportRef{Kind: model.ImportDartString, Spec: t.Head, Suffix: t.Tail, Dynamic: true, Line: t.Line})
		}
	}
}

// loadRef describes the spawnUri / loadUri site at i.
func loadRef(ts []tok, i int) model.ImportRef {
	r := model.ImportRef{Kind: model.ImportDartLoad, Line: ts[i].Line, Dynamic: true}
	if !isPunct(ts, i+1, "(") {
		return r // a tear-off: the URI is supplied elsewhere
	}
	as := args(ts, i+1)
	if len(as) == 0 {
		return r
	}
	a := ts[as[0][0]:as[0][1]]
	switch {
	case len(a) == 1 && a[0].Kind == String:
		if !a[0].Interp {
			r.Spec, r.Dynamic = a[0].Text, false
		} else {
			r.Spec = a[0].Head
		}
	case len(a) == 6 && isWord(a, 0, "Uri") && isPunct(a, 1, ".") && isPunct(a, 3, "(") && isString(a, 4) && isPunct(a, 5, ")"):
		switch a[2].Text {
		case "parse", "file", "directory":
			if a[4].Interp {
				r.Spec = a[4].Head
				break
			}
			r.Spec, r.Dynamic = a[4].Text, false
			if a[2].Text != "parse" {
				r.Suffix = "file"
			}
		}
	}
	if r.Spec == "" {
		r.Dynamic, r.Suffix = true, ""
	}
	return r
}

// joinRefs reports the literal parts of a join(...) call at i.
func joinRefs(ts []tok, i int, add func(model.ImportRef)) {
	as := args(ts, i+1)
	lit := func(a [2]int) (string, bool) {
		if a[1]-a[0] == 1 && ts[a[0]].Kind == String && !ts[a[0]].Interp {
			return ts[a[0]].Text, true
		}
		return "", false
	}
	var head []string
	k := 0
	for ; k < len(as); k++ {
		s, ok := lit(as[k])
		if !ok {
			break
		}
		head = append(head, s)
	}
	line := ts[i].Line
	if k == len(as) {
		if len(head) > 1 {
			if s := strings.Join(head, "/"); pathLike(s) {
				add(model.ImportRef{Kind: model.ImportDartString, Spec: s, Line: line})
			}
		}
		return
	}
	if len(head) > 0 {
		if s := strings.Join(head, "/") + "/"; clean(s) {
			add(model.ImportRef{Kind: model.ImportDartString, Spec: s, Dynamic: true, Line: line})
		}
	}
	var tail []string
	for j := len(as) - 1; j > k; j-- {
		s, ok := lit(as[j])
		if !ok {
			break
		}
		tail = append([]string{s}, tail...)
	}
	if s := strings.Join(tail, "/"); len(tail) > 0 && strings.Contains(s, "/") && clean(s) {
		add(model.ImportRef{Kind: model.ImportDartString, Suffix: "/" + s, Dynamic: true, Line: line})
	}
}

// clean: no character that a repository path cannot contain in practice.
func clean(s string) bool {
	return len(s) <= 512 && !strings.ContainsAny(s, "\x00\n\r\t") && !strings.Contains(s, "://")
}

// pathLike: a literal that may name a file or directory.
func pathLike(s string) bool {
	if s == "" || s == "." || s == ".." || !clean(s) || !strings.ContainsAny(s, "/.") || strings.HasPrefix(s, "dart:") {
		return false
	}
	return true
}
