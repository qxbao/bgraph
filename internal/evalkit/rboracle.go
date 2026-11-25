package evalkit

import (
	"path"
	"regexp"
	"strings"
	"sync"

	"github.com/qxbao/bgraph/internal/tree"
)

// RbTestLoc is where one Ruby FAIL_TO_PASS name is defined, as far as a source scan can tell.
//
// The safe unit of the Ruby ecosystem is the test file and its unit ID is the repo-relative path, so Files are unit IDs directly
// (file-granular, like Python). Dataset name forms handled:
//
//	desc - ./a/b_spec.rb[1:2:3], a/b_spec.rb:12, a/b_test.rb       "path"      (the path names the file)
//	features/x.feature:70  Scenario: ...                            "cucumber"  (the scenario location names the feature file)
//	TestFoo#test_bar, Mod::TestFoo#test_bar, test_bar(TestFoo)      "minitest"  (method + class; Minitest/Test::Unit)
//	test_bar, test_bar[data label]                                  "method"    (bare method name; test-unit data label stripped)
//	TestFoo#test_: ctx ctx should do x.                             "shoulda"   (shoulda-context: class + contexts + should text)
//	test: ctx ctx should do x.                                      "shoulda"   (old shoulda: no class)
//	does x, Foo::Bar#baz does x, strings for y[label]               "desc"      (RSpec example / test-unit `test "..."` description,
//	                                                                             possibly prefixed by the describe/context texts)
//
// Methods are compared by a normalized key (every run of non-word characters becomes "_"), so `def test_x`, test-unit's
// `test "x"` (method "test: x") and the test/should DSLs of asciidoctor or ActiveSupport (`test_` + the sanitized text) meet.
// Asciidoctor's `context 'Foo bar'` blocks define classes `TestFooBar`, which are indexed as classes too.
//
// Required units for T1 are derived from Files of every located name (ambiguous names require all of them, and record whether
// requiring only PatchFiles removes a violation); for f2p_kind == "n2p" keep only names with InPatch; NotFound and NotUnit names are
// reported as unmapped, never silently dropped.
type RbTestLoc struct {
	Name       string   `json:"name"`
	Form       string   `json:"form"`
	Class      string   `json:"class,omitempty"`
	Method     string   `json:"method,omitempty"`
	Files      []string `json:"files,omitempty"`       // candidate test files = unit IDs (sorted)
	PatchFiles []string `json:"patch_files,omitempty"` // candidates the test patch touches
	Helpers    []string `json:"helpers,omitempty"`     // non-unit files where the name was found (shared examples), replaced by their includers in Files
	Via        string   `json:"via,omitempty"`         // how the candidates were found (path, method, class, method+class, desc, composite, template, label, ...)
	Ambiguous  bool     `json:"ambiguous,omitempty"`
	NotFound   bool     `json:"not_found,omitempty"`
	NotUnit    bool     `json:"not_unit,omitempty"` // located only in a helper file with no test-file includers
	InPatch    bool     `json:"in_patch,omitempty"`
}

// rbExcludedDir: directories whose files are never test units (contract §1).
func rbExcludedDir(dir string) bool {
	for _, s := range strings.Split(dir, "/") {
		switch s {
		case "fixtures", "fixture", "vendor", "tmp", "node_modules", ".bundle":
			return true
		}
	}
	return false
}

// RbIsTestFile is the contract's test-file rule: a .rb file under a directory named spec or test whose base name is *_spec.rb,
// *_test.rb, test_*.rb or tc_*.rb, outside fixtures/vendor/tmp/node_modules/.bundle.
func RbIsTestFile(p string) bool {
	if !strings.HasSuffix(p, ".rb") {
		return false
	}
	dir, b := path.Split(p)
	dir = strings.TrimSuffix(dir, "/")
	if dir == "" || rbExcludedDir(dir) {
		return false
	}
	under := false
	for _, s := range strings.Split(dir, "/") {
		if s == "spec" || s == "test" {
			under = true
		}
	}
	return under && (strings.HasSuffix(b, "_spec.rb") || strings.HasSuffix(b, "_test.rb") || strings.HasPrefix(b, "test_") || strings.HasPrefix(b, "tc_"))
}

