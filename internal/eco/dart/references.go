package dart

import (
	"fmt"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/qxbao/bgraph/internal/model"
)

// dartTriggers: the files a computed load can load.
var dartTriggers = []string{"**/*.dart"}

// generatedName: file names of code generators (build_runner builders, protoc, mockito, auto_route, injectable...).
var generatedName = regexp.MustCompile(`\.(g|freezed|mocks|gr|config|gen|pb|pbenum|pbjson|pbserver|pbgrpc|chopper|graphql|reflectable|template)\.dart$`)

var scheme = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*:`)

// references adds the edges and unresolved imports of Dart file f of package p (nil: a file outside every package).
func (b *builder) references(f string, p *pkg) {
	scope := "."
	if p != nil {
		scope = p.dir
	}
	res, err := b.sc.Scan(b.t, f)
	if b.trackHostReaders && (err != nil || res.Uncertain || slices.ContainsFunc(res.Imports, func(ref model.ImportRef) bool {
		return b.mayReadFlutterHost(f, ref, res.Dart)
	})) {
		b.hostReaders = append(b.hostReaders, f)
	}
	if err == nil && res.Dart != nil {
		b.namespaces[f] = res.Dart
	}
	if err != nil || res.Uncertain {
		b.opaqueExportUse = true
		detail := res.UncertainReason
		if err != nil {
			detail = err.Error()
		}
		// The unread part of the file may name any file of the repository.
		b.unresolved(model.Unresolved{File: f, Spec: "*", Class: model.UnresolvedUncertainScan, ScopeTier: model.TierModule, ScopeDir: ".",
			Detail: detail})
	}
	for _, im := range res.Imports {
		switch im.Kind {
		case model.ImportDartImport, model.ImportDartExport, model.ImportDartPart:
			b.opaqueExportUse = b.opaqueExportUse || b.mightObserveExports(p, im.Spec)
			kind := model.EdgeImport
			switch im.Kind {
			case model.ImportDartExport:
				kind = model.EdgeReExport
			case model.ImportDartPart:
				kind = model.EdgeInclude
			}
			b.directive(f, scope, im, kind)
		case model.ImportDartLoad:
			b.opaqueExportUse = true
			if im.Dynamic {
				b.unresolved(model.Unresolved{File: f, Spec: im.Spec, Class: model.UnresolvedDynamic, Line: im.Line, ScopeTier: model.TierModule,
					ScopeDir: scope, TriggerGlobs: dartTriggers, SkipTests: true, Detail: "code loaded from a computed URI"})
				continue
			}
			if strings.HasPrefix(im.Spec, "dart:") {
				continue
			}
			// A relative URI is resolved against the script or the working directory (the package when tests run): every
			// candidate base counts.
			if !b.literal(f, p, im.Spec, model.EdgeImport, im.Line) {
				b.unresolved(model.Unresolved{File: f, Spec: im.Spec, Class: model.UnresolvedUnknown, Line: im.Line, ScopeTier: model.TierModule,
					ScopeDir: scope, TriggerGlobs: dartTriggers, Detail: "loaded file not found in the tree"})
			}
		case model.ImportDartProcess:
			if p != nil {
				// `dart run pkg:tool` runs an executable of any package in the resolution: the package's own and every
				// in-repository package it depends on.
				for _, d := range b.dependenciesOf(p.dir) {
					if bin := joinDir(d, "bin"); b.t.IsDir(bin) {
						b.edge(f, escapeGlob(bin)+"/**", model.EdgePattern, "process launch: executables of "+d, im.Line)
					}
				}
			}
		case model.ImportDartString:
			if im.Dynamic {
				b.dynamicLiteral(f, p, im)
			} else {
				b.literal(f, p, im.Spec, model.EdgeFixtureData, im.Line)
			}
		}
	}
}

// target resolves a directive URI. It returns the candidate files (every directory of a package name), whether the URI
// is external (dart:, or a package not in the tree) and, when it cannot be resolved at all, why.
func (b *builder) target(from, uri string) (cands []string, external bool, why string) {
	switch {
	case strings.HasPrefix(uri, "dart:"):
		return nil, true, ""
	case strings.HasPrefix(uri, "package:"):
		rest := strings.TrimPrefix(uri, "package:")
		name, file, ok := strings.Cut(rest, "/")
		if !ok || name == "" || file == "" {
			return nil, false, "malformed package URI"
		}
		file, err := url.PathUnescape(file)
		if err != nil {
			return nil, false, "malformed package URI"
		}
		dirs := b.r.byName[name]
		if len(dirs) == 0 {
			if name == "flutter_gen" {
				return nil, false, "generated_output: the synthetic flutter_gen package (gen-l10n) is not in the tree"
			}
			// A package whose pubspec has no readable name may be this one: its file, when it exists, is a dependency.
			for _, d := range b.r.unnamed {
				if c, ok := resolveRel(joinDir(d, "lib"), file); ok && within(c, joinDir(d, "lib")) && b.t.Has(c) {
					cands = append(cands, c)
				}
			}
			return cands, len(cands) == 0, ""
		}
		for _, d := range dirs {
			if c, ok := resolveRel(joinDir(d, "lib"), file); ok && within(c, joinDir(d, "lib")) {
				cands = append(cands, c)
			}
		}
		if len(cands) == 0 {
			return nil, false, "package URI leaves the package's lib directory"
		}
		return cands, false, ""
	case scheme.MatchString(uri):
		return nil, false, "unsupported URI scheme"
	}
	u, err := url.PathUnescape(uri)
	if err != nil {
		return nil, false, "malformed URI"
	}
	if c, ok := resolveRel(path.Dir(from), u); ok {
		cands = append(cands, c)
	}
	// A library under lib/ is usually loaded by its package: URI, where Dart resolves ".." segments without leaving the
	// package (package:p/a/b.dart + ../../x.dart is package:p/x.dart). Loaded by a file URI it resolves as a path: both count.
	if c, ok := b.packageRelative(from, u); ok && !slices.Contains(cands, c) {
		cands = append(cands, c)
	}
	if len(cands) == 0 {
		return nil, false, "URI leaves the repository or is absolute"
	}
	return cands, false, ""
}

// packageRelative resolves relative URI u of a file under its package's lib/ directory the way Dart resolves it against the
// file's package: URI: ".." never climbs above lib/. False when from is not under a lib/ directory or u is not relative.
func (b *builder) packageRelative(from, u string) (string, bool) {
	u = strings.ReplaceAll(u, "\\", "/")
	if u == "" || path.IsAbs(u) || strings.Contains(u, ":") {
		return "", false
	}
	p := b.r.nearest(path.Dir(from))
	if p == nil {
		return "", false
	}
	lib := joinDir(p.dir, "lib")
	if !within(from, lib) || from == lib {
		return "", false
	}
	var segs []string
	if d := strings.TrimPrefix(path.Dir(from), lib); d != "" {
		segs = strings.Split(strings.TrimPrefix(d, "/"), "/")
	}
	for _, s := range strings.Split(u, "/") {
		switch s {
		case "", ".":
		case "..":
			if len(segs) > 0 {
				segs = segs[:len(segs)-1]
			}
		default:
			segs = append(segs, s)
		}
	}
	if len(segs) == 0 {
		return "", false
	}
	return joinDir(lib, strings.Join(segs, "/")), true
}

// directive adds the edges of an import, export, part or literal load URI.
func (b *builder) directive(from, scope string, im model.ImportRef, kind model.EdgeKind) {
	cands, external, why := b.target(from, im.Spec)
	if external {
		return
	}
	if why != "" {
		b.missing(from, scope, im, nil, why)
		return
	}
	found := false
	for _, c := range cands {
		if b.t.Has(c) {
			found = true
			b.edge(from, c, kind, im.Spec, im.Line)
		} else {
			b.opaqueExportUse = true // an alternative may resolve to code outside the index
		}
	}
	if found {
		return
	}
	// Not in the tree: the exact paths catch the file when it is added; the unresolved import covers what builds it.
	for _, c := range cands {
		b.edge(from, c, kind, im.Spec, im.Line)
	}
	detail := "import target not in the tree"
	if generatedName.MatchString(cands[0]) {
		detail = "generated_output: " + path.Base(cands[0]) + " is a generated file that is not in the tree"
	}
	b.missing(from, scope, im, cands, detail)
}

// missing records an import whose target is not in the tree: unless a [[generated]] entry names the files it is built from,
// any change in the importer's package (or the target's package) seeds the importer.
func (b *builder) missing(from, scope string, im model.ImportRef, cands []string, detail string) {
	b.opaqueExportUse = true // even a generated target with known inputs has opaque behavior
	if b.rules != nil && len(cands) > 0 {
		var srcs []string
		for _, c := range cands {
			if g := b.rules.GeneratedFor(c); g != nil {
				srcs = append(srcs, g.RawSources...)
			}
		}
		if len(srcs) > 0 {
			for _, s := range srcs {
				k := edgeKey{from, s, model.EdgePattern}
				if !b.seen[k] {
					b.seen[k] = true
					b.a.Edges = append(b.a.Edges, model.Edge{From: from, To: s, Kind: model.EdgePattern, Line: im.Line, Spec: im.Spec, Provenance: model.ProvRule})
				}
			}
			return
		}
	}
	scopes := []string{scope}
	for _, c := range cands {
		if q := b.r.nearest(path.Dir(c)); q != nil && q.dir != scope {
			scopes = append(scopes, q.dir)
		}
	}
	for _, s := range scopes {
		b.unresolved(model.Unresolved{File: from, Spec: im.Spec, Class: model.UnresolvedUnknown, Line: im.Line, ScopeTier: model.TierModule,
			ScopeDir: s, Detail: detail})
	}
}

// bareName: a literal that is only a name, or ./name (a working-directory file such as an executable name, too weak to
// point at a file).
func bareName(s string) bool {
	s = strings.TrimPrefix(s, "./")
	return !strings.Contains(s, "/") && !strings.Contains(s, ".")
}

// literal adds edges from f to what the literal path s names, relative to f's directory, its package and the repository
// root. It reports whether anything was found.
func (b *builder) literal(f string, p *pkg, s string, kind model.EdgeKind, line int) bool {
	if strings.HasPrefix(s, "package:") {
		cands, _, why := b.target(f, s)
		found := false
		if why == "" {
			for _, c := range cands {
				if b.t.Has(c) {
					b.edge(f, c, kind, s, line)
					found = true
				} else if b.t.IsDir(c) {
					b.edge(f, escapeGlob(c)+"/**", model.EdgePattern, s, line)
					found = true
				}
			}
		}
		return found
	}
	s = strings.ReplaceAll(strings.TrimPrefix(s, "file://"), "\\", "/")
	if s == "" || path.IsAbs(s) || scheme.MatchString(s) || bareName(s) || onlyDots(s) {
		return false
	}
	found := false
	for _, base := range bases(f, p) {
		c, ok := resolveRel(base, s)
		if !ok || c == "." || c == f {
			continue
		}
		if b.t.Has(c) {
			b.edge(f, c, kind, s, line)
			found = true
		}
		if b.t.IsDir(c) {
			b.edge(f, escapeGlob(c)+"/**", model.EdgePattern, s, line)
			found = true
		}
	}
	return found
}

// onlyDots: a path that names the working directory or one of its parents once cleaned ("..", "a/../..", "./"): as a
// directory it would stand for a whole package or more, which a literal in code almost never means.
func onlyDots(s string) bool {
	for _, seg := range strings.Split(path.Clean(strings.Trim(s, "/")), "/") {
		if seg != "." && seg != ".." && seg != "" {
			return false
		}
	}
	return true
}

func bases(f string, p *pkg) []string {
	out := []string{path.Dir(f)}
	if p != nil && p.dir != path.Dir(f) {
		out = append(out, p.dir)
	}
	if out[len(out)-1] != "." && path.Dir(f) != "." {
		out = append(out, ".")
	}
	return out
}

// dynamicLiteral adds pattern edges for an interpolated literal: below its static directory prefix, or any path ending in
// its static tail when the tail holds a directory.
func (b *builder) dynamicLiteral(f string, p *pkg, im model.ImportRef) {
	head, tail := strings.ReplaceAll(im.Spec, "\\", "/"), strings.ReplaceAll(im.Suffix, "\\", "/")
	if i := strings.LastIndex(head, "/"); i >= 0 {
		dir, name := head[:i], head[i+1:]
		if dir == "" || onlyDots(dir) || path.IsAbs(head) || scheme.MatchString(head) {
			return
		}
		for _, base := range bases(f, p) {
			d, ok := resolveRel(base, dir)
			if !ok || d == "." || !b.t.IsDir(d) {
				continue
			}
			g := escapeGlob(d) + "/" + escapeGlob(name) + "*"
			b.edge(f, g+escapeGlob(tail), model.EdgePattern, fmt.Sprintf("%s${...}%s", im.Spec, im.Suffix), im.Line)
			b.edge(f, g+"/**", model.EdgePattern, fmt.Sprintf("%s${...}%s", im.Spec, im.Suffix), im.Line)
		}
		return
	}
	t := strings.TrimPrefix(tail, "/")
	if !strings.Contains(t, "/") || onlyDots(t) {
		return
	}
	g := "**/*" + escapeGlob(tail)
	if strings.HasPrefix(tail, "/") {
		g = "**/" + escapeGlob(t)
	}
	b.edge(f, g, model.EdgePattern, fmt.Sprintf("%s${...}%s", im.Spec, im.Suffix), im.Line)
	b.edge(f, g+"/**", model.EdgePattern, fmt.Sprintf("%s${...}%s", im.Spec, im.Suffix), im.Line)
}

func escapeGlob(s string) string {
	var sb strings.Builder
	for _, c := range s {
		if strings.ContainsRune("*?[]{}\\!()", c) {
			sb.WriteByte('\\')
		}
		sb.WriteRune(c)
	}
	return sb.String()
}
