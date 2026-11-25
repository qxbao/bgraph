// Package rules reads bgraph.toml (docs/configuration.md): user-declared rules and declarations of generated-code outputs.
//
// Strict decoding: unknown keys are an error. A bgraph.toml error is NEVER ignored; callers widen to full (user_rule).
package rules

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/qxbao/bgraph/internal/glob"

	"github.com/pelletier/go-toml/v2"
)

// FileName is the name of the rules file at the repo root.
const FileName = "bgraph.toml"

// DefaultMaxChanged is the default threshold on the number of changed files; beyond it a full run is used.
const DefaultMaxChanged = 5000

// Rule is a [[rule]]. AffectsAll/AffectsNone/Affects are mutually exclusive.
type Rule struct {
	Index       int // order in the file, from 0
	Paths       []*glob.Pattern
	RawPaths    []string
	AffectsAll  bool
	AffectsNone bool
	Affects     []string // project directory or test glob
	Reason      string
}

// Generated is a [[generated]]: an ignored output that code imports.
type Generated struct {
	Outputs, Sources       []*glob.Pattern
	RawOutputs, RawSources []string
	Owner                  string
}

// Config is a [[config]]: the user's description of runner-config keys whose value is computed in code, which bgraph
// cannot read and therefore widens on (config_is_code). A declaration replaces that widening for the named keys of one
// config file. It is a user assertion: a wrong declaration can make bgraph skip a test that depends on the change (the
// same trust as affects = "none"), so every field is explicit and a reason is required.
type Config struct {
	Index    int
	File     string   // repo-relative runner config file (vitest.config.ts, jest.config.js...)
	Replaces []string // computed keys this declaration describes (see ConfigKeys)
	// Alias (vite semantics: spec == key or starts with key + "/" → target + rest) and AliasRegex (jest moduleNameMapper
	// semantics: full-match regex → target with $1..$9). Both empty with an alias key replaced: the computed alias only
	// redirects to what normal package resolution (workspace package.json exports/main) finds.
	Alias      map[string]string
	AliasRegex map[string]string
	// Tests: test-file globs (repo-relative) for replaced test-pattern keys; empty → bgraph's default test-file detection
	// finds every test.
	Tests       []*glob.Pattern
	RawTests    []string
	Sources     []string // helper files that compute the keys: their unresolved imports are described by this declaration
	IgnorePaths []string // path literals in the config that belong to the computation, not inputs of every test
	Reason      string
}

// Dynamic is a [[dynamic]]: the user's description of what the dynamic imports (importlib.import_module(name), __import__(name),
// non-literal sys.path edits...) of one file can load. Without it bgraph cannot know and treats any change in the project as a
// possible target. Like [[config]] it narrows the selection on the user's word: a reason is required. The Python and Ruby
// ecosystems consume it (Ruby: computed require/load/autoload sites; sys_path_only does not apply).
type Dynamic struct {
	Index       int
	File        string // repo-relative file containing the dynamic sites
	Lines       []int  // only these sites; empty: every dynamic site of the file
	Loads       []*glob.Pattern
	RawLoads    []string
	None        bool // the sites load nothing from the repo
	SysPathOnly bool // the declaration covers only the sys.path sites of the file
	Reason      string
}

// RubyAutoload declares Ruby files reached through ordinary constant lookup or modeled loads.
type RubyAutoload struct {
	Paths  []*glob.Pattern
	Reason string
}

// Config keys a [[config]] may replace, by kind.
var (
	AliasKeys       = []string{"alias", "moduleNameMapper"}
	TestPatternKeys = []string{"include", "testMatch", "testRegex"}
	ProjectKeys     = []string{"projects", "workspace"}
)

// ConfigKeys are all the keys a [[config]] may replace.
var ConfigKeys = slices.Concat(AliasKeys, TestPatternKeys, ProjectKeys)

// Options holds the optional knobs.
type Options struct {
	MaxChanged int
}

// DynamicEcosystems are the ecosystems with a dynamic layer (docs/dynamic-layer.md).
var DynamicEcosystems = []string{"python", "node", "jvm", "dotnet", "ruby", "php", "go"}

