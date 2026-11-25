// Package runner turns a Selection into test-run commands for each test runner (docs/cli.md, command).
//
// Principle 11: NEVER emit a command with an empty list. scope none → no command; scope full → no selective command.
//
// Positional arguments mean different things between runners:
//   - vitest: a substring of the path → over-selection (aa.test.ts matches a.test.ts) is acceptable.
//   - jest: uses --runTestsByPath, so it is an exact path, not a regex.
//   - playwright: a regex → escaped with regexp.QuoteMeta; prefers --test-list when ListDir is set.
//   - mocha, ava: paths/globs; a file name with glob characters → fall back to a whole-project command.
//   - pytest, django: see python.go (rootdir/config pinned, @argfile for long lists, dotted labels).
//   - cargo: units are Cargo targets addressed by package (-p) and target flags (--lib, --test, ...); see cargo.go.
//   - dotnet: units are MSBuild projects, one `dotnet test <project>` each; see dotnet.go.
//   - maven, gradle: units are modules/projects built by the build tool; see maven.go and gradle.go.
//   - go: units are package directories, one `go test ./<dir> ./<dir2>` per module (run in the module directory); see gotest.go.
//   - rspec, rails, minitest, rake, cucumber: Ruby test files and features, grouped by project and runner; see ruby.go.
//   - swift: `swift test` skips unselected test modules in each package; see swift.go.
//   - ctest, meson: test names in a configured build directory; see cpp.go.
//   - dart, flutter: test files, one `dart test` / `flutter test` per package (integration tests separately); see dart.go.
//   - node --test: globs (Node >= 21); a name with glob characters → fall back to a whole-project `node --test`.
package runner

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/qxbao/bgraph/internal/eco/cpp"
	"github.com/qxbao/bgraph/internal/eco/dart"
	"github.com/qxbao/bgraph/internal/eco/php"
	"github.com/qxbao/bgraph/internal/eco/ruby"
	"github.com/qxbao/bgraph/internal/eco/swift"
	"github.com/qxbao/bgraph/internal/model"
)

// Options configures the adapter.
type Options struct {
	ListDir       string  // where list files are written; "" → no list file, pass arguments directly
	MaxArgChars   int     // default 8000 (below cmd.exe's 8191 limit)
	FullThreshold float64 // 0 = off; selected units / total units > threshold → full
	BuildDir      string  // CTest and Meson build directory, relative to each project or absolute; default "build"
	BuildConfig   string  // CMake multi-config build configuration, e.g. Release
}

// Command is one test-run command.
type Command struct {
	Runner  string   `json:"runner"`
	Project string   `json:"project"` // working directory, relative to the repo root
	Argv    []string `json:"argv"`
	// Env: environment variables (KEY=VALUE) the command needs besides the caller's (e.g. MAVEN_ARGS carrying a class filter).
	Env      []string `json:"env,omitempty"`
	ListFile string   `json:"list_file,omitempty"`
	Batch    int      `json:"batch,omitempty"` // 1..Batches when split into batches
	Batches  int      `json:"batches,omitempty"`
	Note     string   `json:"note,omitempty"`
}

// Plan is the run plan.
type Plan struct {
	Scope    model.Tier `json:"scope"`
	Commands []Command  `json:"commands"`
	Notes    []string   `json:"notes,omitempty"`
}

const defaultMaxArgChars = 8000

const outsideMark = "\x00outside"

var globMeta = regexp.MustCompile(`[*?\[\]{}!()]`)

