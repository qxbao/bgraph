package rbdyn

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/qxbao/bgraph/internal/dynamic"
	"github.com/qxbao/bgraph/internal/model"
	"github.com/qxbao/bgraph/internal/tree"
)

const cartRb = `class Cart
  def total
    1
  end

  def count
    2
  end
end
`

func TestNarrowableCucumber(t *testing.T) {
	u := model.SelectedTest{Unit: "features/cart.feature", Project: ".", Runner: "cucumber",
		Files: []string{"features/cart.feature"}}
	if Narrowable(u, Eco) {
		t.Fatal("the Ruby method collector does not record Cucumber features")
	}
}

var baseFiles = map[string]string{
	"Gemfile":                "gem \"minitest\"\n",
	"lib/cart.rb":            cartRb,
	"lib/price.rb":           "module Price\n  def self.fmt(x)\n    x.to_s\n  end\nend\n",
	"test/test_helper.rb":    "require \"cart\"\n",
	"test/support/extra.rb":  "module Extra\n  def self.v\n    1\n  end\nend\n",
	"test/cart_test.rb":      "require \"test_helper\"\n",
	"test/count_test.rb":     "require \"test_helper\"\n",
	"test/price_test.rb":     "require \"test_helper\"\n",
	"test/fixtures/data.txt": "x\n",
	"README.md":              "# x\n",
}

func with(edits map[string]string) *tree.MapTree {
	m := map[string]string{}
	for k, v := range baseFiles {
		m[k] = v
	}
	for k, v := range edits {
		m[k] = v
	}
	return tree.NewMapTree(m)
}

func change(p string, role model.Role) dynamic.Change {
	return dynamic.Change{Path: p, Status: 'M', OldMode: "100644", NewMode: "100644", BaseRole: role, HeadRole: role}
}

var units = []string{"test/cart_test.rb", "test/count_test.rb", "test/price_test.rb"}

func sel(changed ...string) model.Selection {
	s := model.Selection{Scope: model.TierGraph, Changed: changed, IndexStats: map[string]int{}}
	for _, u := range units {
		s.Tests = append(s.Tests, model.SelectedTest{Unit: u, Runner: "minitest", Files: []string{u}})
	}
	return s
}

func unitEco() map[string]string {
	m := map[string]string{}
	for _, u := range units {
		m[u] = Eco
	}
	return m
}

// records: cart_test runs Cart#total, count_test runs Cart#count and Extra.v, price_test runs Price.fmt and reads the fixture.
func records(t tree.Tree, env string) []dynamic.Record {
	mk := func(unit string, files []string, methods map[string]string, reads []string) dynamic.Record {
		r := dynamic.Record{Eco: Eco, Unit: unit, Collector: CollectorID, Env: env, Complete: true, Pass: true,
			Recorded: time.Unix(1000, 0), Deps: map[string]string{}, Methods: methods, Reads: reads}
		for _, f := range append(append(files, reads...), unit, "test/test_helper.rb", "lib/cart.rb", "lib/price.rb") {
			r.Deps[f] = t.Key(f)
		}
		r.Methods["test/test_helper.rb"] = ""
		return r
	}
	return []dynamic.Record{
		mk("test/cart_test.rb", nil, map[string]string{"lib/cart.rb": "2-4:3,6-8:0", "lib/price.rb": "2-4:0"}, nil),
		mk("test/count_test.rb", []string{"test/support/extra.rb"},
			map[string]string{"lib/cart.rb": "2-4:0,6-8:1", "lib/price.rb": "2-4:0", "test/support/extra.rb": "2-4:1"}, nil),
		mk("test/price_test.rb", nil, map[string]string{"lib/cart.rb": "2-4:0,6-8:0", "lib/price.rb": "2-4:5"},
			[]string{"test/fixtures/data.txt"}),
	}
}

