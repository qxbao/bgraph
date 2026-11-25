package dart

import (
	"maps"
	"net/url"
	"slices"
	"strings"

	"github.com/qxbao/bgraph/internal/glob"
	"github.com/qxbao/bgraph/internal/model"
)

type dartNamespace struct {
	names   map[string]string // exposed name → file declaring it
	targets []string          // direct children of an export-only library
}

func exportNode(file string) string { return "<dart-exports:" + file + ">" }

// libraryExports separates a proven export namespace from edits to the export files themselves. Ordinary imports
// keep their dependencies on every export directive file. They also depend on each leaf declaring a name they use.
// The virtual node preserves the full namespace dependency for other readers, such as a re-export with declarations.
func (b *builder) libraryExports() {
	if b.opaqueExportUse || !b.standardTestResolution() {
		return // files can be observed without spelling their Dart names
	}
	memo := map[string]*dartNamespace{}
	barrels := map[string]*dartNamespace{}
	for _, f := range slices.Sorted(maps.Keys(b.namespaces)) {
		if len(b.namespaces[f].Exports) > 0 {
			if ns := b.exportNamespace(f, memo); ns != nil {
				barrels[f] = ns
			}
		}
	}
	if len(barrels) == 0 {
		return
	}

	for f, ns := range barrels {
		node := exportNode(f)
		b.edge(node, f, model.EdgeImplicit, "Dart export directives", 0)
		for _, target := range ns.targets {
			if barrels[target] != nil {
				b.edge(node, exportNode(target), model.EdgeReExport, "Dart export namespace", 0)
			}
		}
	}
	for i, e := range b.a.Edges {
		if e.Kind == model.EdgeReExport && barrels[e.From] != nil {
			if barrels[e.To] == nil {
				b.a.Edges[i].From = exportNode(e.From)
			}
			continue
		}
		if e.Kind == model.EdgePattern {
			pattern, err := glob.Compile(e.To)
			if err == nil {
				for f := range barrels {
					if pattern.Match(f) {
						b.edge(e.From, exportNode(f), model.EdgeReExport, e.Spec, e.Line)
					}
				}
			}
			continue
		}
		ns := barrels[e.To]
		if ns == nil {
			continue
		}
		consumer := b.namespaces[e.From]
		if e.Kind != model.EdgeImport || consumer == nil || len(consumer.Words) == 0 {
			b.edge(e.From, exportNode(e.To), model.EdgeReExport, e.Spec, e.Line)
			continue
		}
		for name, leaf := range ns.names {
			if _, used := slices.BinarySearch(consumer.Words, name); used {
				b.edge(e.From, leaf, model.EdgeReExport, e.Spec, e.Line)
			}
		}
	}
}

// mightObserveExports detects code that can look up Dart names or read Dart source without referring to a name.
// An unindexed package can call such code too. The conventional Pub test API is the runner's own entry point.
func (b *builder) mightObserveExports(p *pkg, spec string) bool {
	u, err := url.PathUnescape(spec)
	if err != nil || u != spec || strings.ContainsRune(u, '\x00') {
		return true // includes escaped spellings of dart:mirrors that need not look like its literal URI
	}
	prefix, name, ok := strings.Cut(u, ":")
	if !ok {
		return false
	}
	switch strings.ToLower(prefix) {
	case "dart":
		switch name {
		case "async", "collection", "convert", "core", "math", "typed_data":
			return false
		}
		return true
	case "package":
		pkg, _, _ := strings.Cut(name, "/")
		return name != "test/test.dart" && !b.r.localPackage(p, pkg)
	default:
		return true
	}
}

// standardTestResolution checks for project-supplied replacements of packages loaded by the conventional test API.
// Every workspace member can override the runner's transitive packages.
func (b *builder) standardTestResolution() bool {
	for _, p := range b.r.pkgs {
		if p.raw == nil || b.t.Has(joinDir(p.dir, "pubspec_overrides.yaml")) {
			return false
		}
		for _, key := range []string{"dependencies", "dev_dependencies", "dependency_overrides"} {
			specs, _ := p.raw[key].(map[string]any)
			for name, spec := range specs {
				if runnerDeps[name] {
					if _, ok := spec.(string); !ok || key == "dependency_overrides" {
						return false
					}
				}
			}
		}
	}
	return true
}

func (b *builder) exportNamespace(f string, memo map[string]*dartNamespace) *dartNamespace {
	if ns, visited := memo[f]; visited {
		return ns
	}
	memo[f] = nil // also stops cycles in export directives
	scan := b.namespaces[f]
	if scan == nil || len(scan.Exports)+len(scan.Standalone) == 0 {
		return nil
	}
	ns := &dartNamespace{names: map[string]string{}}
	for _, name := range scan.Standalone {
		ns.names[name] = f
	}
	for _, ex := range scan.Exports {
		candidates, external, why := b.target(f, ex.URI)
		if external || why != "" || len(candidates) != 1 {
			return nil
		}
		target := candidates[0]
		child := b.exportNamespace(target, memo)
		if child == nil {
			return nil
		}
		for _, name := range ex.Names {
			if child.names[name] == "" {
				return nil
			}
		}
		for name, leaf := range child.names {
			if len(ex.Names) != 0 && slices.Contains(ex.Names, name) == ex.Hide {
				continue
			}
			if ns.names[name] != "" {
				return nil // compiler-visible ambiguity, even for consumers that never use the name
			}
			ns.names[name] = leaf
		}
		ns.targets = append(ns.targets, target)
	}
	memo[f] = ns
	return ns
}
