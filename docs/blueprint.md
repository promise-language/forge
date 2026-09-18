# Dev Tooling Blueprint

> **Tag:** `blueprint` — remaining work to complete this document: the query named in
> [`docs/index.md`](index.md).

The design doc for Forge: the model behind the build / verify / gate tooling, and why it is shaped this way. The contract of the five tools every project builds — their invocations, their wires, their exit statuses — is [project-tools.md](project-tools.md); the invocation surface they are built on is [command-line.md](command-line.md); the shared library they import is [primitives.md](primitives.md). This document restates none of them.

The model is one bootstrap script (`./make` / `.\make.cmd`) that compiles every dev tool out of a single in-repo Go module into `bin/`, plus a commit gate (`bin/verify`) that repairs what has one right answer and measures what remains, in a single command. Each tool detects its own staleness and refuses to act until `./make` has run again. Adopting this replaces the typical bash + PowerShell + Makefile mix with one source of truth, in Go, that is portable by construction.

The pattern was distilled from a real compiler's dev tooling, where bash + PowerShell + Makefiles had drifted across platforms; Forge packages the design so other projects can adopt it without copy-pasting. This document is language-agnostic for the target project — the tooling itself is Go because Go cross-compiles trivially and has no runtime dependency to install, but the tools can build, test, and verify a project written in any language.

## What Forge ships