// rbScanned: the files the locator reads: every .rb file under spec/test (helpers and support files define shared examples and
// test DSLs) plus test units elsewhere.
func rbScanned(p string) bool {
	if !strings.HasSuffix(p, ".rb") {
		return false
	}
	dir := path.Dir(p)
	if rbExcludedDir(dir) {
		return false
	}
	for _, s := range strings.Split(dir, "/") {
		if s == "spec" || s == "test" {
			return true
		}
	}
	b := path.Base(p)
	return strings.HasSuffix(b, "_spec.rb") || strings.HasSuffix(b, "_test.rb")
}

// ---- name parsing ----

type rbName struct {
	form   string // path | cucumber | minitest | method | shoulda | desc
	path   string
	class  string
	method string
	desc   string
}

var (
	rbPathRe      = regexp.MustCompile(`(?:^|[\s(])(?:\./)?([\w./@+-]+\.(?:rb|feature))(?::\d+(?::\d+)*|\[[\d:,]+\])?(?:\s|$|\))`)
	rbClassMeth   = regexp.MustCompile(`^((?:[A-Z]\w*::)*[A-Z]\w*)(?:#|::|\.)(test(?:_|:).*)$`)
	rbMethClass   = regexp.MustCompile(`^(test\w*[?!]?(?:\[[^\]]*\])?)\s*\(((?:[A-Z]\w*::)*[A-Z]\w*)\)$`)
	rbBareMethod  = regexp.MustCompile(`^test_\w*[?!]?(?:\[[^\]]*\])?$`)
	rbLabelSuffix = regexp.MustCompile(`\[[^\[\]]*\]$`)
	rbNonWord     = regexp.MustCompile(`[^\p{L}\p{N}_]+`)
	rbKeySep      = regexp.MustCompile(`[^\p{L}\p{N}]+`)
	// What may separate adjacent literals that Ruby concatenates ('a ' 'b', or 'a ' \ newline 'b').
	rbLitJoin = regexp.MustCompile(`^[ \t]*(?:\\\r?\n[ \t]*)?`)
)

func parseRbName(name string) rbName {
	n := strings.TrimSpace(name)
	if m := rbPathRe.FindStringSubmatch(n); m != nil {
		p := strings.TrimPrefix(path.Clean(m[1]), "./")
		if strings.HasSuffix(p, ".feature") {
			return rbName{form: "cucumber", path: p, desc: n}
		}
		return rbName{form: "path", path: p, desc: n}
	}
	if m := rbClassMeth.FindStringSubmatch(n); m != nil {
		if d, ok := shouldaDesc(m[2]); ok {
			return rbName{form: "shoulda", class: m[1], desc: d}
		}
		return rbName{form: "minitest", class: m[1], method: m[2]}
	}
	if m := rbMethClass.FindStringSubmatch(n); m != nil {
		return rbName{form: "minitest", class: m[2], method: m[1]}
	}
	if d, ok := shouldaDesc(n); ok {
		return rbName{form: "shoulda", desc: d}
	}
	if rbBareMethod.MatchString(n) {
		return rbName{form: "method", method: n}
	}
	return rbName{form: "desc", desc: n}
}

// shouldaDesc: shoulda-context names its tests "test: <contexts> should <text>. " (newer versions "test_: ...").
func shouldaDesc(m string) (string, bool) {
	for _, p := range []string{"test_: ", "test: "} {
		if strings.HasPrefix(m, p) {
			d := strings.TrimSpace(m[len(p):])
			return strings.TrimSpace(strings.TrimSuffix(d, ".")), true
		}
	}
	return "", false
}

// rbMethodKey normalizes a test method name or a test DSL description: runs of characters other than letters and digits
// (underscores included) become one "_", trimmed. `test ':backend option'` defines test_backend_option with one helper version
// and test__backend_option with another; both meet here.
func rbMethodKey(s string) string {
	return strings.Trim(rbKeySep.ReplaceAllString(s, "_"), "_")
}

// ---- file scan ----

// rbLit is a string literal; an interpolated literal keeps its static pieces (len > 1).
type rbLit []string

func (l rbLit) text() string { return strings.Join(l, "") }

// re matches the whole literal, interpolations matching anything non-empty.
func (l rbLit) re(prefix bool) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("^")
	if prefix {
		b.WriteString("(.*?)")
	}
	for i, p := range l {
		if i > 0 {
			b.WriteString(".+?")
		}
		b.WriteString(regexp.QuoteMeta(p))
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		return nil
	}
	return re
}

