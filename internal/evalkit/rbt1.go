package evalkit

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/qxbao/bgraph/internal/eco"
	"github.com/qxbao/bgraph/internal/eco/ruby"
	"github.com/qxbao/bgraph/internal/glob"
	"github.com/qxbao/bgraph/internal/model"
	"github.com/qxbao/bgraph/internal/scancache"
)

// RbReq is the requirement derived from one FAIL_TO_PASS name: the units that run its candidate test files.
type RbReq struct {
	Name       string   `json:"name"`
	Form       string   `json:"form"`
	Via        string   `json:"via,omitempty"`
	Files      []string `json:"files"`
	Units      []string `json:"units"`
	PatchUnits []string `json:"patch_units,omitempty"` // units of the candidates the test patch touches
	Ambiguous  bool     `json:"ambiguous,omitempty"`
}

// RbViolation is one required unit missing from the selection, with what a reviewer needs to classify it.
type RbViolation struct {
	Unit          string   `json:"unit"`
	Name          string   `json:"name"`
	Files         []string `json:"files"`
	Changed       []string `json:"changed"`
	PatchOnlyGone bool     `json:"patch_only_gone"` // the violation vanishes when ambiguous names require only the test-patch candidates
	// ReachedChanged: changed files reachable from the unit's file along the Ruby graph at P (requires, patterns, implicit edges).
	ReachedChanged []string `json:"reached_changed,omitempty"`
	ReachSize      int      `json:"forward_reach_size"`
	Unresolved     []string `json:"unresolved,omitempty"` // unresolved sites in the forward reach (capped)
	Class          string   `json:"class,omitempty"`      // first pass: "real-miss?" (a path exists) | "no-path" (needs a reviewer)
}

// RbT1Info records how the Ruby oracle treated one task.
type RbT1Info struct {
	Kind      string   `json:"kind"`
	Names     int      `json:"names"`
	Kept      int      `json:"kept"`
	Untouched int      `json:"untouched,omitempty"` // n2p: located only in files the test patch does not touch
	NotFound  int      `json:"not_found,omitempty"`
	Dropped   []string `json:"dropped,omitempty"` // located outside a Ruby test unit (e.g. helper base classes): no requirement
	NoOwner   int      `json:"no_owner,omitempty"`
	// NoOwnerFiles: located test files that no bgraph unit runs (a potential miss of the unit rule; review each).
	NoOwnerFiles     []string      `json:"no_owner_files,omitempty"`
	Ambiguous        int           `json:"ambiguous,omitempty"`
	Reqs             []RbReq       `json:"reqs,omitempty"`
	Violations       []RbViolation `json:"violations,omitempty"`
	PatchOnlyMissing []string      `json:"patch_only_missing,omitempty"`

	a     *eco.Analysis
	files []string
	// pkgOf maps located files to their package directory (Dart: a whole-project unit runs only its own package).
	pkgOf map[string]string
}

// RbWide explains the size of a Ruby selection: widenings by reason and the sites behind them.
type RbWide struct {
	WideCount map[string]int    `json:"wide_count,omitempty"` // reason@tier -> count
	Sites     map[string]string `json:"sites,omitempty"`      // widening path -> "reason: detail" (capped)
	Sel       int               `json:"sel"`
	Widened   int               `json:"widened"` // units selected only because of a widening
	Whole     int               `json:"whole,omitempty"`
}

// rbPrefetchWant: the blobs the Ruby analysis and the locator read, plus the sources other ecosystems scan in mixed repos.
func rbPrefetchWant(f string) bool {
	b := path.Base(f)
	switch path.Ext(b) {
	case ".rb", ".rake", ".ru", ".gemspec", ".js", ".mjs", ".cjs", ".ts", ".py", ".go", ".rs", ".toml":
		return true
	}
	switch b {
	case "Gemfile", "gems.rb", "Rakefile", ".rspec", ".rspec-parallel", ".simplecov", "package.json", "bgraph.toml":
		return true
	}
	d := path.Base(path.Dir(f))
	return (d == "bin" || d == "exe" || d == "libexec") && path.Ext(b) == ""
}

