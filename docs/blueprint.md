# Dev Tooling Blueprint

> **Tag:** `blueprint` — remaining work to complete this document: the query named in
> [`docs/index.md`](index.md).

The design doc for Forge: the build / verify / gate tooling pattern, packaged as a generic recipe and scaffolder.

The model is one bootstrap script (`./make` / `.\make.cmd`) that compiles every dev tool out of a single in-repo Go module into `bin/`, plus a commit gate (`bin/verify`) that bundles format → build → vet → test into a single command. Each tool detects its own staleness and prompts the developer to re-run `./make`. Adopting this replaces the typical bash + PowerShell + Makefile mix with one source of truth, in Go, that is portable by construction.

The pattern was distilled from a real compiler's dev tooling, where bash + PowerShell + Makefiles had drifted across platforms; Forge packages the design so other projects can adopt it without copy-pasting. This document is language-agnostic for the target project — the tooling itself is Go because Go cross-compiles trivially and has no runtime dependency to install, but the tools can build, test, and verify a project written in any language.

## What Forge ships

- **This doc** (`docs/blueprint.md`) — the model and the rationale.
- **`cmd/init`** — a scaffolder. Run it in a target repo and it lays down the file structure described in §2. After it exits, the target repo owns every line. Forge is *not* a runtime dependency at that point.
- **`primitives/`** — the shared library the tools are built from (FNV-128a hash, staleness check, OS detection, exec helpers, flag normalization, help handling, git-hook wiring). A project depends on it at a pinned version in its `tools/build/go.mod`; [`primitives.md`](primitives.md) states what belongs in it and why the pin is what makes that safe. Pipeline orchestration (`RunVerify`, the gate set, the judging terms) is project-specific by nature and stays in each adopter's own `tools/build/common/`.

---

## 1. What the model gives you

