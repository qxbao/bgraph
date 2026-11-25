<p align="center">
  <img src="docs/assets/banner.svg" alt="bgraph: one changed file glows amber, and a teal path through the dependency graph lights up the few tests that depend on it" width="900">
</p>

<h3 align="center">Your agent writes the code in minutes. Don't wait an hour for the tests.</h3>

<p align="center">
  <b>bgraph</b> runs only the tests a change can affect, across eight language ecosystems,<br>
  and never silently skips one.
</p>

<p align="center">
  <a href="https://github.com/qxbao/bgraph/actions/workflows/ci.yml"><img src="https://img.shields.io/github/actions/workflow/status/qxbao/bgraph/ci.yml?branch=main&style=flat&labelColor=0F172A&color=14B8A6&logo=githubactions&logoColor=white&label=CI" alt="CI"></a>
  <a href="https://github.com/qxbao/bgraph/releases/latest"><img src="https://img.shields.io/github/v/release/qxbao/bgraph?style=flat&labelColor=0F172A&color=14B8A6&label=release" alt="Latest release"></a>
  <a href="https://github.com/qxbao/bgraph/releases"><img src="https://img.shields.io/github/downloads/qxbao/bgraph/total?style=flat&labelColor=0F172A&color=14B8A6" alt="Downloads"></a>
  <a href="go.mod"><img src="https://img.shields.io/github/go-mod/go-version/qxbao/bgraph?style=flat&labelColor=0F172A&color=14B8A6&logo=go&logoColor=white&label=built%20with%20Go" alt="Built with Go"></a>
  <a href="https://pkg.go.dev/github.com/qxbao/bgraph"><img src="https://img.shields.io/badge/API%20docs-pkg.go.dev-14B8A6?style=flat&labelColor=0F172A" alt="API docs on pkg.go.dev"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache--2.0-F59E0B?style=flat&labelColor=0F172A" alt="License: Apache-2.0"></a>
  <br>
  <a href="#supported-ecosystems"><img src="https://img.shields.io/badge/ecosystems-Node%20%C2%B7%20Python%20%C2%B7%20Rust%20%C2%B7%20JVM%20%C2%B7%20.NET%20%C2%B7%20Go%20%C2%B7%20Ruby%20%C2%B7%20PHP%20%C2%B7%20Swift%20%C2%B7%20C%2FC%2B%2B%20%C2%B7%20Dart-white?style=flat&labelColor=0F172A" alt="Node, Python, Rust, JVM, .NET, Go, Ruby, PHP, Swift, C/C++, Dart"></a>
  <a href="#built-for-coding-agents"><img src="https://img.shields.io/badge/MCP-server%20included-white?style=flat&labelColor=0F172A" alt="MCP server included"></a>
  <a href="#github-action"><img src="https://img.shields.io/badge/GitHub%20Action-qxbao%2Fbgraph%40v0-white?style=flat&labelColor=0F172A&logo=githubactions&logoColor=white" alt="GitHub Action: qxbao/bgraph@v0"></a>
  <img src="https://img.shields.io/badge/tested%20on-Linux%20%C2%B7%20macOS%20%C2%B7%20Windows-white?style=flat&labelColor=0F172A" alt="Tested on Linux, macOS and Windows">
</p>

---

## Why bgraph

Coding agents changed where the time goes. Writing a patch takes minutes; proving it did not break anything still means
running the whole suite, on every iteration and on every pull request. The test run has become the slowest step of the
loop.

Running fewer tests is easy. Running fewer tests **safely** is the hard part: one skipped test that should have failed
is a regression shipped to production. bgraph is built around that constraint:

- **It selects precisely.** A file-level dependency graph of the old and the new tree, walked backwards from the changed
  files, picks the test files, packages or modules that can be affected, nothing more.
- **It never guesses.** When it cannot be sure (a computed import, a build file it cannot evaluate, a CI definition, a
  lockfile bump), it widens the selection to the project or the full suite and **says why**, with a machine-readable
  reason and a suggested fix.
- **It is fast and local.** Pure Go, a single binary, no package manager, build tool or test runner is ever run to
  decide. A warm selection takes milliseconds to about a second.

<table>
  <tr>
    <td align="center" width="25%"><h3>2,137</h3>real bug-fix tasks replayed<br>across eleven ecosystems</td>
    <td align="center" width="25%"><h3>0</h3>missed failing tests<br>in 647 mutation kills</td>
    <td align="center" width="25%"><h3>3–13%</h3>of the suite selected on<br>well-structured repositories</td>
    <td align="center" width="25%"><h3>&lt; 1 s</h3>typical selection time,<br>index cached between runs</td>
  </tr>
</table>