type rbFileScan struct {
	classes    []string // class names (last segment) incl. asciidoctor context classes
	methods    []string // normalized method keys
	methodTpls []rbLit  // test/should literals with interpolation (keys built from pieces)
	examples   []string // example descriptions (it/specify/test; should literals as "should X")
	exTpls     []rbLit
	groups     []string // describe/context/... texts and constant arguments
	sharedDefs []string // shared_examples names
	sharedUses []string // it_behaves_like / include_examples names
}

var (
	rbCallRe  = regexp.MustCompile(`(?m)(?:^|[\s;({|])((?:RSpec\.)?(?:describe|context|sub_test_case|feature|shared_examples_for|shared_examples|shared_context|it_behaves_like|it_should_behave_like|include_examples|include_context|it|specify|example|scenario|test|should|xit|fit|xspecify|xdescribe|fdescribe|xcontext|fcontext|focus))\b[ \t]*\(?[ \t]*`)
	rbDefRe   = regexp.MustCompile(`(?m)^\s*def\s+(?:self\.)?(test\w*[?!]?)`)
	rbClassRe = regexp.MustCompile(`(?m)^\s*class\s+((?:[A-Z]\w*::)*[A-Z]\w*)`)
	rbConstRe = regexp.MustCompile(`^(?:::)?[A-Z]\w*(?:::[A-Z]\w*)*`)
)

func scanRbTest(src []byte) *rbFileScan {
	s := string(src)
	fs := &rbFileScan{}
	for _, m := range rbDefRe.FindAllStringSubmatch(s, -1) {
		fs.methods = append(fs.methods, rbMethodKey(m[1]))
	}
	for _, m := range rbClassRe.FindAllStringSubmatch(s, -1) {
		c := m[1]
		if i := strings.LastIndex(c, "::"); i >= 0 {
			c = c[i+2:]
		}
		fs.classes = append(fs.classes, c)
	}
	for _, m := range rbCallRe.FindAllStringSubmatchIndex(s, -1) {
		kw := strings.TrimPrefix(s[m[2]:m[3]], "RSpec.")
		rest := s[m[1]:]
		lit, ok := readRbLitConcat(rest)
		if !ok {
			// A constant group argument (describe Foo::Bar, "#baz"), possibly followed by a string literal.
			c := rbConstRe.FindString(rest)
			if c == "" || !rbIsGroup(kw) {
				continue
			}
			fs.groups = append(fs.groups, strings.TrimPrefix(c, "::"))
			after := strings.TrimLeft(rest[len(c):], " \t")
			if strings.HasPrefix(after, ",") {
				if l2, ok2 := readRbLit(strings.TrimLeft(after[1:], " \t")); ok2 && len(l2) == 1 {
					fs.groups = append(fs.groups, l2[0], strings.TrimPrefix(c, "::")+l2[0], strings.TrimPrefix(c, "::")+" "+l2[0])
				}
			}
			continue
		}
		switch {
		case rbIsGroup(kw):
			if len(lit) == 1 {
				fs.groups = append(fs.groups, lit[0])
				fs.classes = append(fs.classes, rbContextClass(lit[0]))
			}
			if strings.HasPrefix(kw, "shared_") {
				fs.sharedDefs = append(fs.sharedDefs, lit.text())
			}
		case kw == "it_behaves_like" || kw == "it_should_behave_like" || kw == "include_examples" || kw == "include_context":
			fs.sharedUses = append(fs.sharedUses, lit.text())
		default: // examples
			ex := lit
			if kw == "should" {
				ex = append(rbLit{"should " + lit[0]}, lit[1:]...)
			}
			if len(ex) == 1 {
				fs.examples = append(fs.examples, ex[0])
			} else {
				fs.exTpls = append(fs.exTpls, ex)
			}
			if kw == "test" || kw == "should" {
				if len(lit) == 1 {
					fs.methods = append(fs.methods, rbMethodKey("test_"+lit[0]), rbMethodKey("test_"+ex[0]))
				} else {
					fs.methodTpls = append(fs.methodTpls, lit, ex)
				}
			}
		}
	}
	return fs
}

func rbIsGroup(kw string) bool {
	switch kw {
	case "describe", "context", "sub_test_case", "feature", "shared_examples_for", "shared_examples", "shared_context",
		"xdescribe", "fdescribe", "xcontext", "fcontext":
		return true
	}
	return false
}

// rbContextClass is the class asciidoctor's test helper creates for `context name`: "Test" + the sanitized words, capitalized.
func rbContextClass(name string) string {
	var b strings.Builder
	b.WriteString("Test")
	for _, w := range strings.Fields(rbNonWord.ReplaceAllString(name, " ")) {
		b.WriteString(strings.ToUpper(w[:1]) + w[1:])
	}
	return b.String()
}

