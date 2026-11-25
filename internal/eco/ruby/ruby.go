// Package ruby is bgraph's Ruby ecosystem (RSpec, Minitest / Test::Unit, Rails test, Cucumber; Bundler), at test-file granularity: Ruby
// loads code with require/require_relative/load/autoload into one process, so a test file's dependencies are the transitive
// closure of what it loads. The scanner is internal/scan/ruby.
//
// # Projects
//
// A Ruby project is a directory holding a Gemfile, gems.rb, a *.gemspec, a Rakefile (rakefile, Rakefile.rb) or a .rspec file,
// plus the repository root when a Ruby file lies outside every such directory. Markers below a test-data directory (a path
// segment spec, test, tests, fixtures, fixture, tmp, vendor, node_modules or .bundle) are data, not projects: an engine's
// test/dummy app or a spec/fixtures gem belongs to the enclosing project. A repository with no marker is not analyzed (a stray
// .rb file is left to the general rules), except an unbundled root Cucumber suite identified by its features and Ruby glue.
// Every file belongs to its nearest project. node_modules, .bundle and vendor/bundle are never repository code.
//
// Ruby files: .rb, .rake, .ru, .gemspec, Gemfile, Rakefile, and extensionless files directly below a bin/, exe/ or libexec/
// directory whose shebang line names ruby (executables, scanned without the cache).
//
// # Test units
//
// One model.TestUnit per test file: Eco "ruby", ID = the repo-relative file (e.g. spec/rubocop/cop/style/foo_spec.rb),
// Files = [the file], Project = its project. A file is a test when it is a .rb file outside the never-unit directories (fixtures,
// fixture, vendor, tmp, node_modules, .bundle), is not a helper (test_helper.rb, spec_helper.rb, rails_helper.rb, helper.rb:
// sources, so that a helper nothing is known to load widens) and
//
//   - it lies below a directory named spec, test or tests and its name matches *_spec.rb, *_test.rb, test_*.rb or tc_*.rb; or
//   - it matches a literal test pattern of its project: .rspec / .rspec-local / .rspec-parallel / spec/spec.opts --pattern (also
//     below --default-path, where *_spec.rb files are tests too), Rake::TestTask / RSpec::Core::RakeTask `pattern =` /
//     `test_files =` (strings, %w[], FileList[...], Dir[...], Dir.glob, FileList.new, ENV["X"] || "literal", ENV.fetch("X",
//     "literal"); .exclude and `- list` only narrow and are ignored), and Rake::TestTask's default test/test*.rb when no task
//     sets either. Patterns are relative to the project directory (rake runs in the Rakefile's directory).
//
// Runner "rspec" for a *_spec.rb file whose nearest spec/test ancestor is spec (unless a Rake::TestTask pattern claims it),
// below the .rspec --default-path, or matched by an RSpec pattern; "rails" for a file below test/ of a Rails application (a
// project with bin/rails and config/application.rb); "minitest" otherwise. Config is the Gemfile bundler uses (the nearest
// Gemfile or gems.rb at or above the project, "" for none); for minitest units it is followed by "|" and the comma-separated
// -I directories: lib plus the literal Rake::TestTask libs, or lib plus the unit's top test directory when there are none.
//
// A computed test pattern (a non-literal `pattern =`, `test_files =` or `ruby_opts`, a pattern bgraph cannot compile, ERB in an
// .rspec file) adds the whole-project unit ID "<rb-all-tests:DIR>" (WholeUnitID / ParseWholeUnitID; DIR is the project, "." for
// the root; Whole, Runner "rake" or "rspec", no Files), which the selector runs on any change (config_is_code).
//
// Cucumber runs one .feature file per conventional unit, with shared dependencies on the features directory's support.
// Cucumber profiles and nonstandard support discovery get a whole-suite unit, <rb-all-cucumber:DIR>.
//
// # Graph (From depends on To)
//
//   - require / require_dependency / autoload / Bundler features "a/b": suffix union. The importer depends on the virtual node
//     <rbreq:a/b>, which depends on every Ruby file whose path is a/b.rb or ends with /a/b.rb, plus a pattern edge **/a/b.rb
//     (which also matches a/b.rb at the root) so that a file added later, or a non-Ruby file loaded under that name, is caught.
//     load "x/y.rake" uses <rbload:x/y.rake> and the exact file name (load adds no extension). The union is a superset of every
//     $LOAD_PATH and working-directory interpretation inside the repository, so $LOAD_PATH edits are only recorded. Leading
//     ./, ../ and / (working-directory or absolute paths) are dropped before the match. Nothing matching: an external gem or
//     the standard library (no widening; the pattern edge stays). .so/.bundle features are compiled code (no edge).
//   - require_relative "x" and evaluated path expressions (File.expand_path("x", __dir__), File.expand_path("../x", __FILE__),
//     File.join(__dir__, ...), File.dirname(__FILE__) + "/x", "#{__dir__}/x", Pathname(__dir__).join, Rails.root.join (the
//     project root), Dir.pwd (working directory: suffix union), single-assignment locals and constants holding such a path):
//     the exact file (x.rb for require). A missing target keeps the exact edge (adding the file is caught) and is reported
//     (unknown, no widening); a target outside the repository is reported only. Because the evaluation may be wrong (or an
//     ENV["X"] || default override may win), every such argument also yields its literal tail as a suffix-union feature:
//     the literal components after the last computed part or "..", e.g. File.join(DIR, "lib", "foo") → lib/foo,
//     File.expand_path("../lib/foo", __dir__) → lib/foo. An argument with no literal tail is dynamic. Every require-like call
//     with an argument yields at least one reference: one the scanner cannot classify is dynamic.
//   - a partly computed path: pattern edges to every file the static parts allow. require "a/#{x}" depends on **/a/*.rb and
//     **/a/*/**/*.rb; with a static tail, require "a/#{x}/version" depends on **/a/*/version.rb and **/a/*/**/*/version.rb;
//     relative ones (File.join(__dir__, "plugins", x)) are anchored at the file's directory.
//   - Dir[...] / Dir.glob / Pathname.glob whose files are required or loaded by a block over them (`.each { |f| require f }`,
//     `.each(&method(:require))`, also through a single-assignment variable, f possibly wrapped in File.expand_path / to_s /
//     sub / chomp): pattern edges to the glob, relative to the file's directory, the project root (Rails.root) or, for a
//     working-directory glob, **/<glob>. A require_relative over a working-directory glob gets the file-relative glob too.
//   - Zeitwerk::Loader.for_gem / for_gem_extension: every .rb below the calling file's directory; loader.push_dir(d): every .rb
//     below d (a partly computed d: below its static prefix).
//   - a class inheriting Rails::Application or Rails::Engine depends on every file of its project outside the test
//     directories: pattern edges DIR/<d>/** for app, lib, config, db, vendor and every other top-level directory of the project
//     except spec, test, tests, tmp, log, coverage, node_modules, doc, docs, .git, .github, .bundle (templates, assets, locales
//     and config/*.yml included: request and system specs render them). Nested projects are reached through their features.
//   - The optional [[ruby_autoload]] declaration replaces blanket loader edges for conventional lazy files with references
//     from Ruby constant lookups. Indirect operations retain a dependency on every declared file. Structural changes widen.
//   - Bundler.require: the features of the gems of the nearest Gemfile (and of files it loads with eval_gemfile): the gem name
//     (and the name with - as /) or its require: option, nothing for require: false, and the gem names of the gemspecs named
//     by `gemspec`.
//   - every unit of a project depends on the --require / -r features of the project's .rspec option files and on the
//     Rake::TestTask ruby_opts -r features (implicit edges; whatever the unit's runner, an extra dependency only over-selects).
//   - test and test-support files (every unit and any file below a spec/test/tests directory) depend on the existing files and
//     directories their path-like string literals and evaluated path expressions name, relative to the file's directory, the
//     project root, the repository root or a fixtures directory of the project (fixture_data edges, pattern edges dir/** for a
//     directory other than an ancestor or a project root); on a partly computed path's prefix (pattern edges); on the files
//     of their other Dir globs (pattern edges relative to the project and the repository root); on the executables named by
//     Gem.bin_path; and on test/fixtures/** and spec/fixtures/** for `fixtures :all`. These edges are heuristic and add-only.
//     Executables are sources: a test naming exe/x depends on it, and the executable depends on what it requires; a bare
//     program name ("rubocop") is not a reference.
//
// Roles: tests; Gemfile, gems.rb and *.gemspec of a project: manifest; Rakefile of a project, *.rake below tasks/ or rakelib/,
// .rspec*, spec/spec.opts and .simplecov of a project: config; Gemfile.lock / gems.locked of a project: lock; other Ruby files:
// source; .erb, .haml, .slim, .builder, .jbuilder, .yml, .yaml files of a project and every file below a spec/test/tests
// directory of a project: data (a sub-tree holding another ecosystem's manifest and no Ruby file is left out). Marker-like
// files below test-data directories get no manifest/config/lock role: those roles are exempt from the unreferenced-file
// widening, test data must not be.
//
// # Unresolved
//
// A require, load, autoload, push_dir or Dir loop whose path is fully computed, `send(:require, x)`, eval/instance_eval/
// class_eval of File.read(x) and a Bundler.require whose Gemfile cannot be read are dynamic; a file the lexer cannot read
// reliably is uncertain_scan (its references found so far still count). Both are Unresolved at repository scope with
// TriggerGlobs on the files a require or load can execute (**/*.rb, **/*.rake, **/*.ru, **/*.gemspec, **/bin/*, **/exe/*,
// **/libexec/*): such a file added, changed or deleted anywhere seeds the file with the dynamic site, and every test reaching
// it is selected. A changed test file nothing references (other than its ownership) does not fire a dynamic site: computed
// loads are assumed not to load test files, which the runner loads; an uncertain scan still fires on them. A bare load(x) in a
// file that defines its own `load` method is that method, not Kernel#load.
//
// A bgraph.toml [[dynamic]] declaration for a Ruby file (optionally limited to lines) replaces the dynamic Unresolved of its
// computed sites with pattern edges to its `loads` (Provenance rule), or nothing for `none = true`. sys_path_only does not
// apply ($LOAD_PATH edits need no declaration) and an uncertain scan is never replaceable. A declaration of a Ruby file that
// matches no computed site is reported (unknown, no widening); declarations of other files are left to their ecosystems.
//
// # Widenings (ClassifyChange, all with Eco "ruby")
//
// Only marker files at a Ruby project are claimed; the same names below test-data directories are left to the general rules.
//
//   - Gemfile / gems.rb: manifest_changed at its directory (it is code: groups, sources and path gems change what is loaded).
//   - *.gemspec: nothing when only summary, description, homepage, author(s), email, metadata, license(s),
//     post_install_message, rdoc_options or extra_rdoc_files assignments changed (comments and layout ignored; the scanner's
//     GemspecFingerprint); manifest_changed at its directory otherwise, or when it was added, deleted or cannot be lexed.
//   - Gemfile.lock / gems.locked: lock_changed (level L0) at its directory, every Ruby project below it included.
//   - .rspec, .rspec-local, .rspec-parallel, spec/spec.opts, Rakefile, *.rake below tasks/ or rakelib/, .simplecov:
//     test_config_changed at the project (nested projects included: a root Rakefile often runs them).
//   - .rubocop.yml, .rubocop_todo.yml, .reek.yml, .yardopts, .document, .standard.yml at a project root, CHANGELOG* and *.rdoc
//     in a project (not below a test directory): nothing. A test naming such a file still reaches it through the graph.
//   - native extension sources below an ext/ directory (C, C++, Java, Objective-C, yacc sources, extconf.rb, depend):
//     unknown_file at the project.
//
// AnalyzerVersion must be bumped on every change of these rules (analysis snapshots are keyed by it).
package ruby