// Commands builds the command plan from the selection.
func Commands(sel model.Selection, opt Options) (Plan, error) {
	if opt.MaxArgChars <= 0 {
		opt.MaxArgChars = defaultMaxArgChars
	}
	plan := Plan{Scope: sel.Scope, Commands: []Command{}}
	full := func(note string) (Plan, error) {
		plan.Scope = model.TierFull
		plan.Commands = []Command{}
		plan.Notes = append(plan.Notes, note)
		return plan, nil
	}
	switch sel.Scope {
	case model.TierNone:
		plan.Notes = append(plan.Notes, "no test is affected: nothing to run")
		return plan, nil
	case model.TierFull:
		return full("run the full suite")
	}
	if len(sel.Tests) == 0 {
		// A scope other than none with no tests: an empty command cannot be emitted, nor silently skipped → full.
		return full("scope " + string(sel.Scope) + " but no test was selected: run the full suite")
	}
	if total := sel.IndexStats["units"]; opt.FullThreshold > 0 && total > 0 &&
		float64(len(sel.Tests))/float64(total) > opt.FullThreshold {
		return full(fmt.Sprintf("selected %d/%d units, exceeding threshold %.2f: run the full suite", len(sel.Tests), total, opt.FullThreshold))
	}

	type key struct{ project, runner string }
	groups := map[key][]string{}
	var keys []key
	var cargoTests, pyTests, dotnetTests, jvmTests, goTests, rbTests, phpTests, swiftTests, dartTests []model.SelectedTest
	var cppTests []model.SelectedTest
	for _, t := range sel.Tests {
		if t.Runner == "cargo" {
			cargoTests = append(cargoTests, t) // units are virtual targets, not files: dedicated adapter
			continue
		}
		if t.Runner == "dotnet" {
			dotnetTests = append(dotnetTests, t) // units are MSBuild projects: dedicated adapter
			continue
		}
		if t.Runner == "maven" || t.Runner == "gradle" {
			jvmTests = append(jvmTests, t) // units are modules/projects: dedicated adapter (maven.go, gradle.go)
			continue
		}
		if t.Runner == php.RunnerPHPUnit || t.Runner == php.RunnerPest {
			phpTests = append(phpTests, t)
			continue
		}
		if t.Runner == "go" {
			goTests = append(goTests, t) // units are package directories: dedicated adapter (gotest.go)
			continue
		}
		if t.Runner == swift.Runner {
			swiftTests = append(swiftTests, t)
			continue
		}
		if t.Runner == cpp.CTestRunner || t.Runner == cpp.MesonRunner {
			cppTests = append(cppTests, t)
			continue
		}
		if t.Runner == dart.RunnerDart || t.Runner == dart.RunnerFlutter {
			dartTests = append(dartTests, t) // one command per package, integration tests apart (dart.go)
			continue
		}
		if t.Runner == "rspec" || t.Runner == "rails" || t.Runner == "minitest" || t.Runner == "rake" || t.Runner == ruby.RunnerCucumber {
			rbTests = append(rbTests, t) // dedicated adapter: bundle exec, minitest loader, -I directories (ruby.go)
			continue
		}
		if t.Runner == "pytest" || t.Runner == "django" {
			pyTests = append(pyTests, t) // dedicated adapter: rootdir/config pinning, dotted labels
			continue
		}
		proj := cleanProject(t.Project)
		k := key{proj, t.Runner}
		if _, ok := groups[k]; !ok {
			keys = append(keys, k)
		}
		files := t.Files
		if t.Whole {
			files = []string{outsideMark} // test files unknown: run the whole project
		} else if len(files) == 0 {
			files = []string{t.Unit}
		}
		for _, f := range files {
			rel, ok := relTo(proj, f)
			if !ok {
				// Cannot express the file relative to the project: fall back to the whole project (wider, safe).
				groups[k] = append(groups[k], outsideMark)
				continue
			}
			groups[k] = append(groups[k], rel)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].project != keys[j].project {
			return keys[i].project < keys[j].project
		}
		return keys[i].runner < keys[j].runner
	})

	for _, k := range keys {
		paths := dedupe(groups[k])
		sp, ok := specs[k.runner]
		if !ok {
			return full(fmt.Sprintf("runner %q (project %s) is not supported: run the full suite", k.runner, k.project))
		}
		whole := false
		for _, p := range paths {
			if p == outsideMark || (sp.globby && globMeta.MatchString(p)) {
				whole = true
			}
		}
		if whole {
			plan.Commands = append(plan.Commands, Command{Runner: k.runner, Project: k.project, Argv: append([]string(nil), sp.whole...),
				Note: "file name has glob characters or is outside the project: running the whole project instead of a list"})
			plan.Notes = append(plan.Notes, fmt.Sprintf("%s/%s: falling back to a whole-project command", k.project, k.runner))
			continue
		}
		cmds, notes, err := sp.build(k.project, k.runner, paths, opt)
		if err != nil {
			return Plan{}, err
		}
		plan.Commands = append(plan.Commands, cmds...)
		plan.Notes = append(plan.Notes, notes...)
	}
	if len(pyTests) > 0 {
		cmds, notes, err := pythonCommands(pyTests, opt)
		if err != nil {
			return Plan{}, err
		}
		plan.Commands = append(plan.Commands, cmds...)
		plan.Notes = append(plan.Notes, notes...)
	}
	if len(cargoTests) > 0 {
		cmds, notes := cargoCommands(cargoTests)
		plan.Commands = append(plan.Commands, cmds...)
		plan.Notes = append(plan.Notes, notes...)
	}
	if len(dotnetTests) > 0 {
		cmds, notes := dotnetCommands(dotnetTests, opt)
		plan.Commands = append(plan.Commands, cmds...)
		plan.Notes = append(plan.Notes, notes...)
	}
	if len(jvmTests) > 0 {
		cmds, notes := jvmCommands(jvmTests, opt)
		plan.Commands = append(plan.Commands, cmds...)
		plan.Notes = append(plan.Notes, notes...)
	}
	if len(goTests) > 0 {
		cmds, notes := goCommands(goTests, opt)
		plan.Commands = append(plan.Commands, cmds...)
		plan.Notes = append(plan.Notes, notes...)
	}
	if len(rbTests) > 0 {
		cmds, notes := rubyCommands(rbTests, opt)
		plan.Commands = append(plan.Commands, cmds...)
		plan.Notes = append(plan.Notes, notes...)
	}
	if len(phpTests) > 0 {
		cmds, notes, ok := phpCommands(phpTests, opt)
		if !ok {
			return full("PHP project does not have a representable runner command: run the full suite")
		}
		plan.Commands = append(plan.Commands, cmds...)
		plan.Notes = append(plan.Notes, notes...)
	}
	if len(swiftTests) > 0 {
		plan.Commands = append(plan.Commands, swiftCommands(swiftTests, opt)...)
	}
	if len(cppTests) > 0 {
		cmds, err := cppCommands(cppTests, opt)
		if err != nil {
			return Plan{}, err
		}
		plan.Commands = append(plan.Commands, cmds...)
	}
	if len(dartTests) > 0 {
		plan.Commands = append(plan.Commands, dartCommands(dartTests, opt)...)
	}
	return plan, nil
}

