package ruby

import (
	"path"
	"sort"
	"strings"

	"github.com/qxbao/bgraph/internal/eco"
	"github.com/qxbao/bgraph/internal/model"
)

// railsSkip: top-level directories of a Rails application that the application does not load.
var railsSkip = map[string]bool{"spec": true, "test": true, "tests": true, "tmp": true, "log": true, "coverage": true,
	"node_modules": true, "doc": true, "docs": true, ".git": true, ".github": true, ".bundle": true}

// fileEdges adds the edges and unresolved sites of one Ruby file.
func (b *builder) fileEdges(fi *fileInfo, unit bool) {
	p := fi.p
	b.constantEdges(p)
	if fi.scan.Uncertain {
		b.unres(p, "*", model.UnresolvedUncertainScan, 0, fi.scan.UncertainReason)
	}
	data := testish(p, unit)
	for _, im := range fi.scan.Imports {
		switch im.Kind {
		case model.ImportRbRequire, model.ImportRbLoad:
			b.require(fi, im)
		case model.ImportRbGlob:
			b.globRef(fi, im, true)
		case model.ImportRbDataGlob:
			if data {
				b.globRef(fi, im, false)
			}
		case model.ImportRbPushDir:
			b.pushDir(fi, im)
		case model.ImportRbZeitwerk:
			b.autoloadEdge(p, under(path.Dir(p), "**/*.rb"), "Zeitwerk::Loader.for_gem", im.Line)
		case model.ImportRbRails:
			b.railsApp(fi, im)
		case model.ImportRbBundler:
			b.bundlerRequire(fi, im)
		case model.ImportRbBinPath:
			for _, x := range b.exesNamed(im.Spec) {
				b.edge(p, x, model.EdgeFixtureData, "Gem.bin_path "+im.Spec, im.Line)
			}
		case model.ImportRbFixturesAll:
			for _, d := range []string{"test/fixtures", "spec/fixtures"} {
				b.edge(p, under(path.Join(fi.proj, d), "**"), model.EdgePattern, "fixtures :all", im.Line)
			}
		case model.ImportRbString:
			if data {
				b.stringRef(fi, im)
			}
		}
	}
}

// under returns the glob for files below directory d matching tail (e.g. "**/*.rb").
func under(d, tail string) string {
	if d == "." || d == "" {
		return tail
	}
	return escapeGlob(d) + "/" + tail
}

// stripWD drops the leading ./ ../ and / of a working-directory relative path (the suffix match covers every working directory).
func stripWD(s string) string {
	for {
		switch {
		case strings.HasPrefix(s, "./"):
			s = s[2:]
		case strings.HasPrefix(s, "../"):
			s = s[3:]
		case strings.HasPrefix(s, "/"):
			s = s[1:]
		case strings.HasPrefix(s, "~/"):
			s = s[2:]
		default:
			return s
		}
	}
}

func nativeFeature(s string) bool {
	switch path.Ext(s) {
	case ".so", ".bundle", ".dll", ".o", ".dylib", ".jar", ".class":
		return true
	}
	return false
}

// baseDir returns the directory a path value with the given base tag is relative to.
func baseDir(fi *fileInfo, tag string) string {
	if tag == "root" {
		return fi.proj
	}
	return path.Dir(fi.p)
}

func (b *builder) require(fi *fileInfo, im model.ImportRef) {
	p := fi.p
	load := im.Kind == model.ImportRbLoad
	what := "require"
	if load {
		what = "load"
	}
	if im.Dynamic {
		b.prefix(fi, im, load)
		return
	}
	if im.Suffix == "" {
		b.feature(p, im.Spec, what, load, model.EdgeImport)
		return
	}
	target := path.Join(baseDir(fi, im.Suffix), im.Spec)
	if outside(target) {
		b.unres(p, im.Spec, model.UnresolvedUnknown, im.Line, what+" target is outside the repository")
		return
	}
	if nativeFeature(target) {
		return
	}
	if !load && path.Ext(target) != ".rb" {
		target += ".rb"
	}
	b.edge(p, target, model.EdgeImport, im.Spec, im.Line)
	if !b.t.Has(target) {
		b.unres(p, im.Spec, model.UnresolvedUnknown, im.Line, what+" target not in the tree (tracked via an exact path edge)")
	}
}

