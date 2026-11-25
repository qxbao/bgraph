// Package model holds the data types shared between bgraph's packages (docs/architecture.md).
//
// Path convention: every path is relative to the repo root, uses forward slashes, is path.Clean'ed, has no leading "./"; the repo root is ".".
package model

import "time"

// Tier is the scope of a selection, from narrow to wide.
type Tier string

const (
	TierNone    Tier = "none"    // no test is affected
	TierGraph   Tier = "graph"   // the selection is provable from the graph
	TierPackage Tier = "package" // widened to a directory / package / Cargo target
	TierModule  Tier = "module"  // widened to a project (package.json, crate, Maven module, .NET project)
	TierFull    Tier = "full"    // run everything
)

// Rank: the ordering used to take the widest tier.
func (t Tier) Rank() int {
	switch t {
	case TierNone:
		return 0
	case TierGraph:
		return 1
	case TierPackage:
		return 2
	case TierModule:
		return 3
	}
	return 4
}

// Max returns the wider tier.
func Max(a, b Tier) Tier {
	if b.Rank() > a.Rank() {
		return b
	}
	return a
}

// Widening reasons (stable, machine-readable codes; documented in docs/architecture.md, "Widening reasons").
const (
	ReasonUnknownImport   = "unknown_import"
	ReasonDynamicImport   = "dynamic_import"
	ReasonAmbiguousImport = "ambiguous_import"
	ReasonUncertainScan   = "uncertain_scan"
	ReasonConfigIsCode    = "config_is_code"
	ReasonManifestChanged = "manifest_changed"
	ReasonTestConfig      = "test_config_changed"
	ReasonLockChanged     = "lock_changed"
	ReasonGlobalInput     = "global_input"
	ReasonNoBase          = "no_base"
	ReasonDiffTooLarge    = "diff_too_large"
	ReasonCaseMismatch    = "case_mismatch"
	ReasonUserRule        = "user_rule"
	ReasonUnknownFile     = "unknown_file" // file belongs to no ecosystem and nobody references it
	// code/data file of an ecosystem that nothing in the graph depends on (test fixture read via fs, script/bin
	// run by a test via child_process, entry point): nobody known to use it → widen to the project.
	ReasonUnreferenced    = "unreferenced_file"
	ReasonUnsupported     = "unsupported_language"
	ReasonGeneratedOutput = "generated_output" // import of an ignored build output
	ReasonFrameworkMagic  = "framework_magic"  // Next file routing, auto-import...
	ReasonSymlinkOutside  = "symlink_outside"  // a symbolic link involved in the change cannot be followed (outside, absolute, dangling, cycle)
	ReasonSubmodule       = "submodule"
	ReasonInternalError   = "internal_error" // bgraph's own error: always widen, never return empty
	ReasonRuleRejected    = "rule_rejected"  // an affects="none" rule matched a file that is a graph node: ignored (warning only, no widening)
	// ReasonDynamicSkipped (warnings only): the dynamic layer is enabled but did not narrow this selection; Detail says why.
	ReasonDynamicSkipped = "dynamic_skipped"
)

// Widening records one widening. Path is the file that caused it; Scope is the widened scope (package/project directory; empty when Tier=full).
type Widening struct {
	Tier    Tier   `json:"tier"`
	Reason  string `json:"reason"`
	Path    string `json:"path,omitempty"`
	Scope   string `json:"scope,omitempty"`
	Detail  string `json:"detail,omitempty"`
	Suggest string `json:"suggest,omitempty"` // suggested bgraph.toml rule to narrow it
	// Eco restricts a module/package widening to the test units of one ecosystem (empty: every ecosystem). Used by ecosystems whose
	// scope directory may also hold other ecosystems' projects (a Cargo.lock at the repo root must not select Node tests).
	Eco string `json:"eco,omitempty"`
	// Exact restricts a module/package widening to the test units whose Project is Scope itself: units of projects nested below
	// Scope (a Go module with its own go.mod inside the widened module) are not covered.
	Exact bool `json:"exact,omitempty"`
	// Seeds are graph nodes the selector walks backwards from, besides the changed files (lockfile levels L1/L2: the files of
	// a project whose dependency closure changed, or the virtual node of a changed package).
	Seeds []string `json:"-"`
}

// Role is the role of a file.
type Role string