// DynamicLayer is the [dynamic_layer] table: the dynamic layer (docs/dynamic-layer.md), off by default. It is a table of its own,
// not [dynamic], because [[dynamic]] is already the array of dynamic-import declarations and TOML cannot hold both.
type DynamicLayer struct {
	// Ecosystems narrowed by recorded dependencies (subset of DynamicEcosystems). Empty: the layer is off unless the caller
	// enables it (bgraph --dynamic).
	Ecosystems []string
	// Python: the interpreter used by `bgraph record` and for the environment fingerprint at selection time. A path with a
	// separator is relative to the repo root; a bare name is looked up on PATH. Empty: "python".
	Python string
	// Node: the node executable used by `bgraph record` and for the Node environment fingerprint (same rules as Python).
	// Empty: "node".
	Node string
	// Ruby: the Ruby executable used by `bgraph record` and for the Ruby environment fingerprint (same rules as Python; its
	// directory is put first on PATH for the recorded test processes). Empty: "ruby".
	Ruby string
	// PHP: the PHP executable used by `bgraph record` (it must load Xdebug 3) and, at selection time, for the PHP environment
	// fingerprint and the tokenizer cross-check of the function-level refinement (same rules as Python). Empty: "php".
	PHP string
	// Go: the go command used by `bgraph record` and, at selection time, for the Go environment fingerprint (same rules as
	// Python). Empty: "go".
	Go string
	// TrustNativeEnv: native extension modules installed in the environment (not CPython's own) do not make a record
	// incomplete; the environment fingerprint covers their versions.
	TrustNativeEnv bool
}

// File is the validated content of bgraph.toml.
type File struct {
	Rules        []Rule
	Generated    []Generated
	Configs      []Config
	Dynamics     []Dynamic
	RubyAutoload []RubyAutoload
	Options      Options
	Dynamic      DynamicLayer // [dynamic_layer]
	Lockfile     Lockfile     // [lockfile]
	Path         string       // path of the file read; empty if there is no file
}

// Lockfile levels (docs/lockfiles.md).
const (
	LockL0 = "L0" // every project of the lockfile's directory
	LockL1 = "L1" // projects whose resolved dependency closure changed, and their reverse dependents (default)
	LockL2 = "L2" // within those projects, only the tests that import a changed package (opt-in, guarded)
)

// Lockfile is the [lockfile] table.
type Lockfile struct {
	Level string // LockL0, LockL1 or LockL2; empty means LockL1
}

// LockLevel returns the configured lockfile level (LockL1 when f is nil or the level is not set).
func (f *File) LockLevel() string {
	if f == nil || f.Lockfile.Level == "" {
		return LockL1
	}
	return f.Lockfile.Level
}

type rawFile struct {
	Rule []struct {
		Paths   []string `toml:"paths"`
		Affects any      `toml:"affects"`
		Reason  string   `toml:"reason"`
	} `toml:"rule"`
	Generated []struct {
		Outputs []string `toml:"outputs"`
		Sources []string `toml:"sources"`
		Owner   string   `toml:"owner"`
	} `toml:"generated"`
	Config []struct {
		File        string            `toml:"file"`
		Replaces    []string          `toml:"replaces"`
		Alias       map[string]string `toml:"alias"`
		AliasRegex  map[string]string `toml:"alias_regex"`
		Tests       []string          `toml:"tests"`
		Sources     []string          `toml:"sources"`
		IgnorePaths []string          `toml:"ignore_paths"`
		Reason      string            `toml:"reason"`
	} `toml:"config"`
	Dynamic []struct {
		File        string   `toml:"file"`
		Lines       []int64  `toml:"lines"`
		Loads       []string `toml:"loads"`
		None        bool     `toml:"none"`
		SysPathOnly bool     `toml:"sys_path_only"`
		Reason      string   `toml:"reason"`
	} `toml:"dynamic"`
	RubyAutoload []struct {
		Paths  []string `toml:"paths"`
		Reason string   `toml:"reason"`
	} `toml:"ruby_autoload"`
	Options struct {
		MaxChanged *int64 `toml:"max_changed"`
	} `toml:"options"`
	DynamicLayer struct {
		Ecosystems     []string `toml:"ecosystems"`
		Python         string   `toml:"python"`
		Node           string   `toml:"node"`
		Ruby           string   `toml:"ruby"`
		PHP            string   `toml:"php"`
		Go             string   `toml:"go"`
		TrustNativeEnv bool     `toml:"trust_native_env"`
	} `toml:"dynamic_layer"`
	Lockfile struct {
		Level string `toml:"level"`
	} `toml:"lockfile"`
}

