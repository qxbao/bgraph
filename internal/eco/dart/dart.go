// Package dart is bgraph's Dart and Flutter ecosystem, at test-file granularity (docs/ecosystems/dart.md).
//
// # Projects
//
// A project is a directory with a pubspec.yaml (directories with a hidden path segment, such as .dart_tool or ios/.symlinks,
// are skipped). Pub workspaces (`workspace:` in a root pubspec, `resolution: workspace` in members) share the root's
// pubspec.lock. A package name maps to every directory whose pubspec has that name.
//
// # Test units
//
// One model.TestUnit per test file: Eco "dart", ID = the repo-relative path of the file, Files = [the file], Project = the
// package directory, Runner "dart" or "flutter" (a package that depends on an SDK package of the Flutter SDK). Test files
// are test/**/*_test.dart, plus the files under the top-level `paths:` of dart_test.yaml matching its `filename:`; Flutter
// packages also have integration_test/**/*_test.dart. When dart_test.yaml cannot be read literally (it does not parse,
// includes another file, or sets paths/filename in presets, tags or platform sections), the package has one unit
// ID = WholeUnitID(dir) = "<dart-all-tests:<dir>>" with Whole set and no files, instead of its test/ file units
// (integration_test units stay file units). IsWholeUnitID recognizes it.
//
// # Graph (From depends on To)
//
//   - import / export / part / import augment: the target file (relative URIs; package:<name>/<path> → <dir>/lib/<path> for
//     every directory of that name). dart: URIs and names of no package in the tree are external. Every URI of a
//     conditional directive counts. A missing target is an edge to its path plus an unknown_import at module tier scoped to
//     the importer's package and the target's package (detail generated_output for generated-looking names, unless a
//     [[generated]] entry of bgraph.toml names its sources).
//   - an export-only library may narrow to the names used by the importer when every leaf can be checked independently;
//   - a test depends on every flutter_test_config.dart in its directory and the ancestors up to the package root;
//   - a Flutter integration test depends on the app's platform inputs. Shared tooling, native dependencies and tests
//     that can read those inputs have their own edges;
//   - the test files of a package and of every in-repository package depending on it depend on <dartassets:dir>, which
//     depends on the paths the package's `flutter:` section names (assets, fonts, shaders): Flutter bundles them;
//   - when a package of the tree is one the test runners load themselves (path, args, collection...: runnerDeps), the tests
//     of every package that may resolve it locally depend on <dartrunner:dir>, which depends on its lib/;
//   - every .dart file of a package with a native-assets hook depends on hook/build.dart and hook/link.dart;
//   - string literals that name a file or directory relative to the file, the package or the repository root
//     (fixture_data, a directory as a <dir>/** pattern), package: URIs in literals, interpolated literals with a static
//     directory prefix (pattern) or a static tail with a directory (a **/ pattern); a process launch depends on the
//     bin/ of the package and of every in-repository package it depends on (dart run pkg:tool);
//   - Isolate.spawnUri / loadUri with a literal URI: the file; a computed one is a dynamic_import at module tier scoped to the
//     package; an uncertain scan is an uncertain_scan scoped to the repository.
package dart

import (
	"context"
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/qxbao/bgraph/internal/eco"
	"github.com/qxbao/bgraph/internal/model"
	"github.com/qxbao/bgraph/internal/rules"
	"github.com/qxbao/bgraph/internal/tree"
)

// Name is the ecosystem name.
const Name = "dart"

// Runner names of the units.
const (
	RunnerDart    = "dart"
	RunnerFlutter = "flutter"
)

// AnalyzerVersion: bump whenever the analysis rules change to invalidate stored analysis snapshots.
const AnalyzerVersion = "dart-13"

// Eco is the Dart ecosystem.
type Eco struct {
	rules *rules.File // [[generated]]; may be nil
}

// New creates the Dart ecosystem.
func New(r *rules.File) *Eco { return &Eco{rules: r} }

func (*Eco) Name() string    { return Name }
func (*Eco) Version() string { return AnalyzerVersion }

const wholePrefix = "<dart-all-tests:"

// WholeUnitID is the ID of the "whole test suite of package dir" unit.
func WholeUnitID(dir string) string { return wholePrefix + dir + ">" }

// IsWholeUnitID reports whether id is a WholeUnitID.
func IsWholeUnitID(id string) bool {
	return strings.HasPrefix(id, wholePrefix) && strings.HasSuffix(id, ">") && len(id) > len(wholePrefix)+1
}

type edgeKey struct {
	from, to string
	kind     model.EdgeKind
}

type builder struct {
	namespaces       map[string]*model.DartScan
	opaqueExportUse  bool
	trackHostReaders bool
	hostReaders      []string
	t                tree.Tree
	sc               eco.Scanner
	r                *repo
	a                *eco.Analysis
	rules            *rules.File
	seen             map[edgeKey]bool
	useq             map[string]bool
	hooks            map[string][]string // package dir → its hook files
	units            map[string][]string // package dir → its file units
}