import (
	"bytes"
	"context"
	"fmt"
	"path"
	"slices"
	"sort"
	"strings"

	"github.com/qxbao/bgraph/internal/eco"
	"github.com/qxbao/bgraph/internal/model"
	"github.com/qxbao/bgraph/internal/rules"
	rbscan "github.com/qxbao/bgraph/internal/scan/ruby"
	"github.com/qxbao/bgraph/internal/tree"
)

// Name is the ecosystem name.
const Name = "ruby"

// Runner names of the units.
const (
	RunnerRSpec    = "rspec"
	RunnerRails    = "rails"
	RunnerMinitest = "minitest"
	RunnerRake     = "rake" // whole-project unit run by `rake test`
)

// AnalyzerVersion: bump whenever the analysis rules change to invalidate stored analysis snapshots.
const AnalyzerVersion = "ruby-8"

// Eco is the Ruby ecosystem.
type Eco struct {
	rules *rules.File // [[dynamic]]; may be nil
}

// New creates the Ruby ecosystem. [[rule]] entries are applied by the selector; [[dynamic]] entries are read here.
func New(r *rules.File) *Eco { return &Eco{rules: r} }

func (e *Eco) Name() string    { return Name }
func (e *Eco) Version() string { return AnalyzerVersion }

// WholeUnitID is the ID of the "whole test suite of project dir" unit.
func WholeUnitID(dir string) string { return "<rb-all-tests:" + dir + ">" }

