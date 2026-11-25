package bgraph

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/qxbao/bgraph/internal/dynamic"
	"github.com/qxbao/bgraph/internal/dynamic/rbdyn"
)

// testRuby returns a Ruby executable with Bundler and Minitest (BGRAPH_TEST_RUBY, else ruby on PATH) or skips, and the
// Gemfile.lock Bundler resolves locally for a Gemfile needing minitest only.
func testRuby(t *testing.T) (ruby, lock string) {
	t.Helper()
	ruby = os.Getenv("BGRAPH_TEST_RUBY")
	if ruby == "" {
		p, err := exec.LookPath("ruby")
		if err != nil {
			t.Skip("no ruby (set BGRAPH_TEST_RUBY to a Ruby with bundler and minitest)")
		}
		ruby = p
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Gemfile"), []byte(rubyGemfile), 0o644); err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(filepath.Dir(ruby), "bundle")
	args := []string{bundle, "lock", "--local"}
	if _, err := os.Stat(bundle); err != nil {
		args = []string{"-S", "bundle", "lock", "--local"}
	}
	cmd := exec.Command(ruby, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PATH="+filepath.Dir(ruby)+string(os.PathListSeparator)+os.Getenv("PATH"), "BUNDLE_GEMFILE=")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("%s cannot lock a minitest bundle offline: %v %s", ruby, err, out)
	}
	b, err := os.ReadFile(filepath.Join(dir, "Gemfile.lock"))
	if err != nil {
		t.Skip("no Gemfile.lock")
	}
	return ruby, string(b)
}

const rubyGemfile = "source \"https://rubygems.org\"\ngem \"minitest\"\n"

const rubyCart = `class Cart
  LIMIT = 10

  def initialize(items)
    @items = items
  end

  def total
    @items.sum
  end

  def count
    @items.size
  end

  def describe
    "cart of #{count}"
  end
end
`

// TestRecordRubyIntegration records a tiny Minitest project with the real collector and narrows one-line changes inside method
// bodies end to end (docs/dynamic-layer.md §10).
func TestRecordRubyIntegration(t *testing.T) {
	ruby, lock := testRuby(t)
	t.Setenv("BUNDLE_GEMFILE", "")
	os.Unsetenv("BUNDLE_GEMFILE")
	t.Setenv("RUBYOPT", "")
	os.Unsetenv("RUBYOPT")
	root := testRepo(t, map[string]string{
		"Gemfile":               rubyGemfile,
		"Gemfile.lock":          lock,
		"features/shop.feature": "Feature: Shop\n  Scenario: Describe the shop\n",
		"lib/shop.rb":           "require \"shop/cart\"\nrequire \"shop/price\"\nrequire \"shop/report\"\n",
		"lib/shop/cart.rb":      rubyCart,
		"lib/shop/price.rb":     "module Price\n  def self.format(x)\n    \"$#{x}\"\n  end\n\n  def self.tax(x)\n    x * 2\n  end\nend\n",
		"lib/shop/report.rb":    "module Report\n  def self.render\n    File.read(File.expand_path(\"../../data/template.txt\", __dir__)).strip\n  end\nend\n",
		"data/template.txt":     "report\n",
		"README.md":             "# shop\n",
		"test/test_helper.rb":   "require \"minitest/autorun\"\nrequire \"shop\"\n",
		"test/support/extra.rb": "module Extra\n  def self.value\n    1\n  end\nend\n",
		"test/cart_test.rb":     "require \"test_helper\"\nrequire \"support/extra\"\nclass CartTest < Minitest::Test\n  def test_total\n    assert_equal 3, Cart.new([1, 2]).total + Extra.value - 1\n    assert_equal 2, Cart.new([1, 2]).count\n  end\nend\n",
		"test/price_test.rb":    "require \"test_helper\"\nclass PriceTest < Minitest::Test\n  def test_format\n    assert_equal \"$1\", Price.format(1)\n  end\nend\n",
		"test/report_test.rb":   "require \"test_helper\"\nclass ReportTest < Minitest::Test\n  def test_render\n    assert_equal \"report\", Report.render\n  end\nend\n",
		"test/sub_test.rb":      "require \"test_helper\"\nclass SubTest < Minitest::Test\n  def test_sub\n    assert system(RbConfig.ruby, \"-e\", \"exit 0\")\n  end\nend\n",
		"test/git_test.rb":      "require \"test_helper\"\nclass GitTest < Minitest::Test\n  def test_git\n    assert_match(/git/, `git --version`)\n  end\nend\n",
	})
	state := t.TempDir()
	ctx := context.Background()
	ix, err := Open(ctx, root, Options{MemoryOnly: true, StateDir: state, Ruby: ruby})
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	sum, err := ix.Record(ctx, RecordOptions{Eco: "ruby", Jobs: 3, Timeout: 5 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	recs, warn := dynamic.Load(dynamic.StorePath(state, "ruby"))
	by := map[string]dynamic.Record{}
	for _, r := range recs {
		by[r.Unit] = r
	}
	if sum.Units != 5 || len(recs) != 5 || len(warn) > 0 {
		t.Fatalf("summary %+v, %d records, %v", sum, len(recs), warn)
	}
	if !slices.Contains(sum.Skipped, "features/shop.feature: Cucumber is handled by static selection") {
		t.Errorf("recorded Ruby tests should report skipped Cucumber features: %v", sum.Skipped)
	}
	for _, u := range []string{"test/cart_test.rb", "test/price_test.rb", "test/report_test.rb", "test/sub_test.rb"} {
		if r := by[u]; !r.Complete || !r.Pass {
			t.Errorf("%s: complete=%v pass=%v (%s)", u, r.Complete, r.Pass, r.Incomplete)
		}
	}
	if r := by["test/git_test.rb"]; r.Complete || !strings.Contains(r.Incomplete, "non-Ruby process") || !r.Pass {
		t.Errorf("git_test: complete=%v pass=%v (%s)", by["test/git_test.rb"].Complete, r.Pass, r.Incomplete)
	}
	cart := by["test/cart_test.rb"]
	ms, ok := rbdyn.ParseMethods(cart.Methods["lib/shop/cart.rb"])
	if !ok || !slices.Contains(ms, rbdyn.Method{Start: 8, End: 10, Count: 1}) || !slices.Contains(ms, rbdyn.Method{Start: 16, End: 18, Count: 0}) {
		t.Errorf("cart_test method table of cart.rb: %v (%q)", ms, cart.Methods["lib/shop/cart.rb"])
	}
	if r := by["test/report_test.rb"]; !slices.Contains(r.Reads, "data/template.txt") || r.Deps["data/template.txt"] == "" {
		t.Errorf("report_test reads %v", r.Reads)
	}
	if _, ok := by["test/price_test.rb"].Deps["test/support/extra.rb"]; ok {
		t.Error("price_test loaded test/support/extra.rb")
	}
	if out, err := exec.Command("git", "-C", root, "status", "--porcelain").CombinedOutput(); err != nil || len(out) > 0 {
		t.Errorf("recording dirtied the tree: %v %s", err, out)
	}

	write := func(rel, c string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sel := func() Selection {
		t.Helper()
		s, err := ix.Select(ctx, Change{Base: "HEAD", Worktree: true, Dynamic: DynamicOn})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	units := func(s Selection) (kept, narrowed []string) {
		for _, x := range s.Tests {
			kept = append(kept, x.Unit)
		}
		for _, x := range s.Narrowed {
			narrowed = append(narrowed, x.Unit)
		}
		return kept, narrowed
	}
	check := func(name string, wantKept, wantNarrowed []string) Selection {
		t.Helper()
		s := sel()
		kept, narrowed := units(s)
		if !slices.Equal(kept, wantKept) || !slices.Equal(narrowed, wantNarrowed) {
			t.Fatalf("%s: kept %v narrowed %v (warnings %+v)", name, kept, narrowed, s.Warnings)
		}
		return s
	}
	all := []string{"test/cart_test.rb", "test/git_test.rb", "test/price_test.rb", "test/report_test.rb", "test/sub_test.rb"}

	// A method nobody calls: every unit with a usable record is removed; git_test (incomplete) is kept.
	write("lib/shop/cart.rb", strings.Replace(rubyCart, `"cart of #{count}"`, `"cart with #{count}"`, 1))
	check("Cart#describe", []string{"test/git_test.rb"}, []string{"test/cart_test.rb", "test/price_test.rb", "test/report_test.rb", "test/sub_test.rb"})
	// A method cart_test runs.
	write("lib/shop/cart.rb", strings.Replace(rubyCart, "@items.sum", "@items.sum + 0", 1))
	s := check("Cart#total", []string{"test/cart_test.rb", "test/git_test.rb"}, []string{"test/price_test.rb", "test/report_test.rb", "test/sub_test.rb"})
	for _, x := range s.Tests {
		if x.Unit == "test/cart_test.rb" && !strings.Contains(x.Dynamic, "executed the changed method") {
			t.Errorf("cart_test note %q", x.Dynamic)
		}
	}
	// Class-level code: the guard refuses, nothing is narrowed.
	write("lib/shop/cart.rb", strings.Replace(rubyCart, "LIMIT = 10", "LIMIT = 11", 1))
	s = check("Cart::LIMIT", all, nil)
	if w := s.Warnings; len(w) == 0 || !strings.Contains(w[len(w)-1].Detail, "outside method bodies") {
		t.Errorf("Cart::LIMIT warnings %+v", w)
	}
	if !strings.Contains(s.Tests[0].Dynamic, "not narrowed by the Ruby method layer: lib/shop/cart.rb:2: code outside method bodies") {
		t.Errorf("Cart::LIMIT note %q", s.Tests[0].Dynamic)
	}
	// A body change that does not compile refuses (parse check).
	write("lib/shop/cart.rb", strings.Replace(rubyCart, "    @items.sum\n", "    @items.sum +\n", 1))
	s = check("syntax error", all, nil)
	if !strings.Contains(s.Tests[0].Dynamic, "does not compile at the head") {
		t.Errorf("syntax error note %q", s.Tests[0].Dynamic)
	}
	write("lib/shop/cart.rb", rubyCart)
	// A method run by price_test.
	write("lib/shop/price.rb", "module Price\n  def self.format(x)\n    \"$\" + x.to_s\n  end\n\n  def self.tax(x)\n    x * 2\n  end\nend\n")
	check("Price.format", []string{"test/git_test.rb", "test/price_test.rb"}, []string{"test/cart_test.rb", "test/report_test.rb", "test/sub_test.rb"})
	write("lib/shop/price.rb", "module Price\n  def self.format(x)\n    \"$#{x}\"\n  end\n\n  def self.tax(x)\n    x * 3\n  end\nend\n")
	check("Price.tax", []string{"test/git_test.rb"}, []string{"test/cart_test.rb", "test/price_test.rb", "test/report_test.rb", "test/sub_test.rb"})
	write("lib/shop/price.rb", "module Price\n  def self.format(x)\n    \"$#{x}\"\n  end\n\n  def self.tax(x)\n    x * 2\n  end\nend\n")
	// Test support: kept by the unit that loaded and ran it.
	write("test/support/extra.rb", "module Extra\n  def self.value\n    2 - 1\n  end\nend\n")
	// (Only cart_test requires it: the static selection is that unit alone, and its record keeps it.)
	s = check("Extra.value", []string{"test/cart_test.rb"}, nil)
	if !strings.Contains(s.Tests[0].Dynamic, "executed the changed method Extra.value") {
		t.Errorf("Extra.value note %q", s.Tests[0].Dynamic)
	}
	write("test/support/extra.rb", "module Extra\n  def self.value\n    1\n  end\nend\n")
	// A changed test file is kept.
	write("test/price_test.rb", "require \"test_helper\"\nclass PriceTest < Minitest::Test\n  def test_format\n    assert_equal \"$2\", Price.format(2)\n  end\nend\n")
	check("price_test", []string{"test/price_test.rb"}, nil)
	write("test/price_test.rb", "require \"test_helper\"\nclass PriceTest < Minitest::Test\n  def test_format\n    assert_equal \"$1\", Price.format(1)\n  end\nend\n")
	// Test support the body guard refuses (class-level code): every unit that loaded it is kept.
	write("test/test_helper.rb", "require \"minitest/autorun\"\nrequire \"shop\"\nX = 1\n")
	s = check("test_helper", all, nil)
	for _, x := range s.Tests {
		if x.Unit != "test/git_test.rb" && !strings.Contains(x.Dynamic, "loaded the changed file test/test_helper.rb") {
			t.Errorf("test_helper: %s note %q", x.Unit, x.Dynamic)
		}
	}
	write("test/test_helper.rb", "require \"minitest/autorun\"\nrequire \"shop\"\n")
	// A data file refuses the whole selection.
	write("data/template.txt", "other\n")
	s = sel()
	if len(s.Narrowed) != 0 {
		t.Errorf("data change narrowed %v", s.Narrowed)
	}
	write("data/template.txt", "report\n")
	// Documentation nobody reads: every unit with a usable record is removed (nothing static selects them anyway, so check
	// that nothing breaks).
	write("README.md", "# shop!\n")
	if s := sel(); len(s.Tests) != 0 && len(s.Narrowed) == 0 {
		t.Logf("README: %d tests", len(s.Tests))
	}
	write("README.md", "# shop\n")
}