const (
	RoleSource    Role = "source"
	RoleTest      Role = "test"
	RoleConfig    Role = "config"
	RoleManifest  Role = "manifest"
	RoleLock      Role = "lock"
	RoleData      Role = "data"
	RoleGenerated Role = "generated"
	RoleUnknown   Role = "unknown"
)

// ImportKind is the syntactic form of an import reference found by a scanner.
type ImportKind string

const (
	ImportStatic      ImportKind = "import"          // import ... from "x"; import "x"; export ... from "x" (see ReExport)
	ImportTypeOnly    ImportKind = "type_only"       // import type / export type ... from
	ImportDynamic     ImportKind = "dynamic"         // import("x")
	ImportRequire     ImportKind = "require"         // require("x"), import x = require("x")
	ImportResolve     ImportKind = "require_resolve" // require.resolve("x")
	ImportMock        ImportKind = "mock"            // jest.mock / vi.mock / jest.requireActual ... ("x")
	ImportGlob        ImportKind = "glob"            // import.meta.glob("pat"), require.context(dir, ...)
	ImportURL         ImportKind = "url"             // new URL("x", import.meta.url)
	ImportReExport    ImportKind = "reexport"        // export * from / export {a} from
	ImportTripleSlash ImportKind = "triple_slash"    // /// <reference path="x" />
	// Python. A Python import is an ImportRef whose Spec is the module path as written ("a.b", ".x", "..a.b"; leading dots = relative)
	// and whose Suffix holds the comma-separated names of a `from <Spec> import <names>` statement ("*" for a star import).
	ImportSysPath ImportKind = "sys_path" // sys.path.insert/append(...): Spec is a path relative to the file's directory, or "cwd:<path>"; Dynamic: not evaluable
	ImportPlugin  ImportKind = "plugin"   // pytest_plugins = ["a.b"]
	ImportRead    ImportKind = "read"     // PHP literal file access
	// C/C++: a quoted #include/#import/#embed is ImportStatic; an angle-bracket one is ImportSystem. Suffix holds the directive name.
	ImportSystem ImportKind = "system"
	// Ruby (internal/scan/ruby documents Spec, Suffix and Dynamic for each kind). Suffix holds the base of a path ("" a feature or a
	// path relative to an unknown directory, "rel" relative to the scanned file's directory, "root" relative to the project root).
	ImportRbRequire     ImportKind = "rb_require"      // require / require_relative / require_dependency / autoload
	ImportRbLoad        ImportKind = "rb_load"         // load (exact file name), eval(File.read(...))
	ImportRbGlob        ImportKind = "rb_glob"         // a Dir glob whose files are required/loaded
	ImportRbDataGlob    ImportKind = "rb_data_glob"    // a Dir glob read for data
	ImportRbString      ImportKind = "rb_string"       // a string literal or path expression that may name a repo file
	ImportRbZeitwerk    ImportKind = "rb_zeitwerk"     // Zeitwerk::Loader.for_gem / for_gem_extension
	ImportRbPushDir     ImportKind = "rb_push_dir"     // loader.push_dir(dir)
	ImportRbRails       ImportKind = "rb_rails"        // class X < Rails::Application (Spec "app") / Rails::Engine (Spec "engine")
	ImportRbTestPattern ImportKind = "rb_test_pattern" // Rake::TestTask / RSpec::Core::RakeTask pattern or test_files (Suffix rake|rspec)
	ImportRbTestTask    ImportKind = "rb_test_task"    // a Rake::TestTask (Spec rake) or RSpec::Core::RakeTask (Spec rspec) is defined
	ImportRbRubyOpt     ImportKind = "rb_ruby_opt"     // Rake::TestTask ruby_opts "-r feature"
	ImportRbTestLib     ImportKind = "rb_test_lib"     // Rake::TestTask libs entry
	ImportRbGem         ImportKind = "rb_gem"          // gem "name" (Gemfile): Spec name, Suffix the require: option ("-" for false)
	ImportRbGemspec     ImportKind = "rb_gemspec"      // gemspec (Gemfile): Spec the path: option
	ImportRbBundler     ImportKind = "rb_bundler"      // Bundler.require
	ImportRbBinPath     ImportKind = "rb_bin_path"     // Gem.bin_path("gem", "exe"): Spec the executable name
	ImportRbFixturesAll ImportKind = "rb_fixtures_all" // fixtures :all (Rails)
	ImportRbLoadPath    ImportKind = "rb_load_path"    // $LOAD_PATH / $: edit (recorded only)
	// Dart (internal/scan/dart documents Spec, Suffix and Dynamic for each kind).
	ImportDartImport  ImportKind = "dart_import"  // import directive (every URI of a conditional import)
	ImportDartExport  ImportKind = "dart_export"  // export directive (every URI of a conditional export)
	ImportDartPart    ImportKind = "dart_part"    // part directive; import augment (Suffix "augment")
	ImportDartString  ImportKind = "dart_string"  // a string literal that may name a file or directory
	ImportDartLoad    ImportKind = "dart_load"    // Isolate.spawnUri / loadUri: code loaded at run time
	ImportDartProcess ImportKind = "dart_process" // Process.run / runSync / start, TestProcess.start
)