// Load reads <root>/bgraph.toml. No file → an empty File with defaults, no error.
func Load(root string) (*File, error) {
	p := filepath.Join(root, FileName)
	data, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &File{Options: Options{MaxChanged: DefaultMaxChanged}}, nil
		}
		return nil, fmt.Errorf("%s: %w", FileName, err)
	}
	return Parse(p, data)
}

// Parse decodes and validates the content; name is used in error messages and assigned to File.Path.
func Parse(name string, data []byte) (*File, error) {
	var raw rawFile
	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		var se *toml.StrictMissingError
		if errors.As(err, &se) {
			return nil, fmt.Errorf("%s: invalid key:\n%s", name, se.String())
		}
		var de *toml.DecodeError
		if errors.As(err, &de) {
			return nil, fmt.Errorf("%s: %s", name, de.String())
		}
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	f := &File{Path: name, Options: Options{MaxChanged: DefaultMaxChanged}}

	for i, r := range raw.Rule {
		out := Rule{Index: i, RawPaths: r.Paths, Reason: r.Reason}
		if len(r.Paths) == 0 {
			return nil, fmt.Errorf("%s: rule[%d]: 'paths' is required and must not be empty", name, i)
		}
		for _, s := range r.Paths {
			p, err := glob.Compile(s)
			if err != nil {
				return nil, fmt.Errorf("%s: rule[%d].paths: %w", name, i, err)
			}
			out.Paths = append(out.Paths, p)
		}
		switch v := r.Affects.(type) {
		case nil:
			return nil, fmt.Errorf("%s: rule[%d]: 'affects' is required", name, i)
		case string:
			switch v {
			case "all":
				out.AffectsAll = true
			case "none":
				out.AffectsNone = true
			default:
				return nil, fmt.Errorf("%s: rule[%d].affects: a string must be \"all\" or \"none\" (got %q); use an array for directories/globs", name, i, v)
			}
		case []any:
			if len(v) == 0 {
				return nil, fmt.Errorf("%s: rule[%d].affects: empty array", name, i)
			}
			for _, e := range v {
				s, ok := e.(string)
				if !ok || s == "" {
					return nil, fmt.Errorf("%s: rule[%d].affects: elements must be non-empty strings", name, i)
				}
				if _, err := glob.Compile(s); err != nil {
					return nil, fmt.Errorf("%s: rule[%d].affects: %w", name, i, err)
				}
				out.Affects = append(out.Affects, s)
			}
		default:
			return nil, fmt.Errorf("%s: rule[%d].affects: must be \"all\", \"none\" or an array of strings", name, i)
		}
		f.Rules = append(f.Rules, out)
	}

	for i, g := range raw.Generated {
		out := Generated{RawOutputs: g.Outputs, RawSources: g.Sources, Owner: g.Owner}
		if len(g.Outputs) == 0 {
			return nil, fmt.Errorf("%s: generated[%d]: 'outputs' is required and must not be empty", name, i)
		}
		if g.Owner == "" {
			return nil, fmt.Errorf("%s: generated[%d]: 'owner' is required", name, i)
		}
		for _, s := range g.Outputs {
			p, err := glob.Compile(s)
			if err != nil {
				return nil, fmt.Errorf("%s: generated[%d].outputs: %w", name, i, err)
			}
			out.Outputs = append(out.Outputs, p)
		}
		for _, s := range g.Sources {
			p, err := glob.Compile(s)
			if err != nil {
				return nil, fmt.Errorf("%s: generated[%d].sources: %w", name, i, err)
			}
			out.Sources = append(out.Sources, p)
		}
		f.Generated = append(f.Generated, out)
	}

	for i, c := range raw.Config {
		at := fmt.Sprintf("%s: config[%d]", name, i)
		file, ok := repoPath(c.File)
		if !ok {
			return nil, fmt.Errorf("%s: 'file' is required: the repo-relative path of the runner config whose keys are computed in code", at)
		}
		out := Config{Index: i, File: file, Replaces: c.Replaces, Alias: map[string]string{}, AliasRegex: map[string]string{},
			RawTests: c.Tests, Reason: c.Reason}
		if len(c.Replaces) == 0 {
			return nil, fmt.Errorf("%s: 'replaces' is required: the computed keys this declaration describes (%s)", at, strings.Join(ConfigKeys, ", "))
		}
		for _, k := range c.Replaces {
			if !slices.Contains(ConfigKeys, k) {
				return nil, fmt.Errorf("%s.replaces: %q is not supported (allowed: %s)", at, k, strings.Join(ConfigKeys, ", "))
			}
		}
		if strings.TrimSpace(c.Reason) == "" {
			return nil, fmt.Errorf("%s: 'reason' is required (a declaration narrows the selection: say why it is right)", at)
		}
		hasAny := func(keys []string) bool {
			return slices.ContainsFunc(c.Replaces, func(k string) bool { return slices.Contains(keys, k) })
		}
		if (len(c.Alias) > 0 || len(c.AliasRegex) > 0) && !hasAny(AliasKeys) {
			return nil, fmt.Errorf("%s: 'alias'/'alias_regex' need an alias key in 'replaces' (%s)", at, strings.Join(AliasKeys, ", "))
		}
		if len(c.Tests) > 0 && !hasAny(TestPatternKeys) {
			return nil, fmt.Errorf("%s: 'tests' needs a test-pattern key in 'replaces' (%s)", at, strings.Join(TestPatternKeys, ", "))
		}
		for k, v := range c.Alias {
			t, ok := repoPath(v)
			if k == "" || !ok {
				return nil, fmt.Errorf("%s.alias: %q = %q: the key must be non-empty and the target a repo-relative path inside the repo", at, k, v)
			}
			out.Alias[k] = t
		}
		for k, v := range c.AliasRegex {
			if _, err := regexp.Compile(k); err != nil {
				return nil, fmt.Errorf("%s.alias_regex: %q: %w", at, k, err)
			}
			if _, ok := repoPath(v); !ok {
				return nil, fmt.Errorf("%s.alias_regex: %q = %q: the target must be a repo-relative path inside the repo", at, k, v)
			}
			out.AliasRegex[k] = strings.ReplaceAll(v, "\\", "/")
		}
		for _, g := range c.Tests {
			p, err := glob.Compile(g)
			if err != nil {
				return nil, fmt.Errorf("%s.tests: %w", at, err)
			}
			out.Tests = append(out.Tests, p)
		}
		for _, list := range []struct {
			key string
			in  []string
			out *[]string
		}{{"sources", c.Sources, &out.Sources}, {"ignore_paths", c.IgnorePaths, &out.IgnorePaths}} {
			for _, v := range list.in {
				t, ok := repoPath(v)
				if !ok {
					return nil, fmt.Errorf("%s.%s: %q: must be a repo-relative path inside the repo", at, list.key, v)
				}
				*list.out = append(*list.out, t)
			}
		}
		f.Configs = append(f.Configs, out)
	}

	for i, d := range raw.Dynamic {
		at := fmt.Sprintf("%s: dynamic[%d]", name, i)
		file, ok := repoPath(d.File)
		if !ok {
			return nil, fmt.Errorf("%s: 'file' is required: the repo-relative path of the file that contains the dynamic imports", at)
		}
		out := Dynamic{Index: i, File: file, RawLoads: d.Loads, None: d.None, SysPathOnly: d.SysPathOnly, Reason: d.Reason}
		if strings.TrimSpace(d.Reason) == "" {
			return nil, fmt.Errorf("%s: 'reason' is required (a declaration narrows the selection: say why it is right)", at)
		}
		for _, ln := range d.Lines {
			if ln < 1 {
				return nil, fmt.Errorf("%s.lines: line numbers start at 1 (got %d)", at, ln)
			}
			out.Lines = append(out.Lines, int(ln))
		}
		switch {
		case d.None && len(d.Loads) > 0:
			return nil, fmt.Errorf("%s: 'none = true' and 'loads' are mutually exclusive", at)
		case len(d.Loads) == 0 && !d.None && !d.SysPathOnly:
			return nil, fmt.Errorf("%s: 'loads' is required (globs of what the dynamic imports can load), or 'none = true', or 'sys_path_only = true'", at)
		}
		if d.SysPathOnly && len(d.Lines) > 0 {
			return nil, fmt.Errorf("%s: 'sys_path_only' and 'lines' cannot be combined (pick the sites by line or all sys.path sites)", at)
		}
		for _, g := range d.Loads {
			if g == "" {
				return nil, fmt.Errorf("%s.loads: empty glob", at)
			}
			p, err := glob.Compile(g)
			if err != nil {
				return nil, fmt.Errorf("%s.loads: %w", at, err)
			}
			out.Loads = append(out.Loads, p)
		}
		f.Dynamics = append(f.Dynamics, out)
	}

	for i, a := range raw.RubyAutoload {
		at := fmt.Sprintf("%s: ruby_autoload[%d]", name, i)
		if len(a.Paths) == 0 || strings.TrimSpace(a.Reason) == "" {
			return nil, fmt.Errorf("%s: 'paths' and 'reason' are required", at)
		}
		out := RubyAutoload{Reason: a.Reason}
		for _, g := range a.Paths {
			if strings.TrimSpace(g) == "" {
				return nil, fmt.Errorf("%s.paths: empty glob", at)
			}
			p, err := glob.Compile(g)
			if err != nil {
				return nil, fmt.Errorf("%s.paths: %w", at, err)
			}
			out.Paths = append(out.Paths, p)
		}
		f.RubyAutoload = append(f.RubyAutoload, out)
	}

	if m := raw.Options.MaxChanged; m != nil {
		if *m < 1 {
			return nil, fmt.Errorf("%s: options.max_changed: must be ≥ 1 (got %d)", name, *m)
		}
		f.Options.MaxChanged = int(*m)
	}
	dl := raw.DynamicLayer
	for _, e := range dl.Ecosystems {
		if !slices.Contains(DynamicEcosystems, e) {
			return nil, fmt.Errorf("%s: dynamic_layer.ecosystems: %q has no dynamic layer (supported: %s)", name, e, strings.Join(DynamicEcosystems, ", "))
		}
		if !slices.Contains(f.Dynamic.Ecosystems, e) {
			f.Dynamic.Ecosystems = append(f.Dynamic.Ecosystems, e)
		}
	}
	if strings.ContainsRune(dl.Python, 0) || dl.Python != strings.TrimSpace(dl.Python) {
		return nil, fmt.Errorf("%s: dynamic_layer.python: invalid interpreter %q", name, dl.Python)
	}
	if strings.ContainsRune(dl.Node, 0) || dl.Node != strings.TrimSpace(dl.Node) {
		return nil, fmt.Errorf("%s: dynamic_layer.node: invalid executable %q", name, dl.Node)
	}
	if strings.ContainsRune(dl.Ruby, 0) || dl.Ruby != strings.TrimSpace(dl.Ruby) {
		return nil, fmt.Errorf("%s: dynamic_layer.ruby: invalid executable %q", name, dl.Ruby)
	}
	if strings.ContainsRune(dl.PHP, 0) || dl.PHP != strings.TrimSpace(dl.PHP) {
		return nil, fmt.Errorf("%s: dynamic_layer.php: invalid executable %q", name, dl.PHP)
	}
	if strings.ContainsRune(dl.Go, 0) || dl.Go != strings.TrimSpace(dl.Go) {
		return nil, fmt.Errorf("%s: dynamic_layer.go: invalid executable %q", name, dl.Go)
	}
	f.Dynamic.Python, f.Dynamic.Node, f.Dynamic.Ruby, f.Dynamic.PHP, f.Dynamic.Go, f.Dynamic.TrustNativeEnv = dl.Python, dl.Node, dl.Ruby,
		dl.PHP, dl.Go, dl.TrustNativeEnv
	switch lv := raw.Lockfile.Level; lv {
	case "", LockL0, LockL1, LockL2:
		f.Lockfile.Level = lv
	default:
		return nil, fmt.Errorf("%s: lockfile.level: %q is not a level (L0, L1 or L2)", name, lv)
	}
	return f, nil
}

// repoPath cleans a user-written repo-relative path (slash form) and reports whether it stays inside the repo.
func repoPath(p string) (string, bool) {
	if p == "" {
		return "", false
	}
	c := path.Clean(strings.ReplaceAll(p, "\\", "/"))
	if c == "." || c == ".." || strings.HasPrefix(c, "../") || strings.HasPrefix(c, "/") || (len(c) > 1 && c[1] == ':') {
		return "", false
	}
	return c, true
}

// Match returns the rules whose paths match path, in file order.
func (f *File) Match(path string) []Rule {
	var out []Rule
	for _, r := range f.Rules {
		for _, p := range r.Paths {
			if p.Match(path) {
				out = append(out, r)
				break
			}
		}
	}
	return out
}

// GeneratedFor returns the first [[generated]] whose outputs match path, or nil.
func (f *File) GeneratedFor(path string) *Generated {
	for i := range f.Generated {
		for _, p := range f.Generated[i].Outputs {
			if p.Match(path) {
				return &f.Generated[i]
			}
		}
	}
	return nil
}