// rbRequired derives the required unit IDs of a task. dir is a repo directory holding commit p (base + test patch); tp is the set of
// files the test patch touches.
func rbRequired(ctx context.Context, dir, p string, ids []string, tp map[string]bool, kind string) ([]string, *RbT1Info, error) {
	if kind == "" {
		kind = "f2p"
	}
	info := &RbT1Info{Kind: kind, Names: len(ids)}
	gt, err := newGitTreeWant(ctx, dir, p, rbPrefetchWant)
	if err != nil {
		return nil, info, err
	}
	defer gt.Close()
	a, err := ruby.New(nil).Analyze(ctx, gt, scancache.New())
	if err != nil {
		return nil, info, err
	}
	info.a, info.files = a, gt.Files()
	unitOf := map[string]string{}
	var wholes []model.TestUnit
	for _, u := range a.Tests {
		if u.Whole {
			wholes = append(wholes, u)
			continue
		}
		for _, f := range u.Files {
			unitOf[f] = u.ID
		}
	}
	unitsFor := func(f string) []string {
		if u, ok := unitOf[f]; ok {
			return []string{u}
		}
		if path.Ext(f) == ".feature" {
			for _, w := range wholes {
				if w.Runner == ruby.RunnerCucumber && pyWithin(f, w.Project) {
					// An opaque profile may change which paths the whole suite runs.
					// Leave this requirement unmapped until a native provider check.
					return nil
				}
			}
			// A scenario location identifies its runnable file even if analysis missed it.
			return []string{f}
		}
		var out []string
		for _, w := range wholes {
			if w.Runner != ruby.RunnerCucumber && pyWithin(f, w.Project) {
				out = append(out, w.ID)
			}
		}
		return out
	}
	set := map[string]bool{}
	noOwn := map[string]bool{}
	for _, l := range LocateRubyTests(gt, ids, tp) {
		switch {
		case l.NotUnit:
			info.Dropped = append(info.Dropped, l.Name)
			continue
		case l.NotFound:
			info.NotFound++
			continue
		case kind == "n2p" && !l.InPatch:
			info.Untouched++
			continue
		}
		req := RbReq{Name: l.Name, Form: l.Form, Via: l.Via, Files: l.Files, Ambiguous: l.Ambiguous}
		us, pus := map[string]bool{}, map[string]bool{}
		for _, f := range l.Files {
			uf := unitsFor(f)
			if len(uf) == 0 {
				noOwn[f] = true
			}
			for _, u := range uf {
				us[u] = true
			}
		}
		for _, f := range l.PatchFiles {
			for _, u := range unitsFor(f) {
				pus[u] = true
			}
		}
		req.Units, req.PatchUnits = sortedKeys(us), sortedKeys(pus)
		if len(req.Units) == 0 {
			info.NoOwner++
			continue
		}
		info.Kept++
		if l.Ambiguous {
			info.Ambiguous++
		}
		for _, u := range req.Units {
			set[u] = true
		}
		info.Reqs = append(info.Reqs, req)
	}
	info.NoOwnerFiles = sortedKeys(noOwn)
	return sortedKeys(set), info, nil
}

// rbMissing returns the required unit IDs the selection lacks (a selected whole-project unit covers its own runner and project),
// records the violations per name and the units still missing if ambiguous names required only the test-patch candidates.
func rbMissing(req []string, sel []SelUnit, info *RbT1Info, changed []string) []string {
	return fileUnitMissingWithCoverage(req, sel, info, changed, rbWholeCovers)
}

func rbWholeCovers(id string, whole SelUnit) bool {
	if d, ok := ruby.ParseWholeUnitID(whole.Unit); ok {
		if required, ok := ruby.ParseWholeUnitID(id); ok {
			return d == required
		}
		return path.Ext(id) == ".rb" && pyWithin(id, d)
	}
	return false
}

// fileUnitMissing checks a file-granular ecosystem whose whole units cover every requirement within their project.
func fileUnitMissing(req []string, sel []SelUnit, info *RbT1Info, changed []string, parseWhole func(string) (string, bool),
	within func(f, dir string) bool) []string {
	return fileUnitMissingWithCoverage(req, sel, info, changed, func(id string, whole SelUnit) bool {
		if d, ok := parseWhole(id); ok {
			id = d
		}
		return within(id, whole.WholeProject)
	})
}