// ImportRef is a reference found in the source text.
//
// Dynamic=true: the argument is not a constant string. Spec is then the static head (possibly empty) of the template/concatenation,
// e.g. `./locales/${x}.json` → Spec="./locales/", Suffix=".json". An empty static part → widen.
type ImportRef struct {
	Spec    string     `json:"spec"`
	Suffix  string     `json:"suffix,omitempty"`
	Kind    ImportKind `json:"kind"`
	Line    int        `json:"line"`
	Dynamic bool       `json:"dynamic,omitempty"`
}

// ScanResult is a scanner's output for one file. Uncertain=true: the import list cannot be trusted (the whole file widens to the project).
type ScanResult struct {
	Imports         []ImportRef `json:"imports"`
	Uncertain       bool        `json:"uncertain,omitempty"`
	UncertainReason string      `json:"uncertain_reason,omitempty"`
	// Rust is set by the Rust scanner only (.rs files); Imports is empty for them.
	Rust *RustScan `json:"rust,omitempty"`
	// Go is set by the Go scanner only (.go files) on a certain result; the imports are in Imports (Kind import, Spec = import path).
	Go *GoScan `json:"go,omitempty"`
	// Dart is the optional namespace information extracted from a Dart file.
	Dart *DartScan `json:"dart,omitempty"`
}

// DartScan describes the syntactic subset used for narrowing through re-exports.
type DartScan struct {
	Words      []string     `json:"words,omitempty"`      // sorted identifiers, including those in interpolation
	Standalone []string     `json:"standalone,omitempty"` // names of independent declarations with known syntax and types
	Exports    []DartExport `json:"exports,omitempty"`    // directives of a library consisting entirely of exports
}

// DartExport is one unconditional export with at most one show or hide clause.
type DartExport struct {
	URI   string   `json:"uri"`
	Names []string `json:"names,omitempty"`
	Hide  bool     `json:"hide,omitempty"`
}

// GoScan is what the Go scanner extracts from one .go file besides its imports.
type GoScan struct {
	Package string    `json:"package,omitempty"`
	Embeds  []GoEmbed `json:"embeds,omitempty"`
	// Strings: in _test.go files, the string literals that may name a path (they contain '/', '\' or '.'), plus the joined
	// literal leading arguments of X.Join("a", "b", ...) calls ("a/b"); in other files, only those starting with "./" or "../".
	Strings []string `json:"strings,omitempty"`
}

// GoEmbed is one pattern of a //go:embed directive.
type GoEmbed struct {
	Pattern string `json:"pattern,omitempty"` // as written, including an "all:" prefix
	Line    int    `json:"line"`
	Unknown string `json:"unknown,omitempty"` // non-empty when the directive could not be parsed (Pattern is then empty)
}

// RustScan is what the Rust scanner extracts from one .rs file: the module declarations that decide which Cargo target
// owns a file, and the include macros that read other files.
type RustScan struct {
	Mods     []RustMod     `json:"mods,omitempty"`
	Includes []RustInclude `json:"includes,omitempty"`
}

// RustMod is a `mod name;` declaration (inline `mod name { ... }` blocks are not listed: only their names appear in Nest).
type RustMod struct {
	Name string   `json:"name"`
	Line int      `json:"line"`
	Nest []string `json:"nest,omitempty"` // names of the enclosing inline modules, outermost first
	Path string   `json:"path,omitempty"` // literal #[path = "..."] attribute
	// Unknown: non-empty when the file this declaration loads cannot be determined lexically (cfg_attr(path), declared inside a
	// macro, non-literal #[path]...). The value says why.
	Unknown string `json:"unknown,omitempty"`
}

