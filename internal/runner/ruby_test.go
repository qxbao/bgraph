package runner

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/qxbao/bgraph/internal/model"
)

func rbsel(runner, project, config string, files ...string) model.Selection {
	s := model.Selection{Scope: model.TierGraph}
	for _, f := range files {
		s.Tests = append(s.Tests, model.SelectedTest{Unit: f, Project: project, Runner: runner, Files: []string{f}, Config: config})
	}
	return s
}

func TestRubyCommands(t *testing.T) {
	cases := []struct {
		name string
		sel  model.Selection
		want [][]string
	}{
		{"rspec with bundler", rbsel("rspec", ".", "Gemfile", "spec/b_spec.rb", "spec/a_spec.rb"),
			[][]string{{"bundle", "exec", "rspec", "spec/a_spec.rb", "spec/b_spec.rb"}}},
		{"rspec without Gemfile", rbsel("rspec", "gems/x", "", "gems/x/spec/a_spec.rb"),
			[][]string{{"rspec", "spec/a_spec.rb"}}},
		{"rspec in a sub-gem with the root Gemfile", rbsel("rspec", "gems/x", "Gemfile", "gems/x/spec/a_spec.rb"),
			[][]string{{"bundle", "exec", "rspec", "spec/a_spec.rb"}}},
		{"rails test", rbsel("rails", ".", "Gemfile", "test/models/user_test.rb"),
			[][]string{{"bin/rails", "test", "test/models/user_test.rb"}}},
		{"minitest loader", rbsel("minitest", "lib/x", "lib/x/Gemfile|lib,test", "lib/x/test/a_test.rb"),
			[][]string{{"bundle", "exec", "ruby", "-Ilib", "-Itest", "-e", minitestLoader, "test/a_test.rb"}}},
		{"option-like name", rbsel("rspec", ".", "", "-x_spec.rb"),
			[][]string{{"rspec", "./-x_spec.rb"}}},
		{"Cucumber with all features' support", rbsel("cucumber", "gems/x", "Gemfile", "gems/x/features/admin/a.feature", "gems/x/features/b.feature"),
			[][]string{{"bundle", "exec", "cucumber", "--require", "features", "features/admin/a.feature", "features/b.feature"}}},
		{"Cucumber without bundler", rbsel("cucumber", ".", "", "features/a.feature"),
			[][]string{{"cucumber", "--require", "features", "features/a.feature"}}},
	}
	for _, c := range cases {
		p, err := Commands(c.sel, Options{})
		if err != nil || !reflect.DeepEqual(argvs(p), c.want) {
			t.Errorf("%s: got %v (%v), want %v", c.name, argvs(p), err, c.want)
		}
	}
	// Separate commands per runner and per -I set in one project.
	mixed := rbsel("rspec", ".", "Gemfile", "spec/a_spec.rb")
	mixed.Tests = append(mixed.Tests, rbsel("minitest", ".", "Gemfile|lib,test", "test/a_test.rb").Tests...)
	mixed.Tests = append(mixed.Tests, rbsel("minitest", ".", "Gemfile|lib,spec", "spec/b_test.rb").Tests...)
	p, _ := Commands(mixed, Options{})
	if len(p.Commands) != 3 {
		t.Fatalf("mixed: %v", argvs(p))
	}
	// Whole-project units.
	for runner, want := range map[string][]string{
		"rake":     {"bundle", "exec", "rake", "test"},
		"rspec":    {"bundle", "exec", "rspec"},
		"minitest": {"bundle", "exec", "rake", "test"},
		"rails":    {"bin/rails", "test"},
		"cucumber": {"bundle", "exec", "cucumber"},
	} {
		w := model.Selection{Scope: model.TierModule, Tests: []model.SelectedTest{
			{Unit: "<rb-all-tests:.>", Project: ".", Runner: runner, Whole: true, Config: "Gemfile"},
			{Unit: "x_spec.rb", Project: ".", Runner: runner, Files: []string{"x_spec.rb"}, Config: "Gemfile"},
		}}
		if p, _ := Commands(w, Options{}); !reflect.DeepEqual(argvs(p), [][]string{want}) {
			t.Errorf("whole %s: %v", runner, argvs(p))
		}
	}
	// A file outside the project directory: the whole project.
	out := rbsel("rspec", "a", "", "b/x_spec.rb")
	if p, _ := Commands(out, Options{}); !reflect.DeepEqual(argvs(p), [][]string{{"rspec"}}) {
		t.Errorf("outside: %v", argvs(p))
	}
}

func TestRubyBatching(t *testing.T) {
	var files []string
	for i := 0; i < 400; i++ {
		files = append(files, fmt.Sprintf("spec/models/%s_%03d_spec.rb", strings.Repeat("m", 30), i))
	}
	p, err := Commands(rbsel("rspec", ".", "Gemfile", files...), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Commands) < 2 {
		t.Fatalf("expected batches, got %d commands", len(p.Commands))
	}
	seen := 0
	for _, c := range p.Commands {
		n := 0
		for _, a := range c.Argv {
			n += len(a) + 1
		}
		if n > defaultMaxArgChars {
			t.Fatalf("batch %d is %d characters", c.Batch, n)
		}
		if c.Batches != len(p.Commands) || c.Argv[0] != "bundle" {
			t.Fatalf("bad batch %+v", c)
		}
		seen += len(c.Argv) - 3
	}
	if seen != len(files) {
		t.Fatalf("%d files in batches, want %d", seen, len(files))
	}
}