<sub>Numbers from the project's own evaluation: FAIL_TO_PASS tasks from public benchmarks, mutation testing on 28 open-source
repositories, median selection on dayjs, hono and the Go holdout set. Method and every result:
<a href="docs/evaluation.md">docs/evaluation.md</a>.</sub>

## See it in action

A one-line fix in [prometheus/common](https://github.com/prometheus/common) (`version/info.go`), 10 test packages:

```console
$ bgraph affected --base f072bc2~1 --head f072bc2
scope: graph
changed files: 1

selected tests: 1
  go:version  (go)  <go:version> --implicit-> <golib:version> ; <golib:version> --implicit-> version/info.go ; version/info.go (changed)
```

One package out of ten, with the exact dependency chain that justifies it. A dependency bump in the same repository is a
different story, and bgraph says so instead of guessing:

```console
$ bgraph affected --base 6bd4fee~1 --head 6bd4fee
scope: module
changed files: 2

widenings:
  - manifest_changed  go.mod  scope=module .
      directives changed: require
  - lock_changed  go.sum  scope=module .
      go.sum at level L0 (checksums not compared)

selected tests: 10
```

Turn a selection into commands your runner understands:

```console
$ bgraph command --base origin/main
# scope: graph
(cd packages/core && npx vitest run src/a.test.ts src/b.test.ts)
```

## Benchmarks

Before writing bgraph, we measured whether a general code-graph tool could do the job. Each tool indexed the old and the
new tree of 58 real bug-fix tasks, and a test counted as selected when the tool's graph connected it to a changed file.
The oracle is the set of tests the upstream fix actually turned from failing to passing.

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/assets/bench-misses-dark.svg">
    <img src="docs/assets/bench-misses-light.svg" alt="Tasks where a required test was missed: bgraph 0 of 58, codegraph 8 of 58, graphify 16 of 43, GitNexus 19 of 43" width="760">
  </picture>
</p>

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/assets/bench-index-dark.svg">
    <img src="docs/assets/bench-index-light.svg" alt="Median time to index the old and new tree: bgraph under 1 s, codegraph 10 s, graphify 16 s, GitNexus 166 s" width="760">
  </picture>
</p>

|  | **bgraph** | codegraph 1.6.2 | graphify 0.8.47 | GitNexus 1.6.12 |
|---|:---:|:---:|:---:|:---:|
| Tasks with a missed required test | **0 / 58** | 8 / 58 | 16 / 43 | 19 / 43 |
| Trap fixtures with a missed test (code changes, 6 languages) | **0 / 30** | 5 / 30 | 9 / 30 | 6 / 30 |
| Median index time, old + new tree | **< 1 s** | 10 s | 16 s | 166 s |
| Says when it is unsure | ✅ widening with a reason | – | – | – |
| Emits ready-to-run test commands | ✅ | – | – | – |
| Understands manifests, runner configs and lockfiles | ✅ | – | – | – |

These are general-purpose code-graph tools, not test selectors, measured with test-selection criteria. Full method, scope
and per-task causes: [docs/benchmarks.md](docs/benchmarks.md).

## Supported ecosystems

| | Ecosystem | Unit of selection | Runners and build tools |
|:---:|---|---|---|
| <img src="https://cdn.simpleicons.org/typescript" width="20" alt=""> | [**Node** (JS / TS, CJS / ESM)](docs/ecosystems/node.md) | test file | Vitest, Jest, Mocha, AVA, `node --test`, Playwright |
| <img src="https://cdn.simpleicons.org/python" width="20" alt=""> | [**Python**](docs/ecosystems/python.md) | test file | pytest, Django's test runner |
| <img src="https://cdn.simpleicons.org/rust/DEA584" width="20" alt=""> | [**Rust**](docs/ecosystems/rust.md) | Cargo target | `cargo test`, workspaces |
| <img src="https://cdn.simpleicons.org/openjdk/ED8B00" width="20" alt=""> | [**JVM** (Java / Kotlin)](docs/ecosystems/jvm.md) | Maven module, Gradle project | Maven, Gradle (wrappers detected) |
| <img src="https://cdn.simpleicons.org/dotnet" width="20" alt=""> | [**.NET** (C# / F# / VB)](docs/ecosystems/dotnet.md) | MSBuild project | `dotnet test` (VSTest, Microsoft.Testing.Platform) |
| <img src="https://cdn.simpleicons.org/go" width="20" alt=""> | [**Go**](docs/ecosystems/go.md) | package | `go test`, modules, `go.work`, vendoring |
| <img src="https://cdn.simpleicons.org/php" width="20" alt=""> | [**PHP**](docs/ecosystems/php.md) | Composer project | PHPUnit, Pest |
| <img src="https://cdn.simpleicons.org/ruby" width="20" alt=""> | [**Ruby**](docs/ecosystems/ruby.md) | test file | RSpec, Minitest, Test::Unit, Rails test, Cucumber, Bundler |
| <img src="https://cdn.simpleicons.org/swift" width="20" alt=""> | [**Swift**](docs/ecosystems/swift.md) | SwiftPM test target or package | `swift test`, XCTest, Swift Testing |
| <img src="https://cdn.simpleicons.org/cplusplus" width="20" alt=""> | [**C / C++**](docs/ecosystems/cpp.md) | registered test or build project | CMake/CTest and Meson |
| <img src="https://cdn.simpleicons.org/dart" width="20" alt=""> | [**Dart / Flutter**](docs/ecosystems/dart.md) | test file | `dart test`, `flutter test`, pub workspaces, melos |

Polyglot repositories work out of the box: every ecosystem selects independently, and a change in one does not select
another's tests unless it is a shared input (CI definitions, toolchain pins).

## Features

| Feature | What it does |
|---|---|
| 🛡️ **Fail-closed by design** | Every uncertainty becomes a widening with a reason code (`dynamic_import`, `manifest_changed`, `lock_changed`, ...) and a suggested `bgraph.toml` entry. [Architecture](docs/architecture.md) |
| 🔒 **Lockfile-aware** | `pnpm-lock.yaml`, `package-lock.json`, `Cargo.lock`, `uv.lock` and `poetry.lock` are compared by resolved dependency closure, so a bump selects only the projects it reaches. [Lockfiles](docs/lockfiles.md) |
| ▶️ **Ready-to-run commands** | `bgraph command` prints runner commands for Vitest, pytest, Cargo, Maven, .NET, Go, PHPUnit, Pest, RSpec, Minitest, Cucumber, SwiftPM, CTest, Meson, `dart test`, `flutter test` and more. [CLI](docs/cli.md#command) |
| 🔍 **Explainable** | `bgraph explain` shows the dependency chain behind every selected test; `bgraph unresolved` lists what could not be read. [CLI](docs/cli.md#explain) |
| ⚡ **Dynamic layer** | Optional per-test records narrow widened selections further for Python and Node test files, for JVM and .NET test classes, and for Ruby test files by the methods they ran. [Dynamic layer](docs/dynamic-layer.md) |
| 🧪 **Shadow mode for CI** | Keep running the full suite while `bgraph audit` reports any failure the selection would have skipped. [Example workflow](docs/ci/shadow-github-actions.yml) |
| 🤖 **Agent integration** | `bgraph agent install` tells coding agents to run the affected tests; `bgraph mcp` gives them `test_commands`, `affected_tests`, `dependents_of` and `explain_selection`. [Coding agents](docs/agents.md) |
| 🧩 **Go library** | Embed the selector in your own tooling with `bgraph.Open` and `Index.Select`. |

## Install

**Prebuilt binaries** for Linux, macOS and Windows (amd64 and arm64) are on the
[releases page](https://github.com/qxbao/bgraph/releases/latest), with a `checksums.txt` (SHA-256) for each release.

Linux and macOS:

```sh
v=$(curl -fsSL -o /dev/null -w '%{url_effective}' https://github.com/qxbao/bgraph/releases/latest | sed 's|.*/tag/v||')
os=$(uname -s | tr '[:upper:]' '[:lower:]'); arch=$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')
curl -fsSL "https://github.com/qxbao/bgraph/releases/download/v$v/bgraph_${v}_${os}_${arch}.tar.gz" | tar -xz bgraph
sudo mv bgraph /usr/local/bin/
```

Windows (PowerShell; use `windows_arm64` on ARM):

```powershell
$v = (Invoke-RestMethod https://api.github.com/repos/qxbao/bgraph/releases/latest).tag_name.TrimStart('v')
Invoke-WebRequest "https://github.com/qxbao/bgraph/releases/download/v$v/bgraph_${v}_windows_amd64.zip" -OutFile bgraph.zip
Expand-Archive bgraph.zip -DestinationPath "$env:LOCALAPPDATA\bgraph" -Force
# then add $env:LOCALAPPDATA\bgraph to your PATH
```

**From source** (Go 1.25 or later):

```sh
go install github.com/qxbao/bgraph/cmd/bgraph@latest
```

**In GitHub Actions:** `uses: qxbao/bgraph@v0` installs a release and computes the selection; see
[GitHub Action](#github-action).

`bgraph version` prints the installed version.

## Quick start

bgraph needs `git` on `PATH`. In any repository:

```sh
bgraph affected --base origin/main          # tests affected by your working tree
bgraph affected --base A --head B --json    # between two commits, as JSON
bgraph command  --base origin/main          # the commands to run them
bgraph explain  src/a.test.ts               # why a test was selected
bgraph unresolved                           # what bgraph could not read, with suggested fixes
```

Caches live in your user cache directory, never inside the repository.

## Built for coding agents

Give your agent the same selection it would get in CI, so it runs the right tests after every edit instead of the whole
suite:

```sh
bgraph agent install --skill                 # a short guidance block in CLAUDE.md / AGENTS.md, and /bgraph-test
claude mcp add bgraph -- bgraph mcp --root . # optional: the MCP tools
```

The guidance is a few lines between markers, and `bgraph agent uninstall` removes it. Other MCP clients:

```json
{
  "mcpServers": {
    "bgraph": { "command": "bgraph", "args": ["mcp", "--root", "/path/to/repo"] }
  }
}
```

The index stays warm for the whole session, so repeated queries return in milliseconds.

## In CI

Adopt it in two steps:

1. **Shadow mode.** Keep the full suite; add `bgraph audit` after it. Every failure the selection would have skipped is
   reported, nothing is ever skipped. See the [GitHub Actions example](docs/ci/shadow-github-actions.yml).
2. **Selection.** Once the shadow runs are clean, run `bgraph command --base origin/${{ github.base_ref }}` and execute
   its output, or use the [GitHub Action](#github-action). A `full` scope tells you to run everything, so the job is never
   less safe than before. See the [selection example](docs/ci/select-github-actions.yml).

Check out with `fetch-depth: 0`: bgraph needs the base commit.

### GitHub Action

`qxbao/bgraph@v0` downloads a release binary for the runner (Linux, macOS or Windows; verified against the release's
`checksums.txt`), adds it to `PATH` and runs the selection:

```yaml
steps:
  - uses: actions/checkout@v7
    with:
      fetch-depth: 0 # required: without the base commit every selection is "full"

  - id: bgraph
    uses: qxbao/bgraph@v0

  - name: Affected tests
    if: steps.bgraph.outputs.full != 'true'
    env:
      SCRIPT: ${{ steps.bgraph.outputs.script }}
    run: bash "$SCRIPT"

  - name: Full suite
    if: steps.bgraph.outputs.full == 'true'
    run: npm test
```

| Input | Default | Meaning |
|---|---|---|
| `version` | `latest` | release to install, e.g. `v0.1.0` |
| `base` | see below | base ref or commit |
| `mode` | `command` | `command` (runner commands), `affected` (selection JSON), `audit` (shadow mode; pass `--results FILE` in `args`) or `record` (dynamic-layer records) |
| `root` | `.` | directory inside the repository |
| `args` | | extra flags for the bgraph command, split on whitespace |
| `records` | | records file: mode `record` writes it, the other modes import it first when it exists |

The default base is `origin/<base branch>` on `pull_request`, the merge group's base on `merge_group`, and the commit
before the push on `push` (`HEAD~1` when that commit is unknown, e.g. for a new branch).

| Output | Meaning |
|---|---|
| `scope` | `none`, `graph`, `package`, `module` or `full` |
| `full` | `'true'` when the full suite must run |
| `commands` | the runner commands, one shell line each (mode `command`) |
| `script` | path to a bash script that runs them with `set -e` (mode `command`) |
| `json` | path to bgraph's JSON output (plan, selection or audit report) |
| `clean` | mode `audit`: `'true'` when no failed test lies outside the selection |
| `records` | mode `record`: path to the records file written |

A `full` selection is not an error: the step fails only when bgraph itself fails. Complete workflows:
[selection](docs/ci/select-github-actions.yml), [shadow mode](docs/ci/shadow-github-actions.yml) and
[dynamic-layer records with a shadow audit](docs/ci/record-github-actions.yml).

## Documentation

| Page | What it covers |
|---|---|
| [Architecture](docs/architecture.md) | the fail-closed contract, the graph, scope tiers, widening reasons |
| [Configuration](docs/configuration.md) | the `bgraph.toml` reference ([annotated example](docs/bgraph.example.toml)) |
| [CLI](docs/cli.md) | commands, flags, JSON output, exit codes, shadow audits, MCP |
| [Coding agents](docs/agents.md) | agent guidance, MCP tools, hook, Go API for agent hosts |
| [Ecosystems](docs/README.md#ecosystems) | per-language rules, units and runner commands |
| [Lockfiles](docs/lockfiles.md) | levels L0, L1 and L2 |
| [Dynamic layer](docs/dynamic-layer.md) | record-based narrowing |
| [Evaluation](docs/evaluation.md) | how soundness is tested, and the results per ecosystem |
| [Benchmarks](docs/benchmarks.md) | the comparison with general code-graph tools |
| [Roadmap](docs/roadmap.md) | ecosystems and integrations that may come next |

## License

Apache License 2.0. See [LICENSE](LICENSE) and [THIRD_PARTY_LICENSES](THIRD_PARTY_LICENSES).
