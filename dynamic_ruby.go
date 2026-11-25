package bgraph

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/qxbao/bgraph/internal/dynamic"
	"github.com/qxbao/bgraph/internal/dynamic/rbdyn"
	"github.com/qxbao/bgraph/internal/eco/ruby"
	"github.com/qxbao/bgraph/internal/model"
	"github.com/qxbao/bgraph/internal/rules"
	"github.com/qxbao/bgraph/internal/selector"
	"github.com/qxbao/bgraph/internal/tree"
)

// rubyExe resolves the Ruby executable of the dynamic layer: Options.Ruby, then bgraph.toml [dynamic_layer] ruby, then ruby on
// PATH.
func (ix *Index) rubyExe(rf *rules.File) (string, error) {
	cfg := ""
	if rf != nil {
		cfg = rf.Dynamic.Ruby
	}
	if ix.opt.Ruby != "" {
		cfg = ix.opt.Ruby
	}
	return rbdyn.ResolveRuby(ix.repo.Root, cfg)
}

// rubyEnv computes the Ruby environment fingerprint for tree t (docs/dynamic-layer.md §10.5).
func (ix *Index) rubyEnv(ctx context.Context, rf *rules.File, t tree.Tree) (string, error) {
	exe, err := ix.rubyExe(rf)
	if err != nil {
		return "", fmt.Errorf("ruby: %w", err)
	}
	desc, err := ix.rubyProbes.Get(ctx, exe)
	if err != nil {
		return "", fmt.Errorf("ruby: %w", err)
	}
	return rbdyn.Fingerprint(desc, ix.repo.Root, t)
}

// applyRubyMethods runs the Ruby method layer (docs/dynamic-layer.md §10) on a selection: a selected Ruby test file is removed when
// its record shows it ran none of the changed methods and loaded or read no other changed file. Anything that prevents it
// leaves the selection unchanged with a dynamic_skipped warning.
func (ix *Index) applyRubyMethods(ctx context.Context, in selector.Input, res selector.Result, sel Selection) Selection {
	candidate := slices.ContainsFunc(sel.Tests, func(t model.SelectedTest) bool { return rbdyn.Narrowable(t, res.UnitEco[t.Unit]) })
	if !candidate {
		return sel
	}
	changes := make([]dynamic.Change, 0, len(res.Changes))
	for _, c := range res.Changes {
		roles := c.Roles[rbdyn.Eco]
		other := false
		for e, r := range c.Roles {
			other = other || e != rbdyn.Eco && (r[0] != "" || r[1] != "")
		}
		changes = append(changes, dynamic.Change{Path: c.Path, Status: c.Status, OldMode: c.OldMode, NewMode: c.NewMode,
			BaseRole: roles[0], HeadRole: roles[1], Global: c.Global, Claimed: len(c.Claimed) > 0,
			Declared: dynamic.Declared(in.Rules, c.Path), Rule: c.Rule, Referenced: c.Dependents, OtherRole: other, Linked: c.Linked})
	}
	changed, why := rbdyn.Guard(sel, changes, in.BaseTree, in.HeadTree)
	if why != "" {
		return rubySkip(sel, res.UnitEco, why)
	}
	if ix.stateDir == "" {
		return rubySkip(sel, res.UnitEco, "no state directory: no records")
	}
	recs, warns := dynamic.Load(dynamic.StorePath(ix.stateDir, rbdyn.Eco))
	for _, w := range warns {
		sel.Warnings = append(sel.Warnings, model.Widening{Tier: model.TierNone, Reason: model.ReasonDynamicSkipped, Detail: w})
	}
	if len(recs) == 0 {
		return rubySkip(sel, res.UnitEco, "no records (run `bgraph record --eco ruby`)")
	}
	env, err := ix.rubyEnv(ctx, in.Rules, in.BaseTree)
	if err != nil {
		return rubySkip(sel, res.UnitEco, "environment fingerprint: "+err.Error())
	}
	// A head that does not compile, or that Ruby warns about differently, fails or differs when the file is loaded.
	files, why := rbdyn.ParseFiles(changed, in.BaseTree, in.HeadTree)
	if why == "" {
		exe, err := ix.rubyExe(in.Rules)
		if err != nil {
			why = "ruby: " + err.Error()
		} else {
			why = rbdyn.ParseCheck(ctx, exe, files)
		}
	}
	if why != "" {
		return rubySkip(sel, res.UnitEco, why)
	}
	return rbdyn.Narrow(sel, rbdyn.Params{Env: env, Records: recs, Base: in.BaseTree, UnitEco: res.UnitEco, Changed: changed})
}