func run(t *testing.T, head *tree.MapTree, recs []dynamic.Record, env string, s model.Selection, changes ...dynamic.Change) (model.Selection, string) {
	t.Helper()
	base := with(nil)
	ch, why := Guard(s, changes, base, head)
	if why != "" {
		return s, why
	}
	return Narrow(s, Params{Env: env, Records: recs, Base: base, UnitEco: unitEco(), Changed: ch}), ""
}

func names(s model.Selection) (kept, removed []string) {
	for _, t := range s.Tests {
		kept = append(kept, t.Unit)
	}
	for _, n := range s.Narrowed {
		removed = append(removed, n.Unit)
	}
	return kept, removed
}

func expect(t *testing.T, s model.Selection, kept, removed []string) {
	t.Helper()
	k, r := names(s)
	if !slices.Equal(k, kept) || !slices.Equal(r, removed) {
		t.Fatalf("kept %v removed %v, want kept %v removed %v", k, r, kept, removed)
	}
}

func TestNarrowMethods(t *testing.T) {
	base := with(nil)
	recs := records(base, "env")
	// Cart#total changed: only cart_test ran it.
	head := with(map[string]string{"lib/cart.rb": strings.Replace(cartRb, "    1\n", "    10\n", 1)})
	s, why := run(t, head, recs, "env", sel("lib/cart.rb"), change("lib/cart.rb", model.RoleSource))
	if why != "" {
		t.Fatal(why)
	}
	expect(t, s, []string{"test/cart_test.rb"}, []string{"test/count_test.rb", "test/price_test.rb"})
	if !strings.Contains(s.Tests[0].Dynamic, "executed the changed method Cart#total (lib/cart.rb:2)") {
		t.Errorf("note %q", s.Tests[0].Dynamic)
	}
	if s.IndexStats["narrowed"] != 2 || !strings.Contains(s.Narrowed[0].Reason, "lib/cart.rb:2") {
		t.Errorf("stats %v reason %q", s.IndexStats, s.Narrowed[0].Reason)
	}
	// Comment only: everything removed, scope none.
	head = with(map[string]string{"lib/cart.rb": "# a comment\n" + cartRb})
	s, _ = run(t, head, recs, "env", sel("lib/cart.rb"), change("lib/cart.rb", model.RoleSource))
	expect(t, s, nil, units)
	if s.Scope != model.TierNone {
		t.Errorf("scope %s", s.Scope)
	}
}