// feature links from to the virtual node of a feature resolved by suffix (every Ruby file whose path ends with it).
func (b *builder) feature(from, spec, what string, load bool, kind model.EdgeKind) {
	s := stripWD(strings.ReplaceAll(spec, "\\", "/"))
	s = path.Clean(s)
	if s == "" || s == "." || outside(s) || nativeFeature(s) {
		return
	}
	name := s
	node := "<rbload:" + s + ">"
	if !load {
		name = strings.TrimSuffix(s, ".rb") + ".rb"
		node = "<rbreq:" + strings.TrimSuffix(s, ".rb") + ">"
	}
	b.edge(from, node, kind, what+" "+spec, 0)
	if b.reqDone[node] {
		return
	}
	b.reqDone[node] = true
	b.edge(node, "**/"+escapeGlob(name), model.EdgePattern, spec, 0)
	for _, f := range b.byBase[path.Base(name)] {
		if f == name || strings.HasSuffix(f, "/"+name) {
			b.edge(node, f, model.EdgeImport, spec, 0)
		}
	}
}

// prefix handles a require/load whose path has a static prefix (im.Spec, possibly empty).
func (b *builder) prefix(fi *fileInfo, im model.ImportRef, load bool) {
	p := fi.p
	spec, suf, _ := strings.Cut(im.Spec, holeMark)
	// The file loaded is <prefix><anything><suffix>[.rb]: one glob for an unknown part without a slash, one with.
	var tails []string
	switch {
	case suf == "" && load:
		tails = []string{"*", "*/**"}
	case suf == "":
		tails = []string{"*.rb", "*/**/*.rb"}
	default:
		e := ""
		if !load && path.Ext(suf) != ".rb" {
			if nativeFeature(suf) {
				return // a compiled extension, not a Ruby file
			}
			e = ".rb"
		}
		t := escapeGlob(suf) + e
		tails = []string{"*" + t, "*/**/*" + t}
	}
	var g string
	switch im.Suffix {
	case "":
		pre := stripWD(spec)
		if suf == "" && strings.Trim(pre, "./") == "" {
			b.unres(p, im.Spec, model.UnresolvedDynamic, im.Line, "the required path is computed")
			return
		}
		g = "**/" + escapeGlob(pre)
	default:
		pre := joinPrefix(baseDir(fi, im.Suffix), spec)
		if outside(pre) {
			b.unres(p, im.Spec, model.UnresolvedDynamic, im.Line, "the required path is computed and leaves the repository")
			return
		}
		g = escapeGlob(pre)
		if pre == "" && suf == "" {
			b.edge(p, "**/*.rb", model.EdgePattern, im.Spec, im.Line)
			if load {
				b.edge(p, "**", model.EdgePattern, im.Spec, im.Line)
			}
			return
		}
	}
	for _, t := range tails {
		b.edge(p, g+t, model.EdgePattern, im.Spec, im.Line)
	}
}

// holeMark separates the static prefix and suffix of a partly computed path (internal/scan/ruby).
const holeMark = "#{}"

// joinPrefix joins a directory and a string prefix relative to it ("" means everything in the directory). The result is a
// string prefix of repo-relative paths ("" for the whole repository).
func joinPrefix(dir, pre string) string {
	tail := ""
	if !strings.HasSuffix(pre, "/") && pre != "" {
		i := strings.LastIndexByte(pre, '/')
		tail = pre[i+1:]
		pre = pre[:i+1]
	}
	d := path.Join(dir, pre)
	if d == "." {
		return tail
	}
	if outside(d) {
		return d
	}
	return d + "/" + tail
}

// globRef adds pattern edges for a Dir glob (forRequire: its files are loaded).
func (b *builder) globRef(fi *fileInfo, im model.ImportRef, forRequire bool) {
	if im.Dynamic {
		spec, suf, _ := strings.Cut(im.Spec, holeMark)
		pre := globEscapePrefix(spec)
		if suf == "" {
			b.globAt(fi, im, []string{pre + "*", pre + "*/**"}, forRequire)
			return
		}
		// The suffix is glob text already ("/*.rb"): the unknown part, without or with slashes, sits between.
		suf = strings.ReplaceAll(suf, "\\", "/")
		b.globAt(fi, im, []string{pre + "*" + suf, pre + "*/**/*" + suf}, forRequire)
		return
	}
	b.globAt(fi, im, []string{strings.ReplaceAll(im.Spec, "\\", "/")}, forRequire)
}

