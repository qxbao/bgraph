# Integration roadmap

What bgraph may support next: new ecosystems, runners and build systems inside existing ones, lockfile levels and the
dynamic layer. Nothing here is a commitment or a date; items move when someone needs them. An item is only marked done
when it meets the bar of [Evaluation](evaluation.md) for its kind (trap fixtures, T1/T2 with 0 violations, regression of
every other ecosystem) and its public page documents it.

**Status**: `planned` (not started), `researching`, `in progress`, `done (vX.Y.Z)`, `dropped` (with the reason).
**Priority**: 1 = next, 3 = when someone asks.

## How to update this page

Whoever finishes an integration (a person or an agent following the `add-ecosystem` skill) updates this page in the same
commit as the feature:

1. Set the item's status to `done (vX.Y.Z)` with the first release that contains it (`done (unreleased)` until then),
   and link its page in the last column.
2. Move what the work left open into new rows (with `planned` and a priority), not into prose.
3. Add a row for any integration discovered along the way, and mark items found infeasible `dropped`, with why.
4. Keep [Supported today](#supported-today) in step with the README and `docs/README.md`.

## Supported today

| Ecosystem | Granularity | Lockfiles at L1 | Dynamic layer | Page |
|---|---|---|---|---|
| Node (JS/TS) | test file | npm and pnpm (up to L2), yarn classic and Berry, bun.lock | widened test files | [node](ecosystems/node.md) |
| Python | test file | uv.lock, poetry.lock | widened test files | [python](ecosystems/python.md) |
| Rust | Cargo target | Cargo.lock | – | [rust](ecosystems/rust.md) |
| JVM (Maven, Gradle) | module / project | – | test classes | [jvm](ecosystems/jvm.md) |
| .NET (VSTest, Microsoft.Testing.Platform) | MSBuild project | – | test classes | [dotnet](ecosystems/dotnet.md) |
| Go | package | go.sum (lines only added) | test functions, per changed function | [go](ecosystems/go.md) |
| PHP | Composer project | – | test files, per changed function | [php](ecosystems/php.md) |
| Ruby (RSpec, Minitest, Test::Unit, Rails, Cucumber) | test file | – | Ruby test files, per changed method | [ruby](ecosystems/ruby.md) |
| Swift (SwiftPM) | test target or package | – | – | [swift](ecosystems/swift.md) |
| C and C++ (CMake/CTest, Meson) | registered test or build project | – | – | [C and C++](ecosystems/cpp.md) |
| Dart / Flutter | test file | – | – | [dart](ecosystems/dart.md) |

## New ecosystems

| Item | Status | Priority | Notes | Page |
|---|---|---|---|---|
| Ruby (RSpec, Minitest; Bundler, `Gemfile.lock`) | done (unreleased) | 1 | Test-file units; `require` by path suffix; Zeitwerk and Rails autoloading depend on whole directories by default; computed `require` widens (describable with `[[dynamic]]`) | [ruby](ecosystems/ruby.md) |
| Ruby: constant-level narrowing for Zeitwerk and Rails autoloading | done (unreleased) | 2 | Opt-in `[[ruby_autoload]]` for lazy files with conventional names. Keeps directory coverage for indirect lookups, eager loading and structural edits. | [ruby](ecosystems/ruby.md#constant-level-autoloading) |
| Ruby: Cucumber features | done (unreleased) | 3 | One unit per conventional `.feature` file, with shared support and step definitions. Profiles use the native whole-suite command. | [ruby](ecosystems/ruby.md#cucumber-features) |
| Ruby: Cucumber Rake tasks | planned | 3 | Discover custom `Cucumber::Rake::Task` commands and their task-specific feature and support paths. | [ruby](ecosystems/ruby.md#known-gaps-and-over-selection) |
| PHP (PHPUnit, Pest; Composer, `composer.lock`) | done (unreleased) | 1 | Project ownership, Composer autoload roots, and static and computed includes | [php](ecosystems/php.md) |
| Deno (`deno test`, `deno.json`, `deno.lock`) | dropped | – | Cloudflare acquired Deno (Oct 2026); runtime development ends around Oct 2027 with maintenance only until then. Not worth a scanner and evaluation for a sunsetting runtime; reopen if a maintained fork gains adoption | |
| Swift (SwiftPM, `swift test`, `Package.resolved`) | done (unreleased) | 2 | SwiftPM test targets, package fallback for computed manifests, L0 lockfiles | [swift](ecosystems/swift.md) |
| Dart / Flutter (`dart test`, `flutter test`, `pubspec.lock`) | done (unreleased) | 2 | Test-file units (not package units: imports are static and each test file runs on its own); `package:` imports across pub workspaces, melos and path dependencies; golden and fixture files named in literals; `pubspec.lock` at L0 | [dart](ecosystems/dart.md) |
| Dart: narrowing through library export files | done (unreleased) | 2 | Follow identifiers through export-only libraries with independently checkable leaf declarations. Keep full dependencies for opaque imports, runtime lookup and compiler-sensitive changes. | [dart](ecosystems/dart.md#narrowing-through-library-exports) |
| Dart: analyzer-backed narrowing for general library declarations | planned | 2 | Extend re-export narrowing to declarations that need Dart compiler validation and symbol resolution. | [dart](ecosystems/dart.md#narrowing-through-library-exports) |
| Dart: Flutter host-app directories | done (unreleased) | 3 | Conventional host files reach the app's integration tests. Shared tooling, plugin inputs and tests that read files directly keep wider coverage. | [dart](ecosystems/dart.md#flutter-host-app-inputs) |
| Elixir (ExUnit via `mix test`; Mix, `mix.lock`) | planned | 2 | Test-file units, as for Ruby; module dependencies are static and can be cross-checked against `mix xref`; macros, `use` and compile-time dependencies need widening. Not in SWE-bench Multilingual or Multi-SWE-bench, so T1 needs its own dataset | |
| C / C++ (CMake + CTest, Meson) | done (unreleased) | 3 | Literal test registrations and build graphs narrow by test name. Computed build logic selects the entire project. | [C and C++](ecosystems/cpp.md) |

## Build systems and runners inside existing ecosystems

| Item | Ecosystem | Status | Priority | Notes | Page |
|---|---|---|---|---|---|
| Bazel / Buck | any | planned | 3 | Has its own target graph (`rdeps`); integration would map changed files to targets rather than scan sources | |
| Scala (sbt) | JVM | planned | 1 | Maven/Gradle Scala modules are units today; sbt builds are not read, and the class layer keeps Scala modules whole | |
| Groovy sources in the class layer | JVM | planned | 3 | Modules with Groovy sources run whole under the class layer | |
| Android (Gradle Android plugin, build variants, `androidTest`) | JVM | planned | 3 | Variants multiply source sets; instrumented tests need a device | |
| Bun test runner (`bun test`) | Node | planned | 2 | Projects using Bun as runner, not only as package manager | |
| Cypress | Node | planned | 3 | Its config is recognized; no runner adapter yet | |
| More than one runner per project (e.g. Vitest + Playwright) | Node | planned | 2 | Today the first detected runner runs every test file of the project | |
| pytest-django | Python | planned | 2 | Settings and the app registry are loaded by name at run time | |
| TUnit (Microsoft.Testing.Platform) | .NET | planned | 3 | Needs `--treenode-filter` in the recorder and the runner; TUnit projects run whole under the class layer today | |
| Composer scripts and custom PHPUnit/Pest invocations | PHP | planned | 3 | Resolve project-defined launchers, configuration paths and additional suite flags | [php](ecosystems/php.md) |
| Xcode project and workspace schemes | Swift | planned | 3 | Discover targets and test plans from Xcode projects and map changes to `xcodebuild test` commands | [swift](ecosystems/swift.md) |
| Native build graph metadata | C and C++ | planned | 3 | Use CMake File API and Meson introspection to narrow configured builds with generated or computed tests, with freshness and configuration checks | [C and C++](ecosystems/cpp.md) |

## Lockfiles

| Item | Status | Priority | Notes | Page |
|---|---|---|---|---|
| `pdm.lock`, `Pipfile.lock`, `pylock.toml`, requirements files to L1 | planned | 2 | L0 today: any change selects every Python test of the project | [lockfiles](lockfiles.md) |
| `bun.lockb` (binary) and `npm-shrinkwrap.json` to L1 | planned | 3 | `bun.lock` (text) and `package-lock.json` are read today | [lockfiles](lockfiles.md) |
| `packages.lock.json` (.NET) to L1 | planned | 2 | L0 today | [lockfiles](lockfiles.md) |
| `Gemfile.lock` / `gems.locked` (Ruby) to L1 | planned | 2 | L0 today; the `GEM` and `PATH` specs give a dependency graph per gem, to be gated by the differential check against Bundler | [lockfiles](lockfiles.md) |
| Maven / Gradle dependency locking to L1 | planned | 3 | | [lockfiles](lockfiles.md) |
| `composer.lock` to L1 | planned | 3 | L0 today. Compare local and transitive dependency resolution against Composer first. | [lockfiles](lockfiles.md) |
| `Package.resolved` to L1 | planned | 3 | L0 today. Compare SwiftPM's resolved package dependencies before narrowing. | [lockfiles](lockfiles.md) |
| `conan.lock` to L1 | planned | 3 | L0 today. Compare Conan's resolved dependency graph under supported build profiles. | [lockfiles](lockfiles.md) |
| `pubspec.lock` to L1 | planned | 2 | L0 today: any change selects every Dart test of the resolution (a workspace shares one). The lockfile lists packages, not edges, so a closure needs the packages' own pubspecs; gate with the differential check against `dart pub deps` | [lockfiles](lockfiles.md) |

## Dynamic layer

| Item | Status | Priority | Notes | Page |
|---|---|---|---|---|
| Default-on decision per ecosystem (replay + mutation on public repositories) | planned | 1 | Gate per ecosystem: ≥ 300 replayed pairs, 0 misses, ≥ 70 % complete records, ≥ 15 % fewer tests selected | [dynamic layer](dynamic-layer.md) |
| .NET class layer on Linux and macOS (recorded runs) | planned | 2 | Implemented and evaluated on Windows; dynamic instrumentation exists on x64 only | [dynamic layer §9.11](dynamic-layer.md#911-net) |
| .NET: map generated sources to their partial type instead of the whole project | planned | 2 | Repositories with their own source generator narrow little today | [dynamic layer §9.11](dynamic-layer.md#911-net) |
| .NET: build-time IL diff to relax the body guard (constants, signatures, added members) | planned | 3 | The body guard passes 17–45 % of code-only commits on the evaluated repositories | [dynamic layer §9.11](dynamic-layer.md#911-net) |
| Go (top-level test functions inside a package, per changed function, from Go coverage per test function) | done (unreleased) | 1 | 0 misses on 133 replayed commits of hcl and prometheus/common, 0 violations on 60 mutants; the accepted commits run 21–42 % fewer test functions, but the guard accepts only 18–27 % of the commits that change Go code (full selections, declarations outside function bodies, `go.mod` updates), so the whole history runs 0.5–13 % fewer. Off by default | [dynamic layer §12](dynamic-layer.md#12-go-per-test-function-narrowing) |
| Go layer: judge declaration changes (constants, signatures, package variables) | planned | 3 | Declarations outside function bodies are the main refusal on connect-go; a type-checked diff of what changed could keep only the functions that use it | [dynamic layer §12](dynamic-layer.md#12-go-per-test-function-narrowing) |
| Go layer: subtests | planned | 3 | Repositories whose tests are a few large functions with subtests (connect-go) narrow little; per-subtest records would need `-run` patterns with `/` | [dynamic layer §12](dynamic-layer.md#12-go-per-test-function-narrowing) |
| Ruby (test files, per changed method, from `Coverage` per test file) | done (unreleased) | 1 | Static selection is about 1.0 on the evaluated repositories (test helpers load the whole library); the method layer removes test files that ran none of the changed methods. Off by default | [dynamic layer §10](dynamic-layer.md#10-ruby-per-method-narrowing) |
| Rust | planned | 3 | Same question at the Cargo-target level | |
| Dart / Flutter (test files, from per-suite VM coverage) | planned | 2 | The Dart VM feasibility check deferred implementation. In 474 path-eligible historical pairs, widened units accounted for at most 1.3% of static Dart test selections. Coverage confirmed per-suite attribution and missed a source file a failing test read as text. Revisit with runtime dependency tracing and compiler validation. Flutter widget tests need a separate recording path | [Dart feasibility](evaluation.md#dart-dynamic-layer-feasibility) |
| PHP (test files inside a Composer project, per changed function, from Xdebug coverage per test file) | done (unreleased) | 1 | Static selection is 1.0 (a unit is the whole Composer project); the layer runs a median of 0.07–0.10 of the test files on the replayed commits of monolog and dbal, with 0 misses. PHPUnit 10+ and Pest. Off by default | [dynamic layer §11](dynamic-layer.md#11-php-per-test-file-narrowing) |
| PHP layer on Windows | planned | 2 | Implemented and evaluated on Linux only | [dynamic layer §11](dynamic-layer.md#11-php-per-test-file-narrowing) |
| PHP layer: PHPUnit 9 and Symfony `simple-phpunit` | planned | 3 | PHPUnit 9 accepts one test file per run, so those projects are never narrowed; projects run by `simple-phpunit` are not narrowed | [dynamic layer §11](dynamic-layer.md#11-php-per-test-file-narrowing) |
| PHP layer: Pest test directories | planned | 3 | Pest lists the whole test directory, so any change under it keeps every test file; fixture files listed as test files never narrow | [dynamic layer §11](dynamic-layer.md#11-php-per-test-file-narrowing) |
| C and C++ (registered tests, from gcov or LLVM coverage) | planned | 3 | Recording needs an instrumented rebuild (`--coverage` or `-fprofile-instr-generate`) and one run per registered test. Header, template and macro code is compiled into the files that use it, so coverage must map it back to the changed header, and the build configuration is part of the fingerprint | [C and C++](ecosystems/cpp.md) |
| Swift (test classes or suites inside a SwiftPM test target, from LLVM coverage) | planned | 3 | Feasibility check deferred implementation. Only 39 of 1,055 first-parent pairs passed a permissive function-body check. LLVM coverage missed a Swift source file read by a failing test. Revisit with runtime dependency tracing and a compiler-checked guard | [Swift feasibility](evaluation.md#swift-dynamic-layer-feasibility) |
