package rbdyn

import (
	"fmt"
	"path"
	"slices"
	"sort"
	"strings"

	"github.com/qxbao/bgraph/internal/classify"
	"github.com/qxbao/bgraph/internal/dynamic"
	"github.com/qxbao/bgraph/internal/model"
	"github.com/qxbao/bgraph/internal/tree"
)

// Kind is how the Ruby layer treats a changed file (docs/dynamic-layer.md §10.2).
type Kind byte

const (
	// KindMethods: library code (or test support) whose changes all lie inside method bodies: a unit is kept when it ran one of
	// the changed methods, read the file as data, or loaded it without a method table.
	KindMethods Kind = 'M'
	// KindFile: a test file, or test support the body guard refused: every unit that loaded or read it is kept.
	KindFile Kind = 'F'
	// KindDoc: an unreferenced documentation file: every unit that read it is kept.
	KindDoc Kind = 'D'
)

// Changed is one changed file as the Ruby layer treats it.
type Changed struct {
	Path    string
	Kind    Kind
	Members []Span // KindMethods: the changed methods (base line spans)
	Note    string // KindFile: why (a test file, or the body guard's refusal of a support file)
}

// testDir reports whether p lies below a spec, test or tests directory.
func testDir(p string) bool {
	for _, s := range strings.Split(path.Dir(p), "/") {
		if s == "spec" || s == "test" || s == "tests" {
			return true
		}
	}
	return false
}

// docFile: a documentation or harmless file that no ecosystem gives a role, that nothing in the graph references and that no
// bgraph.toml entry names (dynamic.docFile).
func docFile(c dynamic.Change) bool {
	return (classify.Doc(c.Path) || classify.Harmless(c.Path)) && !c.Referenced && !c.OtherRole && !c.Rule && !c.Declared &&
		!c.Global && !c.Claimed && c.BaseRole == "" && c.HeadRole == ""
}

// Guard returns "" when the Ruby layer may narrow sel at all (docs/dynamic-layer.md §10.2), otherwise the reason, and how each
// changed file is treated. changes are classified as for dynamic.Guard, with roles of the ruby ecosystem and OtherRole meaning
// a role in another ecosystem. base and head are the trees of the selection.
func Guard(sel model.Selection, changes []dynamic.Change, base, head tree.Tree) ([]Changed, string) {
	if sel.Scope == model.TierFull {
		return nil, "the selection is full (never narrowed)"
	}
	for _, w := range sel.Widening {
		if w.Tier == model.TierFull || w.Reason == model.ReasonGlobalInput {
			return nil, fmt.Sprintf("widening %s on %s is global or full", w.Reason, w.Path)
		}
	}
	if len(changes) == 0 || len(changes) != len(sel.Changed) {
		return nil, "the change set is not classified"
	}
	if base == nil || head == nil {
		return nil, "no base or head tree"
	}
	var out []Changed
	for _, c := range changes {
		var why string
		switch {
		case c.Status != 'M':
			why = fmt.Sprintf("status %c (only modifications of existing files are observable: an addition can shadow a file, a deletion or rename changes loading)", c.Status)
		case c.OldMode != c.NewMode:
			why = fmt.Sprintf("mode changed %s -> %s", c.OldMode, c.NewMode)
		case c.NewMode != "100644" && c.NewMode != "100755":
			why = "mode " + c.NewMode + " is not a regular file"
		case c.Linked:
			why = "read through a symbolic link (the recorded path may be the link's)"
		case c.Global:
			why = "global input"
		case c.Declared:
			why = "named by a bgraph.toml [[config]] or [[dynamic]] declaration"
		case c.Rule:
			why = "named by a bgraph.toml [[rule]] (its targets are not narrowed)"
		case c.Claimed:
			why = "claimed as a manifest, configuration, lockfile or build input"
		case path.Ext(c.Path) == ".rb":
			switch {
			case c.BaseRole == model.RoleTest && c.HeadRole == model.RoleTest:
				out = append(out, Changed{Path: c.Path, Kind: KindFile, Note: "test file"})
			case c.BaseRole == model.RoleSource && c.HeadRole == model.RoleSource:
				b, err1 := base.Read(c.Path)
				h, err2 := head.Read(c.Path)
				if err1 != nil || err2 != nil {
					why = "cannot read both versions"
					break
				}
				spans, bw := BodyGuard(c.Path, b, h)
				switch {
				case bw == "":
					out = append(out, Changed{Path: c.Path, Kind: KindMethods, Members: spans})
				case testDir(c.Path):
					out = append(out, Changed{Path: c.Path, Kind: KindFile, Note: "test support refused by the body guard: " + bw})
				default:
					why = bw
				}
			default:
				why = fmt.Sprintf("role %s/%s at base/head is not source or test", roleName(c.BaseRole), roleName(c.HeadRole))
			}
		case docFile(c):
			out = append(out, Changed{Path: c.Path, Kind: KindDoc})
		default:
			why = "not a Ruby source file (.rb) nor an unreferenced documentation file (data, templates, .rake, .ru, native extensions and executables refuse)"
		}
		if why != "" {
			if !strings.HasPrefix(why, c.Path) {
				why = c.Path + ": " + why
			}
			return nil, why
		}
	}
	return out, ""
}

func roleName(r model.Role) string {
	if r == "" {
		return "none"
	}
	return string(r)
}

// Params is what Narrow needs besides the selection.
type Params struct {
	Env     string
	Records []dynamic.Record
	Base    tree.Tree
	UnitEco map[string]string
	Changed []Changed // Guard's classification
}