// ParseWholeUnitID returns the project directory of a whole-project unit ID.
func ParseWholeUnitID(id string) (string, bool) {
	s, ok := strings.CutPrefix(id, "<rb-all-tests:")
	if !ok || !strings.HasSuffix(s, ">") {
		return "", false
	}
	return strings.TrimSuffix(s, ">"), true
}

// triggers: the files a dynamic require can load.
var triggers = []string{"**/*.rb", "**/*.rake", "**/*.ru", "**/*.gemspec", "**/bin/*", "**/exe/*", "**/libexec/*"}

type edgeKey struct {
	from, to string
	kind     model.EdgeKind
}

type fileInfo struct {
	p    string
	proj string
	scan model.ScanResult
	exe  bool
}

type builder struct {
	t  tree.Tree
	sc eco.Scanner
	a  *eco.Analysis

	markers  map[string]bool
	projOf   map[string]string
	rbFiles  []string        // Ruby files (sorted), executables included
	isRuby   map[string]bool // set of rbFiles
	exes     map[string]bool
	byBase   map[string][]string // base name → Ruby files
	fixtures map[string][]string // project → fixtures directories
	topDirs  map[string]map[string]bool
	gemfiles map[string]string // directory → Gemfile / gems.rb in it
	foreign  map[string]bool   // directories with another ecosystem's manifest
	rbDirs   map[string]bool   // directories with a Ruby file below them
	seen     map[edgeKey]bool
	infos    map[string]*fileInfo
	reqDone  map[string]bool // virtual nodes already linked
	dyn      []rules.Dynamic // [[dynamic]] declarations
	dynUsed  []bool
	rails    map[string]bool // projects that are Rails applications
	autoload *autoloadTargets
}