// readRbLitConcat reads a string literal and the quoted literals adjacent to it (Ruby concatenates them at parse time).
func readRbLitConcat(s string) (rbLit, bool) {
	lit, ok := readRbLit(s)
	if !ok {
		return nil, false
	}
	for {
		n := rbLitEnd(s)
		if n < 0 {
			return lit, true
		}
		s = s[n:]
		s = s[len(rbLitJoin.FindString(s)):]
		if rbLitEnd(s) < 0 {
			return lit, true
		}
		next, ok := readRbLit(s)
		if !ok {
			return lit, true
		}
		lit[len(lit)-1] += next[0]
		lit = append(lit, next[1:]...)
	}
}

// rbLitEnd is the length of the quoted literal ('...' or "...") at the start of s, or -1.
func rbLitEnd(s string) int {
	if s == "" || (s[0] != '\'' && s[0] != '"') {
		return -1
	}
	q := s[0]
	for i := 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case q:
			return i + 1
		}
	}
	return -1
}

// readRbLit reads a string literal at the start of s: '...', "...", %q(...), %Q[...], %(...). Interpolations (#{...}, #@x, #$x) in
// double-quoted forms split the literal into pieces.
func readRbLit(s string) (rbLit, bool) {
	if s == "" {
		return nil, false
	}
	var open, close byte
	interp := false
	i := 0
	switch {
	case s[0] == '\'':
		open, close, i = '\'', '\'', 1
	case s[0] == '"':
		open, close, i, interp = '"', '"', 1, true
	case s[0] == '%' && len(s) > 2 && (s[1] == 'q' || s[1] == 'Q') && strings.IndexByte("([{<|!/", s[2]) >= 0:
		open, i, interp = s[2], 3, s[1] == 'Q'
	case s[0] == '%' && len(s) > 1 && strings.IndexByte("([{<|", s[1]) >= 0:
		open, i, interp = s[1], 2, true
	default:
		return nil, false
	}
	switch open {
	case '(':
		close = ')'
	case '[':
		close = ']'
	case '{':
		close = '}'
	case '<':
		close = '>'
	case '\'', '"':
	default:
		close = open
	}
	var out rbLit
	var cur strings.Builder
	depth := 0
	for ; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\n' && (open == '\'' || open == '"'):
			// A test description on one line; a multi-line literal is unusual enough to keep as is.
			cur.WriteByte(c)
		case c == '\\' && i+1 < len(s):
			i++
			n := s[i]
			if open == '\'' && n != '\'' && n != '\\' {
				cur.WriteByte('\\')
			}
			cur.WriteByte(n)
		case interp && c == '#' && i+1 < len(s) && s[i+1] == '{':
			// Skip the interpolation with brace nesting.
			d := 0
			j := i + 1
			for ; j < len(s); j++ {
				if s[j] == '{' {
					d++
				} else if s[j] == '}' {
					d--
					if d == 0 {
						break
					}
				}
			}
			out = append(out, cur.String())
			cur.Reset()
			i = j
		case interp && c == '#' && i+1 < len(s) && (s[i+1] == '@' || s[i+1] == '$'):
			j := i + 2
			for j < len(s) && (s[j] == '@' || goIdentByte(s[j])) {
				j++
			}
			if j == i+2 || (s[i+1] == '@' && j == i+3 && s[i+2] == '@') {
				cur.WriteByte(c)
				continue
			}
			out = append(out, cur.String())
			cur.Reset()
			i = j - 1
		case c == open && open != close:
			depth++
			cur.WriteByte(c)
		case c == close:
			if depth > 0 {
				depth--
				cur.WriteByte(c)
				continue
			}
			out = append(out, cur.String())
			return out, true
		default:
			cur.WriteByte(c)
		}
	}
	return nil, false
}

// ---- index ----

type rbIndex struct {
	units      map[string]bool
	classes    map[string][]string
	methods    map[string][]string
	methodTpls []rbTpl
	examples   map[string][]string
	exTpls     []rbTpl
	groups     map[string]map[string]bool // file -> group texts
	sharedDefs map[string][]string        // file -> shared example names it defines
	sharedUses map[string][]string        // shared example name -> files using it
}

type rbTpl struct {
	re   *regexp.Regexp
	file string
}

// rbScanCache memoizes per-blob scans across tasks (blob oid -> *rbFileScan).
var rbScanCache sync.Map