// globEscapePrefix escapes a literal prefix.
func globEscapePrefix(s string) string { return escapeGlob(strings.ReplaceAll(s, "\\", "/")) }

func (b *builder) globAt(fi *fileInfo, im model.ImportRef, gs []string, forRequire bool) {
	p := fi.p
	spec := "Dir glob " + im.Spec
	for _, g := range gs {
		switch im.Suffix {
		case "rel", "root":
			full := globJoin(baseDir(fi, im.Suffix), g)
			if outside(full) {
				if forRequire {
					b.unres(p, im.Spec, model.UnresolvedDynamic, im.Line, "required glob leaves the repository")
				}
				continue
			}
			b.edge(p, full, model.EdgePattern, spec, im.Line)
		default:
			rel := stripWD(g)
			if rel == "" {
				continue
			}
			if forRequire {
				// Working directory unknown: every directory of the repository.
				b.edge(p, "**/"+rel, model.EdgePattern, spec, im.Line)
			} else {
				b.edge(p, globJoin(fi.proj, rel), model.EdgePattern, spec, im.Line)
				b.edge(p, rel, model.EdgePattern, spec, im.Line)
			}
		}
	}
}

// globJoin joins an (unescaped) directory and a glob.
func globJoin(dir, g string) string {
	// Resolve leading ../ of the glob against the directory.
	for strings.HasPrefix(g, "../") {
		if dir == "." || dir == "" || outside(dir) {
			return "../" + g
		}
		g = g[3:]
		dir = path.Dir(dir)
	}
	g = strings.TrimPrefix(g, "./")
	if dir == "." || dir == "" {
		return g
	}
	if outside(dir) {
		return dir + "/" + g
	}
	return escapeGlob(dir) + "/" + g
}

func (b *builder) pushDir(fi *fileInfo, im model.ImportRef) {
	p := fi.p
	if im.Dynamic && (im.Spec == "" || im.Suffix == "" && strings.Trim(stripWD(im.Spec), "./") == "") {
		b.unres(p, im.Spec, model.UnresolvedDynamic, im.Line, "Zeitwerk push_dir of a computed directory")
		return
	}
	tail := "/**/*.rb"
	spec := im.Spec
	if im.Dynamic {
		tail = "*/**/*.rb"
	} else {
		spec = strings.TrimSuffix(spec, "/")
	}
	add := func(pattern string) {
		if im.Dynamic {
			b.edge(p, pattern, model.EdgePattern, "push_dir", im.Line)
		} else {
			b.autoloadEdge(p, pattern, "push_dir", im.Line)
		}
	}
	switch im.Suffix {
	case "rel", "root":
		d := path.Join(baseDir(fi, im.Suffix), spec)
		if im.Dynamic {
			d = joinPrefix(baseDir(fi, im.Suffix), spec)
		}
		if outside(d) {
			b.unres(p, im.Spec, model.UnresolvedDynamic, im.Line, "Zeitwerk push_dir outside the repository")
			return
		}
		if d == "." || d == "" {
			add("**/*.rb")
			return
		}
		add(escapeGlob(d) + tail)
	default:
		s := stripWD(spec)
		if s == "" {
			add("**/*.rb")
			return
		}
		add("**/" + escapeGlob(s) + tail)
	}
}

// railsApp: a Rails application or engine loads (autoloads, eager loads, renders) every file of its project outside the tests.
func (b *builder) railsApp(fi *fileInfo, im model.ImportRef) {
	dirs := map[string]bool{"app": true, "lib": true, "config": true, "db": true, "vendor": true}
	for d := range b.topDirs[fi.proj] {
		if !railsSkip[d] {
			dirs[d] = true
		}
	}
	var list []string
	for d := range dirs {
		list = append(list, d)
	}
	sort.Strings(list)
	for _, d := range list {
		b.autoloadEdge(fi.p, under(path.Join(fi.proj, d), "**"), "Rails::"+map[string]string{"app": "Application", "engine": "Engine"}[im.Spec], im.Line)
	}
}