// fileUnitMissingWithCoverage records the per-name misses using the ecosystem's whole-unit coverage rule.
func fileUnitMissingWithCoverage(req []string, sel []SelUnit, info *RbT1Info, changed []string, wholeCovers func(string, SelUnit) bool) []string {
	have := map[string]bool{}
	for _, u := range sel {
		have[u.Unit] = true
	}
	covered := func(id string) bool {
		if have[id] {
			return true
		}
		for _, u := range sel {
			if u.Whole && wholeCovers(id, u) {
				return true
			}
		}
		return false
	}
	var miss []string
	for _, u := range req {
		if !covered(u) {
			miss = append(miss, u)
		}
	}
	if info == nil {
		return miss
	}
	patchOnly := map[string]bool{}
	for _, r := range info.Reqs {
		us := r.Units
		if r.Ambiguous && len(r.PatchUnits) > 0 {
			us = r.PatchUnits
		}
		for _, u := range us {
			patchOnly[u] = true
		}
	}
	for _, u := range miss {
		if patchOnly[u] {
			info.PatchOnlyMissing = append(info.PatchOnlyMissing, u)
		}
	}
	for _, r := range info.Reqs {
		for _, u := range r.Units {
			if !covered(u) {
				info.Violations = append(info.Violations, RbViolation{Unit: u, Name: r.Name, Files: r.Files, Changed: changed, PatchOnlyGone: !patchOnly[u]})
			}
		}
	}
	return miss
}

// rbDiagnose fills the reach fields and Class of the violations from the Ruby graph at P.
func rbDiagnose(info *RbT1Info) { fileUnitDiagnose(info, ruby.ParseWholeUnitID) }

// fileUnitDiagnose is rbDiagnose for any file-granular ecosystem (info.a holds the ecosystem's analysis at P).
func fileUnitDiagnose(info *RbT1Info, parseWhole func(string) (string, bool)) {
	if info == nil || len(info.Violations) == 0 || info.a == nil {
		return
	}
	a := info.a
	fwd := map[string][]string{}
	pats := map[string][]*glob.Pattern{}
	for _, e := range a.Edges {
		if e.Kind == model.EdgePattern {
			if pt, err := glob.Compile(e.To); err == nil {
				pats[e.From] = append(pats[e.From], pt)
			}
			continue
		}
		fwd[e.From] = append(fwd[e.From], e.To)
	}
	unres := map[string][]model.Unresolved{}
	for _, u := range a.Unresolved {
		unres[u.File] = append(unres[u.File], u)
	}
	for i := range info.Violations {
		v := &info.Violations[i]
		start := v.Unit
		if d, ok := parseWhole(start); ok {
			start = d
		}
		seen := map[string]bool{start: true}
		q := []string{start}
		for len(q) > 0 {
			c := q[0]
			q = q[1:]
			next := fwd[c]
			for _, pt := range pats[c] {
				for _, f := range info.files {
					if pt.Match(f) {
						next = append(next, f)
					}
				}
			}
			for _, n := range next {
				if !seen[n] {
					seen[n] = true
					q = append(q, n)
				}
			}
		}
		v.ReachSize = len(seen)
		for _, c := range v.Changed {
			if seen[c] {
				v.ReachedChanged = append(v.ReachedChanged, c)
			}
		}
		for n := range seen {
			for _, u := range unres[n] {
				if len(v.Unresolved) < 20 {
					v.Unresolved = append(v.Unresolved, fmt.Sprintf("%s:%d %s [%s]", u.File, u.Line, u.Spec, u.Class))
				}
			}
		}
		sort.Strings(v.ReachedChanged)
		sort.Strings(v.Unresolved)
		v.Class = "no-path"
		if len(v.ReachedChanged) > 0 {
			v.Class = "real-miss?"
		}
	}
}