func buildRbIndex(t tree.Tree) *rbIndex {
	ix := &rbIndex{units: map[string]bool{}, classes: map[string][]string{}, methods: map[string][]string{}, examples: map[string][]string{},
		groups: map[string]map[string]bool{}, sharedDefs: map[string][]string{}, sharedUses: map[string][]string{}}
	for _, f := range t.Files() {
		if !rbScanned(f) {
			continue
		}
		if RbIsTestFile(f) {
			ix.units[f] = true
		}
		key := t.Key(f)
		var fs *rbFileScan
		if v, ok := rbScanCache.Load(key); ok && key != "" {
			fs = v.(*rbFileScan)
		} else {
			b, err := t.Read(f)
			if err != nil {
				continue
			}
			fs = scanRbTest(b)
			if key != "" {
				rbScanCache.Store(key, fs)
			}
		}
		ix.add(f, fs)
	}
	return ix
}

func (ix *rbIndex) add(f string, fs *rbFileScan) {
	for _, c := range fs.classes {
		ix.classes[c] = append(ix.classes[c], f)
	}
	for _, m := range fs.methods {
		ix.methods[m] = append(ix.methods[m], f)
	}
	for _, l := range fs.methodTpls {
		// Method keys of interpolated test names: normalize each static piece, anything in between.
		var b strings.Builder
		b.WriteString("^test_")
		for i, p := range l {
			if i > 0 {
				b.WriteString(".*")
			}
			b.WriteString(regexp.QuoteMeta(rbMethodKey(p)))
		}
		b.WriteString("$")
		if re, err := regexp.Compile(b.String()); err == nil {
			ix.methodTpls = append(ix.methodTpls, rbTpl{re, f})
		}
	}
	for _, e := range fs.examples {
		ix.examples[e] = append(ix.examples[e], f)
	}
	for _, l := range fs.exTpls {
		if re := l.re(true); re != nil {
			ix.exTpls = append(ix.exTpls, rbTpl{re, f})
		}
	}
	g := map[string]bool{}
	for _, x := range fs.groups {
		g[x] = true
	}
	ix.groups[f] = g
	if len(fs.sharedDefs) > 0 {
		ix.sharedDefs[f] = fs.sharedDefs
	}
	for _, u := range fs.sharedUses {
		ix.sharedUses[u] = append(ix.sharedUses[u], f)
	}
}

// consumeGroups reports whether prefix is a sequence of group texts of file f (RSpec joins them with spaces, but not before a
// description starting with '#', '.' or '::'; shoulda joins contexts with spaces).
func (ix *rbIndex) consumeGroups(f, prefix string, depth int) bool {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return true
	}
	if depth > 12 {
		return false
	}
	for g := range ix.groups[f] {
		if g != "" && strings.HasPrefix(prefix, g) && ix.consumeGroups(f, prefix[len(g):], depth+1) {
			return true
		}
	}
	return false
}

// descFiles finds the files whose examples (possibly prefixed by their groups) produce desc.
func (ix *rbIndex) descFiles(desc string) (files []string, via string) {
	if fs := ix.examples[desc]; len(fs) > 0 {
		return uniqSorted(append([]string(nil), fs...)), "desc"
	}
	set := map[string]bool{}
	// Suffixes of desc starting after a space or at '#', '.', ':' are example candidates; the prefix must be the file's groups.
	for i := 1; i < len(desc); i++ {
		if !(desc[i-1] == ' ' || desc[i] == '#' || desc[i] == '.') {
			continue
		}
		for _, f := range ix.examples[desc[i:]] {
			if ix.consumeGroups(f, desc[:i], 0) {
				set[f] = true
			}
		}
	}
	if len(set) > 0 {
		return sortedKeys(set), "composite"
	}
	for _, tp := range ix.exTpls {
		if m := tp.re.FindStringSubmatch(desc); m != nil && ix.consumeGroups(tp.file, m[1], 0) {
			set[tp.file] = true
		}
	}
	if len(set) > 0 {
		return sortedKeys(set), "template"
	}
	return nil, ""
}

func (ix *rbIndex) methodFiles(m string) (files []string, via string) {
	k := rbMethodKey(m)
	if fs := ix.methods[k]; len(fs) > 0 {
		return uniqSorted(append([]string(nil), fs...)), "method"
	}
	set := map[string]bool{}
	for _, tp := range ix.methodTpls {
		if tp.re.MatchString(k) {
			set[tp.file] = true
		}
	}
	if len(set) > 0 {
		return sortedKeys(set), "method-template"
	}
	return nil, ""
}