func TestNarrowKeeps(t *testing.T) {
	base := with(nil)
	recs := records(base, "env")
	headCart := with(map[string]string{"lib/cart.rb": strings.Replace(cartRb, "    1\n", "    10\n", 1)})
	cartChange := change("lib/cart.rb", model.RoleSource)
	t.Run("env mismatch", func(t *testing.T) {
		s, _ := run(t, headCart, recs, "other", sel("lib/cart.rb"), cartChange)
		expect(t, s, units, nil)
		if !strings.Contains(s.Tests[1].Dynamic, "no record for the current environment") {
			t.Errorf("note %q", s.Tests[1].Dynamic)
		}
	})
	t.Run("stale", func(t *testing.T) {
		stale := records(with(map[string]string{"lib/price.rb": "module Price\nend\n"}), "env")
		s, _ := run(t, headCart, stale, "env", sel("lib/cart.rb"), cartChange)
		expect(t, s, units, nil)
		if !strings.Contains(s.Tests[1].Dynamic, "stale") {
			t.Errorf("note %q", s.Tests[1].Dynamic)
		}
	})
	t.Run("incomplete and failed", func(t *testing.T) {
		rs := records(base, "env")
		rs[1].Complete, rs[1].Incomplete = false, "starts a non-Ruby process: git"
		rs[2].Pass = false
		s, _ := run(t, headCart, rs, "env", sel("lib/cart.rb"), cartChange)
		expect(t, s, units, nil)
	})
	t.Run("other collector", func(t *testing.T) {
		rs := records(base, "env")
		for i := range rs {
			rs[i].Collector = "ruby-cov-0"
		}
		s, _ := run(t, headCart, rs, "env", sel("lib/cart.rb"), cartChange)
		expect(t, s, units, nil)
	})
	t.Run("instrumented: member not in the table", func(t *testing.T) {
		rs := records(base, "env")
		rs[1].Methods["lib/cart.rb"] = "6-8:1"
		s, _ := run(t, headCart, rs, "env", sel("lib/cart.rb"), cartChange)
		expect(t, s, []string{"test/cart_test.rb", "test/count_test.rb"}, []string{"test/price_test.rb"})
		if !strings.Contains(s.Tests[1].Dynamic, "not in the recorded method table") {
			t.Errorf("note %q", s.Tests[1].Dynamic)
		}
	})
	t.Run("instrumented: file without a table", func(t *testing.T) {
		rs := records(base, "env")
		delete(rs[2].Methods, "lib/cart.rb")
		s, _ := run(t, headCart, rs, "env", sel("lib/cart.rb"), cartChange)
		expect(t, s, []string{"test/cart_test.rb", "test/price_test.rb"}, []string{"test/count_test.rb"})
	})
	t.Run("read as data", func(t *testing.T) {
		rs := records(base, "env")
		rs[2].Reads = append(rs[2].Reads, "lib/cart.rb")
		s, _ := run(t, headCart, rs, "env", sel("lib/cart.rb"), cartChange)
		expect(t, s, []string{"test/cart_test.rb", "test/price_test.rb"}, []string{"test/count_test.rb"})
	})
	t.Run("changed unit kept", func(t *testing.T) {
		head := with(map[string]string{"test/count_test.rb": "require \"test_helper\"\n# more\n"})
		s, why := run(t, head, recs, "env", sel("test/count_test.rb"), change("test/count_test.rb", model.RoleTest))
		if why != "" {
			t.Fatal(why)
		}
		expect(t, s, []string{"test/count_test.rb"}, []string{"test/cart_test.rb", "test/price_test.rb"})
	})
	t.Run("always run", func(t *testing.T) {
		s := sel("lib/cart.rb")
		s.AlwaysRun = []string{"test/price_test.rb"}
		out, _ := run(t, headCart, recs, "env", s, cartChange)
		expect(t, out, []string{"test/cart_test.rb", "test/price_test.rb"}, []string{"test/count_test.rb"})
	})
	t.Run("always run sub-unit", func(t *testing.T) {
		// A failed sub-unit ID of a test file (--failed "<file>#<test>") keeps the test file.
		s := sel("lib/cart.rb")
		s.AlwaysRun = []string{"test/price_test.rb#test_fmt"}
		out, _ := run(t, headCart, recs, "env", s, cartChange)
		expect(t, out, []string{"test/cart_test.rb", "test/price_test.rb"}, []string{"test/count_test.rb"})
	})
	t.Run("whole unit", func(t *testing.T) {
		s := sel("lib/cart.rb")
		s.Tests[2].Whole = true
		out, _ := run(t, headCart, recs, "env", s, cartChange)
		expect(t, out, []string{"test/cart_test.rb", "test/price_test.rb"}, []string{"test/count_test.rb"})
	})
	t.Run("support file refused by the body guard", func(t *testing.T) {
		head := with(map[string]string{"test/support/extra.rb": "module Extra\n  X = 1\n  def self.v\n    1\n  end\nend\n"})
		s, why := run(t, head, recs, "env", sel("test/support/extra.rb"), change("test/support/extra.rb", model.RoleSource))
		if why != "" {
			t.Fatal(why)
		}
		expect(t, s, []string{"test/count_test.rb"}, []string{"test/cart_test.rb", "test/price_test.rb"})
		if !strings.Contains(s.Tests[0].Dynamic, "loaded the changed file test/support/extra.rb") {
			t.Errorf("note %q", s.Tests[0].Dynamic)
		}
	})
	t.Run("support method", func(t *testing.T) {
		head := with(map[string]string{"test/support/extra.rb": "module Extra\n  def self.v\n    2\n  end\nend\n"})
		s, _ := run(t, head, recs, "env", sel("test/support/extra.rb"), change("test/support/extra.rb", model.RoleSource))
		expect(t, s, []string{"test/count_test.rb"}, []string{"test/cart_test.rb", "test/price_test.rb"})
	})
	t.Run("documentation", func(t *testing.T) {
		rs := records(base, "env")
		rs[0].Reads = []string{"README.md"}
		rs[0].Deps["README.md"] = base.Key("README.md")
		head := with(map[string]string{"README.md": "# y\n"})
		s, why := run(t, head, rs, "env", sel("README.md"), change("README.md", ""))
		if why != "" {
			t.Fatal(why)
		}
		expect(t, s, []string{"test/cart_test.rb"}, []string{"test/count_test.rb", "test/price_test.rb"})
	})
}

