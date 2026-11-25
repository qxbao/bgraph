# Configuration: `bgraph.toml`

`bgraph.toml` is optional. Put it at the repository root and commit it. It declares what bgraph cannot infer from the
code: dependencies no import expresses, generated files, runner-config keys computed by code, targets of dynamic imports,
and conventional Ruby files reached through lazy constant lookup. It also holds a few settings.

A complete annotated example is [`bgraph.example.toml`](bgraph.example.toml). Start from what bgraph reports:
`bgraph unresolved` prints a ready-to-edit snippet next to every import or config key it could not read, and every
widening in `bgraph affected` output may carry a `suggest` snippet.

## General rules

- **Strict parsing.** Unknown keys and invalid values are errors. A `bgraph.toml` that does not parse or validate makes
  every selection `full` (`user_rule`), and the error says where.
- **Read from the head tree.** The file of the tree being tested is used (the working tree by default).
- **Changing the file** is a change like any other: unless a `[[rule]]` matches it, `bgraph.toml` is an unclaimed file
  and widens to the project at the repository root (`unknown_file`), or to `full` when no project sits at the root.
- **Paths** are repo-relative with forward slashes. The single-path keys of `[[config]]` and `[[dynamic]]` (`file`,
  alias targets, `sources`, `ignore_paths`) must stay inside the repository; a path that leaves it is an error.
- **Globs**: `*` (within one path segment), `**` (any depth), `?`, `[abc]`, `[!a]`, `{a,b}`; case-sensitive. A pattern
  without `/` matches only at the repository root (`*.proto` is not `**/*.proto`).

### Widening versus narrowing entries

| Kind | Effect | A wrong entry... |
|---|---|---|
| **Widening** (safe) | adds dependencies, so more tests run | only costs CI time |
| **Narrowing** (trusted) | tells bgraph that something it would widen on is fine | can make bgraph **skip a test the change breaks** |

`[[config]]`, `[[dynamic]]` and `[[ruby_autoload]]` entries require a `reason`; give one to `affects = "none"` rules too (it is optional
there). Review narrowing entries like code, and re-check them when what they describe changes.

## Tables at a glance