// skipPath: never repository code.
func skipPath(p string) bool {
	return strings.HasPrefix(p, "node_modules/") || strings.Contains(p, "/node_modules/") || strings.HasPrefix(p, ".bundle/") ||
		strings.Contains(p, "/.bundle/") || strings.HasPrefix(p, "vendor/bundle/") || strings.Contains(p, "/vendor/bundle/") ||
		strings.HasPrefix(p, ".git/")
}

// dataDir: a path segment below which markers are test data and files are never units.
func dataSegment(seg string) bool {
	switch seg {
	case "fixtures", "fixture", "tmp", "node_modules", ".bundle":
		return true
	}
	return false
}

func underDataDir(p string, alsoTests bool) bool {
	segs := strings.Split(path.Dir(p), "/")
	for i, s := range segs {
		if dataSegment(s) || alsoTests && (s == "spec" || s == "test" || s == "tests") ||
			s == "vendor" && (alsoTests || i+1 < len(segs) && segs[i+1] == "bundle") {
			return true
		}
	}
	return false
}

func isMarker(base string) bool {
	switch base {
	case "Gemfile", "gems.rb", "Rakefile", "rakefile", "Rakefile.rb", ".rspec":
		return true
	}
	return strings.HasSuffix(base, ".gemspec")
}

func rubyExt(p string) bool {
	switch path.Ext(p) {
	case ".rb", ".rake", ".ru", ".gemspec":
		return true
	}
	switch path.Base(p) {
	case "Gemfile", "Rakefile", "rakefile":
		return true
	}
	return false
}

// exeCandidate: an extensionless file directly below bin/, exe/ or libexec/.
func exeCandidate(p string) bool {
	if path.Ext(p) != "" {
		return false
	}
	switch path.Base(path.Dir(p)) {
	case "bin", "exe", "libexec":
		return true
	}
	return false
}