func TestGuardRefuses(t *testing.T) {
	base := with(nil)
	recs := records(base, "env")
	headCart := with(map[string]string{"lib/cart.rb": strings.Replace(cartRb, "    1\n", "    10\n", 1)})
	c := change("lib/cart.rb", model.RoleSource)
	cases := []struct {
		name string
		s    func() model.Selection
		c    func() dynamic.Change
		head *tree.MapTree
		want string
	}{
		{"full", func() model.Selection { s := sel("lib/cart.rb"); s.Scope = model.TierFull; return s }, nil, headCart, "full"},
		{"global widening", func() model.Selection {
			s := sel("lib/cart.rb")
			s.Widening = []model.Widening{{Tier: model.TierFull, Reason: model.ReasonGlobalInput, Path: ".ruby-version"}}
			return s
		}, nil, headCart, "global or full"},
		{"addition", nil, func() dynamic.Change { x := c; x.Status = 'A'; return x }, headCart, "status A"},
		{"mode", nil, func() dynamic.Change { x := c; x.NewMode = "100755"; return x }, headCart, "mode changed"},
		{"link", nil, func() dynamic.Change { x := c; x.Linked = true; return x }, headCart, "symbolic link"},
		{"claimed", nil, func() dynamic.Change { x := c; x.Claimed = true; return x }, headCart, "claimed"},
		{"declared", nil, func() dynamic.Change { x := c; x.Declared = true; return x }, headCart, "[[config]]"},
		{"rule", nil, func() dynamic.Change { x := c; x.Rule = true; return x }, headCart, "[[rule]]"},
		{"class-level library code", nil, nil, with(map[string]string{"lib/cart.rb": "class Cart\n  X = 1\nend\n"}), "outside method bodies"},
		{"data file", func() model.Selection { return sel("test/fixtures/data.txt") },
			func() dynamic.Change { return change("test/fixtures/data.txt", model.RoleData) }, with(map[string]string{"test/fixtures/data.txt": "y\n"}), "not a Ruby source"},
		{"rake file", func() model.Selection { return sel("lib/tasks/x.rake") },
			func() dynamic.Change { return change("lib/tasks/x.rake", model.RoleSource) }, headCart, "not a Ruby source"},
		{"role change", nil, func() dynamic.Change { x := c; x.HeadRole = model.RoleTest; return x }, headCart, "role"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := sel("lib/cart.rb")
			if tc.s != nil {
				s = tc.s()
			}
			ch := c
			if tc.c != nil {
				ch = tc.c()
			}
			out, why := run(t, tc.head, recs, "env", s, ch)
			if !strings.Contains(why, tc.want) {
				t.Fatalf("why %q, want %q", why, tc.want)
			}
			if len(out.Narrowed) != 0 {
				t.Fatal("narrowed")
			}
		})
	}
}

func TestMethodsFormat(t *testing.T) {
	ms := []Method{{2, 4, 3}, {6, 8, 0}}
	s := FormatMethods(ms)
	if s != "2-4:3,6-8:0" {
		t.Fatal(s)
	}
	back, ok := ParseMethods(s)
	if !ok || !slices.Equal(back, ms) {
		t.Fatal(back)
	}
	for _, bad := range []string{"2-4", "x-4:1", "2:4:1"} {
		if _, ok := ParseMethods(bad); ok {
			t.Errorf("%q parsed", bad)
		}
	}
}