func rbSelStats(sel model.Selection) *RbWide {
	w := &RbWide{WideCount: map[string]int{}, Sites: map[string]string{}, Sel: len(sel.Tests)}
	for _, x := range sel.Widening {
		w.WideCount[x.Reason+"@"+string(x.Tier)]++
		if _, ok := w.Sites[x.Path]; !ok && len(w.Sites) < 40 {
			d := x.Detail
			if len(d) > 160 {
				d = d[:160] + "..."
			}
			w.Sites[x.Path] = x.Reason + ": " + d
		}
	}
	for _, t := range sel.Tests {
		if t.Widened {
			w.Widened++
		}
		if t.Whole {
			w.Whole++
		}
	}
	return w
}

func rbInfoDetail(i *RbT1Info) string {
	return fmt.Sprintf("names=%d not_found=%d dropped=%d untouched=%d no_owner=%d", i.Names, i.NotFound, len(i.Dropped), i.Untouched, i.NoOwner)
}

// WriteRubyReport writes ruby.md next to t1.md: oracle statistics, per-repo results, widening reasons and sites, skipped tasks and
// every violation in detail.
func WriteRubyReport(dir string, rs []T1Result) error {
	return writeFileOracleReport(dir, "ruby.md", "Ruby", rs, func(r T1Result) (*RbT1Info, *RbWide) { return r.Rb, r.RbW })
}