var foreignManifests = map[string]bool{"package.json": true, "pyproject.toml": true, "setup.py": true, "go.mod": true, "Cargo.toml": true,
	"pom.xml": true, "build.gradle": true, "build.gradle.kts": true}

// Analyze analyzes tree t.
func (e *Eco) Analyze(ctx context.Context, t tree.Tree, sc eco.Scanner) (*eco.Analysis, error) {
	a := &eco.Analysis{Roles: map[string]model.Role{}}
	files := t.Files()
	any := false
	for _, p := range files {
		if strings.HasSuffix(p, ".rb") || strings.HasSuffix(p, ".gemspec") || strings.HasSuffix(p, ".feature") || path.Base(p) == "Gemfile" {
			any = true
			break
		}
	}
	if !any {
		return a, nil
	}
	b := &builder{t: t, sc: sc, a: a, markers: map[string]bool{}, projOf: map[string]string{}, isRuby: map[string]bool{},
		exes: map[string]bool{}, byBase: map[string][]string{}, fixtures: map[string][]string{}, topDirs: map[string]map[string]bool{},
		gemfiles: map[string]string{}, foreign: map[string]bool{}, rbDirs: map[string]bool{}, seen: map[edgeKey]bool{},
		infos: map[string]*fileInfo{}, reqDone: map[string]bool{}, rails: map[string]bool{}}
	if e.rules != nil {
		b.dyn = e.rules.Dynamics
		b.dynUsed = make([]bool, len(b.dyn))
	}
	var exeCands []string
	fixtureDirs := map[string]bool{}
	for _, p := range files {
		if skipPath(p) {
			continue
		}
		base := path.Base(p)
		if isMarker(base) && !underDataDir(p, true) {
			b.markers[path.Dir(p)] = true
		}
		if base == "Gemfile" || base == "gems.rb" {
			if _, ok := b.gemfiles[path.Dir(p)]; !ok || base == "Gemfile" {
				b.gemfiles[path.Dir(p)] = p
			}
		}
		if foreignManifests[base] || strings.HasSuffix(base, ".csproj") || strings.HasSuffix(base, ".fsproj") {
			b.foreign[path.Dir(p)] = true
		}
		switch {
		case rubyExt(p):
			b.rbFiles = append(b.rbFiles, p)
		case exeCandidate(p):
			exeCands = append(exeCands, p)
		}
		if i := fixtureIndex(p); i > 0 {
			fixtureDirs[p[:i]] = true
		}
	}
	if !b.markers["."] && standaloneCucumber(files) {
		b.markers["."] = true
	}
	if len(b.markers) == 0 {
		return a, nil // Ruby files but no Ruby project: left to the general rules
	}
	for _, p := range exeCands {
		if isRubyScript(t, p) {
			b.rbFiles = append(b.rbFiles, p)
			b.exes[p] = true
		}
	}
	sort.Strings(b.rbFiles)
	for _, p := range b.rbFiles {
		b.isRuby[p] = true
		b.byBase[path.Base(p)] = append(b.byBase[path.Base(p)], p)
		for d := path.Dir(p); !b.rbDirs[d]; d = path.Dir(d) {
			b.rbDirs[d] = true
			if d == "." {
				break
			}
		}
	}

	// Projects.
	markerDirs := make([]string, 0, len(b.markers))
	for d := range b.markers {
		markerDirs = append(markerDirs, d)
	}
	sort.Strings(markerDirs)
	if !b.markers["."] {
		for _, p := range b.rbFiles {
			if b.project(path.Dir(p)) == "." {
				b.markers["."] = true
				a.Projects = append(a.Projects, eco.Project{Eco: Name, Dir: "."})
				break
			}
		}
	}
	for _, d := range markerDirs {
		a.Projects = append(a.Projects, eco.Project{Eco: Name, Dir: d})
	}
	for d := range fixtureDirs {
		pr := b.project(d)
		b.fixtures[pr] = append(b.fixtures[pr], d)
		if t.IsDir(d + "/files") {
			b.fixtures[pr] = append(b.fixtures[pr], d+"/files")
		}
	}
	for _, fs := range b.fixtures {
		sort.Strings(fs)
	}
	for _, p := range files {
		if skipPath(p) {
			continue
		}
		pr := b.project(path.Dir(p))
		rel := p
		if pr != "." {
			rel = strings.TrimPrefix(p, pr+"/")
		}
		if i := strings.IndexByte(rel, '/'); i > 0 {
			if b.topDirs[pr] == nil {
				b.topDirs[pr] = map[string]bool{}
			}
			b.topDirs[pr][rel[:i]] = true
		}
	}
	for _, d := range append([]string{"."}, markerDirs...) {
		if b.markers[d] && t.Has(path.Join(d, "bin/rails")) && t.Has(path.Join(d, "config/application.rb")) {
			b.rails[d] = true
		}
	}

	// Scan.
	cfg := map[string]*projCfg{}
	for _, p := range b.rbFiles {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		fi := &fileInfo{p: p, proj: b.project(path.Dir(p)), exe: b.exes[p]}
		if fi.exe {
			src, err := t.Read(p)
			if err != nil {
				return nil, fmt.Errorf("read %s: %w", p, err)
			}
			fi.scan = rbscan.Scan(p, src)
		} else {
			res, err := sc.Scan(t, p)
			if err != nil {
				return nil, fmt.Errorf("scan %s: %w", p, err)
			}
			fi.scan = res
		}
		b.infos[p] = fi
		c := cfg[fi.proj]
		if c == nil {
			c = &projCfg{}
			cfg[fi.proj] = c
		}
		c.collect(fi)
	}
	for _, pr := range a.Projects {
		if cfg[pr.Dir] == nil {
			cfg[pr.Dir] = &projCfg{}
		}
		cfg[pr.Dir].readRSpec(t, pr.Dir)
	}

	// Units.
	units := b.units(cfg)
	b.initAutoload(e.rules, units)
	for _, p := range b.rbFiles {
		fi := b.infos[p]
		role := model.RoleSource
		base := path.Base(p)
		// Manifest and configuration roles are exempt from the unreferenced-file rule: only files that really are a project's
		// manifest or configuration get them (a Gemfile below spec/fixtures is test data, a plain source).
		atProject := b.markers[path.Dir(p)] && !underDataDir(p, true)
		switch {
		case units[p] != nil:
			role = model.RoleTest
		case atProject && (base == "Gemfile" || base == "gems.rb" || strings.HasSuffix(base, ".gemspec")):
			role = model.RoleManifest
		case atProject && (base == "Rakefile" || base == "rakefile" || base == "Rakefile.rb") || isTaskFile(p) && !underDataDir(p, true):
			role = model.RoleConfig
		}
		a.Roles[p] = role
		b.fileEdges(fi, units[p] != nil || cucumberFeaturesPath(p, fi.proj))
	}
	var ids []string
	for p := range units {
		ids = append(ids, p)
	}
	sort.Strings(ids)
	for _, p := range ids {
		u := units[p]
		a.Tests = append(a.Tests, *u)
		// Every unit of the project, whatever its runner: a file classified as minitest may still run under RSpec (and the
		// reverse), and an extra dependency only over-selects.
		c := cfg[u.Project]
		for _, r := range c.rspecRequires {
			b.feature(p, r.spec, ".rspec --require", false, model.EdgeImplicit)
		}
		for _, r := range c.rubyOpts {
			b.feature(p, r, "Rake::TestTask ruby_opts -r", false, model.EdgeImplicit)
		}
	}
	// Whole-project units for computed test patterns.
	var projs []string
	for d := range cfg {
		projs = append(projs, d)
	}
	sort.Strings(projs)
	for _, d := range projs {
		c := cfg[d]
		if c.computed == "" {
			continue
		}
		runner := RunnerRake
		if c.computedBy == "rspec" {
			runner = RunnerRSpec
		}
		a.Tests = append(a.Tests, model.TestUnit{Eco: Name, ID: WholeUnitID(d), Project: d, Runner: runner, Whole: true,
			Config: b.gemfileFor(d)})
		a.Unresolved = append(a.Unresolved, model.Unresolved{File: c.computed, Spec: "test pattern", Class: model.UnresolvedUnknown,
			ScopeTier: model.TierNone, Detail: "the test file pattern is computed: the project's whole suite is one unit"})
	}
	b.staleDeclarations()
	b.otherRoles(files)
	b.addCucumber(files)
	slices.SortFunc(a.Edges, func(x, y model.Edge) int {
		if c := strings.Compare(x.From, y.From); c != 0 {
			return c
		}
		if c := strings.Compare(x.To, y.To); c != 0 {
			return c
		}
		return strings.Compare(string(x.Kind), string(y.Kind))
	})
	return a, nil
}