// Kinds of RustInclude.
const (
	RustIncludeLiteral  = "literal"  // relative to the file containing the macro
	RustIncludeManifest = "manifest" // concat!(env!("CARGO_MANIFEST_DIR"), "...") : relative to the package directory
	RustIncludeOutDir   = "outdir"   // concat!(env!("OUT_DIR"), "...") : build script output, not in the tree
	RustIncludeDir      = "dir"      // automod::dir!("path"): modules generated from the files of a directory (relative to the package directory)
	RustIncludeDynamic  = "dynamic"  // anything else
)

// RustInclude is an include!/include_str!/include_bytes! invocation (or automod::dir!, Macro "automod::dir").
type RustInclude struct {
	Macro string `json:"macro"` // include, include_str, include_bytes
	Kind  string `json:"kind"`
	Path  string `json:"path,omitempty"`
	Line  int    `json:"line"`
}

// EdgeKind is the kind of an edge in the file-level graph.
type EdgeKind string

const (
	EdgeImport      EdgeKind = "import"
	EdgeTypeOnly    EdgeKind = "type_only"
	EdgeReExport    EdgeKind = "reexport"
	EdgeMod         EdgeKind = "mod"
	EdgeInclude     EdgeKind = "include"
	EdgeProjectRef  EdgeKind = "project_ref"
	EdgeConftest    EdgeKind = "conftest"
	EdgeImplicit    EdgeKind = "implicit"
	EdgeFixtureData EdgeKind = "fixture_data"
	EdgePattern     EdgeKind = "pattern"
	EdgeProbe       EdgeKind = "probe"
	EdgeGenerated   EdgeKind = "generated"
	EdgeRule        EdgeKind = "rule"
	EdgeMock        EdgeKind = "mock"
	// EdgeSymlink: From is a path that reads To through a symbolic link (a file link, or a file under a directory link).
	EdgeSymlink EdgeKind = "symlink"
)

// Provenance is where an edge came from.
type Provenance string

const (
	ProvScan     Provenance = "scan"
	ProvRule     Provenance = "rule"
	ProvProvider Provenance = "provider"
	ProvDyn      Provenance = "dyn"
)

// Edge: From depends on To (changing To affects From). For Kind=EdgePattern, To is a glob evaluated on both trees.
type Edge struct {
	From       string     `json:"from"`
	To         string     `json:"to"`
	Kind       EdgeKind   `json:"kind"`
	Line       int        `json:"line,omitempty"`
	Spec       string     `json:"spec,omitempty"`
	Provenance Provenance `json:"provenance,omitempty"`
}

// UnresolvedClass classifies an unresolved import.
type UnresolvedClass string

const (
	UnresolvedUnknown       UnresolvedClass = "unknown"
	UnresolvedDynamic       UnresolvedClass = "dynamic"
	UnresolvedAmbiguous     UnresolvedClass = "ambiguous"
	UnresolvedUncertainScan UnresolvedClass = "uncertain_scan"
)

// Unresolved: an import of File that could not be resolved. When a file in ScopeDir (same project/package) changes, the selection widens to
// ScopeTier/ScopeDir. An empty ScopeDir with ScopeTier=full means the whole repo.
type Unresolved struct {
	File      string          `json:"file"`
	Spec      string          `json:"spec"`
	Class     UnresolvedClass `json:"class"`
	Line      int             `json:"line,omitempty"`
	ScopeTier Tier            `json:"scope_tier"`
	ScopeDir  string          `json:"scope_dir"`
	Detail    string          `json:"detail,omitempty"`
	// TriggerGlobs narrows what counts as "a file changed within scope": when non-empty, only a changed path inside ScopeDir that
	// also matches one of these globs seeds the importer. It is for imports that can only load files of a known kind (a dynamic
	// Python import loads modules, never data files). Empty: any changed file within scope triggers.
	TriggerGlobs []string `json:"trigger_globs,omitempty"`
	// SkipTests: a changed test file that no real reference (import, glob, literal; ownership does not count) reaches does not
	// trigger. For computed loads in ecosystems whose test files are only loaded by the runner (a Ruby `require name`): such a
	// file is run on its own when it changes, and a computed load is assumed not to load it.
	SkipTests bool `json:"skip_tests,omitempty"`
}