// Analyze analyzes tree t.
func (e *Eco) Analyze(ctx context.Context, t tree.Tree, sc eco.Scanner) (*eco.Analysis, error) {
	a := &eco.Analysis{}
	r := loadRepo(t)
	if r == nil {
		return a, nil
	}
	a.Roles = map[string]model.Role{}
	b := &builder{t: t, sc: sc, r: r, a: a, rules: e.rules, seen: map[edgeKey]bool{}, useq: map[string]bool{}, hooks: map[string][]string{}, units: map[string][]string{}}
	b.namespaces = map[string]*model.DartScan{}
	dirs := slices.Sorted(maps.Keys(r.pkgs))
	for _, d := range dirs {
		a.Projects = append(a.Projects, eco.Project{Eco: Name, Dir: d, Name: r.pkgs[d].name})
		for _, h := range []string{"hook/build.dart", "hook/link.dart"} {
			if f := joinDir(d, h); t.Has(f) {
				b.hooks[d] = append(b.hooks[d], f)
			}
		}
	}
	files := map[string][]string{} // package dir → its .dart files
	hostFiles := map[string]*flutterHostFiles{}
	flutterAncestors := r.flutterAncestors()
	var stray []string
	for _, f := range t.Files() {
		p := r.nearest(path.Dir(f))
		if p != nil {
			addFlutterHostPath(hostFiles, flutterAncestors[p.dir], p, f, false)
		}
		if path.Ext(f) == ".dart" {
			if p == nil {
				stray = append(stray, f)
			} else {
				files[p.dir] = append(files[p.dir], f)
			}
			a.Roles[f] = model.RoleSource
			continue
		}
		if role, ok := fileRole(f, p, r); ok {
			a.Roles[f] = role
		}
	}
	for _, link := range tree.LinksOf(t) {
		if link.Dir || link.Problem != "" {
			if p := r.nearest(path.Dir(link.Path)); p != nil {
				addFlutterHostPath(hostFiles, flutterAncestors[p.dir], p, link.Path, true)
			}
		}
	}
	b.trackHostReaders = len(hostFiles) > 0
	for _, d := range dirs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		b.pkg(r.pkgs[d], files[d])
	}
	for _, f := range stray {
		b.references(f, nil)
	}
	b.assets()
	b.runnerPackages()
	b.flutterHosts(hostFiles)
	b.libraryExports()
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

// fileRole is the role of a non-Dart file the ecosystem owns.
func fileRole(f string, p *pkg, r *repo) (model.Role, bool) {
	base := path.Base(f)
	switch base {
	case "analysis_options.yaml", "melos.yaml":
		return model.RoleConfig, true
	}
	if p == nil || path.Dir(f) != p.dir {
		return "", false
	}
	switch {
	case base == "pubspec.yaml" && r.pkgs[p.dir] == p, base == "pubspec_overrides.yaml":
		return model.RoleManifest, true
	case base == "pubspec.lock":
		return model.RoleLock, true
	case base == "dart_test.yaml", base == "l10n.yaml", isBuildYAML(base):
		return model.RoleConfig, true
	}
	return "", false
}

// isBuildYAML: build.yaml or a build.<name>.yaml configuration of build_runner.
func isBuildYAML(base string) bool {
	return base == "build.yaml" || strings.HasPrefix(base, "build.") && strings.HasSuffix(base, ".yaml") && strings.Count(base, ".") == 2
}

func runnerOf(p *pkg) string {
	if p.flutter {
		return RunnerFlutter
	}
	return RunnerDart
}

func rel(dir, f string) string {
	if dir == "." {
		return f
	}
	return strings.TrimPrefix(f, dir+"/")
}

// pkg builds the units and edges of package p, whose .dart files are files.
func (b *builder) pkg(p *pkg, files []string) {
	cfg := loadTestCfg(b.t, p.dir)
	runner := runnerOf(p)
	var units []string
	for _, f := range files {
		rp := rel(p.dir, f)
		integration := p.flutter && within(rp, "integration_test") && defaultFilename.Match(path.Base(rp))
		if !integration && !cfg.isTest(rp) {
			continue
		}
		b.a.Roles[f] = model.RoleTest
		if integration || cfg.opaque == "" {
			units = append(units, f)
			b.a.Tests = append(b.a.Tests, model.TestUnit{Eco: Name, ID: f, Project: p.dir, Files: []string{f}, Runner: runner})
		}
	}
	b.units[p.dir] = units
	if cfg.opaque != "" {
		b.a.Tests = append(b.a.Tests, model.TestUnit{Eco: Name, ID: WholeUnitID(p.dir), Project: p.dir, Runner: runner, Whole: true})
		b.a.Unresolved = append(b.a.Unresolved, model.Unresolved{File: joinDir(p.dir, "dart_test.yaml"), Spec: "paths", Class: model.UnresolvedUnknown,
			ScopeTier: model.TierNone, Detail: cfg.opaque + ": the package's whole suite is one unit"})
	}
	for _, f := range units {
		for d := path.Dir(f); within(d, p.dir); d = path.Dir(d) {
			if c := joinDir(d, "flutter_test_config.dart"); c != f && b.t.Has(c) {
				b.edge(f, c, model.EdgeImplicit, "flutter_test_config.dart", 0)
			}
			if d == p.dir || d == "." {
				break
			}
		}
	}
	for _, f := range files {
		if !within(f, joinDir(p.dir, "hook")) {
			for _, h := range b.hooks[p.dir] {
				b.edge(f, h, model.EdgeImplicit, "native assets build hook", 0)
			}
		}
		b.references(f, p)
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

func (b *builder) unresolved(u model.Unresolved) {
	k := u.File + "\x00" + u.Spec + "\x00" + u.ScopeDir + "\x00" + string(u.Class)
	if b.useq[k] {
		return
	}
	b.useq[k] = true
	b.a.Unresolved = append(b.a.Unresolved, u)
}
