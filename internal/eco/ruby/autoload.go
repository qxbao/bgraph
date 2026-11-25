package ruby

import (
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/qxbao/bgraph/internal/dynamic/rbdyn"
	"github.com/qxbao/bgraph/internal/glob"
	"github.com/qxbao/bgraph/internal/model"
	"github.com/qxbao/bgraph/internal/rules"
	rbscan "github.com/qxbao/bgraph/internal/scan/ruby"
	"github.com/qxbao/bgraph/internal/tree"
)

const autoloadDynamic = "<rb-autoload:dynamic>"

type autoloadTargets struct {
	byFile map[string]bool
	byName map[string][]string
}

// autoloadName folds casing and underscores so acronyms and ambiguous namespace lookups keep every possible candidate.
func autoloadName(s string) string {
	return strings.ToLower(strings.ReplaceAll(s, "_", ""))
}

func autoloadDeclared(rf *rules.File, p string) bool {
	if rf == nil || len(rf.RubyAutoload) == 0 || path.Ext(p) != ".rb" || isMarker(path.Base(p)) || underDataDir(p, true) {
		return false
	}
	base := strings.TrimSuffix(path.Base(p), ".rb")
	if base == "" || base[0] < 'a' || base[0] > 'z' {
		return false
	}
	for _, c := range base {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_') {
			return false
		}
	}
	for _, a := range rf.RubyAutoload {
		for _, g := range a.Paths {
			if g.Match(p) {
				return true
			}
		}
	}
	return false
}

func (b *builder) initAutoload(rf *rules.File, units map[string]*model.TestUnit) {
	if rf == nil || len(rf.RubyAutoload) == 0 {
		return
	}
	targets := &autoloadTargets{byFile: map[string]bool{}, byName: map[string][]string{}}
	aliases := tree.AliasesOf(b.t)
	for _, p := range b.rbFiles {
		if units[p] != nil || aliases[p] != "" || !autoloadDeclared(rf, p) {
			continue
		}
		targets.byFile[p] = true
		name := autoloadName(strings.TrimSuffix(path.Base(p), ".rb"))
		targets.byName[name] = append(targets.byName[name], p)
	}
	if len(targets.byFile) != 0 {
		b.autoload = targets
	}
}

func (b *builder) constantEdges(from string) {
	if b.autoload == nil {
		return
	}
	src, err := b.t.Read(from)
	if err != nil {
		b.dynamicConstant(from, 0)
		return
	}
	names, dynamicLine := rbscan.AutoloadReferences(src)
	if dynamicLine != 0 {
		b.dynamicConstant(from, max(0, dynamicLine))
	}
	for _, name := range slices.Sorted(maps.Keys(names)) {
		for _, target := range b.autoload.byName[autoloadName(name)] {
			b.edge(from, target, model.EdgeImport, "Ruby constant "+name, names[name])
		}
	}
}

func (b *builder) dynamicConstant(from string, line int) {
	b.edge(from, autoloadDynamic, model.EdgeImport, "indirect Ruby constant lookup", line)
	if b.reqDone[autoloadDynamic] {
		return
	}
	b.reqDone[autoloadDynamic] = true
	for p := range b.autoload.byFile {
		b.edge(autoloadDynamic, p, model.EdgeImport, "possible target of indirect Ruby constant lookup", 0)
	}
}

// autoloadEdge keeps the original glob unless it covers a declared lazy file. Expand that glob in each tree so only the
// declared files lose the blanket edge. The base/head union still sees added and removed files; ClassifyChange widens on
// layout changes that could change how the loader resolves names.
func (b *builder) autoloadEdge(from, pattern, spec string, line int) {
	if b.autoload != nil {
		if g, err := glob.Compile(pattern); err == nil {
			for p := range b.autoload.byFile {
				if g.Match(p) {
					for _, target := range b.t.Files() {
						if !b.autoload.byFile[target] && g.Match(target) {
							b.edge(from, target, model.EdgeImplicit, spec, line)
						}
					}
					return
				}
			}
		}
	}
	b.edge(from, pattern, model.EdgePattern, spec, line)
}

func (e *Eco) autoloadChange(p string, base, head tree.Tree) string {
	if !autoloadDeclared(e.rules, p) {
		return ""
	}
	if !base.Has(p) || !head.Has(p) {
		return "a declared Ruby autoload target was added or removed"
	}
	before, beforeErr := base.Read(p)
	after, afterErr := head.Read(p)
	if beforeErr != nil || afterErr != nil {
		return "a declared Ruby autoload target could not be read"
	}
	_, why := rbdyn.BodyGuard(p, before, after)
	return why
}
