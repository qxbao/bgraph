# Architecture

This page explains how bgraph turns a change into a test selection: the contract it keeps, the data it builds, and the
order of the decisions it takes. Per-language details are in the [ecosystem pages](README.md#ecosystems).

## The fail-closed contract

For every change, bgraph returns one of two things:

- a set of test units that contains **every** unit the change could affect, or
- a wider scope (a project, a package, the full suite) together with a machine-readable **reason**.

It never drops a test silently. Every "don't know" branch of the code widens; an internal error widens to `full`
(`internal_error`); an empty selection is only returned when bgraph can show that no test is affected (scope `none`).

Precision is secondary: over-selection costs CI time, a miss costs trust. Where an exact answer needs information bgraph
does not have (code that computes import paths, build logic it cannot evaluate), it selects more and says why, and
[`bgraph.toml`](configuration.md) lets you supply the missing facts.

## Vocabulary

| Term | Meaning |
|---|---|
| **Change** | The files that differ between a *base* commit and a *head* (a commit, or the working tree including staged, unstaged and untracked files). |
| **Test unit** | The smallest thing bgraph can select soundly: a test file (Node, Python, Ruby, including Cucumber feature files), a Cargo target, a Go package, a Maven module or Gradle project, an MSBuild project, or a PHP project. |
| **Project** | An ecosystem's project directory (`package.json`, Python project, Ruby project, crate, Go module, Maven module, .NET project...). Widenings are scoped to projects. |
| **Ecosystem** | A language integration: it analyzes one tree into roles, edges, unresolved imports and test units, and classifies changes to its own manifests, configs and lockfiles. |
| **Widening** | A record `{tier, reason, path, scope, detail, suggest}` explaining why the selection is wider than the graph alone. |

Paths are always repo-relative, with forward slashes, cleaned, without a leading `./`; the repository root is `.`.

## Pipeline

```text
change set ──► rules / global table / ecosystem classification ──► seeds + widenings
                                                                      │
base tree ─┐                                                          ▼
           ├─► analysis per ecosystem ─► union file graph ─► backward walk ─► test units ─► scope
head tree ─┘
```

1. **Change set.** bgraph asks git for the changed paths with their status (`A`, `M`, `D`, `T`) and modes. Renames are
   a deletion plus an addition.
2. **Early exits** (each ends the selection with scope `full`):

   | Condition | Reason |
   |---|---|
   | `bgraph.toml` does not parse or fails validation | `user_rule` |
   | no base (none given, none found, or a base that does not resolve, for example in a shallow clone) | `no_base` |
   | more changed files than `[options] max_changed` (default 5000) | `diff_too_large` |
   | an ecosystem fails to analyze a tree | `internal_error` |

3. **Analysis.** Every ecosystem analyzes the base tree **and** the head tree. The results are unioned into one graph
   (see [The file graph](#the-file-graph)).
4. **Classification** of each changed file, in this order:
   1. A gitlink (submodule pointer) change: `full` (`submodule`).
   2. `[[rule]]` entries of `bgraph.toml` that match the file (see [Rules come first](#rules-come-first)).
   3. The [global widening table](#global-inputs-and-harmless-files): `full` (`global_input`).
   4. Every ecosystem's change classifier: a manifest, configuration file or lockfile of that ecosystem produces the
      ecosystem's widenings (for example `manifest_changed`, `test_config_changed`, `lock_changed`). Several ecosystems may
      claim the same file; their widenings add up.
   5. A **graph node** (a file some ecosystem gives a role, or that something depends on) becomes a **seed** of the
      backward walk. If nothing depends on it, no ecosystem claimed it in step 4, and it is not a test, config, manifest
      or lockfile, a test may still use it
      outside the graph (read as data, run as a script): it widens to its nearest project (`unreferenced_file`), or to
      `full` when it lies outside every project.
   6. Otherwise, a file claimed by an ecosystem, a harmless file or a documentation file is ignored.
   7. Anything else widens to its nearest project, or to `full` outside every project (`unknown_file`).
5. **Unresolved imports** whose scope contains a changed file fire: the importing file becomes a seed (see
   [Unresolved imports](#unresolved-imports)).
6. **Walk and select.** The graph is walked backwards from the seeds with no depth limit. A unit is selected when the
   walk reaches one of its files. Module and package widenings then add every unit inside their scope.
7. **Scope.** `none` when nothing is selected; otherwise `graph`, raised to the widest tier among the widenings.

## Scope tiers

| Tier | Meaning |
|---|---|
| `none` | No test is affected. Nothing needs to run. |
| `graph` | The selection follows from the dependency graph alone. |
| `package` | Widened to a directory, package or Cargo target. Defined, but no current ecosystem produces it. |
| `module` | Widened to a whole project (a `package.json` project, crate, Go module, Maven module, .NET project...). |
| `full` | Run the whole suite. `tests` is empty. |

A widening caused by an unresolved import does not raise the scope (unless its tier is `full`): the importer is treated
as changed and the result is still computed from the graph. Such widenings are listed for explanation only. Their tier
field holds the import's scope tier, usually `module`.

A test runner adapter never receives an empty list for a non-`none` scope: if the scope is not `none` and no unit was
selected, `bgraph command` falls back to the full suite.

## Widening reasons

These codes are stable and appear in the `reason` field of `widening` and `warnings` entries.

| Reason | Typical tier | Meaning |
|---|---|---|
| `unknown_import` | module (scope stays `graph`) | An import that resolves to nothing; its importer is treated as changed when a file in its scope changes. |
| `dynamic_import` | module (scope stays `graph`) | An import whose target is computed at run time (`import(x)`, `importlib.import_module(name)`, a non-literal `sys.path` edit...). |
| `ambiguous_import` | module (scope stays `graph`) | An import that could resolve to several targets in ways bgraph cannot decide. |
| `uncertain_scan` | module (scope stays `graph`) | The scanner could not trust its own reading of the file (unsupported syntax, an unclosed block...). |
| `config_is_code` | module | Configuration computed by code requires a whole-project unit. For Node, this can be the detail of an `unknown_import` for each test, and [`[[config]]`](configuration.md#config) can describe the computed key. SwiftPM also reports this reason when an opaque manifest changes. |
| `manifest_changed` | module | A manifest (`package.json`, `Cargo.toml`, `pyproject.toml`, `pom.xml`, `go.mod`...) changed in a way that can affect builds or tests. |
| `test_config_changed` | module | A test runner or compiler configuration changed (`vitest.config.*`, `tsconfig.json`, pytest settings, `.cargo/config.toml`, `*.runsettings`...). |
| `lock_changed` | module or graph | A lockfile changed. See [Lockfiles](lockfiles.md). |
| `global_input` | full | The file is in the [global widening table](#global-inputs-and-harmless-files) (CI definitions, Dockerfiles, toolchain pins...). The detail names the category. |
| `no_base` | full | There is no base commit to compare with. |
| `diff_too_large` | full | The change has more files than `[options] max_changed`. |
| `user_rule` | full | `bgraph.toml` is invalid, or a `[[rule]]` with `affects = "all"` matched. |
| `unknown_file` | module or full | A file no ecosystem claims and nothing references. |
| `unreferenced_file` | module or full | A file of an ecosystem that nothing in the graph depends on; a test may still read or run it. |
| `generated_output` | module | (detail of an `unknown_import`) an import of a git-ignored build output; describe it with [`[[generated]]`](configuration.md#generated). |
| `submodule` | full | A submodule pointer changed. |
| `symlink_outside` | module or full | A symbolic link involved in the change cannot be followed: its target is outside the repository, absolute, dangling, or part of a link cycle. See [symbolic links](#paths-case-symlinks-and-lfs). |
| `internal_error` | full | bgraph's own failure (analysis error, invalid glob edge, cancelled context). |

Warnings (never change the scope):

| Reason | Meaning |
|---|---|
| `case_mismatch` | An import matches a file only when ignoring case: it works on case-insensitive file systems and breaks on Linux. The edge is still added. |
| `rule_rejected` | A `[[rule]]` with `affects = "none"` matched a file that is part of the graph; the rule is ignored for that file. |
| `dynamic_skipped` | The [dynamic layer](dynamic-layer.md) is enabled but did not narrow this selection; the detail says why. |

Reserved codes (defined for compatibility, not emitted today): `framework_magic`, `unsupported_language`.

Each widening may carry a `suggest` field with a ready-to-edit `bgraph.toml` snippet, and an `eco` field when it applies
to one ecosystem's units only (for example a `Cargo.lock` at the root must not select Node tests).

## The file graph

The graph has one node per file (plus virtual nodes, below) and directed edges `From → To` meaning "From depends on To":
a change to `To` can affect `From`.

### Edge kinds

| Kind | Example |
|---|---|
| `import` | `import x from "./a"`, `from pkg import a`, a Go import |
| `type_only` | `import type { T } from "./t"` |
| `reexport` | `export * from "./a"` |
| `include` | `include_str!("data.txt")`, MSBuild `<Import>` / item includes, a file a Maven or Gradle build names |
| `project_ref` | a Maven `<dependency>` on another module of the build, a Gradle `project(":x")`, an included build |
| `conftest` | a pytest `conftest.py` above a test |
| `implicit` | a dependency the runner or build adds on its own (setup files, runner configs, the module files of a Cargo target, a .NET `ProjectReference`, a parent POM, the files of a Go package) |
| `fixture_data` | a data file a Go or Ruby test names in a string literal |
| `pattern` | a glob edge (below), including `//go:embed` patterns and the edges of `[[generated]]` and `[[dynamic]]` declarations |
| `mock` | `jest.mock("./a")`, `vi.mock(...)`, Python `mock.patch("a.b")` |
| `symlink` | a path that reads a file through a symbolic link (a file link, or a file under a directory link) depends on the target file |

The kinds `mod`, `probe`, `generated` and `rule` are defined but not emitted today.

The walk follows every edge kind with no depth limit, so transitive dependents are always reached.

### Virtual nodes

Some units are not files. Their unit's file list holds a single virtual node, and the ecosystem connects real files to it:

| Node | Used by |
|---|---|
| `<rust:ID>` | a Cargo target (and package-level groups) |
| `<go:dir>`, `<golib:dir>`, `<gomod:dir>` | a Go package's test unit, its library (what importers compile), and a module's module-level files |
| `<rbreq:feature>`, `<rbload:file>` | a Ruby `require` / `load` target, matched by path suffix anywhere in the repository (tracked through glob edges) |
| `<rb-autoload:dynamic>` | all declared lazy Ruby autoload targets, reached by indirect lookup, eager loading or uncertain scans |
| `<rb-cucumber-support:DIR>` | shared Cucumber hooks, step definitions and nearby data of a Ruby project |
| `<jvm:ID>` | a Maven module or Gradle project |
| `<dotnet:project file>` | an MSBuild project |
| `<npm:package>` | an external npm package (lockfile level L2 only) |
| `<bare:spec>` | an undeclared bare Node specifier (tracked through glob edges) |
| `<pymod:module>` | a Python absolute import no import root resolves (tracked through glob edges) |
| `<pyext:module>` | an external top-level Python module no import root resolves (read by the `uv.lock` / `poetry.lock` comparison) |

Selection of such a unit is reachability of its virtual node.

### Pattern edges

A `pattern` edge points to a glob instead of a file. It is evaluated against **both** trees and against files added
later, so `import.meta.glob("./pages/*.ts")`, `require.context`, a template import `` import(`./locales/${x}.json`) ``,
a `//go:embed` pattern or an unresolved module tracked by name stay sound when a matching file is added. An invalid glob
widens to `full` (`internal_error`).

### The base/head union

Both trees are analyzed and their graphs unioned:

- an edge that exists in either tree counts, so deleting an import, or the file it pointed to, still selects the old
  dependents;
- a file that is a test in either tree keeps the test role;
- test units come from the head tree: a deleted test is not selected;
- a test file added by the change is always run (`always_run`).

## Selection details

- **Precise and widened units.** A unit reached from the changed files themselves, chosen by a `[[rule]]` target,
  re-run because it failed before, or newly added, is *precise*. A unit chosen only because of a widening (an unresolved
  import seed, a module or package widening, a "whole project" unit) is marked `widened: true`. Only widened units can be
  removed by the [dynamic layer](dynamic-layer.md).
- **Whole-project units.** When an ecosystem cannot tell which files are tests (a test pattern computed in code), it
  emits one unit for the whole project (`whole: true`). It is selected whenever anything is seeded or widened.
- **Previously failed tests.** Unit IDs passed with `--failed` (or `previously_failed` over MCP) are always selected if
  they still exist.
- **Reasons.** Each selected unit carries a reason chain from the unit back to a seed, for example
  `src/a.test.ts --import-> src/util.ts ; src/util.ts (changed)`. `bgraph explain` prints it.

## Unresolved imports

An import bgraph cannot resolve is recorded with a class (`unknown`, `dynamic`, `ambiguous`, `uncertain_scan`) and a
**scope**: a tier and a directory (usually the importer's project, sometimes the repository). When a changed file lies
inside that scope, the importing file is treated as changed and its dependents are selected. Some unresolved imports can
only load files of a known kind and carry trigger globs: a Python dynamic import fires only when a loadable module file
(`.py`, `.pyi`, `.pyc`, `.so`, `.so.*`, `.pyd`) changes in scope, never for a data or documentation file.

An unresolved import with scope `none` is report-only: the ecosystem already encoded the dependency as glob edges.

`bgraph unresolved` lists every unresolved import of the working tree with a suggested `bgraph.toml` entry
([`[[rule]]`](configuration.md#rule), [`[[config]]`](configuration.md#config) or [`[[dynamic]]`](configuration.md#dynamic)).

## Rules come first

`[[rule]]` entries of [`bgraph.toml`](configuration.md#rule) are checked before anything else for each changed file:

| `affects` | Effect |
|---|---|
| `"all"` | `full` (`user_rule`). |
| `"none"` | The file is ignored, **unless** it is a graph node; then the rule is rejected with a `rule_rejected` warning and the file is classified normally. |
| a list of directories, unit IDs or globs | Every unit under the directories, named by the unit IDs, or with a file matching the globs, is selected (precise). A target that matches no unit makes the selection `full` (`user_rule`). No further widening applies to the file; if it is a graph node the walk still starts from it. |

## Global inputs and harmless files

Files in the global widening table affect every test and widen to `full` (`global_input`, the detail is the category).
`**/x` matches at any depth; a pattern without `**/` matches at the repository root only.

| Category | Patterns |
|---|---|
| `ci` | `.github/workflows/**`, `.github/actions/**`, `.gitlab-ci.yml`, `.gitlab/ci/**`, `**/azure-pipelines*.yml`, `.circleci/**`, `**/Jenkinsfile*`, `.buildkite/**`, `bitbucket-pipelines.yml`, `.travis.yml`, `appveyor.yml` |
| `container` | `**/Dockerfile*`, `**/*.dockerfile`, `**/.dockerignore`, `**/docker-compose*.y*ml`, `**/compose*.y*ml` |
| `build_script` | `**/Makefile`, `**/GNUmakefile`, `**/*.mk`, `**/justfile`, `**/Justfile`, `**/.justfile`, `**/Taskfile.y*ml` |
| `env` | `**/.env`, `**/.env.*`, `**/.envrc` |
| `toolchain_pin` | `**/.nvmrc`, `**/.node-version`, `**/.python-version`, `**/rust-toolchain`, `**/rust-toolchain.toml`, `**/global.json`, `**/.tool-versions`, `**/mise.toml`, `**/.mise.toml`, `**/.sdkmanrc`, `**/.java-version`, `**/.ruby-version`, `**/go.work`, `**/go.work.sum`, `**/.fvmrc`, `**/.fvm/fvm_config.json` |
| `workspace_def` | `**/pnpm-workspace.yaml`, `**/lerna.json`, `**/nx.json`, `**/turbo.json`, `rush.json`, `.gitmodules` |
| `git_attributes` | `**/.gitattributes` |
| `package_manager_config` | `**/.npmrc`, `**/.yarnrc`, `**/.yarnrc.yml`, `**/.pnpmfile.cjs`, `**/bunfig.toml`, `**/.yarn/**` |

A `[[rule]]` that matches the file overrides the table.

Harmless files are ignored when no ecosystem claims them: `LICENSE*`, `COPYING*`, `NOTICE*` (root), `**/.gitignore`,
`CODEOWNERS`, `.github/CODEOWNERS`, `docs/CODEOWNERS`, `.github/ISSUE_TEMPLATE/**`, `.github/PULL_REQUEST_TEMPLATE*`,
`.github/FUNDING.yml`, `.mailmap`.

Documentation files (`**/*.md`, `**/*.markdown`, `**/*.rst`, `**/*.adoc`) are ignored too, but only when they are not
graph nodes: a Markdown file that is embedded, imported or collected as a test still selects its dependents.

## Paths, case, symlinks and LFS

- **Case.** Paths are matched against git's index, case-sensitively. A Node import that matches a file only when
  ignoring case still gets its edge, plus a `case_mismatch` warning. A case-only rename is a deletion plus an addition.
- **Symbolic links.** bgraph reads links from git (mode `120000`, the blob holds the target path), so they are handled
  the same way whether the checkout made a real link or a plain text file (`core.symlinks=false`, usual on Windows).
  A link whose target is inside the repository is followed, in every ecosystem: a file link is a file that reads its
  target, and a directory link behaves as if the target directory's files were present under the link path (an import
  of `link/x.js` reaches `target/x.js`, a glob over the link directory matches the target's files, a file added later
  under the target is seen under the link too). Each such path is tied to the file it reads by a `symlink` edge, and a
  change to the target counts as a change of every path that reads it through a link.
  A change to the link itself (added, removed, retargeted) is a changed path, and also a change of what it pointed to
  and of what it points to now: the dependents of both targets are selected.
  A link that cannot be followed (target outside the repository, absolute, dangling, or a link cycle) is never skipped
  silently: when it is part of the change, the selection widens with `symlink_outside` to the project that owns the link,
  or to `full` outside every project. A directory link that re-enters itself is followed one level, and a change under its
  target widens the same way.
- **Git LFS.** Files are compared by their git blobs. An LFS object change changes its pointer file, so the path counts
  as changed like any other file.
- **Submodules.** A changed gitlink widens to `full` (`submodule`); so does an untracked nested repository.

## Index and caches

bgraph keeps three kinds of state, all outside the repository, in the user cache directory
(`<user cache dir>/bgraph/<hash of the repository path>/`):

| Store | Content | Keyed by |
|---|---|---|
| `scan.sqlite` | per-file import scan results | file content (blob OID) and the scanner version |
| `analysis/` | per-ecosystem analysis of commit trees (newest 32 kept) | ecosystem and its version, scanner version, commit, `bgraph.toml` content |
| `dynamic/<eco>.jsonl` | records of the [dynamic layer](dynamic-layer.md) | unit and environment |

The caches are accelerators only: a locked, corrupt or unwritable store degrades to memory with a warning and never
changes a selection. Unchanged files are not read again; the working tree is re-analyzed on each query (the scan cache
makes that cheap). A long-lived process (`bgraph mcp`, a library `Index`) also keeps trees and analyses in memory.

`bgraph index` analyzes the working tree and prints counts (files, edges, units, unresolved imports, elapsed time).

## Library API

The Go package `github.com/qxbao/bgraph` exposes the same engine:

```go
idx, err := bgraph.Open(ctx, root, bgraph.Options{})
defer idx.Close()
sel, err := idx.Select(ctx, bgraph.Change{Base: "origin/main", Worktree: true})
```

`Select` returns an error only for infrastructure failures (git cannot run); every other uncertainty is a widening in the
returned `Selection`. The JSON form of a `Selection` is described in [CLI](cli.md#json-output).

```go
plan, err := bgraph.Commands(sel, bgraph.CommandOptions{ListDir: ".bgraph-lists"})
if plan.Scope == bgraph.TierFull {
	// run the full suite; sel.Widening says why
}
for _, c := range plan.Commands { /* run c.Argv in c.Project with c.Env */ }
why := bgraph.Explain(sel, "tests/test_api.py")
```

`Commands` is `bgraph command --json` and `Explain` is `bgraph explain`. The scope tiers (`TierNone` … `TierFull`) and the
widening reasons (`ReasonNoBase`, `ReasonLockChanged`, …) are exported as constants.

- `Change.Base` and `Change.Head` accept any ref or commit OID, including a commit on no branch (for example one made
  with `git commit-tree` from a snapshot of the working tree), as long as the object exists in the repository.
- `Change.Exclude` takes glob patterns (bgraph.toml syntax, relative to the repository root) of changed files that are
  inputs of no test, for example a tool's own state files in the working tree. Matching changed files are dropped
  before selection: they are not in `Selection.Changed`, cause no widening and seed nothing. The trees and the graph
  are unchanged. A plain data file is dropped even inside a project or package (the caller vouches for it), but a file
  is kept, with a `rule_rejected` warning, when something references it (an import, embed, include or path literal),
  when it is code, a test, a manifest, a config, a lockfile or generated output, or when a symbolic link or a submodule
  is involved. Keep patterns narrow: `**/*.json` would also drop test fixtures nothing references by a literal path.
- Several processes may open the same `StateDir` and call `Select` concurrently: the scan cache is SQLite in WAL mode
  (one writer at a time, waiting up to 5 s for the lock; a cache that cannot be written degrades to memory with a
  warning, never a wrong result), and dynamic-layer records are replaced atomically. An `Index` is safe for concurrent
  `Select` calls within one process.