func (b *builder) bundlerRequire(fi *fileInfo, im model.ImportRef) {
	gf := b.gemfileFor(fi.proj)
	if gf == "" {
		return
	}
	g := b.infos[gf]
	if g == nil {
		return
	}
	gems := append([]model.ImportRef(nil), g.scan.Imports...)
	for _, r := range g.scan.Imports {
		// eval_gemfile "other": its gems are required too.
		if r.Kind == model.ImportRbLoad && !r.Dynamic && r.Suffix == "rel" {
			if o := b.infos[path.Join(path.Dir(gf), r.Spec)]; o != nil {
				gems = append(gems, o.scan.Imports...)
			}
		}
	}
	for _, r := range gems {
		switch r.Kind {
		case model.ImportRbGem:
			switch r.Suffix {
			case "-":
			case "":
				b.feature(fi.p, r.Spec, "Bundler.require", false, model.EdgeImport)
				if strings.Contains(r.Spec, "-") {
					b.feature(fi.p, strings.ReplaceAll(r.Spec, "-", "/"), "Bundler.require", false, model.EdgeImport)
				}
			default:
				b.feature(fi.p, r.Suffix, "Bundler.require", false, model.EdgeImport)
			}
		case model.ImportRbGemspec:
			d := path.Join(path.Dir(gf), r.Spec)
			for _, f := range b.rbFiles {
				if path.Dir(f) == d && strings.HasSuffix(f, ".gemspec") {
					b.feature(fi.p, strings.TrimSuffix(path.Base(f), ".gemspec"), "Bundler.require (gemspec)", false, model.EdgeImport)
				}
			}
		}
	}
	if g.scan.Uncertain {
		b.unres(fi.p, "Bundler.require", model.UnresolvedDynamic, im.Line, "the Gemfile cannot be read reliably")
	}
	for _, r := range g.scan.Imports {
		if (r.Kind == model.ImportRbRequire || r.Kind == model.ImportRbLoad) && r.Dynamic && r.Spec == "" {
			b.unres(fi.p, "Bundler.require", model.UnresolvedDynamic, im.Line, "the Gemfile loads computed files")
			break
		}
	}
}

func (b *builder) exesNamed(name string) []string {
	var out []string
	for x := range b.exes {
		if path.Base(x) == name {
			out = append(out, x)
		}
	}
	sort.Strings(out)
	return out
}

// stringRef: a test or support file names an existing file or directory.
func (b *builder) stringRef(fi *fileInfo, im model.ImportRef) {
	p := fi.p
	s := strings.ReplaceAll(im.Spec, "\\", "/")
	var cands []string
	switch im.Suffix {
	case "rel", "root":
		base := baseDir(fi, im.Suffix)
		if im.Dynamic {
			pre := joinPrefix(base, s)
			if outside(pre) || pre == "" || pre == fi.proj+"/" || eco.Within(p, strings.TrimSuffix(pre, "/")) {
				return
			}
			b.edge(p, escapeGlob(pre)+"*/**", model.EdgePattern, "path "+s+"…", im.Line)
			b.edge(p, escapeGlob(pre)+"*", model.EdgePattern, "path "+s+"…", im.Line)
			return
		}
		cands = []string{path.Join(base, s)}
	default:
		if im.Dynamic {
			return
		}
		s = strings.TrimPrefix(s, "./")
		if strings.HasPrefix(s, "/") || strings.HasPrefix(s, "~") {
			return
		}
		cands = []string{path.Join(path.Dir(p), s), path.Join(fi.proj, s), path.Clean(s)}
		for _, fd := range b.fixtures[fi.proj] {
			cands = append(cands, path.Join(fd, s))
		}
	}
	for _, c := range cands {
		if c == p || c == "." || outside(c) {
			continue
		}
		switch {
		case b.t.Has(c):
			b.edge(p, c, model.EdgeFixtureData, s, im.Line)
		case b.t.IsDir(c):
			if eco.Within(p, c) || b.markers[c] {
				continue // the file's own directory or an ancestor, or a whole project: not a data reference
			}
			b.edge(p, escapeGlob(c)+"/**", model.EdgePattern, "directory "+s, im.Line)
		}
	}
}
