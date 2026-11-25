package runner

import (
	"sort"
	"strings"

	"github.com/qxbao/bgraph/internal/eco/ruby"
	"github.com/qxbao/bgraph/internal/model"
)

// minitestLoader is what Rake::TestTask's default loader does: require every file given on the command line.
const minitestLoader = "ARGV.each { |f| require File.expand_path(f) }"

const rubyNote = "ruby: the selected files of a project run in one process (per batch): global state leaking between files that are not selected together is not modeled"

// rubyConfig splits a Ruby unit's Config ("<Gemfile>" or "<Gemfile>|<lib,test>", internal/eco/ruby) into the Gemfile and
// the -I directories.
func rubyConfig(c string) (gemfile string, libs []string) {
	g, l, _ := strings.Cut(c, "|")
	if l != "" {
		libs = strings.Split(l, ",")
	}
	return g, libs
}

// rubyCommands turns selected Ruby units into commands: one per (project, runner, Config), run in the project directory with
// file paths relative to it.
//   - rspec:    [bundle exec] rspec <files>              (whole project: [bundle exec] rspec)
//   - rails:    bin/rails test <files>                   (whole project: bin/rails test)
//   - minitest: [bundle exec] ruby -I<dir>... -e "ARGV.each { |f| require File.expand_path(f) }" <files>
//     (whole project: [bundle exec] rake test)
//   - rake:     [bundle exec] rake test                  (a whole-project unit)
//   - cucumber: [bundle exec] cucumber --require features <files> (whole project: [bundle exec] cucumber)
//
// `bundle exec` is used when the project has a Gemfile (at or above its directory).
func rubyCommands(tests []model.SelectedTest, opt Options) ([]Command, []string) {
	type key struct{ project, runner, config string }
	groups := map[key][]model.SelectedTest{}
	var keys []key
	for _, t := range tests {
		k := key{cleanProject(t.Project), t.Runner, t.Config}
		if _, ok := groups[k]; !ok {
			keys = append(keys, k)
		}
		groups[k] = append(groups[k], t)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if a.project != b.project {
			return a.project < b.project
		}
		if a.runner != b.runner {
			return a.runner < b.runner
		}
		return a.config < b.config
	})
	var cmds []Command
	notes := []string{rubyNote}
	for _, k := range keys {
		gemfile, libs := rubyConfig(k.config)
		var bundle []string
		if gemfile != "" {
			bundle = []string{"bundle", "exec"}
		}
		var prefix, whole []string
		switch k.runner {
		case "rspec":
			prefix = append(append([]string(nil), bundle...), "rspec")
			whole = prefix
		case ruby.RunnerCucumber:
			whole = append(append([]string(nil), bundle...), "cucumber")
			// Some Cucumber versions search for support relative to the selected
			// feature. Load the full conventional support tree in every batch.
			prefix = append(append([]string(nil), whole...), "--require", "features")
		case "rails":
			prefix = []string{"bin/rails", "test"}
			whole = prefix
		case "minitest":
			prefix = append([]string(nil), bundle...)
			prefix = append(prefix, "ruby")
			for _, l := range libs {
				prefix = append(prefix, "-I"+l)
			}
			prefix = append(prefix, "-e", minitestLoader)
			whole = append(append([]string(nil), bundle...), "rake", "test")
		default: // rake
			whole = append(append([]string(nil), bundle...), "rake", "test")
		}
		var args []string
		all := prefix == nil
		for _, t := range groups[k] {
			if t.Whole {
				all = true
				continue
			}
			files := t.Files
			if len(files) == 0 {
				files = []string{t.Unit}
			}
			for _, f := range files {
				rel, ok := relTo(k.project, f)
				if !ok {
					all = true
					continue
				}
				args = append(args, safeArg(rel))
			}
		}
		if all || len(args) == 0 {
			cmds = append(cmds, Command{Runner: k.runner, Project: k.project, Argv: append([]string(nil), whole...),
				Note: "the test file set is computed (or a file lies outside the project): running the whole project"})
			notes = append(notes, k.project+"/"+k.runner+": running the whole project suite")
			continue
		}
		c, n := batch(k.runner, k.project, prefix, dedupe(args), maxArgs(opt))
		cmds = append(cmds, c...)
		notes = append(notes, n...)
	}
	return cmds, notes
}
