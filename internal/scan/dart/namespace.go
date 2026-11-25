package dart

import (
	"bytes"
	"maps"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/qxbao/bgraph/internal/model"
)

func namespace(src []byte, lx Lexed, refs []model.ImportRef) *model.DartScan {
	if !utf8.Valid(src) || bytes.ContainsAny(src, "\u0085\u2028\u2029") || bytes.Count(src, []byte("\r")) != bytes.Count(src, []byte("\r\n")) {
		return nil
	}
	tokens := lx.Segments[0]
	if !bytes.Contains(src, []byte("@dart")) && !bytes.Contains(src, []byte("#!")) {
		if exports := exportLibrary(src, tokens); len(exports) > 0 {
			return &model.DartScan{Exports: exports}
		}
		if names := standaloneLibrary(src, tokens); len(names) > 0 {
			return &model.DartScan{Standalone: names}
		}
	}
	if !slices.ContainsFunc(refs, func(ref model.ImportRef) bool { return ref.Kind == model.ImportDartImport }) {
		return nil
	}
	// Parts and augmentations may use a name outside the importing file. Dynamically loaded code can observe the
	// libraries already loaded by the importer. Keep all the re-export edges for these consumers.
	for _, ref := range refs {
		if ref.Kind == model.ImportDartPart || ref.Kind == model.ImportDartLoad {
			return nil
		}
	}
	words := map[string]struct{}{}
	addWord := func(word string) {
		if len(word) == utf8.RuneCountInString(word) {
			words[word] = struct{}{}
			return
		}
		// The lexer retains non-ASCII bytes in word tokens. The Dart compiler can end a shorthand interpolation
		// before such a character (for example, $name🚀). Every ASCII run is a possible name from a standalone leaf.
		for _, part := range strings.FieldsFunc(word, func(r rune) bool { return r >= utf8.RuneSelf }) {
			words[part] = struct{}{}
		}
	}
	for _, seg := range lx.Segments {
		for _, t := range seg {
			if t.Kind != Word {
				continue
			}
			if t.Text == "part" || t.Text == "augment" {
				return nil
			}
			addWord(t.Text)
		}
	}
	for _, name := range lx.InterpolatedNames {
		addWord(name)
	}
	return &model.DartScan{Words: slices.Sorted(maps.Keys(words))}
}

// libraryBody recognizes the optional library directive. Metadata and other directives are left to the callers.
func libraryBody(ts []Token) []Token {
	if !tokenIs(ts, 0, Word, "library") {
		return ts
	}
	if len(ts) < 2 || !identifier(ts[1]) {
		return nil // the unnamed directive depends on the package's Dart language version
	}
	i := 2
	for tokenIs(ts, i, Punct, ".") && i+1 < len(ts) && identifier(ts[i+1]) {
		i += 2
	}
	if !tokenIs(ts, i, Punct, ";") {
		return nil
	}
	return ts[i+1:]
}

func exportLibrary(src []byte, ts []Token) []model.DartExport {
	var exports []model.DartExport
	for ts = libraryBody(ts); len(ts) > 0; {
		if len(ts) < 3 || !tokenIs(ts, 0, Word, "export") || !plainString(src, ts[1]) || !exportURI(ts[1].Text) {
			return nil
		}
		ex := model.DartExport{URI: ts[1].Text}
		ts = ts[2:]
		if tokenIs(ts, 0, Word, "show") || tokenIs(ts, 0, Word, "hide") {
			ex.Hide = ts[0].Text == "hide"
			for {
				if len(ts) < 2 || !identifier(ts[1]) {
					return nil
				}
				ex.Names = append(ex.Names, ts[1].Text)
				ts = ts[2:]
				if !tokenIs(ts, 0, Punct, ",") {
					break
				}
			}
		}
		if !tokenIs(ts, 0, Punct, ";") {
			return nil
		}
		ts = ts[1:]
		exports = append(exports, ex)
	}
	return exports
}

func exportURI(uri string) bool {
	file := strings.TrimPrefix(uri, "package:")
	if !strings.HasSuffix(file, ".dart") {
		return false
	}
	for i := 0; i < len(file); i++ {
		c := file[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '/' || c == '.') {
			return false
		}
	}
	return true
}