- **One bootstrap command** (`./make`) on a fresh clone: compiles every tool, enables git hooks, and is idempotent on subsequent runs.
- **Native binaries under `bin/`** — `bin/verify`, `bin/gate`, `bin/run`, `bin/setup`, and whatever else the project adds (`bin/build`, `bin/test`, `bin/format`, `bin/vet`, `bin/coverage`, `bin/stress`, `bin/prereqs`, …). Each is a Go binary, ~5–10 MB, fast to invoke.
- **A commit gate** (`bin/verify`) that runs the full pre-commit pipeline. The only thing a contributor needs to know is "run `bin/verify` before committing."
- **A self-staleness check**: every tool embeds the FNV-128a hash of the tool-source tree at compile time. If the source has changed since the binary was built, the tool prints `tools source has changed — run: ./make` and exits non-zero.
- **A single, cross-platform implementation**: no bash/PowerShell drift. Tools detect host OS at runtime when behavior must differ.
- **A measuring and a judging entry point** (`bin/gate`, `bin/run`). `bin/gate <name> --envelope` prints one envelope of measurements and nothing else; `bin/gate --list` names what the project answers; `bin/run <gate> --verdict` reads that envelope on stdin and writes one verdict. This is how anything outside the tree — a flow, a scheduler, a person — learns what this project can measure and whether a measurement is acceptable (§11, §12).
- **The judging terms as a separate artefact** (`tools/gates/`). `thresholds.json` holds the caps a verdict is reached against and `baselines.json` the ratcheted metrics; both are committed, reviewed with the code they judge, and read by the judge rather than compiled into it.
- **Committed hooks that name tools the project does not build.** `.githooks/pre-commit` reaches `bin/precommit-guard` and `.claude/settings.json` wires `bin/tool-guard` — both delivered by the organization's workspace, not compiled by `./make` (§8, §9, §10).
- **Deterministic repo-root resolution.** `./make` computes the absolute repo root at bootstrap time and bakes it into every compiled tool via `-ldflags "-X main.repoRoot=<abs>"`. Each binary already knows where its repo lives — no runtime walk-up, no sentinel-file scan, no `os.Executable()` games. Robust to cwd changes, subdirectory invocations, agents that move around the filesystem, and binaries copied into other repos (they still point at their birth-repo). The only tool that resolves root at runtime is the meta-builder itself (it runs via `go run`, so ldflags don't apply), and the `./make` trampoline `cd`s it into a known path first.

---

## 2. Repository layout

```
<repo-root>/
├── make                       # bash bootstrap trampoline
├── make.cmd                   # Windows bootstrap trampoline
├── bin/                       # gitignored — built tools land here
│   ├── verify, gate, run, setup
│   ├── precommit-guard, tool-guard    # installed by the workspace, never built here (§8)
│   └── .tools.hash            # sidecar — tools source hash, drives the up-to-date check
├── .workspace/                # gitignored — provisioning marker and the verified-tree record
├── .githooks/
│   └── pre-commit             # trampoline that execs bin/precommit-guard (§9)
├── .claude/
│   └── settings.json          # wires bin/tool-guard on PreToolUse and PostToolUse (§10)
├── docs/
│   └── index.md               # the map of docs/ — every document is listed here
├── tools/
│   ├── gates/                 # the judging terms — committed, read by bin/run (§12)
│   │   ├── thresholds.json    # the caps a verdict is reached against
│   │   └── baselines.json     # ratcheted metric values (machine-managed)
│   └── build/                 # single Go module containing every tool
│       ├── go.mod
│       ├── common/            # shared library — all real logic lives here
│       │   ├── hash.go        # ToolsSourceHash() — FNV-128a of tools/build/**/*.go,go.mod
│       │   ├── stale.go       # CheckStale() — called first thing by every tool's main
│       │   ├── platform.go    # OS/arch, BinaryName(), ExeSuffix(), Which()
│       │   ├── exec.go        # RunIn(), RunOutputIn(), RunSilent()
│       │   ├── args.go        # NormalizeArgs() — accepts `-foo` and `--foo`
│       │   ├── verify.go      # the verify pipeline
│       │   ├── gate.go        # the gates: what this project measures, and how
│       │   ├── run.go         # the judge: thresholds, the one comparison, the verdict
│       │   ├── verifiedtree.go # the record verify writes for the commit gate (§7)
│       │   └── setup.go       # `git config core.hooksPath .githooks`
│       └── cmd/               # one thin main.go per binary; discovered by ./make
│           ├── make/main.go   # the meta-builder
│           ├── verify/main.go
│           ├── setup/main.go
│           ├── gate/main.go
│           └── run/main.go
└── <project source>/          # the actual project — anything: Go, Rust, C++, JS, …
```

**Why the gate terms live in `tools/gates/`**: a fixed path is what lets something outside the project find them without being configured. A flow, a scheduler, or a conformance checker asks whether this project's judging terms are an artefact distinct from the judge, and it can only answer that by looking somewhere it already knows. A configurable location makes the question un-askable, and a location the judge alone knows makes the terms a property of the binary rather than of the tree it judges.

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

These are [`primitives/`](../primitives/), imported rather than copied ([`primitives.md`](primitives.md) §1). Pipeline-orchestration helpers (`RunVerify`, `RunGate`, the judging terms) are project-specific and stay in each adopter's own `common/`.

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

The single command a contributor runs before committing. Before step one it **clears the verified-tree record**, so a run that dies part-way leaves nothing blessed and an in-flight verify blesses nothing. Then it bundles:

1. **Format** — `gofmt -w .` (or the target project's formatter), then the project's own code formatter.
2. **Build** — full build pipeline.
3. **Vet** — static analysis (`go vet`, `clippy`, `eslint`, whatever applies).
4. **Test (host)** — unit tests + e2e tests.
5. **Test (optional alt targets)** — for example, a project that compiles to WASM might run an additional WASM test pass behind `--wasm`.
6. **Summary block** — always printed, even on failure. Includes a per-target pass/FAIL line and elapsed time.
7. **Failed-tests excerpt** — re-prints the `FAILED:` section so an agent tailing the last ~40 lines sees every failure.
8. **Record** — write the id of the tree just blessed to `.workspace/verified-tree`. It runs last and only after every other step passed, so a red run blesses nothing. The reading end is the workspace's commit gate (`bin/precommit-guard`, §9), which refuses a commit whose staged tree differs from the recorded one: the exit status says the tree is sound, and the record says *which* tree that was.

Key design choices in `verify.go`:

- **Global file lock** at `~/.<project>/verify.lock` (uses `gofrs/flock`). Concurrent verify runs from different worktrees block on each other rather than thrashing the cache. The lock file records the holder's repo path so waiters print `Waiting for verify run in <path> to finish...`.
- **Interrupt handling**: between every pipeline step, check `Interrupted()` and bail with a clean message. The signal handler sets a flag; tools don't die mid-test with a stack trace.
- **Local-by-default cache**: `bin/verify` (and other tools) call `SetupLocalCache(root)` which sets the project's home env var to `<root>/.<project>-home/` so a verify run doesn't pollute the user's `~`. `--shared` opts back into `~/.<project>`.
- **`--clean` is up front**, before `SetupLocalCache`, so a clean run starts from a known state.
- **Format runs first.** Running format last means a botched format diff lands in the next commit instead of failing this one.
- **Exit code is the only contract.** Non-zero = "not safe to commit," printed in a red banner. Zero = "OK to commit," printed in green.

---

## 8. Tools the project does not build

Three of the binaries in `bin/` are not compiled by `./make` and their source is not in this tree: **`precommit-guard`**, **`tool-guard`** and **`issue`**. They are owned and delivered by a separate organization repository, arriving as a release artifact that `workspace setup` installs into `bin/` and `workspace update` refreshes. The project neither builds them, vendors them, nor keeps a variant of them under another name.

The rule they come from is the workspace's [`tool-contract.md`](https://github.com/promise-language/workspace/blob/main/docs/tool-contract.md) §1, §2 and §5, and this document restates none of it. What matters here is the shape it leaves in the layout:

- **The meta-builder compiles what is under `tools/build/cmd/`, and that set contains no twin.** A project that built its own `guard` or `precommit` would hold a second copy of a policy the workspace is accountable for — and a second copy is where that policy can quietly be weaker, in exactly the repository nobody is looking at.
- **What the project owns is the wiring, not the tool.** The committed `.githooks/pre-commit` (§9) and the committed `.claude/settings.json` (§10) *name* those binaries. Committing the wiring is what makes the gate live in a fresh clone, before `workspace setup` has ever run there — a hook that provisioning had to write would be absent exactly when it was most needed.
- **A project that adopts no workspace still has a gate.** It is `bin/verify`, and §9 says how the hook says so.

---

## 9. The commit gate hook (`bin/precommit-guard`)

`.githooks/pre-commit` is a short shell trampoline that `exec`s `bin/precommit-guard`. The hooks dir is wired up by `bin/setup` (called from `./make`), so a fresh clone gets hooks on first `./make`.

The hook is intentionally light, and that is a division of labour rather than a compromise: anything that requires running tests belongs in `bin/verify`, which the developer runs explicitly. Putting test execution in the hook makes commits slow and gets the hook disabled — which kills the whole gate. What the two ends share instead is the *record* `bin/verify` leaves (§7): the tree it blessed, which the commit gate compares against the tree being staged. What else that tool checks is its own document's to say, not this one's.

**A scaffolded project fails closed onto a workspace tool only where it has opted in. Opt-in is the provisioning marker `.workspace/project.json`. Absent the marker, the project's commit gate is `bin/verify`, run before committing, and the hook says so and gets out of the way.**

This is the one judgement in this section, and it follows from [`tool-contract.md`](https://github.com/promise-language/workspace/blob/main/docs/tool-contract.md) §1: fail-closed is a consequence of having opted in, never a tax on a repository that did not. The workspace repository is private. An unconditional refusal would therefore block a public adopter's very first `git commit` on a recovery they cannot perform, and the only other way to unblock them — building a local twin — is what §5 forbids. Making the refusal conditional on the marker costs a provisioned checkout nothing: it has the marker, so it takes the refusing branch exactly as before.

One hook text, used by this repository and by everything it scaffolds:

```bash
#!/usr/bin/env bash
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
if [ -x "$root/bin/precommit-guard" ]; then
    exec "$root/bin/precommit-guard"
fi
if [ -f "$root/.workspace/project.json" ]; then
    echo "pre-commit: this checkout is provisioned but bin/precommit-guard is missing —" >&2
    echo "            run 'workspace update' (or 'workspace setup'), then commit" >&2
    exit 1
fi
echo "pre-commit: no workspace commit guard is installed here; the gate in this project" >&2
echo "            is bin/verify — run it before committing" >&2
```

A provisioned checkout takes branch 1, or branch 2 if the tool was removed. Only a checkout that never opted in reaches branch 3.

---

## 10. The agent guard (`bin/tool-guard`)

`bin/tool-guard` is the harness-level guard for Claude Code, wired in the project's committed `.claude/settings.json` on **both** tool-use events:

```json
{
  "hooks": {
    "PreToolUse": [{
      "matcher": "*",
      "hooks": [{
        "type": "command",
        "command": "\"$CLAUDE_PROJECT_DIR/bin/tool-guard\" || exit 2",
        "shell": "bash",
        "timeout": 10
      }]
    }],
    "PostToolUse": [{
      "matcher": "*",
      "hooks": [{
        "type": "command",
        "command": "\"$CLAUDE_PROJECT_DIR/bin/tool-guard\" || true",
        "shell": "bash",
        "timeout": 10
      }]
    }]
  }
}
```

`PreToolUse` is the gate and fails closed (`|| exit 2`); `PostToolUse` observes and fails quiet (`|| true`), because by then the tool has already run and an enforcing shape could only inject an error after a completed call. Which tools matter is the guard's decision, never a list in a settings file — hence `"matcher": "*"` on both.

**The command strings above are exact, not a shape.** A conformance checker compares them byte-for-byte and reports any difference at error severity, so a wrapper, a reordering, or a helpfully-added flag is itself the deviation. This is the one place in this document where the text matters more than the intent behind it: two projects whose guards are wired "equivalently" are two projects whose guards can be made to differ.

`$CLAUDE_PROJECT_DIR` is set by the harness on every invocation, so the hook is immune to subprocess cwd drift.

---

## 11. The gate entry point (`bin/gate`)

`bin/gate` is how anything outside the tree learns what this project can measure, and gets a measurement.

```
bin/gate <name> --envelope     # one JSON envelope on stdout, and nothing else
bin/gate --list                # the names this project answers, one per line
```

Design rules:

- **A gate measures and modifies nothing.** That is the whole difference between a gate and `bin/verify`, which formats the tree on its way to an answer: an answer about a tree that was repaired first is not an answer about the tree anyone proposed.
- **A gate holds no threshold and reaches no verdict.** It reports what it found and stops. Whether `unformatted_files: 3` is acceptable is §12's question, and a gate that answered it would be the threshold sitting inside the party under measurement.
- **Stdout carries the envelope and nothing else.** Every child process a gate spawns has its stdout captured; progress goes to stderr. The envelope is written whole, in one write, so a run killed part-way leaves output that does not parse — which is how a reader tells "measured nothing" from "measured and reported" without asking a process that is no longer alive to answer.
- **The gate vocabulary is closed.** A name absent from the project's gate map is refused rather than guessed at: a runner asking for a gate this project does not have must learn that, not receive an empty measurement that reads like a clean result.
- **`--list` is the only other thing that writes to stdout**, and it exists so that nothing outside the project has to hold a second copy of what the project can measure. Asking the entry point is the only way to learn it that cannot go stale.
- **Discovery replaces a registry.** An in-tree config file declaring which gates exist would be a second list beside the one the code implements, and the two would eventually disagree. `--list` is generated from the map the measurements come from, so it cannot.

---

## 12. The judge (`bin/run`) and the terms (`tools/gates/`)

`bin/run` is the judging layer, and it is a **different program from the gates on purpose**: a gate that held its own thresholds could be made to pass by editing the gate — and when the thing being measured is a change written by an agent, the agent can edit it. The party under judgement must not hold what judges it.

It has two modes, and the difference is who ran the gate:

```
bin/run <gate>                 # measure, then judge — the by-hand path
bin/run <gate> --verdict       # judge an envelope given on stdin, spawn nothing
```

- **`run <gate>`** executes `bin/gate <gate> --envelope` as a process and judges what came back, printing each measurement beside the term it was judged on. It is the path for someone iterating on one failing area, and it goes through the process boundary rather than calling in, so a gate that is broken in a way only visible across that boundary is broken here too — where a person can see it.
- **`run <gate> --verdict`** reads the envelope on stdin, writes one JSON verdict on stdout, and spawns nothing. This is the mode an external runner asks, and spawning nothing is the point: an entry point that ran the gate itself would *be* the runner, and the runner may not come from the tree it is running against.

Both reach the verdict through the same comparison, so they cannot disagree about what the project allows. **The verdict is the JSON, not the exit status** — `--verdict` exits 0 whether or not it found the measurement acceptable, and the verdict carries the terms it was reached from, so a reader who was not there can re-check it.

The terms live in `tools/gates/`, versioned with the tree they judge:

- **`thresholds.json`** — one entry per capped metric, each a direction (`at_most` / `at_least`) and a cap. A metric nothing caps cannot fail; an incomplete run cannot pass, whatever the numbers say, because honest numbers that understate what was checked are indistinguishable from an improvement unless the run says so.
- **`baselines.json`** — the ratcheted-metric artefact: committed values (test count, leak count, coverage) that may only move in the approved direction. Tracking the file is the declaration that the ratchet check applies to this project; an empty `{}` is a complete and honest starting state.

A genuine threshold or baseline change is a deliberate edit to a committed file, reviewed with the code it judges. That is the whole mechanism, and it is why the terms are an artefact rather than a constant in the judge.

---

## 13. Cache isolation

Tools default to a repo-local home (`<root>/.<project>-home/`) instead of `~/.<project>`. Reasons:

- A `verify --clean` doesn't blow away an unrelated worktree's cache.
- Two worktrees of the same repo don't fight over a single shared cache.
- A new contributor doesn't end up with a polluted `~`.

`SetupLocalCache(root)` is one function: set `<PROJECT>_HOME=<root>/.<project>-home`, create the dir, done. Every tool that runs in the verify pipeline picks it up via env. `--shared` is the opt-out flag for developers who want a single shared cache across worktrees.

The same env-var trick works for any toolchain (Cargo home, NPM cache, Bazel disk cache, ccache, etc.) — set it before the first subprocess that uses it.

---

## 14. Step-by-step implementation guide

The easiest path is `go run github.com/promise-language/forge/cmd/init@latest` in your target repo, which lays down the layout below. The steps are listed here so you know what `init` produces and so you can reproduce it by hand if you prefer.

1. **No root sentinel and no `FindRoot()`.** The repo root is computed once by `./make` (from its own cwd, which the trampoline pins to `<root>/tools/build`) and baked into every compiled tool via `-ldflags "-X main.repoRoot=<abs>"`. Tools never walk up looking for anything; they just use their baked-in `repoRoot`. This step is a reminder, not an action — the actual mechanism is implemented when you write `cmd/make/main.go` in step 4.

2. **Initialize `tools/build/` as a Go module.** `cd tools/build && go mod init <module-name>/tools/build`. Add it to `go.work` if the project already has one; otherwise just leave it as an island Go module.

3. **Depend on `github.com/promise-language/forge/primitives`** at an exact version. It carries the load-bearing helpers — the hash, the staleness check, OS detection, exec, flag normalization, help — and a project writes none of them itself ([`primitives.md`](primitives.md) §1). There is no `common/root.go` — root is baked into each tool's `main` by the meta-builder. Helpers that need root take it as a `repoRoot string` parameter.

4. **Write `cmd/make/main.go`.** Just the meta-builder — discover `cmd/` subdirs, build each into `bin/`, write the hash sidecar, wire up git hooks via `RunSetup`.

5. **Add `./make` and `make.cmd` at the repo root.** Two-line trampolines (see §3).

6. **Gitignore `bin/`.** Add `bin/` to `.gitignore` so built tools are never committed.

7. **Write `cmd/setup/main.go` and `common/setup.go`.** Configure `core.hooksPath` to `.githooks`.

8. **Add `.githooks/pre-commit`** — the shell trampoline that execs `bin/precommit-guard`, the commit gate the workspace installs (§9). Take the text verbatim: it fails closed onto that tool where the checkout is provisioned, and names `bin/verify` where it is not.

9. **Write `cmd/build/main.go` + `common/build.go`.** Wraps your project's actual build command. Detect missing prereqs and print actionable hints rather than letting subprocess errors propagate raw.

10. **Write `cmd/test/main.go`, `cmd/format/main.go`, `cmd/vet/main.go`.** Thin wrappers; each is ~20 lines.

11. **Write `cmd/verify/main.go` + `common/verify.go`.** The orchestrator. Format → build → vet → test, with a summary block printed unconditionally and a global file lock.

12. **Write `cmd/gate/main.go` + `common/gate.go`.** The measuring entry point (§11): a closed map of gate names, one measurement function each, `--envelope` on stdout and `--list` for discovery. Start with the four that make up `integration` — formatted, builds, checked, tested — and `fit`, which measures whether the machine is fit to be given work.

13. **Write `cmd/run/main.go` + `common/run.go` + `tools/gates/thresholds.json`.** The judge (§12) and its terms: a cap for every metric a gate emits, one comparison serving both the by-hand path and `--verdict`. Add `tools/gates/baselines.json` — `{}` is a complete starting state, and tracking the file is what declares that the ratchet applies here.

14. **Wire `.claude/settings.json`** at `bin/tool-guard`, on both `PreToolUse` and `PostToolUse` (§10). Copy the command strings exactly; they are compared byte-for-byte.

15. **Document the workflow in `README.md` and `CLAUDE.md`.** Three lines:

    > Bootstrap: `./make`.
    > Build: `bin/build`.
    > Before committing: `bin/verify`.

    Everything else is discoverable.

The first 8 steps get you a working bootstrap; the remaining 7 get you the full gate. Each step is independent and the system is usable at every checkpoint.

---

## 15. What this model deliberately avoids

- **No Makefiles.** Make's dependency model is excellent for actual compilation graphs and useless for "run this tool, then that tool, and print a summary." The Go pipeline is clearer and portable.
- **No bash + PowerShell pair.** Every script duplicated across platforms diverges. One Go binary with `runtime.GOOS` checks is shorter than two scripts.
- **No npm-style postinstall magic.** `./make` is explicit. The user knows what just happened.
- **No "framework". ** `common/` is a flat package of helpers, not an abstraction. Each tool's `cmd/<name>/main.go` calls the helpers directly.
- **No silent failures.** Every error path returns a wrapped error with context. The verify summary always prints, even on failure, so an agent can grep the result without re-running.
- **No tests-in-precommit-hook.** Tests run in `bin/verify` which the developer runs explicitly. The hook stays sub-second so it never gets disabled.
- **No *unpinned* dependency on Forge.** The tools module depends on `primitives/` at an exact version recorded in its own `go.mod` and `go.sum`, so an upstream change reaches a project when that project raises the version and never before ([`primitives.md`](primitives.md) §3). What the project owns is the pipeline and the pin; what it does not own is a private copy of a helper with one right answer.
- **No twin of a tool another party owns.** A project does not build its own copy of a tool the workspace is accountable for, under that name or any other (§8). A local copy is where the policy can quietly be weaker — the same disagreement the bullet above avoids, on the enforcement side rather than the helper side, and worse there because nothing downstream reads a guard's source to find out which version it was.

---

## 16. Reference — the runnable implementation

This blueprint ships its own worked example: the [`cmd/init`](../cmd/init/main.go) scaffolder embeds every file described above as a string constant and writes a complete, runnable tooling tree into a target repo. Read the scaffolder to see the exact source the doc prescribes — it is the single source of truth, kept in lockstep with this document.

Where each piece lives in [`cmd/init/main.go`](../cmd/init/main.go):

- Bootstrap: the `makeSh` / `makeCmd` constants (`./make`, `make.cmd`) and the `cmdMakeGo` constant (the meta-builder).
- Common library: the `platformGo`, `execGo`, `argsGo`, `hashGo`, `staleGo`, `setupGo`, `verifyGo`, `gateGo`, `runGo` and `verifiedTreeGo` constants — written into `tools/build/common/`.
- Staleness & root: `staleGo` + `hashGo`. There is no `root.go` — the root is baked into each binary via ldflags by the meta-builder (see §4–§5), so no runtime walk-up exists.
- Verify pipeline: `verifyGo` (a language-detecting example pipeline; adopters replace `verifySteps` with their real commands), with `verifiedTreeGo` carrying the record it writes (§7).
- Gates and the judge: `cmdGateGo` + `gateGo` (§11), `cmdRunGo` + `runGo` (§12), and the terms in the `thresholdsJSON` and `baselinesJSON` constants — written into `tools/gates/`.
- Committed wiring for the tools the project does not build (§8): the `preCommitHook` trampoline naming `bin/precommit-guard` (§9), and the `settingsJSON` constant wiring `bin/tool-guard` on both events (§10).
- Agent-facing workflow doc: the `buildDocRaw` constant, appended to `CLAUDE.md` so the `./make` → `bin/verify` → `bin/run` loop is discoverable; and `docsIndexMd`, the map of `docs/`.

The low-churn helpers are not among those constants: they are the importable [`primitives/`](../primitives/) package, which the scaffolded module depends on rather than reproduces ([`primitives.md`](primitives.md) §1).