// Narrowable reports whether the Ruby layer records this unit's runner and granularity.
func Narrowable(t model.SelectedTest, eco string) bool {
	return eco == Eco && !t.Whole && path.Ext(t.Unit) != ".feature"
}

// Latest returns the latest Ruby record per unit for environment env.
func Latest(recs []dynamic.Record, env string) map[string]dynamic.Record {
	byUnit := map[string]dynamic.Record{}
	for _, r := range recs {
		if r.Eco != Eco || r.Env != env {
			continue
		}
		if cur, ok := byUnit[r.Unit]; !ok || !r.Recorded.Before(cur.Recorded) {
			byUnit[r.Unit] = r
		}
	}
	return byUnit
}

// keepReason returns why rec (usable) keeps its unit for the changed files, "" when nothing it executed, loaded or read changed.
func keepReason(rec dynamic.Record, changed []Changed) string {
	for _, c := range changed {
		if _, ok := rec.Deps[c.Path]; !ok {
			continue // neither loaded nor read: none of its code ran
		}
		switch c.Kind {
		case KindFile:
			return "loaded the changed file " + c.Path + " (" + c.Note + ")"
		case KindDoc:
			return "read the changed file " + c.Path
		}
		if slices.Contains(rec.Reads, c.Path) {
			return "read the changed file " + c.Path + " as data"
		}
		if len(c.Members) == 0 {
			continue // comments or layout only, in a file loaded as code
		}
		tbl, ok := rec.Methods[c.Path]
		if !ok {
			return "loaded " + c.Path + " without a method table (loaded before the collector started): the change cannot be judged"
		}
		ms, ok := ParseMethods(tbl)
		if !ok {
			return "malformed method table for " + c.Path
		}
		for _, s := range c.Members {
			found, ran := false, false
			for _, m := range ms {
				if m.Start == s.From && m.End == s.To {
					found = true
					ran = ran || m.Count > 0
				}
			}
			if !found {
				return fmt.Sprintf("%s:%d: the changed %s is not in the recorded method table (defined by eval or not covered): the change cannot be judged", c.Path, s.From, s.What)
			}
			if ran {
				return fmt.Sprintf("executed the changed %s (%s:%d)", s.What, c.Path, s.From)
			}
		}
	}
	return ""
}

// Narrow applies the Ruby method layer to sel (docs/dynamic-layer.md §10.1): every selected Ruby test file with a usable record
// that ran none of the changed methods and loaded or read none of the other changed files is moved from Tests to Narrowed.
// Kept Ruby units get a Dynamic note saying why. Call it only after Guard passed.
func Narrow(sel model.Selection, p Params) model.Selection {
	bp := dynamic.Params{Eco: Eco, Collector: CollectorID, Env: p.Env, BaseOID: func(f string) string {
		if !p.Base.Has(f) {
			return ""
		}
		return p.Base.Key(f)
	}}
	byUnit := Latest(p.Records, p.Env)
	changed := map[string]bool{}
	for _, c := range sel.Changed {
		changed[c] = true
	}
	always := dynamic.AlwaysRun(sel)
	var methods []string
	for _, c := range p.Changed {
		for _, s := range c.Members {
			methods = append(methods, fmt.Sprintf("%s:%d", c.Path, s.From))
		}
	}
	out := sel
	out.Tests = nil
	out.Narrowed = slices.Clone(sel.Narrowed)
	removed := 0
	for _, t := range sel.Tests {
		if p.UnitEco[t.Unit] != Eco {
			out.Tests = append(out.Tests, t)
			continue
		}
		keep := ""
		files := t.Files
		if len(files) == 0 {
			files = []string{t.Unit}
		}
		rec, ok := byUnit[t.Unit]
		switch {
		case t.Whole:
			keep = "whole-project unit: no per-file record"
		case slices.ContainsFunc(files, func(f string) bool { return changed[f] }):
			keep = "the test file itself changed"
		case dynamic.AlwaysRunUnit(always, t.Unit):
			keep = "always run (previously failed or new)"
		case !ok:
			keep = "no record for the current environment"
		default:
			if why := dynamic.Usable(rec, files, bp); why != "" {
				keep = "record not usable: " + why
			} else {
				keep = keepReason(rec, p.Changed)
			}
		}
		if keep != "" {
			t.Dynamic = "kept by the Ruby method layer: " + keep
			out.Tests = append(out.Tests, t)
			continue
		}
		what := "none of the changed files"
		if len(methods) > 0 {
			what = fmt.Sprintf("none of the %d changed methods (%s)", len(methods), strings.Join(firstN(methods, 5), ", "))
		}
		out.Narrowed = append(out.Narrowed, model.NarrowedTest{Unit: t.Unit, Recorded: rec.Recorded, Commit: rec.Commit,
			Reason: fmt.Sprintf("record by %s: ran %s and loaded or read no other changed file", rec.Collector, what)})
		removed++
	}
	if removed == 0 {
		return out
	}
	sort.SliceStable(out.Narrowed, func(i, j int) bool { return out.Narrowed[i].Unit < out.Narrowed[j].Unit })
	if len(out.Tests) == 0 && out.Scope != model.TierFull {
		out.Scope = model.TierNone // every unit was removed: nothing is affected (an empty non-none scope means "run everything")
	}
	if out.IndexStats != nil {
		st := make(map[string]int, len(out.IndexStats)+1)
		for k, v := range out.IndexStats {
			st[k] = v
		}
		st["narrowed"] = len(out.Narrowed)
		out.IndexStats = st
	}
	return out
}

func firstN(s []string, n int) []string {
	if len(s) <= n {
		return s
	}
	return append(slices.Clone(s[:n]), fmt.Sprintf("+%d more", len(s)-n))
}