// standaloneLibrary only accepts declarations whose compilation can be checked locally. Even an unused re-export
// is compiler input, so a general Dart declaration (or any other directive) must keep the ordinary file dependency.
func standaloneLibrary(src []byte, ts []Token) []string {
	var names []string
	seen := map[string]bool{}
	for ts = libraryBody(ts); len(ts) > 0; {
		var name Token
		switch {
		case tokenIs(ts, 0, Word, "class") && tokenIs(ts, 2, Punct, "{") && tokenIs(ts, 3, Punct, "}"):
			name, ts = ts[1], ts[4:]
		case len(ts) >= 6 && tokenIs(ts, 0, Word, "const") && ts[1].Kind == Word && tokenIs(ts, 3, Punct, "="):
			name = ts[2]
			ts = literalEnd(src, ts[1].Text, ts[4:])
			if !tokenIs(ts, 0, Punct, ";") {
				return nil
			}
			ts = ts[1:]
		case len(ts) >= 8 && ts[0].Kind == Word && tokenIs(ts, 2, Punct, "(") && tokenIs(ts, 3, Punct, ")"):
			name = ts[1]
			kind, body := ts[0].Text, ts[4:]
			block := tokenIs(body, 0, Punct, "{") && tokenIs(body, 1, Word, "return")
			if !block && !(tokenIs(body, 0, Punct, "=") && tokenIs(body, 1, Punct, ">") && body[0].Start+1 == body[1].Start) {
				return nil
			}
			ts = literalEnd(src, kind, body[2:])
			if !tokenIs(ts, 0, Punct, ";") {
				return nil
			}
			ts = ts[1:]
			if block {
				if !tokenIs(ts, 0, Punct, "}") {
					return nil
				}
				ts = ts[1:]
			}
		default:
			return nil
		}
		if !identifier(name) || seen[name.Text] {
			return nil
		}
		seen[name.Text] = true
		if !strings.HasPrefix(name.Text, "_") {
			names = append(names, name.Text)
		}
	}
	return names
}

func literalEnd(src []byte, kind string, ts []Token) []Token {
	if len(ts) == 0 {
		return nil
	}
	t := ts[0]
	switch kind {
	case "bool":
		if tokenIs(ts, 0, Word, "true") || tokenIs(ts, 0, Word, "false") {
			return ts[1:]
		}
	case "int", "double":
		if t.Kind != Number {
			return nil
		}
		i, frac, dot := strings.Cut(t.Text, ".")
		if decimal32(i) && (!dot || kind == "double" && decimal32(frac)) {
			return ts[1:]
		}
	case "String":
		if plainString(src, t) {
			return ts[1:]
		}
	}
	return nil
}

func plainString(src []byte, t Token) bool {
	if t.Kind != String || t.Interp || strings.ContainsAny(t.Text, "$\\\x00\r\n") {
		return false
	}
	q, end := src[t.Start], t.Start+1+len(t.Text)
	return (q == '\'' || q == '"') && end < len(src) && src[end] == q && string(src[t.Start+1:end]) == t.Text &&
		!(t.Start+2 < len(src) && src[t.Start+1] == q && src[t.Start+2] == q)
}

func decimal32(s string) bool {
	_, err := strconv.ParseUint(s, 10, 31)
	return err == nil
}

func tokenIs(ts []Token, i int, kind Kind, text string) bool {
	return i < len(ts) && ts[i].Kind == kind && ts[i].Text == text
}

func identifier(t Token) bool {
	if t.Kind != Word || t.Text == "_" || strings.Contains(reservedNames, " "+t.Text+" ") {
		return false
	}
	for i := 0; i < len(t.Text); i++ {
		c := t.Text[i]
		if c >= 0x80 || c == '$' {
			return false
		}
	}
	return true
}

// Include contextual keywords and core type names so a standalone declaration cannot shadow a scalar type.
const reservedNames = " abstract as assert async augment await base bool break case catch class const continue covariant " +
	"default deferred do double dynamic else enum export extends extension external factory false final finally for " +
	"Function get hide if implements import in int interface is late library mixin native new null Null Object of on " +
	"operator part required rethrow return sealed set show static String super switch sync this throw true try type " +
	"typedef var void when while with yield Never Record "
