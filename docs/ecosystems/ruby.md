# Ruby

bgraph selects Ruby tests one test file at a time, including RSpec, Minitest, Test::Unit, Rails tests and Cucumber features. Ruby
loads code with `require`, `require_relative`, `load` and `autoload` into one process, so a test file depends on the transitive
closure of what it loads. bgraph reads `Gemfile`, gemspecs, `.rspec` and `Rakefile`s as text and scans every Ruby file with its
own lexer; it never runs Ruby or Bundler.

## At a glance

| | |
|---|---|
| Granularity | One test or feature file per unit. Computed discovery uses a whole-project unit. |
| Unit ID format | Repository-relative path, e.g. `spec/models/user_spec.rb` or `features/login.feature`. Whole suites use `<rb-all-tests:DIR>` or `<rb-all-cucumber:DIR>`, with `.` for the root. |
| Runners | RSpec (`rspec`), Rails (`bin/rails test`), Minitest / Test::Unit (a `ruby -I…` loader, like `Rake::TestTask`'s), Cucumber (`cucumber`) |
| Manifests/configs read | `Gemfile`, `gems.rb`, `*.gemspec`, `Rakefile` and `*.rake` (`Rake::TestTask`, `RSpec::Core::RakeTask`), `.rspec`, `.rspec-local`, `.rspec-parallel`, `spec/spec.opts`, Cucumber profile file locations |
| Lockfiles (and level) | `Gemfile.lock`, `gems.locked`: **L0**. See [lockfiles](../lockfiles.md) |
| Dynamic layer | per-method narrowing of selected test files: [dynamic layer, section 10](../dynamic-layer.md#10-ruby-per-method-narrowing) (off by default) |

## Supported layouts

- **Projects.** A directory with a `Gemfile`, `gems.rb`, a `*.gemspec`, a `Rakefile` (`rakefile`, `Rakefile.rb`) or a `.rspec`
  file is a project. When a Ruby file lies outside every such directory, the repository root is a project too. A file belongs
  to the nearest project.
- **Monorepos** with a root `Gemfile` and gems in subdirectories (each with its own gemspec and `spec/`), engines, and Rails
  applications.
- **Test data is not a project**: a `Gemfile`, gemspec or `Rakefile` below a `spec`, `test`, `tests`, `fixtures`, `fixture`,
  `tmp` or `vendor` directory (a Rails engine's `test/dummy` app, a gem used as a fixture) belongs to the enclosing project and
  gets no manifest role.
- **Mixed repositories**: a Ruby project next to a Node or Python project. A sub-tree holding another ecosystem's manifest and no
  Ruby file is not Ruby's; Ruby widenings select Ruby units only.
- A repository with no Ruby marker file is not analyzed: a stray `.rb` script is left to the general rules.
- An unbundled Cucumber suite can establish a root Ruby project when `features/` contains Ruby glue and a feature, or
  when a root Cucumber profile accompanies the glue and a feature in another directory.
- **Never repository code**: paths through `node_modules`, `.bundle` or `vendor/bundle`.

**Ruby files**: `.rb`, `.rake`, `.ru`, `.gemspec`, `Gemfile`, `Rakefile`, and extensionless files directly below `bin/`, `exe/`
or `libexec/` whose shebang names `ruby`.

## Test units

A `.rb` file is a test unit when:

1. it lies below a directory named `spec`, `test` or `tests`, and its name matches `*_spec.rb`, `*_test.rb`, `test_*.rb` or
   `tc_*.rb`; or it matches a literal test pattern of its project (below);
2. no directory on its way is named `fixtures`, `fixture`, `vendor`, `tmp`, `node_modules` or `.bundle`;
3. it is not a helper: `spec_helper.rb`, `rails_helper.rb`, `test_helper.rb` and `helper.rb` are sources.

**Literal test patterns** add files with other names:

- `--pattern` and `--default-path` in `.rspec`, `.rspec-local`, `.rspec-parallel` and `spec/spec.opts`;
- `pattern =` and `test_files =` of a `Rake::TestTask` or `RSpec::Core::RakeTask` in the project's `Rakefile` or `*.rake`
  files: strings, `%w[]`, `FileList[...]`, `Dir[...]`, `Dir.glob(...)`, `FileList.new(...)`, `ENV["X"] || "…"`,
  `ENV.fetch("X", "…")`; patterns are relative to the project directory;
- `Rake::TestTask`'s default `test/test*.rb` when no task sets either key.

**Whole-project unit.** When a test pattern or `ruby_opts` is computed (not a literal), a pattern cannot be interpreted, or an
`.rspec` file contains ERB, the project gets the unit `<rb-all-tests:DIR>`. It is selected whenever anything is selected or
widened, and runs the project's whole suite (`config_is_code`).

**Runner of a unit**: `rspec` for a `*_spec.rb` file whose nearest test directory is `spec` (unless a `Rake::TestTask` pattern
claims it), a file below the `.rspec` `--default-path`, or a file an RSpec pattern matches; `rails` for a file below `test/` of
a Rails application (a project with `bin/rails` and `config/application.rb`); `minitest` otherwise.

### Cucumber features

A Ruby project's `features/**/*.feature` files are Cucumber units. Each unit runs every scenario in that file. Each
depends on the shared support tree, including hooks, step definitions, parameter types, nearby data and any code the
support files load. A step definition can satisfy any feature, so changing it selects every feature of the suite.
If a suite uses a Cucumber.js options mapping as its default profile, bgraph requires Ruby glue under `features/` or a
bundled Cucumber gem to classify the suite as Ruby.

A `cucumber.yml` or `cucumber.yaml` at the project root, under `.config/`, or under `config/` delegates discovery to
Cucumber. Tracked glue under `vendor/gems/*/cucumber` or `vendor/plugins/*/cucumber` and overlapping nested Ruby projects
under `features/` also use `<rb-all-cucumber:DIR>`. Like other whole-suite units, this unit runs on any meaningful change.
The Cucumber command honors its default profile, including any custom feature paths, requires and filters.
When a profile is present, changes to tracked documentation can also select the suite because a profile can load fixtures
outside its project directory.

## What a unit depends on

**Loads** recognized anywhere in a file (method bodies, conditionals, `begin`/`rescue`; strings, comments, heredocs and
`=begin`/`=end` blocks are skipped):

- `require "a/b"`, `require_dependency`, `autoload :X, "a/b"`, `Kernel.require`, and the gems of the nearest `Gemfile` for
  `Bundler.require` (the gem name, the name with `-` as `/`, or its `require:` option): a **suffix union**. The file depends on
  every Ruby file whose path is `a/b.rb` or ends with `/a/b.rb`, plus a glob edge `**/a/b.rb` so that a file added later is
  caught. This is a superset of every `$LOAD_PATH` the tests could use inside the repository, so load-path edits need no
  evaluation. A feature that matches nothing is an installed gem or the standard library: no edge, no widening.
- `load "x/y.rake"`: the same, by the exact file name (`load` adds no extension).
- `require_relative "x"` and evaluated path expressions: the exact file. bgraph evaluates `__dir__`, `__FILE__`,
  `File.expand_path`, `File.join`, `File.dirname`, `File.absolute_path`, `Pathname(...).join`, string interpolation of those,
  `Rails.root.join`, and local variables and constants assigned once to such an expression. A missing target keeps its exact
  edge (adding the file is caught).
- **The literal tail.** The evaluation may be wrong, or an `ENV["X"] || default` override may win, so every path argument that
  is not a plain literal also depends on the suffix union of its literal tail (the components after the last computed part or
  `..`): `require File.join(ROOT, "lib", "foo")` depends on `**/lib/foo.rb` whatever `ROOT` is, and
  `File.expand_path("../lib/foo", __dir__)` on `**/lib/foo.rb` besides its exact target.
- **A partly computed path** depends on every file its static parts allow: `require "a/#{x}/version"` depends on
  `**/a/*/version.rb` and `**/a/*/**/*/version.rb`; a path with no literal tail is dynamic (below).
- `Dir[...]`, `Dir.glob` and `Pathname.glob` whose files are required or loaded (`.each { |f| require f }`,
  `.each(&method(:require))`): glob edges to that glob, relative to the file's directory or the project.
- **Autoloaders**: `Zeitwerk::Loader.for_gem` makes the calling file depend on every `.rb` below its directory, and
  `push_dir(d)` on every `.rb` below `d`. A class inheriting `Rails::Application` or `Rails::Engine` depends on every file of
  its project outside the test, log, tmp, coverage and doc directories (templates, locales and `config/*.yml` included: request
  and system specs render them).

### Constant-level autoloading

Use [`[[ruby_autoload]]`](../configuration.md#ruby_autoload) to declare lazy-loaded Ruby files with conventional
constant names. The declaration covers accesses in Ruby sources scanned by bgraph and explicit loads that bgraph follows.
The default dependencies still cover whole autoload directories.

```toml
[[ruby_autoload]]
paths = ["app/services/**/*.rb"]
reason = "The test environment loads these services lazily via their conventional constants"
```

For a declared file, a method-body edit can follow the constants that name it. A reference such as `Admin::EmailSender`
keeps every declared `email_sender.rb` candidate, including those under a different namespace. This covers lexical
lookup, inheritance, acronyms and ambiguous names without evaluating Ruby. Explicit `require`, `load` and other ordinary
edges remain in the graph. Files outside the declaration retain the loader's blanket dependencies, including Rails templates
and other non-Ruby inputs.

Known reflective calls such as `const_get` and `constantize`, Zeitwerk's `load_file`, eager-load operations and uncertain Ruby
scans connect to all declared targets. A computed `push_dir` keeps its original dependency. Adding or removing a declared
file or changing code outside method bodies widens to Ruby tests across the repository (`config_is_code`), since another
project can load the same file. Omit files reached via custom constant names, boot callbacks, framework conventions such as
routes and STI, templates, or lookup in external code from a declaration unless their dependencies are already covered. See
[Rails autoloading](https://guides.rubyonrails.org/autoloading_and_reloading_constants.html) and [Zeitwerk](https://github.com/fxn/zeitwerk).

### Other dependencies

**Implicit inputs of a test**: the `--require` / `-r` features of the project's `.rspec` option files, and the `-r` features
of `Rake::TestTask` `ruby_opts`.

**Data and executables** (heuristic edges that only add dependencies): a test or a file below a `spec`, `test` or `tests`
directory depends on the existing files and directories its path-like string literals and evaluated path expressions name
(relative to the file, the project, the repository root, or a `fixtures` directory), on `test/fixtures/**` or
`spec/fixtures/**` for `fixtures :all`, and on executables named by `Gem.bin_path`. A test naming `exe/tool` depends on that
executable, and the executable on what it requires; a bare program name (`"rubocop"`) is not a reference.

## Changes to manifests, configuration and lockfiles

Widenings below are `module` tier and tagged `ruby`: they select Ruby units only.

| Change | Effect |
|---|---|
| `Gemfile`, `gems.rb` | `manifest_changed`, scope = its directory (it is code: groups, sources and path gems change what is loaded) |
| `*.gemspec`: only `summary`, `description`, `homepage`, `author(s)`, `email`, `metadata`, `license(s)`, `post_install_message`, `rdoc_options`, `extra_rdoc_files` assignments (comments and layout ignored) | Nothing |
| `*.gemspec`: anything else (`version`, `files`, dependencies, `require_paths`, `executables`), added, deleted, or unreadable | `manifest_changed`, scope = its directory |
| `Gemfile.lock`, `gems.locked` | `lock_changed` at level L0, scope = its directory (every Ruby project below it) |
| `.rspec`, `.rspec-local`, `.rspec-parallel`, `spec/spec.opts`, `Rakefile`, `*.rake` below `tasks/` or `rakelib/`, `.simplecov` | `test_config_changed`, scope = the project, nested projects included (a root `Rakefile` often runs them) |
| `cucumber.yml` or `cucumber.yaml` at a Cucumber project's root, in `.config/` or in `config/` | `test_config_changed`, scope = the project; the command uses native profile discovery |
| `.rubocop.yml`, `.rubocop_todo.yml`, `.reek.yml`, `.yardopts`, `.document`, `.standard.yml` at a project root; `CHANGELOG*` and `*.rdoc` outside test directories | Nothing |
| Native extension sources below `ext/` (C, C++, Java, yacc, `extconf.rb`, `depend`) | `unknown_file`, scope = the project |
| Any file below a `spec`, `test` or `tests` directory that nothing references (test-site markdown, fixtures) | `unreferenced_file`, scope = the project |
| `.ruby-version` | `full`, `global_input` (global table, see [architecture](../architecture.md)) |

## Unresolved and uncertain imports

When a change fires an unresolved site, the file with the site counts as changed and its dependents are selected. Inspect them
with `bgraph unresolved`.

| Case | Class | Fires on |
|---|---|---|
| `require`, `load`, `autoload`, `push_dir` or a `Dir` loop whose path is fully computed (`require name`), `send(:require, x)`, `eval`/`instance_eval`/`class_eval` of `File.read(x)`, a `Bundler.require` whose `Gemfile` cannot be read | `dynamic` | a Ruby file (`.rb`, `.rake`, `.ru`, `.gemspec`, `bin/*`, `exe/*`, `libexec/*`) added, changed or deleted **anywhere in the repository**, except a test file that nothing else references |
| A require-like call whose argument the scanner cannot classify, or a path argument with no literal tail | `dynamic` | as above |
| A file the lexer cannot read with certainty: an unterminated heredoc or literal, or a `%` or `/` after a name that is a local variable somewhere in the file (whether it starts a literal depends on scope) | `uncertain_scan` | the same files, test files included |
| `require_relative` of a missing file | `unknown`, report only | exact-path edge: adding the file is caught |

A test file is run by its runner, never by a computed `require`; a dynamic site therefore ignores a change to a test file that
no other file references. A test file another file requires (a shared test) still fires it.

Describe a site with [`[[dynamic]]`](../configuration.md#dynamic) in `bgraph.toml`: it then depends only on the declared
globs, or on nothing with `none = true`. `lines` limits a declaration to some sites; `sys_path_only` does not apply to Ruby
(`$LOAD_PATH` edits need no declaration). An uncertain scan cannot be described away, and a declaration that matches no
computed site of the file is reported by `bgraph unresolved` as stale.

```toml
[[dynamic]]
file = "lib/mytool/plugins.rb"
loads = ["lib/mytool/plugins/**/*.rb"]
reason = "load_plugin only requires files of lib/mytool/plugins"

[[dynamic]]
file = "lib/mytool/feature_loader.rb"
none = true
reason = "loads gems named on the command line, never repository files"
```

## Running the selection

`bgraph command` emits one command per (project, runner, Gemfile, load path), run from the project directory with paths
relative to it. `bundle exec` is used when a `Gemfile` or `gems.rb` lies at or above the project.

| Runner | Command | Whole-project unit |
|---|---|---|
| rspec | `bundle exec rspec <files>` | `bundle exec rspec` |
| rails | `bin/rails test <files>` | `bin/rails test` |
| minitest | `bundle exec ruby -Ilib -Itest -e 'ARGV.each { \|f\| require File.expand_path(f) }' <files>` (`-I`: `lib` plus the literal `Rake::TestTask` `libs`, or `lib` plus the test directory) | `bundle exec rake test` |
| cucumber | `bundle exec cucumber --require features <files>` | `bundle exec cucumber` |

Long file lists are split into batches under the Windows command-line limits.

A repository with a root `Gemfile`, an `.rspec` that requires `spec_helper` (which loads the whole library), and a gem in
`gems/api` with Minitest tests; `lib/shop/user.rb` and `gems/api/lib/client.rb` changed:

```console
$ bgraph command --base HEAD
# scope: graph
# ruby: the selected files of a project run in one process (per batch): global state leaking between files that are not selected together is not modeled
bundle exec rspec spec/models/billing_spec.rb spec/models/user_spec.rb
(cd gems/api && bundle exec ruby -Ilib -Itest -e 'ARGV.each { |f| require File.expand_path(f) }' test/client_test.rb)
```

`billing_spec.rb` is selected although it never names `user.rb`: the `.rspec` `--require spec_helper` loads `lib/shop.rb`, which
requires every file of the library.

For a change to `features/nested/two.feature`:

```console
$ bgraph command --base HEAD
# scope: graph
# ruby: the selected files of a project run in one process (per batch): global state leaking between files that are not selected together is not modeled
bundle exec cucumber --require features features/nested/two.feature
```

The selected files of a project run in one process (per batch): state that leaks between test files not selected together is
not modeled.

## Addressing units in bgraph.toml

A `[[rule]]` `affects` entry is a test or feature file, a directory (every unit below it), a glob over test and feature files,
or a whole-project unit ID:

```toml
[[rule]]
paths   = ["config/locales/**"]
affects = ["spec/i18n", "spec/models/user_spec.rb"]
reason  = "translations are read by the i18n specs and the user model"
```

`"."` selects every unit of every ecosystem; `affects = "all"` widens to `full`. A target that matches no unit is a
configuration error: the selection is `full` (`user_rule`).

## Known gaps and over-selection

- **Library loaders dominate.** Most gems' test helpers load the whole library (`require "mygem"`, which requires every file),
  so a change to any library file selects every test that loads the helper. Test-file changes stay precise. The
  [dynamic layer](../dynamic-layer.md#10-ruby-per-method-narrowing) narrows changes inside method bodies to the test
  files that ran the changed methods.
- **Computed requires.** Plugin loaders, `require name` helpers and command-line `--require` handling are common in
  frameworks; each one reached by the test helper makes almost any Ruby change select every test. Describe them with
  `[[dynamic]]`.
- **Rails applications** have broad boot dependencies. `[[ruby_autoload]]` narrows declared targets when the remaining boot
  dependencies leave room. Use it for conventional lazy code rather than files with boot-time or framework-wide effects.
- **`Gemfile.lock` is L0**: any change selects every Ruby test below it.
- **Heuristic data edges add, never remove.** A data file named by one test's string literal counts as referenced; another
  test reading it through a computed path is not seen.
- **Cucumber** selects features at file granularity. All features share their support dependencies, and projects with a profile
  use whole-suite selection. Custom `Cucumber::Rake::Task` options live outside the CLI's discovery. The generated command
  uses the default Cucumber profile. The Ruby dynamic layer handles Ruby test files.
- **Not modeled**: `eval` of strings other than `File.read(...)`, `instance_eval` of a code variable, gems activated or
  plugins loaded by RubyGems outside the repository, `~/.rspec` and `SPEC_OPTS`.

## See also

- [Architecture](../architecture.md): tiers, widening reasons, unresolved imports, global table
- [Configuration](../configuration.md): `bgraph.toml`
- [Lockfiles](../lockfiles.md): levels L0, L1, L2
- [CLI](../cli.md)