// fixtureIndex returns the length of the prefix of p up to and including its first "fixtures" or "fixture" directory below a
// spec/test/tests directory (0 when there is none).
func fixtureIndex(p string) int {
	off := 0
	inTests := false
	segs := strings.Split(p, "/")
	for i, s := range segs[:len(segs)-1] {
		off += len(s)
		if i > 0 {
			off++
		}
		if s == "spec" || s == "test" || s == "tests" {
			inTests = true
		}
		if inTests && (s == "fixtures" || s == "fixture") {
			return off
		}
	}
	return 0
}

// isRubyScript: the file starts with a shebang naming ruby.
func isRubyScript(t tree.Tree, p string) bool {
	b, err := t.Read(p)
	if err != nil || !bytes.HasPrefix(b, []byte("#!")) {
		return false
	}
	line := b
	if i := bytes.IndexByte(b, '\n'); i >= 0 {
		line = b[:i]
	}
	return bytes.Contains(line, []byte("ruby"))
}

func isTaskFile(p string) bool {
	if path.Ext(p) != ".rake" {
		return false
	}
	for _, s := range strings.Split(path.Dir(p), "/") {
		if s == "tasks" || s == "rakelib" {
			return true
		}
	}
	return false
}

// project returns the project directory of the files in dir.
func (b *builder) project(dir string) string {
	if v, ok := b.projOf[dir]; ok {
		return v
	}
	v := "."
	if b.markers[dir] {
		v = dir
	} else if dir != "." && dir != "" {
		v = b.project(path.Dir(dir))
	}
	b.projOf[dir] = v
	return v
}