type spec struct {
	whole  []string // whole-project command
	globby bool     // positional arguments are globs: names with glob characters cannot be passed safely
	build  func(project, runner string, paths []string, opt Options) ([]Command, []string, error)
}

func positional(prefix []string, note string) func(string, string, []string, Options) ([]Command, []string, error) {
	return func(project, runner string, paths []string, opt Options) ([]Command, []string, error) {
		cmds, notes := batch(runner, project, prefix, paths, opt.MaxArgChars)
		if note != "" {
			notes = append(notes, note)
		}
		return cmds, notes, nil
	}
}

var specs = map[string]spec{
	"vitest": {whole: []string{"npx", "vitest", "run"},
		build: positional([]string{"npx", "vitest", "run"},
			"vitest: the argument is a substring of the path, so a.test.ts also matches aa.test.ts (over-selection, safe)")},
	"jest": {whole: []string{"npx", "jest"},
		build: positional([]string{"npx", "jest", "--runTestsByPath"}, "")},
	"mocha": {whole: []string{"npx", "mocha"}, globby: true,
		build: positional([]string{"npx", "mocha"}, "")},
	"ava": {whole: []string{"npx", "ava"}, globby: true,
		build: positional([]string{"npx", "ava"}, "")},
	"node-test": {whole: []string{"node", "--test"}, globby: true,
		build: positional([]string{"node", "--test"}, "node --test: positional arguments are globs (Node >= 21); the selected paths contain no glob characters")},
	"playwright": {whole: []string{"npx", "playwright", "test"}, build: buildPlaywright},
}

