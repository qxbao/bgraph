# Evaluation

bgraph's claim is that it never drops an affected test. This page describes how that claim is tested and summarizes the
results per ecosystem. A comparison with general code-graph tools is on a separate page:
[Comparison with code-graph tools](benchmarks.md).

## Method

### In-repository fixtures

`internal/e2e/testdata/fixtures/<ecosystem>/*.txtar` holds several hundred "trap" fixtures: a base tree, a change and
hand-written expectations (`select`, `noselect`, `full`, `widen <reason>`, `warn <reason>`...). They cover negative
dependencies (a new file shadowing an old one), globs, case mismatches, renames, deletions, new files, data files,
configuration computed in code, lockfile levels and every ecosystem's manifest rules. Every soundness bug found by the
external evaluation below got a fixture that fails before the fix.

### T1: FAIL_TO_PASS

Real bug-fix tasks from SWE-bench-family datasets (SWE-bench Verified, SWE-bench Multilingual, Multi-SWE-bench,
SWE-bench-Live MultiLang, SWE-Sharp-Bench, [Longtail-SWE](https://huggingface.co/datasets/nebius/Longtail-SWE),
[SWE-rebench V2](https://huggingface.co/datasets/nebius/SWE-rebench-V2)) and
[BugsPHP](https://github.com/EmInReLab/bugsPHP). Each task has a parent
commit P, the fixed commit C (gold patch + test patch) and the list of tests that fail at P and pass at C (FAIL_TO_PASS).
BugsPHP reconstructs P by restoring the buggy versions of the source files on the fixed tree. C is the recorded fixed
tree. The oracle takes PHPUnit tests recorded as failing or erroring at P and passing at C.

- bgraph selects for the change P → C. A task is a **violation** when a required test's unit is not selected and the
  scope is not `full`.
- Required test names are located in test files of C by a name locator; ambiguous names require every candidate.
- Tasks are capped per repository (40) and split into a **dev** set and a **holdout** set of repositories that are never
  used while developing.
- Every violation is classified by hand. Some are oracle artefacts: the required test's file is unrelated to the gold
  patch (flaky or environment tests listed as FAIL_TO_PASS), or a test patch edits a file whose pre-existing tests are
  then listed as new.
- With 0 real violations out of n tasks, the **rule of three** gives a 95 % upper bound of 3/n on the miss rate.

Metrics: the **full rate** (share of tasks whose scope is `full`) and the **median selection** (selected units / all
units, on non-full tasks).

### T2: mutation

Pinned repositories per ecosystem. One bug (an operator or literal mutation) is seeded into one line of a source file at
a commit R; the whole suite runs; every unit that turns red must be in bgraph's selection for the mutated change. Mutants
that do not compile or time out are excluded. Red units are confirmed by a rerun.

### Native-tool and provider checks

bgraph reads build definitions as text and never runs the build tool during selection. Its reading is checked against
the real tools:

| Check | Tool |
|---|---|
| Cargo packages, targets and target roots | `cargo metadata --no-deps` |
| Maven reactor and inter-module edges | effective POM |
| Gradle project list | `gradle projects` |
| .NET project references | `dotnet msbuild -getItem:ProjectReference` |
| Go packages, imports and embeds | `go list -e -json -test ./...` |
| PHP projects, autoload roots and runnable suites | `composer show --self`, `composer show --path`, PHPUnit and Pest test listing |
| SwiftPM packages, test targets and source files | `swift package dump-package`, `swift package describe --type json` |
| CMake and Meson test registration and target sources | CTest JSON, CMake File API, `meson introspect --tests --targets` |
| Dart test files | the suites `dart test` loads in a whole run (JSON reporter) |
| JVM selection superset | Gradle/Maven incremental-build plugin (GIB) |
| .NET selection superset | dotnet-affected |
| Node selection superset | Vitest / Jest `--changed` |
| Lockfile closures | the package managers ([below](#lockfile-differential-check)) |

### Replay

Hundreds of historical commits per repository are replayed, checking invariants (a changed test is selected, a scope
other than `none` never yields an empty command, the result is deterministic) and recording the full rate, median
selection and latency.

### Dynamic layer

The [dynamic layer](dynamic-layer.md#7-evaluation) is evaluated in a hybrid configuration (static + records), with
oracles that never come from the collector: mutation (the whole suite on each mutant), replay of 150 commits with
records made once or fresh at each parent, an equality assertion on commits without a widened unit, and verification of
every narrowed commit by running the removed units.

### Lockfile differential check

A lockfile format is enabled at [level L1](lockfiles.md) only after this check: for sampled lockfile commits of real
repositories, the dependency closure bgraph computes for every project is compared with the package manager's own
resolution. Any mismatch blocks the format.

## Results

The original six ecosystem results are from Windows 11. The PHP results are from macOS on arm64 with the native PHP
suites run in a Linux arm64 container. Median selection is selected units / all units on non-full tasks.

### T1 summary

| Ecosystem | Tasks evaluated | Real violations | Full rate | Median selection | 95 % bound |
|---|---|---|---|---|---|
| Node | 223 (dev 176, holdout 47) | 0 | 0 % | 1.000 (holdout 0.956) | 1.3 % (holdout 6.4 %) |
| Rust | 154 (dev 111, holdout 43) | 0 (1 oracle artefact) | 3 % | 0.985 (holdout 1.000) | 1.9 % (holdout 7.0 %) |
| Python | 270 (holdout 86) | 0 | 0 % | 1.000 (dev 0.992) | 1.1 % (holdout 3.5 %) |
| JVM | 243 (holdout 81) | 0 | 5 % (dev 7 %, holdout 1 %) | 0.94 (holdout 0.82) | holdout 3.7 % |
| .NET | 248 (holdout 75) | 0 (6 oracle artefacts) | 5 % | 0.89 (holdout 0.80) | holdout 4.0 % |
| Go | 243 (dev 169, holdout 74) | 0 (26 oracle artefacts, hand-classified) | 1 % | 0.380 (holdout 0.133) | holdout 4.1 % |
| PHP | 269 (dev 162, holdout 107) | 0 | 0 % | 1.000 | holdout 2.8 % |
| Ruby | 117 (dev 61, holdout 56); all 836 fetched tasks: 0 violations | 0 | 2 % (holdout 4 %) | 1.000 | 2.6 % (holdout 5.6 %) |
| Swift | 10 (holdout) | 0 | 40 % | 0.977 | 50 % over the 6 non-full tasks |
| C and C++ | 86 (holdout) | 0 | 15 % | 1.000 | 4.1 % over the 73 non-full tasks |
| Dart / Flutter | 274 (dev 185, holdout 89) | 0 (31 oracle artefacts, hand-classified) | 6 % (holdout 3 %) | 0.850 (holdout 0.849) | 1.1 % (holdout 3.4 %) |

Swift's holdout covers six Longtail-SWE repositories. Four tasks widened to the full suite because they changed a global
input. In the dev set, 13 of 16 tasks could be evaluated. One upstream repository was unavailable, one task used
generated tests, and one generated a SwiftPM wrapper around an Xcode project outside the committed tree.

The C and C++ holdout evaluated 86 of 100 task rows from pinned SWE-bench datasets. Thirteen widened to full on
global inputs and 14 had no usable mapping. The dev set evaluated 46 of 53 rows with zero violations. Some zstd
requirements use the CTest or Meson registration for the same executable or script as a proxy for a benchmark command.

Notes per ecosystem:

- **Node** (SWE-bench Multilingual + Multi-SWE-bench). No task widens to `full` in the latest run. Low selectivity comes
  from real dependencies: dynamic `import()` in a test helper, barrel files, fixture runners; Vue and Svelte compute
  their runner config in code (a [`[[config]]`](configuration.md#config) declaration took one Vue change from 208 to 18
  selected tests).
- **Rust** (275 tasks fetched, 180 in the manifest; holdout tokio, nushell, axum). The violation was checked by hand:
  the gold patch only touches a crate the failing test's crate does not depend on, even as a dev-dependency; the
  required names are pre-existing tests listed as new because the test patch edits their file.
- **Python** (SWE-bench Verified, 12 repositories; holdout astropy, seaborn, xarray, sympy). Selectivity is about 1.0
  because name-based dynamic imports (Django settings, Sphinx autodoc, matplotlib backends, test runners) fire in 266 of
  270 tasks; without them the median would be 0.064. This is what the dynamic layer addresses.
- **JVM** (SWE-bench Multilingual Java + Multi-SWE-bench Java/Kotlin, 22 repositories). Narrowing works where the module
  structure allows it: 0.31 of 224 modules on druid, 0.15 of 41 on jib. Median selection time 0.47 s.
- **.NET** (SWE-Sharp-Bench and SWE-bench-Live, 55 repositories). The 6 violations were classified as oracle artefacts:
  the required test's file is untouched by the test patch and the gold patch changes a project outside the test
  project's transitive `ProjectReference` closure, or only documentation.
- **Go** (Multi-SWE-bench, SWE-bench-Live MultiLang, SWE-bench Multilingual; 97 repositories). All 26 violations were
  checked against `go list -deps -test` at C: 10 are name-ambiguity artefacts and 16 are FAIL_TO_PASS entries whose
  packages have no changed file in their closure (flaky or timing tests). An acceptance test that executes a binary built
  by a Makefile through a relative path (`../../../alertmanager`, from a helper package) was a real gap; it is now covered
  by tying such paths to the main package of that name. Median selection time 46 ms.
- **PHP** (BugsPHP, 15 repositories). The raw set contained 513 tasks and the oracle located all 1,636 failing-to-passing
  test names. The capped benchmark evaluated every task without a full fallback. Project-level selectivity varied with the
  repository layout. The median selected fraction on the Symfony holdout was 0.430 across 40 tasks.
- **Ruby** (SWE-bench Multilingual + Multi-SWE-RL Ruby, 7 repositories; holdout rubocop, jekyll, faker; 2 holdout tasks have
  no oracle: their FAIL_TO_PASS names lie only in files the test patch does not touch). The only full widenings are CI files
  (`global_input`). The first run found 390 real misses on asciidoctor, all from two path-evaluation bugs in how its test
  helper computes the library path (`File.dirname File.dirname(__FILE__)`, `ENV["X"] || (File.join …)`); besides the fixes,
  every computed require now also depends on the suffix union of its literal tail, so a wrong evaluation can no longer end in
  a missing target. Selectivity is about 1.0: each test helper loads the whole library, and computed requires (plugin
  loaders, `require name` helpers) fire in 113 of 117 tasks. Median selection time 0.6 s.
- **Dart / Flutter** (Longtail-SWE and SWE-rebench V2, 291 tasks from 50 repositories, 275 after the 40-per-repository cap;
  holdout 25 repositories). The locator mapped 17,091 of 17,161 failing-to-passing names to one test file. Each of the 31
  violations was checked by computing the import closure of the missing test file at the fixed commit: none contains a
  changed file. 23 come from harness commands that chain packages (`cd a && dart test && cd ../b && …`), so the later
  packages never ran on the unfixed tree; 5 are tests that fail without a changed file in their closure (timing tests);
  2 are names shared by several files where the file the test patch touches is selected; 1 regenerates code from a
  generator before testing while the regenerated files sit in the test patch. Full widenings are CI files
  (`global_input`), one submodule and three computed loads or unreferenced files outside every package. Selectivity is
  low because most packages export their whole library through one file that tests import, and a `version` bump in
  `pubspec.yaml` widens the package and its dependents. Median selection time 645 ms. During the evaluation, relative
  imports under `lib/` were found to resolve against the library's `package:` URI (`..` stops at `lib/`); both readings
  now count.

### T2 mutation summary

| Ecosystem | Repositories | Killed mutants | Violations | Median selection |
|---|---|---|---|---|
| Node | zod, hono, dayjs | 67 | 0 | 0.280 / 0.056 / 0.032 |
| Rust | bytes, bat, ripgrep | 60 | 0 | 1.000 / 1.000 / 0.259 |
| Python | click, marshmallow, attrs | 60 | 0 | 1.000 / 1.000 / 1.000 |
| JVM | gson (Maven), moshi (Gradle) | 40 | 0 | 0.875 / 0.708 |
| .NET | Polly, RestSharp | 40 | 0 | 0.239 / 0.923 |
| Go | hcl, prometheus/common, connect-go | 60 | 0 | 0.389 / 0.100 / 0.200 |
| PHP | php-option, collection, pest-plugin-arch | 33 | 0 | 1.000 / 1.000 / 1.000 |
| Ruby | rack (Minitest), dry-types (RSpec, Zeitwerk), rake (test-unit) | 188 | 0 | 0.975 / 1.000 / 1.000 |
| Swift | swift-argument-parser, swift-collections, swift-numerics | 14 | 0 | 1.000 / 1.000 / 1.000 |
| C and C++ | fmt, Catch2 (CTest), FriBidi (Meson) | 11 | 0 | 0.750 / 1.000 / 1.000 |
| Dart | petitparser, dart-lang/core (characters, fixnum, os_detect, platform), melos (pub workspace) | 74 | 0 | 1.000 / 0.028 / 0.871 |

On Node, Vitest's and Jest's own `--changed` selection was a subset of bgraph's on every non-full mutant. The Go set
found one real miss during development (a test that builds and runs a command of the same module); test literals naming
in-repository import paths now depend on that package.

The three PHP repositories have one runnable project each. The native Composer path-dependency fixture adds a second
suite to check that an unrelated PHP project stays unselected when the shared package breaks its consumer.

Swift's three XCTest suites ran in Linux arm64 with Swift 6.2.1. The evaluation used a local workaround for a Foundation
run-loop stall in Docker Desktop, applied to both baselines and all mutant runs. Swift Collections used its release
suite, which excludes 24 debug-only cases. Four mutants failed to compile and were excluded. The pinned runs selected
the whole indexed suite. A separate native fixture verified that the generated command skips the unaffected target
and still runs tests declared in a linked library. Linux CI repeats this check for XCTest and Swift Testing using the
same pinned Swift toolchain. [Runner smoke test](../evalkit/repro/swift-runner.sh)

The C and C++ T2 run used two whole-suite baselines per repository. One fmt mutant was excluded because a CTest
case timed out. Linux CI includes a fixture that verifies both generated commands against native failures and
checks that the independent tests do not run. [Runner smoke test](../evalkit/repro/cpp-runner.sh)

Dart's suites ran in the `dart:stable` image (Dart 3.14.1, pinned by digest) with `dart test --reporter json`, giving a
verdict per test file. 8 mutants did not compile and were excluded. In melos, 39 tests that shell out to pub and git
already fail at R and were excluded one by one. In the melos workspace, mutants of `conventional_commit` selected the melos
tests that import it through the workspace, and every red unit among them. Packages that `package:test` itself depends
on (args, path, collection) were left out: inside such a package, pub resolves the test runner's own dependency to the
mutated copy, so a mutant breaks `dart test` itself.

### Native-tool and provider checks

| Ecosystem | Result |
|---|---|
| Rust | 60 commits × 12 repositories: every member package, target and target root file matches `cargo metadata`; bgraph also indexes packages outside the workspace (fuzz crates, examples), which only adds units |
| JVM | Maven: 20/20 builds read correctly, 1,601 of 1,601 inter-module edges found. Gradle: 8/12 builds identical; the others differ by intermediate projects without files or by settings bgraph cannot evaluate (which fail closed with a full-scope widening). GIB: 34 tasks agree, 1 divergence classified as GIB over-approximation |
| .NET | `ProjectReference`: 55 repositories / 151 commits, 9,086 edges outside dotnet/runtime, 0 missing; dotnet/runtime: 0 missing (377 edges, 278 of them covered by an unresolved-construct widening). dotnet-affected: 217 tasks without a real violation; 1 divergence by design (it counts a Markdown file under a docs-site project) |
| Go | 25 repositories, 50 base commits, 656 modules, 7,234 packages with tests: every package with tests is a unit; 87,926 in-repo import edges and 6,602 embedded files covered. 107 extra units (packages excluded by build constraints on the host OS): over-selection only |
| PHP | 15 revisions across 3 repositories matched the project and a source file in a PSR-4 root reported by Composer. The generated commands ran all three native suites. In a native two-project fixture, changing a Composer path dependency selected its one consumer. |
| Ruby | rack, dry-types, rake: the test files the runners collect (`RSpec.configuration.files_to_run`, the `Rake::TestTask` file list) are exactly bgraph's units (121 of 121, none missing, none extra) |
| Swift | On both macOS and Linux arm64 with Swift 6.2.1, 210 package instances parsed statically matched native test metadata, including 65 test targets and 687 source paths. Another 36 instances were covered by whole-package units. Five were unavailable for comparison. |
| C and C++ | Linux arm64 configured 37 project instances at 26 revisions. The 29 nonempty suites (22 CTest, 7 Meson) were covered by whole-project units at the correct build roots. Eight registered no tests. Separate literal fixtures matched both named tests and both direct source links for each backend. |
| Dart | 3 repositories, 7 packages: the test files `dart test` loads in a whole run are exactly bgraph's units (66 of 66, none missing, none extra); a `@TestOn('browser')` file skipped on the VM is the one explained extra seen in a probe |
| Node | Vitest / Jest `--changed` ⊆ bgraph on every non-full T2 mutant. Comparison with general code-graph tools: [Benchmarks](benchmarks.md) |

#### Ruby Cucumber and constant autoloading

- The capped Ruby T1 regression scored 117 tasks across seven repositories, with 115 graph or scoped selections, two full
  selections (`global_input`) and zero violations. Two more tasks had no oracle. Explicit scenario paths from two Jekyll
  tasks both passed the Cucumber T1 check. Opaque profile paths require a separate native discovery check.
- Jekyll's conventional discovery matched all 28 native Cucumber 9.2.1 feature files. Aruba's default profile matched all
  102 native Cucumber 12.0.1 feature files through the generated whole-suite command. A conventional nested feature ran
  through the generated filtered command and loaded its step from a sibling directory. A separate native Cucumber fixture
  selected all failures from six shared-support mutations. A Jekyll permalink mutation failed four feature files out of 28,
  all covered by the shared-support selection.
- With opt-in `[[ruby_autoload]]`, six Zeitwerk 2.8.3 method mutations each failed one of six Minitest files and selected exactly
  that file. The six declared names matched Zeitwerk's native constant paths. A method rename selected all six. A Rails 8.1
  service mutation selected the single failing test in a two-test native application.

### Replay

Node: 500 historical commits, 0 invariant violations, 0 full scopes on ordinary commits, median selection 0.954
overall (0.100 on hono), median 239 ms per commit (p95 1.0 s).

### Dynamic layer

| | Python | Node | JVM (class layer) | Ruby (method layer) |
|---|---|---|---|---|
| Repositories | click, marshmallow, attrs, Python-Markdown, pygments, werkzeug | dayjs, hono, express (+ immutable-js in replay) | gson, moshi | rack, dry-types, rake (+ rubocop, jekyll, asciidoctor for the guard rate) |
| Records complete | 89–100 % with `trust_native_env` (0 % with the strict default on Windows) | 98–100 % on dayjs and express; hono 125 of 161 (26 incomplete: a native bundler binding not on the allowlist, workerd spawns; the rest fail on Windows: bun/deno/workerd runtime tests) | 122 of 130 (gson), 32 of 39 (moshi) | 112 of 121 test files (the rest fail at R or start a non-Ruby process); 8–49 s per repository |
| Hybrid mutation: violations caused by the layer | **0** (92 narrowed mutants) | **0** (62 narrowed mutants) | **0** (33 narrowed mutants, 339 failing classes all kept) | **0** (152 narrowed mutants of 188, 1,105 red test files all kept) |
| Median selection, static → hybrid | 1.000 → 0.118–0.194 on widening-heavy repositories | 0.032 → 0.021, 0.056 → 0.025, 0.957 → 0.946 | 1.0 → 0.70 (gson), 0.80 (moshi) | 0.975 → 0.175, 1.000 → 0.186, 1.000 → 0.120 |
| Replay: verified narrowed commits / removed units / violations | 29 / 734 / **0** | 28 / 829 / **0** | 9 / 383 classes / **0** | 13 / 395 / **0** |
| Equality on non-widened commits | 0 failures (1,200 checks) | 0 failures | – | – |

Findings:

- Records made once and kept for months almost never narrow: a recorded dependency changes (staleness) or the
  environment does. With records refreshed on the base branch, guard-passing widened commits are narrowed to a median of
  0.04–0.20 of the units (Python) and 0.03–0.04 (Node).
- On Windows, `trust_native_env = true` is needed for Python: pytest's terminal setup loads a system library through
  ctypes, so with the strict default no record is complete.
- The JVM constant guard passes 94 % (gson) and 90 % (moshi) of code-only commits.
- The Ruby body guard (with the compile check) accepts 33–87 % of the commits that change only `.rb` files over 500
  first-parent commits of six repositories; nearly every refusal is class- or module-level code. Ruby narrows precise
  units: on rack, dry-types and rake every test file reaches every library file statically, and with the method records
  a median of 0.12–0.19 of the test files remains on the mutants.

These checks are not a release gate: the dynamic layer stays off by default.

#### Swift dynamic layer feasibility

A history sample of 1,055 first-parent pairs from Swift Argument Parser, Swift Collections and Swift Numerics yielded
39 body-only candidates (3.7%) using a permissive lexical check. It allowed edits confined to function bodies in
existing source files with unchanged Git modes, alongside optional documentation edits. The parser could not parse
the source in 104 other pairs. The result estimates guard eligibility.

On Linux arm64 with SwiftPM 6.2.1, one filtered invocation per XCTest class or Swift Testing suite produced distinct LLVM
coverage reports. A test failed after an edit confined to a function body in the Swift file it read as text. Its base
profile recorded execution in the test itself and zero executed regions in the changed file. A coverage-only selector
could therefore drop a failing test. SwiftPM merges profiles per invocation, including in the parallel XCTest probe.
[SwiftPM 6.2.1 coverage export](https://github.com/swiftlang/swift-package-manager/blob/swift-6.2.1-RELEASE/Sources/Commands/SwiftTestCommand.swift#L575-L615)

The dynamic layer remains planned pending runtime dependency tracing and a compiler-checked guard.

#### Dart dynamic layer feasibility

Across 1,500 first-parent pairs in dart-lang/http, melos and dart-petitparser, 474 passed a preliminary path and mode
check. Of those, seven had widened Dart file units. They accounted for 69 of the 5,434 static Dart test selections
(1.3%), even if every eligible widened unit could be removed. A separate syntax-only check found 139 pairs with edits
confined to function or method bodies, with optional documentation edits.

On Dart 3.14.1/Linux arm64, `dart test --coverage` produced one report per VM test file. All 18 reports from
dart-petitparser included executed lines in their own test file. Median runtime was 5.88 s for the plain suite and
9.22 s for coverage. Serial isolated coverage runs took 27.47 s in total.
[Per-suite coverage export in test_core 0.6.18](https://github.com/dart-lang/test/blob/test_core-v0.6.18/pkgs/test_core/lib/src/runner/coverage.dart)

A test that read an unimported Dart file as text passed at the base. Its coverage report omitted the read file.
Changing a function body in that file made the test fail, and bgraph's static selector kept it through an
`unreferenced_file` widening. A separate body-only edit caused a suite to fail to load despite having no
execution hits in the edited function. The Dart layer remains planned pending runtime dependency tracing and
compiler validation. The probe covered the Dart VM; Flutter's test runner needs its own evaluation.

### Lockfile differential check

| Format | Repositories | Commits | Project checks | Mismatches | Parse errors |
|---|---|---|---|---|---|
| pnpm 9.0 | 13 | 302 | ~2,900 | 0 | 0 |
| pnpm 6.0 | 3 | 60 | 1,294 | 0 | 0 |
| npm v2/v3 | 12 | 271 | 549 | 0 | 0 |
| Cargo v3/v4 | 8 | 176 | 5,137 | 0 | 4 (corrupt upstream lockfiles that cargo also rejects) |
| yarn classic (v1) | 5 | 101 | 3,002 | 0 | 1 (a stale lockfile missing a pattern yarn would add) |
| yarn Berry (`__metadata` 4, 5, 6, 8, 9, 10) | 4 | 150 | 11,123 | 0 | 0 |
| bun.lock (`lockfileVersion` 1) | 3 | 82 | 1,680 | 0 | 0 |
| uv.lock v1 (revisions 0–5) | 8 | 169 | 740 | 0 | 0 |
| poetry.lock 1.1/2.0/2.1 | 3 | 87 | 87 | 0 | 0 (2 more commits whose `pyproject.toml` poetry itself rejects as out of sync) |

For uv the reference is `uv export --frozen --all-extras --all-groups` per workspace member (split per extra where
declared conflicts forbid exporting them together), and for poetry `poetry export --all-groups --all-extras`. Besides
the superset check, every pair of consecutive commits is checked the other way: a project whose exported package set
changed must be among the projects bgraph reports as changed (uv 674 project checks, poetry 76: no miss).

pnpm 5.4 and npm lockfile v1 are unsupported and stay at L0. The check found and fixed two parser bugs before L1 was
enabled (a two-document `pnpm-lock.yaml` whose closures were read as empty, and a Cargo path crate shadowing a registry
crate of the same name).

The yarn and bun references read the package manager's own resolution: `yarn list --json` (yarn classic's resolver
and hoister, without installing), `yarn info -AR --json` (yarn Berry, with the release each commit pins), and a
`bun install --frozen-lockfile` whose `node_modules` is walked with Node's module lookup. Besides the per-commit
superset check, every compared pair is checked against the tool: a project whose package set the tool reports as
changed must be among the projects Compare marks as changed (0 misses over 2,795 yarn classic, 10,080 Berry and
1,440 bun project pairs). The check found what the lockfiles do not record and the parsers now model: the root
manifest's `resolutions` and `.yarnrc.yml` catalogs (Berry), dependencies added by yarn's built-in package extensions
(even to workspaces), and peers that bun's isolated linker resolves outside the install path.

## Known limits of the evidence

- The datasets are bug-fix tasks: they rarely change manifests, lockfiles or CI files, so those rules are covered mainly
  by fixtures.
- Some ecosystems' holdout sets are small; the rule-of-three bounds above are wide.
- Selectivity is low where code computes what it loads (Python, some Node repositories). That is the sound outcome; the
  [dynamic layer](dynamic-layer.md) and `bgraph.toml` declarations are the ways to narrow it.