- **This doc** (`docs/blueprint.md`) — the model and the rationale.
- **`cmd/init`** — a scaffolder. Run it in a target repo and it lays down the file structure described in [Repository layout](#repository-layout). After it exits, the target repo owns every line. Forge is *not* a runtime dependency at that point.
- **`primitives/`** — the shared library the tools are built from: the hash, the staleness check, OS detection, exec helpers, the invocation surface, and the tooling harness the five tools are one call into. A project depends on it at a pinned version in its `tools/build/go.mod`; [`primitives.md`](primitives.md) states what belongs in it and why the pin is what makes that safe. What stays with the project is its **definition** — the gates it answers, the steps its `verify` runs, and the terms it is judged by ([project-tools.md](project-tools.md), [The definition](project-tools.md#the-definition)).

---

## What the model gives you

- **One bootstrap command** (`./make`) on a fresh clone: compiles every tool, wires `core.hooksPath`, and is idempotent on subsequent runs.
- **Native binaries under `bin/`** — `bin/setup`, `bin/verify`, `bin/gate`, `bin/run`, and the project's own commands ([project-tools.md](project-tools.md), [Layout](project-tools.md#layout)). Each is a Go binary, ~5–10 MB, fast to invoke.
- **A commit gate** (`bin/verify`) that runs the full pre-commit pipeline. The only thing a contributor needs to know is "run `bin/verify` before committing."
- **A self-staleness check**: every tool carries a link-time stamp of the source it was built from, and refuses to act when that source has moved ([project-tools.md](project-tools.md), [Staleness](project-tools.md#staleness)).
- **A single, cross-platform implementation**: no bash/PowerShell drift. Tools detect host OS at runtime when behavior must differ.
- **A measuring and a judging entry point** (`bin/gate`, `bin/run`). One reports what it found and reaches no verdict; the other judges an envelope against terms it does not hold. This is how anything outside the tree — a flow, a scheduler, a person — learns what this project can measure and whether a measurement is acceptable ([the gate entry point](#the-gate-entry-point), [the judge and the terms](#the-judge-and-the-terms)); their invocations are [project-tools.md](project-tools.md)'s ([Gate](project-tools.md#gate), [Run](project-tools.md#run)).
- **The judging terms as a separate artefact** (`tools/gates/`). Both term files are committed, reviewed with the code they judge, and read by the judge rather than compiled into it.
- **Committed wiring that names a tool the project does not build.** `.claude/settings.json` wires `bin/tool-guard`, which the organization's workspace delivers and `./make` never compiles ([tools the project does not build](#tools-the-project-does-not-build), [the agent guard](#the-agent-guard)). The commit-gate hook comes from the same place and is written by provisioning ([the commit gate hook](#the-commit-gate-hook)).
- **Deterministic repo-root resolution.** `./make` computes the absolute repo root at bootstrap time and links it into every compiled tool as part of that tool's stamp. Each binary already knows where its repo lives — no runtime walk-up, no sentinel-file scan, no `os.Executable()` games. Robust to cwd changes, subdirectory invocations, agents that move around the filesystem, and binaries copied into other repos (they still point at their birth-repo). The only tool that resolves root at runtime is the meta-builder itself (it runs via `go run`, so ldflags don't apply), and the `./make` trampoline `cd`s it into a known path first.

---

## Repository layout

```
<repo-root>/
├── make                       # bash bootstrap trampoline
├── make.cmd                   # Windows bootstrap trampoline
├── bin/                       # gitignored — built tools land here
│   ├── verify, gate, run, setup
│   ├── precommit-guard, tool-guard    # installed by the workspace, never built here (Tools the project does not build)
│   └── .tools.hash            # sidecar — drives the up-to-date check (project-tools.md, Make)
├── .home/                     # gitignored — scratch and toolchain caches (Cache isolation)
├── .workspace/                # gitignored — provisioning marker and the verified-tree record
├── .githooks/
│   └── pre-commit             # written by 'workspace setup', never by the project (The commit gate hook)
├── .claude/
│   └── settings.json          # wires bin/tool-guard on PreToolUse and PostToolUse (The agent guard)
├── docs/
│   └── index.md               # the map of docs/ — every document is listed here
├── tools/
│   ├── gates/                 # the judging terms — committed, read by bin/run (The judge and the terms)
│   │   ├── thresholds.json    # the caps a verdict is reached against
│   │   └── baselines.json     # ratcheted values a green run may move
│   └── build/                 # single Go module containing every tool
│       ├── go.mod             # requires forge at an exact version
│       ├── common/
│       │   └── project.go     # Define() — the whole definition (The definition)
│       └── cmd/               # one thin main per binary; discovered by ./make
│           ├── make/main.go   # the meta-builder
│           ├── setup/main.go
│           ├── verify/main.go
│           ├── gate/main.go
│           └── run/main.go
└── <project source>/          # the actual project — anything: Go, Rust, C++, JS, …
```

`tools/build/` and `tools/gates/` are [project-tools.md](project-tools.md), [Layout](project-tools.md#layout)'s, down to the shape of each `main`; what surrounds them is this document's.

**Why the gate terms live in `tools/gates/`**: a fixed path is what lets something outside the project find them without being configured. A flow, a scheduler, or a conformance checker asks whether this project's judging terms are an artefact distinct from the judge, and it can only answer that by looking somewhere it already knows. A configurable location makes the question un-askable, and a location the judge alone knows makes the terms a property of the binary rather than of the tree it judges.

**Why the repo root is baked into every binary at build time, not discovered at runtime:** Walking up from `cwd` looking for a sentinel file (the old design) breaks in three ways agents routinely trigger — `cwd` set outside the repo, `cwd` inside a *different* repo that has its own `./make`, or `cwd` inside a sub-checkout that has a sentinel-file collision. Linking the absolute path into each binary removes all three failure modes: the binary always knows its real repo regardless of `cwd`, regardless of what the surrounding filesystem looks like, regardless of how an agent invokes it. The cost is that binaries are tied to their source worktree — copying `bin/verify` from worktree A to worktree B leaves it pointing at A. That's the right semantics: it's the same binary, built for A. The fix is `./make` in worktree B, which costs ~1 second.

Conventions that make this work:

- **The repo root is determined exactly once, by `./make`, and frozen at build time.** It arrives in each tool as part of the link-time stamp the meta-builder writes. At runtime the binary simply uses it — no discovery, no walking, no fallbacks.
- **The meta-builder is the only tool that resolves root at runtime, because it runs via `go run` and ldflags don't apply.** The `./make` trampoline `cd`s it into `<root>/tools/build` first, so `cmd/make` reads its root as `filepath.Dir(filepath.Dir(cwd))` — two levels up from a known location. This is the one tightly-scoped place that still touches the filesystem to locate root, and it cannot be reached from a stale binary.
- **`bin/` is gitignored.** Tools are always built locally, never committed. This also keeps the stamped root honest: every `bin/<tool>` was, by construction, built in the worktree it claims to belong to.
- **The trampoline form pins `cwd` before `go run`:**
    ```bash
    exec go run -C "$(cd "$(dirname "$0")" && pwd)/tools/build" ./cmd/make "$@"
    ```
    The inner `cd "$(dirname "$0")"` resolves `$0` via its directory regardless of where the user invoked `./make` from (`./make`, `../make`, `/abs/path/make`), then `pwd` returns the absolute path. Result: `go run -C "/abs/repo/tools/build"`, so `cwd` inside `cmd/make` is always `<root>/tools/build`. Do not simplify the trampoline to `go run ./tools/build/cmd/make` — that depends on the user being at the repo root, which `./make` should not require.

---

## The bootstrap entry point

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

## The meta builder

`cmd/make` is the one tool that is not compiled. It runs from source through the trampoline, so it is never stale and is always the recovery a stale tool names. What it does, in order — the source set it derives, the hooks and ignores it checks, the build set it compiles, the stamp it links in, the pruning, and the sidecar it writes last — is [project-tools.md](project-tools.md), [Make](project-tools.md#make).

What belongs here is why the root is settled in that one place:

1. The trampoline `cd`'d into `<root>/tools/build` before `go run`, so `cmd/make` computes the root as `filepath.Dir(filepath.Dir(cwd))` — two levels up from a known location — and verifies the result is absolute.
2. That value is linked into every binary it builds, so no compiled tool ever looks for it, and there is no runtime path-discovery code anywhere in the compiled set.

Key properties:

- **Tools are discovered, not enumerated.** Drop a new directory under `cmd/`, run `./make`, and you have a new `bin/<name>` binary. No registration step.
- **The sidecar is the staleness contract.** It carries the same hash that is stamped into each binary, so a sidecar that is missing or stale and a binary that has gone away both mean the next `./make` rebuilds.
- **Each binary is tied to its source worktree by construction.** The stamped root is the absolute path of the worktree it was built in. Two worktrees of the same repo produce two distinct sets of binaries — there is no shared `bin/` that could point at the wrong tree.
- **Trim path and strip symbols.** `-trimpath` keeps binaries reproducible and stripping keeps them small; the stamp survives both, because only the symbol table is stripped, not initialized data.

---

## The staleness self check

Every tool but `make` compares the hash it was stamped with against the hash of its stamped source set, and refuses when the two differ. At what point in an invocation it checks, which conditions it refuses on, what status it exits with, and where the refusal is written are [project-tools.md](project-tools.md), [Staleness](project-tools.md#staleness); what the hash covers, and why a local `replace` widens it, is [`primitives.md`](primitives.md), [The staleness contract holds with nothing added](primitives.md#the-staleness-contract-holds-with-nothing-added).

This single check is what makes "edit a tool → re-run `./make`" the one-and-only developer workflow. Without it, a stale binary can silently produce wrong results for hours. And because the root is stamped in, the check is anchored to the binary's actual source tree — copying `bin/verify` into a different repo doesn't trick the staleness check into re-hashing the wrong `tools/build/`.

---

## The definition

The machinery every project's tools are made of — the invocation surface, the envelope and the listings, the judge's comparison, the ratchet and the verified-tree record, the staleness check, the confinement of what a tool writes — is imported from [`primitives/`](../primitives/) at the project's pinned version. What the project holds is one file: a `common/project.go` returning the definition that says which gates it answers, what its `verify` runs, and what each is judged by ([project-tools.md](project-tools.md), [One implementation](project-tools.md#one-implementation) and [The definition](project-tools.md#the-definition)).

The line between the two is [`primitives.md`](primitives.md), [What belongs here](primitives.md#what-belongs-here)'s membership test: could two projects disagree about this and both be right? A hash function, an envelope written whole, a judge that applies every term it was given — none of those is a place a project has standing to differ, and a copy of one is a future disagreement. Which gates to answer, which steps `verify` takes to get there, and what caps them, is exactly where projects do differ, and that is what the definition carries.

Inside a tool, work is reached by direct Go function calls rather than by spawning a sibling: `verify` measures with the same gate implementations `gate` runs, called in-process. This is fast, gives proper error propagation, and eliminates a class of "subprocess returned 1 but no usable output" bugs. The one deliberate exception is `bin/run <gate>`, which crosses the process boundary on purpose ([the judge and the terms](#the-judge-and-the-terms)).

Note: there is no `FindRoot()`. The root arrives in each tool's `main` as part of the link-time stamp, and anything that needs it is handed it.

---

## The commit gate

The single command a contributor runs before committing, and the only thing about the pipeline they need to know. It **repairs what has one right answer, then measures what remains**: each toolchain's formatter runs in rewrite mode, and then the parts of `integration` — `formatted`, `builds`, `checked`, `tested` — are measured by the same gate implementations `bin/gate` runs and judged against the same terms `bin/run` applies. Its stages, its ratchet, its lock, its summary and its exit status are [project-tools.md](project-tools.md), [Verify](project-tools.md#verify).

Two properties are this document's, because they are why the command has this shape at all:

- **Repair comes first**, and repairing at all is what separates `bin/verify` from a gate ([the gate entry point](#the-gate-entry-point)). Running the formatter last means a botched format diff lands in the next commit instead of failing this one.
- **The record is the seam.** `bin/verify` writes the id of the tree it blessed — cleared before the first stage and written after the last, so a red or interrupted run blesses nothing. The reading end is the workspace's commit gate (`bin/precommit-guard`, [the commit gate hook](#the-commit-gate-hook)), which refuses a commit whose staged tree differs from the recorded one: the exit status says the tree is sound, and the record says *which* tree that was.

---

## Tools the project does not build

Three of the binaries in `bin/` are not compiled by `./make` and their source is not in this tree: **`precommit-guard`**, **`tool-guard`** and **`issue`**. They are owned and delivered by a separate organization repository, arriving as a release artifact that `workspace setup` installs into `bin/` and `workspace update` refreshes. The project neither builds them, vendors them, nor keeps a variant of them under another name.

The rule they come from is workspace's `tool-contract.md`, The two sets, Required tools and One name one builder, and this document restates none of it. What matters here is the shape it leaves in the layout:

- **The meta-builder compiles what is under `tools/build/cmd/`, and that set contains no twin.** A project that built its own `guard` or `precommit` would hold a second copy of a policy the workspace is accountable for — and a second copy is where that policy can quietly be weaker, in exactly the repository nobody is looking at.
- **What the project owns is the wiring for the agent guard; the commit-gate hook is provisioning's.** The committed `.claude/settings.json` ([the agent guard](#the-agent-guard)) *names* `bin/tool-guard`, and a fresh clone therefore carries it. `.githooks/pre-commit` does not work that way: the hook that reaches `bin/precommit-guard` is written by `workspace setup`, alongside the binary it names, so that one party owns both ends of the guard ([the commit gate hook](#the-commit-gate-hook)). **The consequence that choice accepts:** a checkout that is never provisioned has no hook at all, where a scaffolded hook could have named `bin/verify` and got out of the way.
- **The settings file is provisioned *and* committed, where the hook is provisioning's at both ends.** One rule would be cheaper to hold than two, and it is not the rule either end is under: `workspace setup` writes `.claude/settings.json` without owning it, the project commits it, and a provisioned path the project commits is reported as something to commit rather than offered for ignoring (workspace's `tooling.md`, Provisioned does not mean untracked, and `bootstrap.md`, Setup owns the installed name set). What holds the file there is that a tracked one is live in a checkout provisioning has never reached — a first-class configuration rather than a half-finished install (workspace's `tool-contract.md`, A project stands alone) — where the hook needs no equivalent, because `core.hooksPath` makes the hook directory live the moment the tools build ([the commit gate hook](#the-commit-gate-hook)). So the scaffolder emitting `.claude/settings.json` and no `.githooks/` wiring is the shape those rules leave, not an oversight in it.
- **A project that adopts no workspace still has a gate.** It is `bin/verify`, run by hand — and [the commit gate hook](#the-commit-gate-hook) says what such a checkout gives up by having nothing enforce it.

---

## The commit gate hook

`core.hooksPath` is `.githooks`, set by `setup` and re-checked by `make` on every run ([project-tools.md](project-tools.md), [Setup](project-tools.md#setup)), so a checkout that can build can always run whatever hook is in that directory. The hook *file* is not the project's to write. `workspace setup` lays down `.githooks/pre-commit` when it installs `bin/precommit-guard`, and the scaffolder emits no `.githooks/` wiring at all — one party owns the guard and the wiring that reaches it, which is the same rule that stops a project building a twin of the guard itself (workspace's `tool-contract.md`, One name one builder).

**The consequence this accepts, stated rather than left implied: a project that is scaffolded and never provisioned has nothing running on `git commit`.** Its gate is still `bin/verify` — the same gate a provisioned checkout is held to — but until provisioning has run there, or the adopter writes a hook of their own, nothing enforces it. The alternative was a committed hook branching on the provisioning marker, which puts a copy of the guard's posture into every scaffolded repository, in exactly the place nobody is looking when it drifts.

The hook is intentionally light, and that is a division of labour rather than a compromise: anything that requires running tests belongs in `bin/verify`, which the developer runs explicitly. Putting test execution in the hook makes commits slow and gets the hook disabled — which kills the whole gate. What the two ends share instead is the *record* `bin/verify` leaves ([the commit gate](#the-commit-gate)): the tree it blessed, which the commit gate compares against the tree being staged. What else that tool checks is its own document's to say, not this one's.

---

## The agent guard

`bin/tool-guard` is the harness-level guard for Claude Code, wired in the project's committed `.claude/settings.json` on **both** tool-use events; why that file is committed where the commit-gate hook is not is [tools the project does not build](#tools-the-project-does-not-build). The file's text is the `settingsJSON` constant in [`cmd/init/main.go`](../cmd/init/main.go), which is what writes it; this document does not carry a second copy of it.

`PreToolUse` is the gate and fails closed; `PostToolUse` observes and fails quiet, because by then the tool has already run and an enforcing shape could only inject an error after a completed call. Which tools matter is the guard's decision, never a list in a settings file, so both events match every tool.

**The command strings are exact, not a shape.** A conformance checker compares them byte-for-byte and reports any difference at error severity, so a wrapper, a reordering, or a helpfully-added flag is itself the deviation. That is also why they are stated once, in the constant that emits them: two projects whose guards are wired "equivalently" are two projects whose guards can be made to differ, and a prose copy of the wiring is the first place the two spellings part company.

`$CLAUDE_PROJECT_DIR` is set by the harness on every invocation, so the hook is immune to subprocess cwd drift.

---

## The gate entry point

`bin/gate` is how anything outside the tree learns what this project can measure, and gets a measurement. Its invocations, what each writes to stdout, and what each exits with are [project-tools.md](project-tools.md), [Gate](project-tools.md#gate) — `--list` included, whose rendering follows the one output rule every tool in the organization obeys ([`cli-guide.md`](org/cli-guide.md), [Output modes](org/cli-guide.md#output-modes)) rather than a form fixed here.

Design rules:

- **A gate measures and modifies nothing.** That is the whole difference between a gate and `bin/verify`, which formats the tree on its way to an answer: an answer about a tree that was repaired first is not an answer about the tree anyone proposed.
- **A gate holds no threshold and reaches no verdict.** It reports what it found and stops. Whether `unformatted_files: 3` is acceptable is [the judge and the terms](#the-judge-and-the-terms)' question, and a gate that answered it would be the threshold sitting inside the party under measurement.
- **Stdout carries the envelope and nothing else.** Every child process a gate spawns has its stdout captured; progress goes to stderr. The envelope is written whole, in one write, so a run killed part-way leaves output that does not parse — which is how a reader tells "measured nothing" from "measured and reported" without asking a process that is no longer alive to answer.
- **The gate vocabulary is closed.** A name absent from the project's gate map is refused rather than guessed at: a runner asking for a gate this project does not have must learn that, not receive an empty measurement that reads like a clean result.
- **`--list` exists so that nothing outside the project has to hold a second copy of what the project can measure.** Asking the entry point is the only way to learn it that cannot go stale.
- **Discovery replaces a registry.** An in-tree config file declaring which gates exist would be a second list beside the one the code implements, and the two would eventually disagree. `--list` is generated from the map the measurements come from, so it cannot.

---

## The judge and the terms

`bin/run` is the judging layer, and it is a **different program from the gates on purpose**: a gate that held its own thresholds could be made to pass by editing the gate — and when the thing being measured is a change written by an agent, the agent can edit it. The party under judgement must not hold what judges it.

It has two modes, and the difference is who ran the gate ([project-tools.md](project-tools.md), [Run](project-tools.md#run)):

- **Measure, then judge** — the by-hand path. It executes `bin/gate` as a process and judges what came back, printing each measurement beside the term it was judged on. It is the path for someone iterating on one failing area, and it goes through the process boundary rather than calling in, so a gate that is broken in a way only visible across that boundary is broken here too — where a person can see it.
- **Judge an envelope given on stdin**, spawning nothing. This is the mode an external runner asks, and spawning nothing is the point: an entry point that ran the gate itself would *be* the runner, and the runner may not come from the tree it is running against.

Both reach the verdict through the same comparison, so they cannot disagree about what the project allows. **The verdict is the JSON, not the exit status**, and it carries the terms it was reached from, so a reader who was not there can re-check it. An incomplete run is never acceptable, whatever the numbers say: honest numbers that understate what was checked are indistinguishable from an improvement unless the run says so.

The terms live in `tools/gates/`, versioned with the tree they judge, and they split by who may move them — a cap is a bound only a person moves, a baseline is the best value a complete, green run has recorded ([project-tools.md](project-tools.md), [The terms](project-tools.md#the-terms)). Either way a genuine change is a deliberate edit to a committed file, reviewed with the code it judges. That is the whole mechanism, and it is why the terms are an artefact rather than a constant in the judge.

---

## Cache isolation

Tools write inside the repository root rather than in the user's home: scratch under `.home/tmp/`, and whatever a toolchain computes from this checkout under `.home/cache/`, with every child told so through its own environment ([project-tools.md](project-tools.md), [Writes and processes](project-tools.md#writes-and-processes)). Reasons:

- A clean run doesn't blow away an unrelated worktree's cache.
- Two worktrees of the same repo don't fight over a single shared cache.
- A new contributor doesn't end up with a polluted `~`.

There is no flag that opts back out, because the alternative is a tool whose effects the checkout cannot show. The same env-var mechanism works for any toolchain (Cargo home, NPM cache, Bazel disk cache, ccache, etc.) — set it before the first subprocess that uses it.

---

## Step by step implementation guide

The easiest path is `go run github.com/promise-language/forge/cmd/init@latest` in your target repo, which lays down everything below. The steps are listed here so you know what `init` produces and so you can reproduce it by hand if you prefer.

1. **Add `./make` and `make.cmd` at the repo root.** Two-line trampolines (see [the bootstrap entry point](#the-bootstrap-entry-point)).

2. **Initialize `tools/build/` as a Go module** requiring `github.com/promise-language/forge` at an exact version, and nothing else. That one pin carries the hash, the staleness check, OS detection, exec, the invocation surface and the tooling harness, and a project writes none of them itself ([`primitives.md`](primitives.md), [One implementation](primitives.md#one-implementation) and [The dependency is pinned](primitives.md#the-dependency-is-pinned)).

3. **Write `common/project.go`** — the definition, starting from the standard one, which is the whole of it for a project whose work needs nothing of its machine but a toolchain and disk ([project-tools.md](project-tools.md), [The definition](project-tools.md#the-definition)).

4. **Write the five `main`s** under `cmd/` — `make`, `setup`, `verify`, `gate`, `run` — each one call into the library, holding no logic of its own ([project-tools.md](project-tools.md), [Layout](project-tools.md#layout)).

5. **Add `tools/gates/thresholds.json` and `tools/gates/baselines.json`.** A cap for every metric a gate emits; `{}` is a complete starting state for the baselines, and tracking the file is what declares that the ratchet applies here ([the judge and the terms](#the-judge-and-the-terms)).

6. **Ignore `/bin/`, `/.workspace/` and `/.home/`** in the committed `.gitignore`. `setup` refuses until all three are ignored, and never edits the file itself — the entries belong to the project, not to a clone ([project-tools.md](project-tools.md), [Setup](project-tools.md#setup)).

7. **Wire `.claude/settings.json`** at `bin/tool-guard`, on both `PreToolUse` and `PostToolUse` ([the agent guard](#the-agent-guard)). Copy the command strings exactly; they are compared byte-for-byte.

8. **Document the workflow in `README.md` and `CLAUDE.md`.** Three lines:

    > Bootstrap: `./make`.
    > Before committing: `bin/verify`.
    > Iterating on one failing area: `bin/run <gate>`.

    Everything else is discoverable.

Steps 1 and 2 get you a tree that bootstraps; the definition in step 3 is what makes it measure anything. There is no `.githooks/` step: the commit-gate hook arrives with provisioning ([the commit gate hook](#the-commit-gate-hook)).

---

## What this model deliberately avoids

- **No Makefiles.** Make's dependency model is excellent for actual compilation graphs and useless for "run this tool, then that tool, and print a summary." The Go pipeline is clearer and portable.
- **No bash + PowerShell pair.** Every script duplicated across platforms diverges. One Go binary with `runtime.GOOS` checks is shorter than two scripts.
- **No npm-style postinstall magic.** `./make` is explicit. The user knows what just happened.
- **No framework in the project's own tree.** What a project writes is a definition, not an abstraction: one file saying what it measures and what may vary. The harness under it *is* a framework, deliberately and in exactly one place, because its parts are the ones no project has standing to differ about ([project-tools.md](project-tools.md), [One implementation](project-tools.md#one-implementation)).
- **No silent failures.** Every error path returns a wrapped error with context. The verify summary always prints, even on failure, so an agent can grep the result without re-running.
- **No tests-in-precommit-hook.** Tests run in `bin/verify` which the developer runs explicitly. The hook stays sub-second so it never gets disabled.
- **No *unpinned* dependency on Forge.** The tools module depends on `primitives/` at an exact version recorded in its own `go.mod` and `go.sum`, so an upstream change reaches a project when that project raises the version and never before ([`primitives.md`](primitives.md), [The dependency is pinned](primitives.md#the-dependency-is-pinned)). What the project owns is the definition and the pin; what it does not own is a private copy of a helper with one right answer.
- **No twin of a tool another party owns.** A project does not build its own copy of a tool the workspace is accountable for, under that name or any other ([tools the project does not build](#tools-the-project-does-not-build)). A local copy is where the policy can quietly be weaker — the same disagreement the bullet above avoids, on the enforcement side rather than the helper side, and worse there because nothing downstream reads a guard's source to find out which version it was.

---

## Reference

The implementation of everything described above is [`primitives/`](../primitives/) — the harness each of the five tools is one call into. It is not a reference in the sense of an example to copy: a project imports it at a pinned version and writes no part of it ([`primitives.md`](primitives.md), [One implementation](primitives.md#one-implementation)).

This repository is its own first consumer. Its `tools/build` is the layout [project-tools.md](project-tools.md), [Layout](project-tools.md#layout) describes, with its dependency `replace`d onto this working tree, so a change to the harness is measured by this repository's own gates before any project raises its pin ([`primitives.md`](primitives.md), [This repository is its own first consumer](primitives.md#this-repository-is-its-own-first-consumer)).

[`cmd/init`](../cmd/init/main.go) writes that layout into a target repository and exits. What it emits is the tree, not the machinery: the target owns every line it wrote, and the machinery arrives as the pinned dependency ([project-tools.md](project-tools.md), [The scaffolder and this repository](project-tools.md#the-scaffolder-and-this-repository)).