// writeFileOracleReport writes the report of a file-granular oracle (Ruby, Dart) to dir/file; get returns a result's oracle details.
func writeFileOracleReport(dir, file, lang string, rs []T1Result, get func(T1Result) (*RbT1Info, *RbWide)) error {
	var md bytes.Buffer
	fmt.Fprintf(&md, "# %s T1 detail\n\n", lang)
	var di RbT1Info
	nt := 0
	var dropped, noOwn []string
	for _, r := range rs {
		rb, _ := get(r)
		if rb == nil {
			continue
		}
		nt++
		di.Names += rb.Names
		di.Kept += rb.Kept
		di.Untouched += rb.Untouched
		di.NotFound += rb.NotFound
		di.NoOwner += rb.NoOwner
		di.Ambiguous += rb.Ambiguous
		for _, d := range rb.Dropped {
			dropped = append(dropped, r.ID+": "+d)
		}
		for _, f := range rb.NoOwnerFiles {
			noOwn = append(noOwn, r.ID+": "+f)
		}
	}
	fmt.Fprintf(&md, "## Oracle\n\n%d tasks reached the oracle; FAIL_TO_PASS names %d; kept as requirements %d (ambiguous %d); dropped: untouched by the test patch (n2p) %d, "+
		"not found %d, outside a %s test unit %d, no owning unit %d\n\n", nt, di.Names, di.Kept, di.Ambiguous, di.Untouched, di.NotFound, lang, len(dropped), di.NoOwner)
	for _, d := range dropped {
		fmt.Fprintf(&md, "- dropped `%.160s`\n", d)
	}
	for _, d := range noOwn {
		fmt.Fprintf(&md, "- no owning unit `%s`\n", d)
	}

	fmt.Fprintf(&md, "\n## Skipped / no oracle / errors\n\n")
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Status, "skipped:") || strings.HasPrefix(r.Status, "error:") {
			n++
			d := r.Detail
			if len(d) > 240 {
				d = d[:240] + "..."
			}
			fmt.Fprintf(&md, "- `%s` (%s) %s: %s\n", r.ID, r.Role, r.Status, strings.ReplaceAll(d, "\n", " "))
		}
	}
	if n == 0 {
		fmt.Fprintf(&md, "(none)\n")
	}

	fmt.Fprintf(&md, "\n## Per repo\n\n| repo | role | kind | tried | ok | full | violations | skipped | units | median sel | median sel/units %% | select-all | median select ms | full reasons |\n|---|---|---|---|---|---|---|---|---|---|---|---|---|---|\n")
	by := map[string][]T1Result{}
	var names []string
	for _, r := range rs {
		if _, ok := by[r.Repo]; !ok {
			names = append(names, r.Repo)
		}
		by[r.Repo] = append(by[r.Repo], r)
	}
	sort.Strings(names)
	reasonsAll := map[string]int{}
	for _, nm := range names {
		var ok, full, vio, sk, all int
		var sel, frac, ms []float64
		units := 0
		reasons := map[string]int{}
		for _, r := range by[nm] {
			switch r.Status {
			case "ok":
				ok++
			case "full":
				full++
				for k := range r.Widenings {
					if strings.HasPrefix(k, "full:") {
						reasons[strings.TrimPrefix(k, "full:")]++
					}
				}
			case "violation":
				vio++
			default:
				sk++
				continue
			}
			for k := range r.Widenings {
				reasonsAll[k]++
			}
			if r.Status != "full" {
				sel = append(sel, float64(r.Selected))
				if r.Units > 0 {
					frac = append(frac, 100*float64(r.Selected)/float64(r.Units))
					if r.Selected >= r.Units {
						all++
					}
				}
			}
			ms = append(ms, float64(r.Ms))
			units = r.Units
		}
		fmt.Fprintf(&md, "| %s | %s | %s | %d | %d | %d | %d | %d | %d | %.0f | %.1f | %d | %.0f | %s |\n", nm, by[nm][0].Role, by[nm][0].Kind, len(by[nm]), ok, full, vio, sk, units,
			Percentile(sel, 50), Percentile(frac, 50), all, Percentile(ms, 50), mapStr(reasons))
	}
	type kv struct {
		k string
		v int
	}
	var rk []kv
	for k, v := range reasonsAll {
		rk = append(rk, kv{k, v})
	}
	sort.Slice(rk, func(i, j int) bool { return rk[i].v > rk[j].v || rk[i].v == rk[j].v && rk[i].k < rk[j].k })
	fmt.Fprintf(&md, "\n## Widening reasons (tasks with at least one, tier:reason)\n\n")
	for _, x := range rk {
		fmt.Fprintf(&md, "- %s: %d\n", x.k, x.v)
	}

	fmt.Fprintf(&md, "\n## Widening sites (tasks that are full or select more than half of the units)\n\n")
	for _, r := range rs {
		_, rw := get(r)
		if rw == nil || !(r.Status == "full" || r.Units > 0 && 2*r.Selected > r.Units) {
			continue
		}
		fmt.Fprintf(&md, "- `%s` %s sel=%d/%d widened=%d whole=%d %v\n", r.ID, r.Status, r.Selected, r.Units, rw.Widened, rw.Whole, mapStr(rw.WideCount))
		var ks []string
		for k := range rw.Sites {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		for i, k := range ks {
			if i == 8 {
				fmt.Fprintf(&md, "  - ... +%d sites\n", len(ks)-8)
				break
			}
			fmt.Fprintf(&md, "  - `%s` %s\n", k, rw.Sites[k])
		}
	}

	fmt.Fprintf(&md, "\n## Violations (detail)\n\n")
	nv, gone := 0, 0
	for _, r := range rs {
		rb, _ := get(r)
		if r.Status != "violation" || rb == nil {
			continue
		}
		nv++
		if len(rb.PatchOnlyMissing) == 0 {
			gone++
		}
		fmt.Fprintf(&md, "### `%s` (%s, %s) scope=%s, selected %d/%d\n\n- missing: %v\n- still missing under the patch-only policy: %v\n- changed (%d): %v\n- widenings: %v\n",
			r.ID, r.Repo, r.Role, r.Scope, r.Selected, r.Units, r.Missing, rb.PatchOnlyMissing, len(r.Changed), r.Changed, r.Widenings)
		for _, v := range rb.Violations {
			fmt.Fprintf(&md, "- unit `%s` (name `%.120s`, files %v, vanishes under patch-only: %v): class **%s**, forward reach %d nodes, reached changed files %v, unresolved in reach %v\n",
				v.Unit, v.Name, v.Files, v.PatchOnlyGone, v.Class, v.ReachSize, v.ReachedChanged, v.Unresolved)
		}
		md.WriteString("\n")
	}
	if nv == 0 {
		fmt.Fprintf(&md, "(none)\n")
	} else {
		fmt.Fprintf(&md, "Violating tasks that would be clean under the patch-only policy: %d of %d.\n", gone, nv)
	}
	return os.WriteFile(filepath.Join(dir, file), md.Bytes(), 0o644)
}