// gemfileFor returns the Gemfile bundler uses for a project (the nearest one at or above it).
func (b *builder) gemfileFor(dir string) string {
	for d := dir; ; d = path.Dir(d) {
		if g, ok := b.gemfiles[d]; ok {
			return g
		}
		if d == "." || d == "" {
			return ""
		}
	}
}

func (b *builder) edge(from, to string, kind model.EdgeKind, spec string, line int) {
	if from == to {
		return
	}
	k := edgeKey{from, to, kind}
	if b.seen[k] {
		return
	}
	b.seen[k] = true
	b.a.Edges = append(b.a.Edges, model.Edge{From: from, To: to, Kind: kind, Spec: spec, Line: line, Provenance: model.ProvScan})
}

func (b *builder) unres(from, spec string, class model.UnresolvedClass, line int, detail string) {
	if class == model.UnresolvedDynamic && b.declared(from, line) {
		return
	}
	u := model.Unresolved{File: from, Spec: spec, Class: class, Line: line, Detail: detail}
	switch class {
	case model.UnresolvedDynamic, model.UnresolvedUncertainScan:
		u.ScopeTier, u.ScopeDir, u.TriggerGlobs = model.TierModule, ".", triggers
		// A computed require does not load a test file nothing else references (the runner does); an uncertain scan may have
		// missed a literal reference to it, so it keeps firing on test files.
		u.SkipTests = class == model.UnresolvedDynamic
	default:
		u.ScopeTier = model.TierNone
	}
	b.a.Unresolved = append(b.a.Unresolved, u)
}