// buildPlaywright: prefers positional arguments. The argument is a regex matched against the absolute path of the test file (Windows uses "\"),
// so each "/" becomes [\\/]. --test-list (Playwright ≥ 1.56) does not say what its paths are relative to: a line that does not match makes
// Playwright run 0 tests (a silent miss), so it is only used when the list is too long, with a note, and each line is a path relative to
// the project exactly as --list prints it.
func buildPlaywright(project, runner string, paths []string, opt Options) ([]Command, []string, error) {
	args := make([]string, len(paths))
	total := 0
	for i, p := range paths {
		args[i] = strings.ReplaceAll(regexp.QuoteMeta(p), "/", `[\\/]`)
		total += len(args[i]) + 1
	}
	if opt.ListDir != "" && total > maxArgs(opt) {
		if err := os.MkdirAll(opt.ListDir, 0o755); err != nil {
			return nil, nil, err
		}
		file := filepath.Join(opt.ListDir, "playwright-"+sanitize(project)+".txt")
		if err := os.WriteFile(file, []byte(strings.Join(paths, "\n")+"\n"), 0o644); err != nil {
			return nil, nil, err
		}
		return []Command{{Runner: runner, Project: project, Argv: []string{"npx", "playwright", "test", "--test-list", file}, ListFile: file}},
			[]string{"playwright: --test-list needs Playwright ≥ 1.56; paths in the file are relative to the project directory — check with --list that the test count is > 0"}, nil
	}
	cmds, notes := batch(runner, project, []string{"npx", "playwright", "test"}, args, opt.MaxArgChars)
	return cmds, append(notes, "playwright: the argument is a regex (QuoteMeta'd, unanchored): may over-select"), nil
}

func maxArgs(opt Options) int {
	if opt.MaxArgChars > 0 {
		return opt.MaxArgChars
	}
	return 8000
}

// batch splits args into batches so that the command-line length is <= max (an over-long argument still gets its own batch and is not dropped).
func batch(runner, project string, prefix, args []string, max int) ([]Command, []string) {
	base := 0
	for _, a := range prefix {
		base += len(a) + 1
	}
	var groups [][]string
	var cur []string
	size := base
	for _, a := range args {
		n := len(a) + 1
		if len(cur) > 0 && size+n > max {
			groups = append(groups, cur)
			cur, size = nil, base
		}
		cur = append(cur, a)
		size += n
	}
	if len(cur) > 0 {
		groups = append(groups, cur)
	}
	cmds := make([]Command, 0, len(groups))
	for i, g := range groups {
		c := Command{Runner: runner, Project: project, Argv: append(append([]string(nil), prefix...), g...)}
		if len(groups) > 1 {
			c.Batch, c.Batches = i+1, len(groups)
			c.Note = "batched: session-scoped fixtures re-run in every batch; exit codes must be merged (take the largest error code)"
		}
		cmds = append(cmds, c)
	}
	var notes []string
	if len(groups) > 1 {
		notes = append(notes, fmt.Sprintf("%s/%s: split into %d batches because the command line exceeds %d characters; batching changes session-scoped fixtures and how exit codes are merged", project, runner, len(groups), max))
	}
	return cmds, notes
}

func cleanProject(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	p = path.Clean(p)
	if p == "" || p == "/" {
		return "."
	}
	return strings.TrimPrefix(p, "./")
}

// relTo returns f (relative to the repo root, forward or back slashes) relative to the project directory.
func relTo(project, f string) (string, bool) {
	f = path.Clean(strings.ReplaceAll(f, "\\", "/"))
	if project == "." {
		return f, !strings.HasPrefix(f, "../") && !strings.HasPrefix(f, "/")
	}
	if !strings.HasPrefix(f, project+"/") {
		return "", false
	}
	return strings.TrimPrefix(f, project+"/"), true
}

func dedupe(in []string) []string {
	m := map[string]bool{}
	var out []string
	for _, s := range in {
		if !m[s] {
			m[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

func sanitize(s string) string {
	b := []byte(s)
	for i, c := range b {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
			b[i] = '_'
		}
	}
	return string(b)
}
