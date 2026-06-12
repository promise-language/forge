# Dev Tooling Blueprint

The design doc for Forge: the build / verify / gate tooling pattern, packaged as a generic recipe and scaffolder.

The model is one bootstrap script (`./make` / `.\make.cmd`) that compiles every dev tool out of a single in-repo Go module into `bin/`, plus a commit gate (`bin/verify`) that bundles format → build → vet → test into a single command. Each tool detects its own staleness and prompts the developer to re-run `./make`. Adopting this replaces the typical bash + PowerShell + Makefile mix with one source of truth, in Go, that is portable by construction.

The pattern was distilled from a real compiler's dev tooling, where bash + PowerShell + Makefiles had drifted across platforms; Forge packages the design so other projects can adopt it without copy-pasting. This document is language-agnostic for the target project — the tooling itself is Go because Go cross-compiles trivially and has no runtime dependency to install, but the tools can build, test, and verify a project written in any language.

## What Forge ships

- **This doc** (`docs/blueprint.md`) — the model and the rationale.
- **`cmd/init`** — a scaffolder. Run it in a target repo and it lays down the file structure described in §2. After it exits, the target repo owns every line. Forge is *not* a runtime dependency at that point.
- **`primitives/`** — optional helper library for the genuinely-stable bits (FNV-128a hash, ldflags-injected `repoRoot` accessor, file lock, OS detection, exec helpers, interrupt handler). Import it OR copy the source into your own `common/` — both supported. Pipeline orchestration (`RunBuild`, `RunVerify`, etc.) is project-specific by nature and stays in each adopter's own `tools/build/common/`.

---

## 1. What the model gives you

