# bgraph documentation

| Page | What it covers |
|---|---|
| [Architecture](architecture.md) | How a selection is computed: the fail-closed contract, the file graph, scope tiers, every widening reason, caches |
| [Configuration](configuration.md) | `bgraph.toml` reference: `[[rule]]`, `[[generated]]`, `[[config]]`, `[[dynamic]]`, `[dynamic_layer]`, `[lockfile]`, `[options]` |
| [bgraph.example.toml](bgraph.example.toml) | An annotated example of every `bgraph.toml` key |
| [CLI](cli.md) | Commands, flags, JSON output, exit codes, shadow audits, the MCP server |
| [Coding agents](agents.md) | Making agents run the affected tests: `bgraph agent install`, the MCP tools, the hook, the Go API for agent hosts |
| [Lockfiles](lockfiles.md) | Lockfile levels L0 / L1 / L2, supported formats, fallbacks |
| [Dynamic layer](dynamic-layer.md) | Optional narrowing with recorded test dependencies (Python and Node test files, JVM and .NET test classes, Ruby test files per method) |
| [Roadmap](roadmap.md) | Ecosystems, runners, lockfiles and dynamic-layer work that may come next, with status and priority |
| [Evaluation](evaluation.md) | How soundness is tested (fixtures, FAIL_TO_PASS, mutation, provider checks) and the results |
| [Benchmarks](benchmarks.md) | Comparison with general code-graph tools (codegraph, GitNexus, graphify) |
| [Shadow-mode workflow](ci/shadow-github-actions.yml) | A GitHub Actions workflow that audits selections against full runs |
| [Selection workflow](ci/select-github-actions.yml) | A GitHub Actions workflow that runs only the affected tests with the `qxbao/bgraph` action |
| [Records workflow](ci/record-github-actions.yml) | A GitHub Actions workflow that records dynamic-layer records nightly and audits the dynamic layer on pull requests |

## Ecosystems

| Ecosystem | Granularity | Page |
|---|---|---|
| Node (JS/TS) | test file | [ecosystems/node.md](ecosystems/node.md) |
| Python | test file (or directory for collection hooks) | [ecosystems/python.md](ecosystems/python.md) |
| Rust | Cargo target | [ecosystems/rust.md](ecosystems/rust.md) |
| JVM (Maven, Gradle) | module / project | [ecosystems/jvm.md](ecosystems/jvm.md) |
| .NET | MSBuild project | [ecosystems/dotnet.md](ecosystems/dotnet.md) |
| Go | package | [ecosystems/go.md](ecosystems/go.md) |
| PHP | Composer project | [ecosystems/php.md](ecosystems/php.md) |
| Ruby | test file (including Cucumber features) | [ecosystems/ruby.md](ecosystems/ruby.md) |
| Swift | SwiftPM test target or package | [ecosystems/swift.md](ecosystems/swift.md) |
| C and C++ | registered test or build project | [ecosystems/cpp.md](ecosystems/cpp.md) |
| Dart / Flutter | test file | [ecosystems/dart.md](ecosystems/dart.md) |