| Table | Kind | Read by | Purpose |
|---|---|---|---|
| [`[[rule]]`](#rule) | widening (`"none"`: narrowing) | all ecosystems | a dependency no import expresses |
| [`[[generated]]`](#generated) | narrowing | Node, Dart | git-ignored build outputs that code imports |
| [`[[config]]`](#config) | narrowing | Node | runner-config keys computed in code |
| [`[[dynamic]]`](#dynamic) | narrowing | Python, Ruby | what dynamic imports of a file can load |
| [`[[ruby_autoload]]`](#ruby_autoload) | narrowing | Ruby | which lazy autoload targets can follow constant references |
| [`[dynamic_layer]`](#dynamic_layer) | narrowing | Python, Node, JVM, .NET, Ruby, PHP, Go | enable the record-based dynamic layer |
| [`[lockfile]`](#lockfile) | narrowing at `L2` | Node, Rust, Python | lockfile comparison level |
| [`[options]`](#options) | – | selector | thresholds |

## `[[rule]]`

A dependency that no import expresses. Rules are checked **before** the global widening table and the ecosystems, so a
rule can also override them (see [Architecture](architecture.md#rules-come-first)).

| Key | Required | Value |
|---|---|---|
| `paths` | yes | non-empty list of globs: the files the rule is about |
| `affects` | yes | `"all"`, `"none"`, or a non-empty list of directories, unit IDs or globs |
| `reason` | no (recommended) | free text, shown in explanations |

`affects`:

| Value | Effect | Kind |
|---|---|---|
| `"all"` | run the full suite (`user_rule`) | widening |
| `["dir", "glob"]` | also select every test unit under these directories, named by these unit IDs, or with a file matching these globs | widening |
| `"none"` | the change affects no test | narrowing |

`"none"` is accepted only for files that are not part of the import graph. If a matching file is a graph node, the rule
is ignored for it and reported as `rule_rejected`.

Directory targets select units whose project or files lie under the directory; for Go, a unit's package directory counts
too, so a module directory selects every package of the module and a package directory selects that package and the
packages below it. A target may also be a unit ID as bgraph prints it (`go:internal/store`), which selects exactly that
unit. Globs match the units' files; a glob over `.go` files selects no Go unit (Go units are packages, see
[Go](ecosystems/go.md#addressing-units-in-bgraphtoml)).

For PHP, use a project directory or an exact ID such as `services/api::php` (`.::php` at the repository root).
See [PHP](ecosystems/php.md#addressing-units-in-bgraphtoml).

For Swift, use a package or test target directory, or an exact ID such as `swift:packages/kit::test:KitTests`. An opaque package has
an ID such as `swift:packages/kit::all`. See [Swift](ecosystems/swift.md#addressing-units-in-bgraphtoml).

For C and C++, use a build project directory or an exact ID such as `cpp:cmake:library::test:parse`.
See [C and C++](ecosystems/cpp.md#addressing-units-in-bgraphtoml).

For Dart, use a test file, a glob over test files, a package directory, or a whole-package ID such as
`<dart-all-tests:packages/core>`. See [Dart](ecosystems/dart.md#addressing-units-in-bgraphtoml).

A target that matches no test unit of the current tree (a mistyped or stale directory, a package without tests, a glob
that matches no unit's file) is a configuration error: when the rule matches a changed file, the selection is `full`
(`user_rule`) and the detail names the target.

```toml
[[rule]]
# Code is generated from the .proto files at build time; nothing imports them.
paths = ["proto/**/*.proto"]
affects = ["services/api", "web/src/gen/**/*.test.ts"]
reason = "services/api and the web client are generated from the protos"

[[rule]]
paths = [".vscode/**"]
affects = "none"
reason = "not read by any build or test"
```

## `[[generated]]`

Build outputs that git ignores but code imports (for example tests importing `"../dist/index.js"`, a workspace
package whose `exports` point to `dist/`, or a Dart `part 'user.g.dart'` written by build_runner). Without an entry, such an import is an unresolved import scoped to the package
that builds it (`unknown_import`, detail `generated_output`): any change in that package selects the importer. An entry
names the files the output is built from, so only changes to those select the importing tests.

| Key | Required | Value |
|---|---|---|
| `outputs` | yes | non-empty list of globs of the generated files |
| `sources` | no | globs of the files they are built from; empty keeps the default (any file of the package) |
| `owner` | yes | the project directory that builds them |

If the build also reads files outside `sources` (its own configuration, for example), list them too.

```toml
[[generated]]
outputs = ["packages/ui/dist/**"]
sources = ["packages/ui/src/**", "packages/ui/tsup.config.ts"]
owner = "packages/ui"
```

## `[[config]]`

Describes runner-configuration keys whose value is computed by code (a function call, a variable, a spread, a helper
module). bgraph reads Vitest, Vite, Jest and Playwright configs as literals; when a key that controls module resolution
or the test set is computed, it reports `config_is_code` and selects every test of the project for any change in it. A
`[[config]]` entry states what the code computes, so that widening stops for the listed keys of that one file.

Every other check stays: the config file is still a dependency of all its tests, an unlisted key still widens, and an
entry whose `file` does not exist does nothing.

| Key | Required | Value |
|---|---|---|
| `file` | yes | the runner config, e.g. `"vitest.config.ts"` |
| `replaces` | yes | the computed keys described: `alias`, `moduleNameMapper`, `include`, `testMatch`, `testRegex`, `projects`, `workspace` |
| `reason` | yes | why the description is right |
| `alias` | no | `{ "<spec>" = "<path>" }`, Vite semantics: an import equal to `<spec>` or starting with `<spec>/` resolves to `<path>` plus the rest. Needs an alias key in `replaces`. |
| `alias_regex` | no | `{ "<regex>" = "<path>" }`, Jest `moduleNameMapper` semantics: the regex must match the whole import; `$1`..`$9` are replaced by groups. Use single-quoted TOML strings. Needs an alias key in `replaces`. |
| `tests` | no | globs of the test files, for test-pattern keys. Omitted: bgraph's default test detection must find every test. Needs a test-pattern key in `replaces`. |
| `sources` | no | helper files the config imports to compute the keys; their computed lookups are covered by this entry (their static imports stay, and editing them still selects every test of the config) |
| `ignore_paths` | no | path literals in the config that belong to the computation, not inputs of every test |

Notes:

- Both `alias` and `alias_regex` may be omitted when the computed alias only points imports at what normal package
  resolution already finds (a workspace package's own `exports` / `main`).
- For `projects` / `workspace` there is no extra key: the entry states that the sub-projects only split the same tests
  (sharding, per-environment runs) and add no aliases or test files beyond what the entry describes.

```toml
[[config]]
file = "vitest.config.ts"
replaces = ["alias"]
alias = { "vue" = "packages/vue/src/index.ts" }
alias_regex = { '^@vue/([a-z-]+)$' = "packages/$1/src/index.ts" }
sources = ["scripts/aliases.js"]
reason = "scripts/aliases.js maps vue and @vue/<dir> to the package sources"
```

## `[[dynamic]]`

Describes what the dynamic imports of one Python or Ruby file can load: in Python `importlib.import_module(name)`,
`__import__(name)`, `pkgutil.iter_modules(...)`, a `sys.path` edit built at run time; in Ruby a `require`, `load`,
`autoload`, `push_dir` or `Dir` loop whose path is computed. By default such a site is unresolved at repository scope: when a
loadable file (Python: `.py`, `.pyi`, `.pyc`, `.so`, `.so.*`, `.pyd`; Ruby: `.rb`, `.rake`, `.ru`, `.gemspec`, `bin/*`,
`exe/*`, `libexec/*`) is added, changed or deleted anywhere in the repository, the file with the site and everything that
depends on it is selected.

An entry replaces that rule, for the sites it names, with glob edges to what you say they can load: a change to a matching
file (including files added later) reaches the file and its dependents, and nothing else does.

| Key | Required | Value |
|---|---|---|
| `file` | yes | repo-relative file containing the dynamic sites |
| `reason` | yes | why the description is right |
| `lines` | no | only the sites on these lines (1-based); default: every dynamic site of the file |
| `loads` | one of the three | globs of the files the sites can load |
| `none` | one of the three | `true`: the sites load nothing from the repository (only installed packages) |
| `sys_path_only` | one of the three | Python only: `true` describes only the file's `sys.path` edits (bgraph already matches unresolved imports anywhere in the repository); cannot be combined with `lines`. A Ruby declaration with it is inert (`$LOAD_PATH` edits need no declaration) |

`none = true` and `loads` are mutually exclusive. An entry whose file does not exist, is neither Python nor Ruby, or matches
no dynamic site (a wrong `lines`) is inert; `bgraph unresolved` lists it as a stale declaration. A file the lexer cannot read
with certainty (`uncertain_scan`) cannot be described away.

```toml
[[dynamic]]
file = "app/plugins.py"
none = true
reason = "load_plugins() imports entry points of installed packages only"

[[dynamic]]
file = "pkg/runtests.py"
loads = ["pkg/**/tests/test_*.py", "pkg/**/tests/__init__.py"]
reason = "the runner only imports the test modules it collects"

[[dynamic]]
file = "lib/mytool/plugin_loader.rb"
loads = ["lib/mytool/plugins/*.rb"]
reason = "require \"mytool/plugins/#{name}\" only loads the bundled plugins"
```

## `[[ruby_autoload]]`

Declares conventional Zeitwerk or Rails `.rb` files whose lazy loads are covered by constant lookups in scanned Ruby
sources and explicit dependencies. The declaration replaces blanket loader edges for those files. Keep its globs scoped
to files that can be analyzed this way. Review the runtime assumptions when loader configuration or framework conventions
change. See [Ruby constant-level autoloading](ecosystems/ruby.md#constant-level-autoloading).

| Key | Required | Value |
|---|---|---|
| `paths` | yes | non-empty list of repository-relative globs matching lazy autoload targets |
| `reason` | yes | why the files' implicit dependencies are covered by the constant graph |

```toml
[[ruby_autoload]]
paths = ["lib/shop/services/**/*.rb"]
reason = "The service classes load lazily and all callers name them as ordinary Ruby constants"
```

## `[dynamic_layer]`

Enables the [dynamic layer](dynamic-layer.md): recorded test dependencies narrow widened selections (and, for JVM, .NET,
Ruby, PHP and Go, precise ones: see [9](dynamic-layer.md#9-jvm-and-net-per-test-class-narrowing),
[10](dynamic-layer.md#10-ruby-per-method-narrowing), [11](dynamic-layer.md#11-php-per-test-file-narrowing) and [12](dynamic-layer.md#12-go-per-test-function-narrowing)). Off by default. It
is a table of its own (not `[dynamic]`) because `[[dynamic]]` is the array of dynamic-import declarations.

| Key | Default | Value |
|---|---|---|
| `ecosystems` | `[]` (off) | ecosystems to narrow: `"python"`, `"node"`, `"jvm"`, `"dotnet"`, `"ruby"`, `"php"`, `"go"` |
| `python` | `"python"` on PATH | interpreter for `bgraph record` and the Python environment fingerprint; a path with a separator is relative to the repository root |
| `node` | `"node"` on PATH | node executable for `bgraph record` and the Node fingerprint (same rules) |
| `ruby` | `"ruby"` on PATH | Ruby executable for `bgraph record`, the Ruby fingerprint and the parse check of changed files (same rules); its directory is put first on `PATH` for the recorded test processes |
| `php` | `"php"` on PATH | PHP executable for `bgraph record` (it must load Xdebug 3), the PHP fingerprint and the tokenizer cross-check of the function-level refinement (same rules) |
| `go` | `"go"` on PATH | go command for `bgraph record` and the Go fingerprint (same rules) |
| `trust_native_env` | `false` | native extension modules of the environment do not make a record incomplete; their versions are in the fingerprint. Records made under the other setting are not used. |

The CLI overrides these with `--dynamic` / `--dynamic=false`, `--python`, `--node`, `--ruby`, `--php`, `--go` and `--trust-native-env`.

```toml
[dynamic_layer]
ecosystems = ["python"]
python = ".venv/bin/python"
```

## `[lockfile]`

| Key | Default | Value |
|---|---|---|
| `level` | `"L1"` | `"L0"`, `"L1"` or `"L2"` |

See [Lockfiles](lockfiles.md) for what each level does and which formats support it. `L2` is narrowing.

## `[options]`

| Key | Default | Value |
|---|---|---|
| `max_changed` | `5000` | above this many changed files, run the full suite (`diff_too_large`); must be at least 1 |