// otherRoles gives roles to the non-Ruby files the ecosystem owns.
func (b *builder) otherRoles(files []string) {
	for _, p := range files {
		if skipPath(p) || b.isRuby[p] {
			continue
		}
		base := path.Base(p)
		dir := path.Dir(p)
		atProject := b.markers[dir] && !underDataDir(p, true)
		switch {
		case base == "Gemfile.lock" || base == "gems.locked":
			if atProject {
				b.a.Roles[p] = model.RoleLock
			}
		case base == ".rspec" || base == ".rspec-local" || base == ".rspec-parallel":
			if atProject {
				b.a.Roles[p] = model.RoleConfig
			}
		case base == "spec.opts" && path.Base(dir) == "spec":
			if b.markers[path.Dir(dir)] && !underDataDir(dir, true) {
				b.a.Roles[p] = model.RoleConfig
			}
		case base == ".simplecov":
			if b.markers[b.project(dir)] && !underDataDir(p, true) {
				b.a.Roles[p] = model.RoleConfig
			}
		case (dataExt(p) || testish(p, false)) && b.inRuby(p):
			// Every file below a spec/test directory is test data (a Jekyll test site's .markdown pages are not
			// documentation): as a graph node, a change nothing is known to read widens instead of being skipped as a doc.
			b.a.Roles[p] = model.RoleData
		}
	}
}

func dataExt(p string) bool {
	switch path.Ext(p) {
	case ".erb", ".haml", ".slim", ".builder", ".jbuilder", ".yml", ".yaml":
		return true
	}
	return false
}

// inRuby: p lies in a Ruby project and not in a sub-tree that holds another ecosystem's manifest and no Ruby file.
func (b *builder) inRuby(p string) bool {
	pr := b.project(path.Dir(p))
	if !b.markers[pr] {
		return false
	}
	for d := path.Dir(p); eco.Within(d, pr); d = path.Dir(d) {
		if b.foreign[d] && !b.rbDirs[d] {
			return false
		}
		if d == pr || d == "." {
			break
		}
	}
	return true
}

// testish: a test or a file below a spec/test/tests directory (helpers, support files).
func testish(p string, unit bool) bool {
	if unit {
		return true
	}
	for _, s := range strings.Split(path.Dir(p), "/") {
		if s == "spec" || s == "test" || s == "tests" {
			return true
		}
	}
	return false
}

// escapeGlob escapes the glob metacharacters of a literal path.
func escapeGlob(s string) string {
	if !strings.ContainsAny(s, "*?[]{}\\!") {
		return s
	}
	var sb strings.Builder
	for i := 0; i < len(s); i++ {
		if strings.IndexByte("*?[]{}\\!", s[i]) >= 0 {
			sb.WriteByte('\\')
		}
		sb.WriteByte(s[i])
	}
	return sb.String()
}

// outside: a cleaned repo-relative path that leaves the repository.
func outside(p string) bool {
	return p == ".." || strings.HasPrefix(p, "../") || strings.HasPrefix(p, "/")
}

// declared: a [[dynamic]] declaration describes the computed require at from:line. Its loads become pattern edges (nothing
// for none = true) in place of the Unresolved. sys_path_only declarations do not apply: $LOAD_PATH edits need none (suffix
// union), and an uncertain scan is never replaceable.
func (b *builder) declared(from string, line int) bool {
	for i, d := range b.dyn {
		if d.File != from || d.SysPathOnly || len(d.Lines) > 0 && !slices.Contains(d.Lines, line) {
			continue
		}
		b.dynUsed[i] = true
		for _, g := range d.RawLoads {
			k := edgeKey{from, g, model.EdgePattern}
			if !b.seen[k] {
				b.seen[k] = true
				b.a.Edges = append(b.a.Edges, model.Edge{From: from, To: g, Kind: model.EdgePattern, Line: line,
					Spec: "bgraph.toml [[dynamic]]", Provenance: model.ProvRule})
			}
		}
		return true
	}
	return false
}

// staleDeclarations reports the [[dynamic]] declarations of Ruby files that describe no computed require (other files are
// left to the ecosystems that own them).
func (b *builder) staleDeclarations() {
	for i, d := range b.dyn {
		if b.dynUsed[i] || !b.isRuby[d.File] {
			continue
		}
		why := "matches no computed require of the file (stale declaration?): the widening stays"
		if d.SysPathOnly {
			why = "sys_path_only does not apply to Ruby ($LOAD_PATH edits need no declaration): the declaration is inert"
		}
		b.a.Unresolved = append(b.a.Unresolved, model.Unresolved{File: rules.FileName, Spec: fmt.Sprintf("[[dynamic]] #%d file = %q", i+1, d.File),
			Class: model.UnresolvedUnknown, ScopeTier: model.TierNone, Detail: "[[dynamic]] " + why})
	}
}