// TestUnit is a unit a test runner can run on its own (docs/architecture.md).
type TestUnit struct {
	Eco     string   `json:"eco"`
	ID      string   `json:"id"`      // stable identifier: test file (JS/Python), "crate::target"...
	Project string   `json:"project"` // directory of the project containing the unit
	Files   []string `json:"files"`
	Runner  string   `json:"runner,omitempty"` // jest, vitest, node-test, pytest, cargo...
	// Whole: the "whole test suite of the project" unit, used when the set of test files is unknown (test pattern computed by code). Files is empty; the runner runs
	// the whole project, without a list.
	Whole bool `json:"whole,omitempty"`
	// Package is the package name of the unit for runners that address units by package (cargo -p).
	Package string `json:"package,omitempty"`
	// Config is a runner-specific launch file: the pytest config file (repo-relative; empty when pytest has none), or for the Python
	// "django" runner the driver script (tests/runtests.py or manage.py).
	Config string `json:"config,omitempty"`
	// BinDir and RunnerPaths hold the Composer launcher directory and explicit
	// test operands for PHP. Their paths are relative to the repository.
	BinDir      string   `json:"bin_dir,omitempty"`
	RunnerPaths []string `json:"runner_paths,omitempty"`
	// TestModules lists every declared test module in the unit's SwiftPM package.
	// The runner can exclude unselected modules while keeping other discovered tests.
	TestModules []string `json:"test_modules,omitempty"`
	// Dir is the directory the unit's tests live in, set when Files holds only a virtual node and the unit may be narrower than its
	// project (a Go package inside a module): a [[rule]] directory target matches it like a file of the unit.
	Dir string `json:"dir,omitempty"`
}

// SelectedTest is a selected test unit together with its reason chain.
type SelectedTest struct {
	Unit        string   `json:"unit"`
	Project     string   `json:"project,omitempty"`
	Runner      string   `json:"runner,omitempty"`
	Files       []string `json:"files"`
	Reasons     []string `json:"reasons"`
	Whole       bool     `json:"whole,omitempty"`   // run the project's whole test suite (see TestUnit.Whole)
	Package     string   `json:"package,omitempty"` // see TestUnit.Package
	Config      string   `json:"config,omitempty"`  // see TestUnit.Config
	BinDir      string   `json:"bin_dir,omitempty"`
	RunnerPaths []string `json:"runner_paths,omitempty"`
	TestModules []string `json:"test_modules,omitempty"` // see TestUnit.TestModules
	Dir         string   `json:"dir,omitempty"`          // see TestUnit.Dir (Go: the package directory)
	// Widened: the unit is selected only because of a widening (unresolved import seed, module/package scope widening, "whole
	// project" unit), not by the backward walk from the changed files, a [[rule]] target, previously_failed or as a new test
	// (docs/dynamic-layer.md §1). Only widened units can be removed by the dynamic layer.
	Widened bool `json:"widened,omitempty"`
	// Dynamic: why the dynamic layer kept this widened unit (a changed file is among its recorded dependencies, or its record
	// is not usable). Empty when the dynamic layer did not run or the unit is precise.
	Dynamic string `json:"dynamic,omitempty"`
	// ClassFilter: the JVM class layer narrowed the unit (docs/dynamic-layer.md §9): the unit is still built, but only the test classes
	// in Classes run (none when Classes is empty).
	ClassFilter bool     `json:"class_filter,omitempty"`
	Classes     []string `json:"classes,omitempty"` // binary names of the top-level test classes to run (with ClassFilter)
}

// NarrowedTest is a widened unit the dynamic layer removed from the selection (docs/dynamic-layer.md §6).
type NarrowedTest struct {
	Unit     string    `json:"unit"`
	Recorded time.Time `json:"recorded"`         // when the record that justified the removal was made
	Commit   string    `json:"commit,omitempty"` // HEAD when it was recorded (informational)
	Reason   string    `json:"reason"`
}

// Selection is the selection result (its JSON form is documented in docs/cli.md).
type Selection struct {
	Scope     Tier           `json:"scope"`
	Base      string         `json:"base,omitempty"`
	Head      string         `json:"head,omitempty"`
	Changed   []string       `json:"changed"`
	Tests     []SelectedTest `json:"tests,omitempty"`
	Widening  []Widening     `json:"widening,omitempty"`
	Warnings  []Widening     `json:"warnings,omitempty"` // do not change the scope (case_mismatch, rule_rejected...)
	AlwaysRun []string       `json:"always_run,omitempty"`
	// Narrowed: widened units removed by the dynamic layer (docs/dynamic-layer.md); they are not in Tests.
	Narrowed   []NarrowedTest `json:"narrowed,omitempty"`
	IndexStats map[string]int `json:"index_stats,omitempty"`
}