// rubySkip returns sel with a dynamic_skipped warning for the Ruby layer, and the reason on every Ruby unit it would have
// considered (bgraph explain shows it: Ruby units are precise, so the warning alone would not be attached to them).
func rubySkip(sel Selection, unitEco map[string]string, why string) Selection {
	sel = dynamic.Skip(sel, rbdyn.Eco, why)
	sel.Tests = slices.Clone(sel.Tests)
	for i, t := range sel.Tests {
		if rbdyn.Narrowable(t, unitEco[t.Unit]) && t.Dynamic == "" {
			sel.Tests[i].Dynamic = "not narrowed by the Ruby method layer: " + why
		}
	}
	return sel
}

// recordRuby records the Ruby test files (docs/dynamic-layer.md §10.4): each in a process tree of its own, run as the runner
// adapter runs it, with the collector loaded first through RUBYOPT.
func (ix *Index) recordRuby(ctx context.Context, opt RecordOptions, rf *rules.File, an *analyzed, wt tree.Tree, head string,
	now time.Time, keep func(string) bool, sum *RecordSummary) ([]dynamic.Record, []string, error) {
	var units []model.TestUnit
	for _, a := range an.Analyses {
		for _, u := range a.Tests {
			if u.Eco == rbdyn.Eco {
				units = append(units, u)
			}
		}
	}
	var todo []rbdyn.Unit
	var skipped []string
	for _, u := range selector.DedupUnits(units) {
		if !keep(u.ID) {
			continue
		}
		if u.Whole {
			skipped = append(skipped, u.ID+": whole-project unit (test files unknown)")
			continue
		}
		if u.Runner == ruby.RunnerCucumber {
			skipped = append(skipped, u.ID+": Cucumber is handled by static selection")
			continue
		}
		todo = append(todo, rbdyn.Unit{ID: u.ID, Project: u.Project, Runner: u.Runner, Config: u.Config})
	}
	if len(todo) == 0 {
		return nil, skipped, nil
	}
	exe, err := ix.rubyExe(rf)
	if err != nil {
		return nil, skipped, fmt.Errorf("ruby: %w", err)
	}
	desc, err := ix.rubyProbes.Get(ctx, exe)
	if err != nil {
		return nil, skipped, fmt.Errorf("ruby: %w", err)
	}
	env, err := rbdyn.Fingerprint(desc, ix.repo.Root, wt)
	if err != nil {
		return nil, skipped, fmt.Errorf("ruby environment fingerprint: %w", err)
	}
	sum.Units += len(todo)
	sum.Env = env
	jobs := opt.Jobs
	if jobs <= 0 {
		jobs = rbdyn.DefaultJobs()
	}
	byID := map[string]rbdyn.Unit{}
	for _, u := range todo {
		byID[u.ID] = u
	}
	ropt := rbdyn.RunOptions{Ruby: exe, Root: ix.repo.Root, TmpDir: ix.tmpDir(), Timeout: opt.Timeout, Jobs: jobs, Env: opt.Env}
	if opt.Progress != nil {
		ropt.Progress = func(done, total int, o rbdyn.Outcome) {
			opt.Progress(done, total, rbdyn.RecordOf(o, byID[o.Unit], wt, ix.repo.Root, env, head, now), o.Elapsed, o.Tail)
		}
	}
	outs, err := rbdyn.Collect(ctx, ropt, todo)
	if err != nil {
		return nil, skipped, err
	}
	recs := make([]dynamic.Record, 0, len(outs))
	for i, o := range outs {
		recs = append(recs, rbdyn.RecordOf(o, todo[i], wt, ix.repo.Root, env, head, now))
	}
	return recs, skipped, nil
}