- **One bootstrap command** (`./make`) on a fresh clone: compiles every tool, enables git hooks, and is idempotent on subsequent runs.
- **Native binaries under `bin/`** — `bin/build`, `bin/test`, `bin/verify`, `bin/format`, `bin/vet`, `bin/coverage`, `bin/stress`, `bin/setup`, `bin/prereqs`, `bin/guard`, `bin/precommit`. Each is a Go binary, ~5–10 MB, fast to invoke.
- **A commit gate** (`bin/verify`) that runs the full pre-commit pipeline. The only thing a contributor needs to know is "run `bin/verify` before committing."
- **A self-staleness check**: every tool embeds the FNV-128a hash of the tool-source tree at compile time. If the source has changed since the binary was built, the tool prints `tools source has changed — run: ./make` and exits non-zero.
- **A single, cross-platform implementation**: no bash/PowerShell drift. Tools detect host OS at runtime when behavior must differ.
- **A Claude Code guard hook** (`bin/guard`) that blocks dangerous Bash commands and forbidden Edit/Write patterns at the harness level.
- **A git pre-commit hook** (`bin/precommit`) that rejects staged binaries, enforces GitHub noreply commit identities, keeps the tree source-only (no binary or oversized blobs), and validates ratcheted quality metrics.
- **Ratcheted baselines** (`.baselines.json` at the repo root) — committed metrics (test count, leak count, coverage, binary size) that can only move in the approved direction, enforced on every commit.
- **Deterministic repo-root resolution.** `./make` computes the absolute repo root at bootstrap time and bakes it into every compiled tool via `-ldflags "-X main.repoRoot=<abs>"`. Each binary already knows where its repo lives — no runtime walk-up, no sentinel-file scan, no `os.Executable()` games. Robust to cwd changes, subdirectory invocations, agents that move around the filesystem, and binaries copied into other repos (they still point at their birth-repo). The only tool that resolves root at runtime is the meta-builder itself (it runs via `go run`, so ldflags don't apply), and the `./make` trampoline `cd`s it into a known path first.
- **An in-tree gate registry: `project.toml`.** Declares which gates exist, which `bin/gate` subcommand runs each, on what schedule (commit / nightly / weekly / on-demand), and which metric names from `.baselines.json` they produce. The project owns its gate definitions; trackers / CI schedulers read this file rather than defining gates externally.

---

## 2. Repository layout

```
<repo-root>/
├── make                       # bash bootstrap trampoline
├── make.cmd                   # Windows bootstrap trampoline
├── project.toml               # project identity + gate registry (optional until you define gates)
├── .baselines.json            # ratcheted quality metric values (machine-managed)
├── bin/                       # gitignored — built tools land here
│   ├── build, verify, test, format, vet, coverage, stress, setup, prereqs
│   ├── guard, precommit
│   └── .tools.hash            # sidecar — tools source hash, drives the up-to-date check
├── .githooks/
│   └── pre-commit             # trampoline that execs bin/precommit
├── .claude/
│   ├── settings.json          # wires bin/guard as a PreToolUse hook
│   └── edit_gates.json        # forbidden Edit/Write patterns (consumed by bin/guard)
├── tools/
│   └── build/                 # single Go module containing every tool
│       ├── go.mod
│       ├── common/            # shared library — all real logic lives here
│       │   ├── hash.go        # ToolsSourceHash() — FNV-128a of tools/build/**/*.go,go.mod
│       │   ├── stale.go       # CheckStale() — called first thing by every tool's main
│       │   ├── platform.go    # OS/arch, BinaryName(), ExeSuffix(), Which()
│       │   ├── exec.go        # RunIn(), RunOutputIn(), RunSilent()
│       │   ├── args.go        # NormalizeArgs() — accepts `-foo` and `--foo`
│       │   ├── build.go       # the build pipeline
│       │   ├── verify.go      # the verify pipeline
│       │   ├── test.go, vet.go, format.go, clean.go, stress.go, coverage.go
│       │   ├── gate.go        # bin/gate — runs tests, emits JSON gate values
│       │   ├── commitgate.go  # ratchet check: compare gate-values.json vs .baselines.json
│       │   ├── precommit.go   # the pre-commit hook logic
│       │   └── setup.go       # `git config core.hooksPath .githooks`
│       └── cmd/               # one thin main.go per binary; discovered by ./make
│           ├── make/main.go   # the meta-builder
│           ├── build/main.go
│           ├── verify/main.go
│           ├── test/main.go
│           ├── format/main.go
│           ├── vet/main.go
│           ├── coverage/main.go
│           ├── stress/main.go
│           ├── setup/main.go
│           ├── prereqs/main.go
│           ├── guard/main.go
│           └── precommit/main.go
└── <project source>/          # the actual project — anything: Go, Rust, C++, JS, …
```

**Why `.baselines.json` lives at the repo root**: it's a single committed file (~50 lines of JSON) that every contributor and CI run reads. Top-level, dot-prefixed matches the convention of `.editorconfig`, `.gitignore`, `.prettierrc` — machine-managed config that's discoverable on `ls` but visually quiet. `edit_gates.json` is a separate concern (Claude Code guard policy, not quality ratchets), so it lives under `.claude/` next to `settings.json` where the guard hook is wired up.

**Why the repo root is baked into every binary at build time, not discovered at runtime:** Walking up from `cwd` looking for a sentinel file (the old design) breaks in three ways agents routinely trigger — `cwd` set outside the repo, `cwd` inside a *different* repo that has its own `./make`, or `cwd` inside a sub-checkout that has a sentinel-file collision. Baking the absolute path into each binary via `-ldflags "-X main.repoRoot=<abs>"` removes all three failure modes: the binary always knows its real repo regardless of `cwd`, regardless of what the surrounding filesystem looks like, regardless of how an agent invokes it. The cost is that binaries are tied to their source worktree — copying `bin/verify` from worktree A to worktree B leaves it pointing at A. That's the right semantics: it's the same binary, built for A. The fix is `./make` in worktree B, which costs ~1 second.

Conventions that make this work:

- **The repo root is determined exactly once, by `./make`, and frozen at build time.** Each tool's `main.go` declares `var repoRoot = ""`; the meta-builder overwrites it via `-ldflags "-X main.repoRoot=<abs>"`. At runtime the binary simply uses `repoRoot` — no discovery, no walking, no fallbacks.
- **The meta-builder is the only tool that resolves root at runtime, because it runs via `go run` and ldflags don't apply.** The `./make` trampoline `cd`s it into `<root>/tools/build` first, so `cmd/make` reads its root as `filepath.Dir(filepath.Dir(cwd))` — two levels up from a known location. This is the one tightly-scoped place that still touches the filesystem to locate root, and it cannot be reached from a stale binary.
- **`bin/` is gitignored.** Tools are always built locally, never committed. This also keeps the baked-in `repoRoot` honest: every `bin/<tool>` was, by construction, built in the worktree it claims to belong to.
- **The trampoline form pins `cwd` before `go run`:**
    ```bash
    exec go run -C "$(cd "$(dirname "$0")" && pwd)/tools/build" ./cmd/make "$@"
    ```
    The inner `cd "$(dirname "$0")"` resolves `$0` via its directory regardless of where the user invoked `./make` from (`./make`, `../make`, `/abs/path/make`), then `pwd` returns the absolute path. Result: `go run -C "/abs/repo/tools/build"`, so `cwd` inside `cmd/make` is always `<root>/tools/build`. Do not simplify the trampoline to `go run ./tools/build/cmd/make` — that depends on the user being at the repo root, which `./make` should not require.

---

## 3. The bootstrap entry point

`./make` is two lines:

```bash
#!/usr/bin/env bash
exec go run -C "$(cd "$(dirname "$0")" && pwd)/tools/build" ./cmd/make "$@"
```

`make.cmd` mirrors it for Windows:

```bat
@echo off
go run -C "%~dp0tools\build" ./cmd/make %*
```

These are the only platform-specific files in the whole tooling system. They exist solely so that on a fresh clone — when no `bin/` binaries exist yet — there is one command that works. From that point on, everything is invoked via the compiled binaries under `bin/`.

`go run` is used because it works without any prior compilation step. The meta-builder itself takes ~1 second to start; subsequent runs short-circuit on a stored hash and finish in <100 ms.

---

## 4. The meta-builder (`tools/build/cmd/make/main.go`)

Responsibilities, in order:

1. Resolve the absolute repo root. The trampoline `cd`'d into `<root>/tools/build` before `go run`, so `cmd/make` computes it as `filepath.Dir(filepath.Dir(cwd))` and verifies the result is an absolute path. This value is what gets baked into every binary in step 5.
2. Compute the current `ToolsSourceHash` over `<root>/tools/build/`.
3. **Enable git hooks** unconditionally (`git config core.hooksPath .githooks`). Idempotent and fast — safe to do every run.
4. Read `bin/.tools.hash`. If it matches the current hash and every expected binary exists in `bin/`, print `Tools up to date` and exit. Skipped if `-force` is passed.
5. Otherwise, discover every subdir under `tools/build/cmd/` (excluding `make` itself) and `go build` it into `bin/<name><exe-suffix>` with:
    ```
    -trimpath
    -ldflags "-s -w -X main.sourceHash=<hash> -X main.repoRoot=<absolute repo root>"
    ```
    Both `sourceHash` and `repoRoot` are package-level `string` variables in each tool's `main.go`, initialized to `""`. Go's linker overwrites them at link time. There is no runtime path-discovery code anywhere in the compiled tools.
6. Write `bin/.tools.hash`.

Key properties:

- **Tools are discovered, not enumerated.** Drop a new directory under `cmd/`, run `./make`, and you have a new `bin/<name>` binary. No registration step.
- **The hash sidecar is the staleness contract.** It's the same hash that's baked into each binary. If the sidecar is missing or stale, the next `./make` rebuilds; if the sidecar exists but a tool was deleted from `bin/`, the next `./make` rebuilds.
- **Each binary is tied to its source worktree by construction.** The baked-in `repoRoot` is the absolute path of the worktree it was built in. Two worktrees of the same repo produce two distinct sets of binaries — there is no shared `bin/` that could point at the wrong tree.
- **Trim path and strip symbols.** `-trimpath -ldflags "-s -w"` keeps binaries small and reproducible. (`-X` overrides survive `-s -w`; only the symbol table is stripped, not initialized data.)

---

## 5. The staleness self-check

In every tool's `main.go`:

```go
package main

import "<module>/tools/build/common"

// Both overwritten by the meta-builder via -ldflags at build time.
var (
    repoRoot   = ""
    sourceHash = ""
)

func main() {
    common.CheckStale(repoRoot, sourceHash)
    if err := common.RunVerify(repoRoot, os.Args[1:]); err != nil { /* ... */ }
}
```

`CheckStale`:

```go
func CheckStale(repoRoot, compiledHash string) {
    if repoRoot == "" || compiledHash == "" {
        // Binary wasn't built via ./make (e.g., go install, manual go build).
        // Refuse to run rather than guessing — the blueprint contract is broken.
        fmt.Fprintln(os.Stderr, "this binary was not built via ./make — rebuild from the repo")
        os.Exit(1)
    }
    currentHash, err := ToolsSourceHash(repoRoot)
    if err != nil {
        // The baked-in repoRoot no longer exists (worktree moved / deleted).
        fmt.Fprintf(os.Stderr, "binary's repo (%s) is unreachable: %v\n", repoRoot, err)
        fmt.Fprintln(os.Stderr, "rebuild with ./make from the current worktree")
        os.Exit(1)
    }
    if compiledHash != currentHash {
        suffix := "./make"
        if IsWindows() { suffix = ".\\make.cmd" }
        fmt.Fprintf(os.Stderr, "tools source has changed — run: %s/%s\n", repoRoot, suffix)
        os.Exit(1)
    }
}
```

`ToolsSourceHash(repoRoot)` walks `<repoRoot>/tools/build/` recursively, collects every `.go`, `go.mod`, `go.sum` file (sorted by relative path), and computes an FNV-128a hash of `<relpath>\n<size>\n<contents>` for each. The size delimiter prevents file-boundary collisions.

This single check is what makes "edit a tool → re-run `./make`" the one-and-only developer workflow. Without it, a stale binary can silently produce wrong results for hours. And because `repoRoot` is baked in, the check is anchored to the binary's actual source tree — copying `bin/verify` into a different repo doesn't trick the staleness check into re-hashing the wrong `tools/build/`.

---

## 6. Shared common library (`tools/build/common/`)

Tools call each other via direct Go function calls, never subprocesses. `bin/verify` calls `common.RunBuild()` directly; it never `exec`s `bin/build`. This is fast, gives proper error propagation, and eliminates a class of "subprocess returned 1 but no usable output" bugs.

Essential common helpers to write first:

| Function | Purpose |
|---|---|
| `ToolsSourceHash(repoRoot) (string, error)` | FNV-128a of all `.go`/`go.mod`/`go.sum` under `<repoRoot>/tools/build/`. |
| `CheckStale(repoRoot, compiledHash)` | Compare and exit 1 on mismatch (or when either input is empty). |
| `IsWindows() bool`, `ExeSuffix() string`, `BinaryName() string` | OS detection. |
| `Which(cmd) string` | Resolve a binary in `PATH`, returns "" if missing. |
| `Exists(path) bool` | Quick file existence check. |
| `RunIn(dir, name, args...) error` | Subprocess with stdout/stderr attached. |
| `RunOutputIn(dir, name, args...) (string, error)` | Capture stdout. |
| `RunSilent(name, args...) error` | Subprocess with discarded output. |
| `NormalizeArgs(args) []string` | Accept both `-foo` and `--foo`. |
| `Interrupted() bool` | True if Ctrl+C was received; checked between pipeline steps. |
| `SetupLocalCache(repoRoot) error` | Set env vars so the project's tooling uses `<repoRoot>/.<project>-home/` instead of `~`. |

Many of these have stable, generic implementations in [`primitives/`](../primitives/); pipeline-orchestration helpers (`RunBuild`, `RunVerify`, `RunTest`, …) are project-specific and stay in each adopter's own `common/`.

Note: there is no `FindRoot()`. Each tool's `main.go` already has `repoRoot` as a package-level variable, populated by ldflags at build time. Common helpers that need it take it as a parameter.

Each pipeline tool (`RunBuild`, `RunVerify`, `RunTest`, `RunFormat`, `RunVet`, `RunClean`, `RunStress`, `RunCoverage`) takes `(repoRoot, args)` and returns an `error`. Their thin `main.go` wrappers are nearly identical:

```go
func main() {
    common.CheckStale(repoRoot, sourceHash)
    if err := common.RunVerify(repoRoot, os.Args[1:]); err != nil {
        fmt.Fprintln(os.Stderr, "verify failed:", err)
        os.Exit(1)
    }
}
```

---

## 7. The commit gate (`bin/verify`)

The single command a contributor runs before committing. It bundles:

1. **Format** — `gofmt -w .` (or the target project's formatter), then the project's own code formatter.
2. **Build** — full build pipeline.
3. **Vet** — static analysis (`go vet`, `clippy`, `eslint`, whatever applies).
4. **Test (host)** — unit tests + e2e tests.
5. **Test (optional alt targets)** — for example, a project that compiles to WASM might run an additional WASM test pass behind `--wasm`.
6. **Summary block** — always printed, even on failure. Includes a per-target pass/FAIL line and elapsed time.
7. **Failed-tests excerpt** — re-prints the `FAILED:` section so an agent tailing the last ~40 lines sees every failure.
8. **Gate values sidecar** — `bin/.gate-values.json` capturing test counts, leak counts, failure counts, coverage. Read by the pre-commit hook for ratchet enforcement.

Key design choices in `verify.go`:

- **Global file lock** at `~/.<project>/verify.lock` (uses `gofrs/flock`). Concurrent verify runs from different worktrees block on each other rather than thrashing the cache. The lock file records the holder's repo path so waiters print `Waiting for verify run in <path> to finish...`.
- **Interrupt handling**: between every pipeline step, check `Interrupted()` and bail with a clean message. The signal handler sets a flag; tools don't die mid-test with a stack trace.
- **Local-by-default cache**: `bin/verify` (and other tools) call `SetupLocalCache(root)` which sets the project's home env var to `<root>/.<project>-home/` so a verify run doesn't pollute the user's `~`. `--shared` opts back into `~/.<project>`.
- **`--clean` is up front**, before `SetupLocalCache`, so a clean run starts from a known state.
- **Format runs first.** Running format last means a botched format diff lands in the next commit instead of failing this one.
- **Exit code is the only contract.** Non-zero = "not safe to commit," printed in a red banner. Zero = "OK to commit," printed in green.

---

## 8. Git hooks (`bin/precommit`)

`.githooks/pre-commit` is a 10-line shell trampoline that `exec`s `bin/precommit`. The hooks dir is wired up by `bin/setup` (called from `./make`), so a fresh clone gets hooks on first `./make`.

`bin/precommit` is fast (<50 ms) and enforces invariants that don't require running tests:

- **Block committed binaries under `bin/`.** Anything staged at `bin` or under `bin/` (gitignored, built by `./make`) fails with a `git reset HEAD <path>` hint.
- **Enforce GitHub noreply commit identities.** Both the author and committer email must end in `@users.noreply.github.com`, which keeps a personal address out of the public history. The check reads `git var GIT_AUTHOR_IDENT` / `GIT_COMMITTER_IDENT` — git's own resolution, honoring `GIT_*_EMAIL` env vars and `user.email` config alike — so it judges exactly the identities the impending commit will record, not a guess. (The domain is the one project-policy knob here; swap it if your project uses a different identity convention.)
- **Reject binary blobs and oversized files.** The repo is source-only. Each staged file's *index* content is inspected via `git cat-file` (`:<path>` reads the staged blob, so it judges exactly what would be committed): reject any blob with a NUL byte in its first 16 KiB (git's own binary heuristic, widened) or larger than 256 KiB. Catches accidentally-staged images, vendored archives, and build artifacts before they bloat history. Deletions and submodule gitlinks are skipped; rename/copy entries are judged by their destination path.
- **Block forbidden patterns** in staged diffs (e.g., `allow_leaks: true`, `TODO(remove me)`, `console.log`).
- **Validate ratcheted baselines.** If `.baselines.json` is staged, parse both the HEAD and staged versions and reject any metric that moved in the wrong direction.

The first three checks are language-agnostic and ship in the scaffolded starter; the last two are sketches you grow into. The hook is intentionally light. Anything that requires running tests belongs in `bin/verify`, which the developer runs explicitly before committing. Putting test execution in the hook makes commits slow and gets the hook disabled — which kills the whole gate.

---

## 9. The Claude Code guard hook (`bin/guard`)

`bin/guard` is a PreToolUse hook for Claude Code that blocks dangerous operations at the harness level. Configured in the project's `.claude/settings.json`:

```json
{
  "hooks": {
    "PreToolUse": [{
      "matcher": "Bash|Edit|Write",
      "hooks": [{
        "type": "command",
        "command": "\"$CLAUDE_PROJECT_DIR/bin/guard\" || exit 2"
      }]
    }]
  }
}
```

It reads the hook JSON from stdin, decodes `tool_name` + `tool_input`, and:

- **For Bash**: pattern-matches against a blocklist (`git push`, `rm -rf`, `git reset --hard`, etc.). Returns a JSON denial with a clear reason, or exits 0 to allow.
- **For Edit/Write**: matches `new_string`/`content` against the regex set in `.claude/edit_gates.json` (e.g., "no `allow_leaks: true` in test files," "no committed binaries").

The `|| exit 2` is **fail-closed**: if the guard binary crashes or doesn't exist (fresh clone), the tool call is blocked. The recovery is to run `./make` once from a real terminal to bootstrap `bin/guard`.

`$CLAUDE_PROJECT_DIR` is set by the Claude Code harness on every PreToolUse invocation, so the hook is immune to subprocess cwd drift.

---

## 10. Gate registry (`project.toml`)

`project.toml` is the **in-tree source of truth for what gates exist and how they run.** It lives at the repo root, is human-edited, and is read by:

- `bin/verify` — to know which on-commit gates to run, in what order, with what flags.
- `bin/gate <name>` — to look up the runner subcommand and produce structured output.
- `bin/commitgate` — to map gate output to baseline metric names for ratchet checking.
- Any external scheduler (tracker, GitHub Actions, cron) — to discover non-commit gates (nightly, weekly, on-demand) and how to invoke them.

Schema (TOML, designed to fit on one screen for a small project):

```toml
[project]
name = "my-project"

[gates.tests]
runner   = "test"              # → bin/gate test
schedule = "commit"            # commit | nightly | weekly | on-demand
metrics  = ["host_test_count", "host_leak_count", "host_test_failures"]

[gates.coverage]
runner   = "coverage"
schedule = "commit"
metrics  = ["coverage_pct"]

[gates.stress]
runner   = "stress"
schedule = "nightly"
args     = ["-iterations", "1000"]
metrics  = ["stress_flaky_count"]

[gates.binary_size]
runner   = "size"
schedule = "commit"
metrics  = ["binary_size_release", "binary_size_debug"]
```

Design rules:

- **Gate definitions belong to the project, not to a tracker.** A tracker or CI system reads `project.toml` to discover what to run; it does not own the list. This means moving between trackers / CI providers is a config swap, not a re-definition of every gate.
- **The `runner` field names a `bin/gate <subcommand>`** — it does not embed a shell command. Adding a new gate runner means adding a new case in `common/gate.go`, not pasting shell into a config file. Keeps gates type-checked and cross-platform.
- **The `metrics` field is the contract with `.baselines.json`.** Every name in `metrics` must appear in `.baselines.json` once the gate has run at least once. `bin/commitgate` cross-checks the two and complains if a metric is declared by a gate but never produced (or produced but never declared).
- **`schedule = "commit"` gates run inside `bin/verify`.** Other schedules are advisory — the blueprint doesn't run a cron daemon; an external scheduler queries `project.toml` and invokes `bin/gate <name>` on the right cadence.
- **`project.toml` is optional during early adoption.** Nothing about root resolution depends on it (the root is baked into binaries via ldflags); `bin/verify` runs without it, just with no extra gates. Add it when you're ready to define the first gate.

---

## 11. Ratcheted baselines (`.baselines.json`)

A flat JSON file at the repo root, dot-prefixed to signal "machine-managed config":

```json
{
  "darwin-arm64": {
    "host_test_count":    { "value": 6087, "direction": "up",    "updated": "2026-05-24" },
    "host_leak_count":    { "value": 0,    "direction": "down",  "updated": "2026-04-06" },
    "host_test_failures": { "value": 0,    "direction": "exact", "updated": "2026-04-06" },
    "coverage_pct":       { "direction": "up" }
  }
}
```

Three states:

- **Enforced** (`direction` + `value` both set) — the ratchet check rejects any commit where the metric moved against the direction.
- **Pending** (`direction` set, `value` absent) — populated automatically by the next verify run, then enforced from there on.
- **Informational** (`type: "informational"`) — tracked but never enforced. Used for platforms not on the main CI.

`bin/verify` writes the current run's metrics to `bin/.gate-values.json`. The pre-commit hook reads it and the staged `.baselines.json`, then refuses commits that would drop test count, raise leak count, drop coverage, etc. A genuine baseline update is a deliberate edit to `.baselines.json` in the same commit — the staged-vs-HEAD check ensures even that edit cannot regress an enforced direction.

This is the mechanism that keeps the test count growing instead of silently shrinking, and that turns "we have 0 leaks" into a guarantee instead of a hope.

---

## 12. Cache isolation

Tools default to a repo-local home (`<root>/.<project>-home/`) instead of `~/.<project>`. Reasons:

- A `verify --clean` doesn't blow away an unrelated worktree's cache.
- Two worktrees of the same repo don't fight over a single shared cache.
- A new contributor doesn't end up with a polluted `~`.

`SetupLocalCache(root)` is one function: set `<PROJECT>_HOME=<root>/.<project>-home`, create the dir, done. Every tool that runs in the verify pipeline picks it up via env. `--shared` is the opt-out flag for developers who want a single shared cache across worktrees.

The same env-var trick works for any toolchain (Cargo home, NPM cache, Bazel disk cache, ccache, etc.) — set it before the first subprocess that uses it.

---

## 13. Step-by-step implementation guide

The easiest path is `go run github.com/promise-language/forge/cmd/init@latest` in your target repo, which lays down the layout below. The steps are listed here so you know what `init` produces and so you can reproduce it by hand if you prefer.

1. **No root sentinel and no `FindRoot()`.** The repo root is computed once by `./make` (from its own cwd, which the trampoline pins to `<root>/tools/build`) and baked into every compiled tool via `-ldflags "-X main.repoRoot=<abs>"`. Tools never walk up looking for anything; they just use their baked-in `repoRoot`. This step is a reminder, not an action — the actual mechanism is implemented when you write `cmd/make/main.go` in step 4.

2. **Initialize `tools/build/` as a Go module.** `cd tools/build && go mod init <module-name>/tools/build`. Add it to `go.work` if the project already has one; otherwise just leave it as an island Go module.

3. **Write `common/hash.go`, `stale.go`, `platform.go`, `exec.go`, `args.go`.** These are the load-bearing primitives. Either import `github.com/promise-language/forge/primitives` for the stable subset, or copy the source in directly. There is no `common/root.go` — root is baked into each tool's `main` by the meta-builder. Helpers that need root take it as a `repoRoot string` parameter.

4. **Write `cmd/make/main.go`.** Just the meta-builder — discover `cmd/` subdirs, build each into `bin/`, write the hash sidecar, wire up git hooks via `RunSetup`.

5. **Add `./make` and `make.cmd` at the repo root.** Two-line trampolines (see §3).

6. **Gitignore `bin/`.** Add `bin/` to `.gitignore` so built tools are never committed.

7. **Write `cmd/setup/main.go` and `common/setup.go`.** Configure `core.hooksPath` to `.githooks`.

8. **Add `.githooks/pre-commit`** — the 10-line shell trampoline that execs `bin/precommit`.

9. **Write `cmd/build/main.go` + `common/build.go`.** Wraps your project's actual build command. Detect missing prereqs and print actionable hints rather than letting subprocess errors propagate raw.

10. **Write `cmd/test/main.go`, `cmd/format/main.go`, `cmd/vet/main.go`.** Thin wrappers; each is ~20 lines.

11. **Write `cmd/verify/main.go` + `common/verify.go`.** The orchestrator. Format → build → vet → test, with a summary block printed unconditionally and a global file lock.

12. **Write `cmd/precommit/main.go` + `common/precommit.go`.** The scaffolder ships three language-agnostic checks: block staged binaries under `bin/`, enforce GitHub noreply author/committer identities, and reject binary or oversized blobs (source-only tree). Grow it with project-specific checks — forbidden-pattern scans and `.baselines.json` ratchet-direction validation.

13. **Add `project.toml`** at the repo root when you're ready to define your first gate. Start with one gate definition (typically `tests`) wired to the `bin/gate test` runner. Root resolution does not depend on this file — adoption can be gradual.

14. **Add `.baselines.json`** at the repo root with the metrics declared in `project.toml`'s `[gates.*].metrics`, initially in Pending state (no `value`). The first `bin/verify` populates them; from then on, the ratchet is enforced.

15. **Write `cmd/guard/main.go`** and wire it into `.claude/settings.json` as a PreToolUse hook. Start with a small Bash blocklist (`git push`, `rm -rf /`, `git reset --hard`) and grow from there.

16. **Document the workflow in `README.md` and `CLAUDE.md`.** Three lines:

    > Bootstrap: `./make`.
    > Build: `bin/build`.
    > Before committing: `bin/verify`.

    Everything else is discoverable.

The first 8 steps get you a working bootstrap; the remaining 7 get you the full gate. Each step is independent and the system is usable at every checkpoint.

---

## 14. What this model deliberately avoids

- **No Makefiles.** Make's dependency model is excellent for actual compilation graphs and useless for "run this tool, then that tool, and print a summary." The Go pipeline is clearer and portable.
- **No bash + PowerShell pair.** Every script duplicated across platforms diverges. One Go binary with `runtime.GOOS` checks is shorter than two scripts.
- **No npm-style postinstall magic.** `./make` is explicit. The user knows what just happened.
- **No "framework". ** `common/` is a flat package of helpers, not an abstraction. Each tool's `cmd/<name>/main.go` calls the helpers directly.
- **No silent failures.** Every error path returns a wrapped error with context. The verify summary always prints, even on failure, so an agent can grep the result without re-running.
- **No tests-in-precommit-hook.** Tests run in `bin/verify` which the developer runs explicitly. The hook stays sub-second so it never gets disabled.
- **No runtime dependency on Forge.** Once `cmd/init` has run, the project owns its tooling. Forge upstream changes do not affect existing adopters unless they explicitly pull in a new `primitives/` version.

---

## 15. Reference — the runnable implementation

This blueprint ships its own worked example: the [`cmd/init`](../cmd/init/main.go) scaffolder embeds every file described above as a string constant and writes a complete, runnable tooling tree into a target repo. Read the scaffolder to see the exact source the doc prescribes — it is the single source of truth, kept in lockstep with this document.

Where each piece lives in [`cmd/init/main.go`](../cmd/init/main.go):

- Bootstrap: the `makeSh` / `makeCmd` constants (`./make`, `make.cmd`) and the `cmdMakeGo` constant (the meta-builder).
- Common library: the `platformGo`, `execGo`, `argsGo`, `hashGo`, `staleGo`, `setupGo`, `verifyGo`, `precommitGo`, and `guardGo` constants — written into `tools/build/common/`.
- Staleness & root: `staleGo` + `hashGo`. There is no `root.go` — the root is baked into each binary via ldflags by the meta-builder (see §4–§5), so no runtime walk-up exists.
- Verify pipeline: `verifyGo` (a language-detecting example pipeline; adopters replace `verifySteps` with their real commands).
- Pre-commit: the `preCommitHook` trampoline and `precommitGo` (staged-binary, noreply-identity, and source-only blob checks — see §8).
- Guard hook: `cmdGuardGo` + `guardGo`, wired by the `settingsJSON` constant into `.claude/settings.json`.
- Agent-facing workflow doc: the `buildDocRaw` constant, appended to `CLAUDE.md` so the `./make` → `bin/verify` loop is discoverable.

The gate registry (`project.toml`, §10) and ratcheted baselines (`.baselines.json` with `gate.go` / `commitgate.go`, §11) are described in this doc as the model to grow into; they are not part of the starter the scaffolder emits, so adopt them by following §10–§11 directly.

The reusable, low-churn subset of the common helpers is also published as the importable [`primitives/`](../primitives/) package, for adopters who would rather depend on it than copy the scaffolded source.