func (ix *rbIndex) classFiles(c string) []string {
	if i := strings.LastIndex(c, "::"); i >= 0 {
		c = c[i+2:]
	}
	return uniqSorted(append([]string(nil), ix.classes[c]...))
}

func rbIntersect(a, b []string) []string {
	in := map[string]bool{}
	for _, x := range b {
		in[x] = true
	}
	var out []string
	for _, x := range a {
		if in[x] {
			out = append(out, x)
		}
	}
	return out
}

// LocateRubyTests resolves every name to candidate test files of the tree t. patch is the set of files the test patch touches.
func LocateRubyTests(t tree.Tree, names []string, patch map[string]bool) []RbTestLoc {
	return locateRb(t, buildRbIndex(t), names, patch)
}

func locateRb(t tree.Tree, ix *rbIndex, names []string, patch map[string]bool) []RbTestLoc {
	out := make([]RbTestLoc, 0, len(names))
	for _, name := range names {
		pn := parseRbName(name)
		l := RbTestLoc{Name: name, Form: pn.form, Class: pn.class, Method: pn.method}
		var files []string
		switch pn.form {
		case "path", "cucumber":
			if t.Has(pn.path) {
				files, l.Via = []string{pn.path}, "path"
				l.NotUnit = pn.form == "path" && !RbIsTestFile(pn.path)
			} else if files, l.Via = ix.descFiles(pn.desc); len(files) > 0 { // a description that merely mentions a file name
				pn.form, l.Form = "desc", "desc"
			}
		case "minitest", "method":
			m := pn.method
			files, l.Via = ix.methodFiles(m)
			if len(files) == 0 && rbLabelSuffix.MatchString(m) {
				m = rbLabelSuffix.ReplaceAllString(m, "")
				if files, l.Via = ix.methodFiles(m); len(files) > 0 {
					l.Via += "+label"
				}
			}
			if pn.class != "" {
				cf := ix.classFiles(pn.class)
				switch both := rbIntersect(files, cf); {
				case len(both) > 0:
					files, l.Via = both, l.Via+"+class"
				case len(files) == 0 && len(cf) > 0:
					files, l.Via = cf, "class-only"
				case len(cf) > 0: // an inherited method (jekyll's helper JekyllUnitTest#test_dir runs in every subclass)
					files, l.Via = cf, "class-inherited"
				}
			}
			if len(files) == 0 { // test-unit `test "a b"` reported as "test_a_b"? already covered by the key; try it as a description
				if d := strings.TrimPrefix(m, "test_"); d != m {
					files, l.Via = ix.descFiles(strings.ReplaceAll(d, "_", " "))
				}
			}
		case "shoulda":
			df, via := ix.descFiles(pn.desc)
			files, l.Via = df, via
			if pn.class != "" {
				cf := ix.classFiles(pn.class)
				switch both := rbIntersect(df, cf); {
				case len(both) > 0:
					files, l.Via = both, via+"+class"
				case len(cf) > 0:
					files, l.Via = cf, "class-only"
				}
			}
		case "desc":
			files, l.Via = ix.descFiles(pn.desc)
			if len(files) == 0 && rbLabelSuffix.MatchString(pn.desc) {
				if files, l.Via = ix.descFiles(strings.TrimSpace(rbLabelSuffix.ReplaceAllString(pn.desc, ""))); len(files) > 0 {
					l.Via += "+label"
				}
			}
		}
		// Shared examples: a helper file that defines them is replaced by the test files that include them.
		var units []string
		for _, f := range files {
			if ix.units[f] || pn.form == "path" || pn.form == "cucumber" {
				units = append(units, f)
				continue
			}
			l.Helpers = append(l.Helpers, f)
			for _, sd := range ix.sharedDefs[f] {
				for _, u := range ix.sharedUses[sd] {
					if ix.units[u] {
						units = append(units, u)
					}
				}
			}
		}
		if len(l.Helpers) > 0 && len(units) == 0 {
			units, l.NotUnit = files, true // found only in helpers that no test file includes by a literal name
		}
		l.Files = uniqSorted(units)
		for _, f := range l.Files {
			if patch[f] {
				l.PatchFiles = append(l.PatchFiles, f)
				l.InPatch = true
			}
		}
		l.NotFound = len(l.Files) == 0
		l.Ambiguous = len(l.Files) > 1
		out = append(out, l)
	}
	return out
}
