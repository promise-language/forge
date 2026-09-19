# Project tools

> **Tag:** `project-tools` — remaining work to complete this document: the query named in
> [`docs/index.md`](index.md).

The one implementation of `make`, `setup`, `verify`, `gate` and `run` that every managed project
builds its tools from. A project shapes that implementation with a definition written in Go, and
with nothing else. The standard definition measures and repairs Go and Promise projects without a
line of customization.

**Out of scope:**

- **Which tools a project must have, and who builds each one.** That is workspace's
  `tool-contract.md`, The two sets and Required tools.
- **The envelope, the manifest, and what a runner may conclude from a gate.** That is base's
  `gate-contract.md`.
- **Where the judging terms live and what is in them.** That is the same document, Caps and
  baselines; workspace's `tool-contract.md`, Layout, is what places the two files in a managed
  project.
- **Which gates a flow asks for, and what a verdict must carry.** That is flow's
  `gates-and-commands.md`.
- **How any tool parses, reports and exits.** That is [command-line.md](command-line.md), which
  every tool here is built on.

This document restates none of them. It says how the one implementation satisfies them, and what a
project may change.

---

## One implementation

> **A project's `tools/build` holds its definition and one thin `main` per tool.** The machinery is
> `primitives/tooling`, imported at the project's pinned version:
>
> - the invocation surface;
> - the envelope, and the listings;
> - the judge's comparison;
> - the ratchet, and the verified-tree record;
> - the staleness check;
> - the confinement of what a tool writes.

The open question in [`primitives.md`](primitives.md) was whether this machinery could be held
in common while the terms it judges by could not. It can. The membership test that document states
answers it:

- **The harness is the part with one right answer.** A gate that prints half an envelope, or a
  judge that applies none of its terms, has no project behind it that chose that behaviour.
- **The gate set, the steps and the terms are the parts with many.** They stay the project's, and
  [the definition](#the-definition) is how the project states them.

Copied harnesses disagree exactly where no project chose to differ: a `verify` that repairs
formatting beside one that only reports it; a coverage gate measuring one module beside one
measuring every module; gates reading a child's stdout alone, where `go build` and `go vet` write
their diagnostics to stderr, so both counts collapse to zero or one; a judge applying one term out
of eight, so every machine reads as fit.

> **A project customizes its tools in Go. The only data files the tools read are the judging terms
> in `tools/gates/`.**

**Why Go and not configuration.** A configuration file is a second language for behaviour. It is
parsed separately and validated separately, and nobody reviewing the code it steers reads it. A
definition in Go is type-checked, tested by the project's own gates, and reviewed as code.

**The terms are outside that rule rather than an exception to it.** They are not behaviour: they
are what a measurement is judged against, kept apart from the code under measurement so the party
under judgement cannot move them in the same change (tool-contract.md, Layout).

> **The surfaces specified here are language-neutral, and two implementations of them are
> interchangeable.** A caller spawns an entry point, reads a wire, and reads an exit status. It
> never learns which language the tools it is talking to were written in, and nothing it does
> depends on knowing.

**That invariant is this document's own**, because a caller cannot be taught a dialect per
project: a flow spawns `bin/gate <name> --envelope` in every repository it resolves an item in,
`workspace setup` asks `bin/run --list`, and a conformance checker reads both, with no way to
discover which tooling a checkout holds.

**What must be identical is what a caller can observe**: the invocations, the wires, the exit
statuses, and the vocabulary of names. What may differ is delivery — what builds a tool, and where
it lands. A state one implementation cannot enter it simply never reports: an implementation that
runs its tools from source is never stale, so it never refuses for [staleness](#staleness), and a
caller handles the refusal because another implementation can.

## Layout

The project-tool layout is tool-contract.md's Layout, and the definition is what fills it:

```
tools/build/
    go.mod                   requires github.com/promise-language/forge at an exact version
    common/project.go        func Define() tooling.Project — the whole definition
    cmd/make/main.go         each main is one call into primitives/tooling
    cmd/setup/main.go
    cmd/verify/main.go
    cmd/gate/main.go
    cmd/run/main.go
    cmd/<command>/main.go    the project's own commands, built on primitives/command
tools/gates/
    thresholds.json          caps a person sets
    baselines.json           values a green run ratchets
```

Every tool's `main` has the same shape:

```go
// Command gate is this project's measuring entry point.
package main

import (
	"os"

	"github.com/promise-language/forge/primitives/tooling"
	"example.com/project/tools/build/common"
)

// stamp is written at link time by ./make.
var stamp string

func main() {
	os.Exit(tooling.Gate(common.Define(), stamp).Run(os.Args[1:]))
}
```

- **`common/` is where a repository's shared tool logic already lives**, so the definition is one
  file in it rather than a directory of its own.
- **A `main` holds no logic.** A change in behaviour is a change to the definition, or to the
  library.
- **`make` is the one `main` without a stamp.** It runs from source and is never stale.
- **A project's own commands** — a release, a product build, a bindings generator — are ordinary
  tools built on [the command library](command-line.md). They belong to the build set like every
  other tool, and `bin/run --list` reports them.

## The definition

```go
package common

import "github.com/promise-language/forge/primitives/tooling"

// Define is this project's tooling: the standard set, the frontend its server embeds, and a size
// limit that decides whether a change may land.
func Define() tooling.Project {
	p := tooling.Standard()
	p.Verify.Before(tooling.Builds, tooling.Step{
		Name:    "frontend",
		Summary: "build web/dist, which the server embeds",
		Run:     buildFrontend,
	})
	p.Gates.Add(tooling.Gate{
		Name:        "size",
		Summary:     "bytes in the release binary",
		Metrics:     []tooling.Metric{tooling.Bytes("binary_bytes")},
		Measure:     measureSize,
		Remediation: "remove what grew the binary, or raise the cap in a reviewed change",
	})
	p.Integration(tooling.Formatted, tooling.Builds, tooling.Checked, tooling.Tested, "size")
	return p
}
```

The sample shows the shape. The API's home is the library's source.

**`tooling.Standard()` is the whole definition for a project whose work needs nothing of its
machine but a toolchain and disk.** It carries both toolchains, the concepts flow names,
`integration` composed of `formatted`, `builds`, `checked` and `tested`, `fit` divided into
`fit:disk` and `fit:toolchain`, the standard [verify](#verify), and [setup](#setup)'s checks. A
toolchain with no units in the repository contributes nothing. A project whose suites need a
service adds `fit:services`, which is the seam flow's `environment.md` names for exactly that.

The definition admits four moves:

- **Add:**
  - a gate — a concept outside the ones flow names, or an instance of one of them;
  - a `verify` step, or a `verify` stage;
  - a `setup` step;
  - a step before or after `make` compiles;
  - a typed flag on `verify`, `setup` or `make`, bound to the steps it enables;
  - a toolchain.
- **Replace:**
  - the measurement behind a concept or an instance;
  - a toolchain's program, as a project that measures with the compiler it has just built does;
  - a standard step;
  - `verify`'s lock scope.
- **Compose:** what `integration` is made of, and in what order.
- **Remove:** `formatted`, `builds`, `checked`, `tested` or `covered`. A project need not have all
  of them (gates-and-commands.md, A project has gates the flow knows nothing about). `integration`
  and `fit` cannot be removed: a flow requires both.

> **What the definition does not reach is the same in every project.** A project that needs any of
> these to behave differently is asking for a change to the library, which every project then
> receives. It never gets a variant of its own.
>
> - the surface of the five tools;
> - the envelope;
> - the listings;
> - the judge's comparison, including that an incomplete run never passes;
> - the ratchet;
> - the verified-tree record;
> - the staleness refusal;
> - the confinement of writes.

> **Every tool validates the definition before it acts.** A tool whose definition has a defect
> refuses to run and names every defect, with status 1. `-help` and `-version` still answer. The
> defects are:
>
> - a gate name that is also a command's name;
> - `integration` or `fit` absent;
> - a composition part that names no gate;
> - one metric name reported twice in one envelope;
> - a gate without a remediation;
> - a name outside the CLI guide's alphabet, the instance separator below excepted;
> - two derived instance names that coincide.

The project's own tests call the same validation, so a defect fails `tested` before any invocation
reaches it.

## Units and instances

> **A unit is a directory whose manifest the repository tracks: `go.mod` for Go, `promise.toml` for
> Promise.** Units are found by asking git for tracked manifests, never listed by hand. A manifest
> under a `testdata` directory is a fixture, not a unit.

That rule is this document's, chosen because an untracked manifest is not part of the tree a gate
measures, and git answers from the tree's own index in one subprocess. Three consequences follow:

- A repository whose root has no `go.mod` has no root Go unit.
- `tools/build` is always a unit, so the tools measure their own source.
- A project that adds a module has added a unit, with no edit anywhere else.

**A toolchain supplies what its units need.** It knows each unit's source files: the tracked files
of its language under that unit, excluding nested units and `testdata`. It supplies:

- a formatter that rewrites, and one that checks;
- a build, a check, a test run, and coverage, where the language has them;
- the program it runs;
- the directory it caches in, under `.home/cache/`
  ([writes and processes](#writes-and-processes)).

The standard toolchains find their programs on `PATH` — `go` and `gofmt`, and `promise` — and
nowhere else. A fallback location would be a second answer to where the compiler is. A missing
program is reported by `fit:toolchain`. Any other gate that needs it cannot measure ([gate](#gate)).

> **An instance name is derived, and a level appears only where it distinguishes something.**

- **A project with units of more than one toolchain** has an instance per toolchain: `tested:go`,
  `tested:promise`.
- **A toolchain with more than one unit** has an instance per unit. The unit is named by its
  repository-relative directory, with `/` written as `-`, and `root` stands for the repository
  root: `tested:root`, `tested:tools-build`.
  - Where the project also has a second toolchain, the unit's name is prefixed by its toolchain's:
    `tested:go-tools-build`.
- **The concept alone measures every unit.**

Every name therefore narrows to something no other name does, and no name is a second spelling of
another. A project with one toolchain and one unit lists only the concepts. `fit` divides by
condition rather than by unit.

### What the standard toolchains measure

| Concept | Go | Promise |
|---|---|---|
| `formatted` | `gofmt -l` over the unit's source files → `unformatted_files` | `promise format -check` over the unit's `.pr` files, in batches that fit the host's command-line limit → `unformatted_files` |
| `builds` | `go build ./...` → `unbuildable_packages` | `promise build` of the unit into scratch → `unbuildable_modules` |
| `checked` | `go vet -json ./...` → `vet_findings` | `promise check` over the unit → `check_findings` |
| `tested` | `go test -json ./...` → `failed_tests`, `failed_packages`, `test_count` | `promise test --json <unit>/...` → `failed_tests`, `leaked_tests`, `test_count` |
| `covered` | `go test -coverprofile` into scratch → `statement_coverage` | `promise test -coverage --json <unit>/...` → `block_coverage`, from the per-file `{"kind": "coverage", "covered": …, "total": …}` records on the same stream |
| `fit:disk` | `worktree_free_bytes`, and `cache_free_bytes` at `.home/cache/` | as for Go |
| `fit:toolchain` | `missing_toolchains`: the toolchains with units whose program does not run and state its version | as for Go |

- **A count is read from a structured report wherever the toolchain has one**, never by matching
  prose meant for a person.
  - A vet run that printed no JSON at all is not zero findings. It is a toolchain that did not
    report, and nothing was measured.
- **A run that failed, and reported nothing countable, could not be measured.** "Zero failures" is
  not a reading of a run that did not happen.
- **A metric is named for what it counts.** A Go profile counts statements and a Promise run counts
  blocks, so they are `statement_coverage` and `block_coverage`; `go vet` reports vet findings and
  `promise check` reports check findings. One name over two counts would put two different
  measurements under one term, and the term would mean whichever toolchain last reported.
- **A diagnostic is a line naming a source position**, `path:line:column:`, which is the form both
  checkers print. The headers that group them are not findings.
- **Metrics combine across units by meaning.**
  - Counts sum.
  - Statement coverage sums statements and covered statements, then divides. Averaging percentages
    would let a small, well-tested unit hide a large untested one.
  - Each unit is also a group in the envelope, which is what tells a reader where a number came
    from.
- **How much disk is enough is a term, not code** ([the terms](#the-terms)).

## Make

`make` runs from source through the committed trampoline and needs nothing pre-built
([blueprint.md](blueprint.md), The bootstrap entry point). In order:

1. **Resolve the root.** The trampoline pins the working directory to `<root>/tools/build`. `make`
   refuses unless `<root>/tools/build/cmd/make/main.go` exists there.
2. **Derive the source set.**
   - It is all of `tools/build`.
   - It also includes every package outside `tools/build` that the tools import from a local
     `replace` target, with that package's embedded files, as `go list` reports them.
   - The set is derived, never declared (primitives.md, The staleness contract holds with nothing
     added): a new import changes a file inside `tools/build`, which changes the hash, and the next
     build derives the set again.
   - The hash covers every regular file in the set whose name does not begin with `.`. That
     includes embedded templates, which a `.go`-only hash misses.
3. **Wire the hooks and check the ignores**, exactly as [setup](#setup) does.
4. **Determine the build set.** It is every directory under `tools/build/cmd` except `make`. `run
   --list` reports the same set, computed by the same function. A directory whose name the CLI
   guide's alphabet does not admit is refused.
5. **Refuse a collision.** A name in that set which `.workspace/project.json` records as a workspace
   tool is refused, with the name (tool-contract.md, One name one builder). With no marker there is
   nothing to check.
6. **Stop if nothing is stale.** `make` compares its sidecar with the source hash and each binary's
   SHA-256, and when every one matches it reports the tools up to date. `-rebuild` compiles anyway.
   The CLI guide's No general switches forbids a flag called `-force`; this one is named for the one
   thing it overrides.
7. **Run the project's before-build steps.**
8. **Compile every tool** with `-trimpath`. Each binary gets one link-time stamp carrying the root,
   the source hash and the source set, encoded so that a root containing a space survives the
   linker's own flag parsing.
   - Every tool is attempted, and every failure is reported.
   - Any failure exits 1, and no sidecar is written.
9. **Prune.** A name the previous sidecar recorded, and the build set no longer holds, is removed
   from `bin/`. A name `make` did not record is never touched, because workspace tools share `bin/`.
10. **Run the project's after-build steps.**
11. **Write the sidecar, last:** the source hash, then each built name with its binary's SHA-256.

**`make` reads no `make.local` and runs no hook of any other kind**
(workspace's `bootstrap.md`, Setup and make compose).

**Its result** in JSON is `{"up_to_date": bool, "built": [...], "removed": [...]}`. In human mode it
is `Tools up to date`, or one line per tool built and removed. Progress goes to stderr.

## Staleness

> **Every tool except `make` compares the hash it was stamped with against the hash of its stamped
> source set.** It does so before it reads the command line at all, `-help` and `-version`
> included ([cli-guide.md](org/cli-guide.md#exit-codes)). On any mismatch it refuses with the
> refusal status
> ([command-line.md#exit-status-and-refusal](command-line.md#exit-status-and-refusal)).

| The tool finds | Refusal |
|---|---|
| a stamped hash that differs from the source set's | `stale` |
| no stamp at all — built by `go build` or `go install`, not by `make` | `unstamped` |
| a stamped root that no longer exists | `repository-unreachable` |
| a command it lists with no binary, asked to run it | `unbuilt` |

- **A refusal from a child is relayed, not reinterpreted.** `run` measuring a stale gate refuses
  with the gate's own refusal object.
- **A refusal writes nothing to stdout in the two protocol modes.** `gate <name> --envelope` and
  `run <gate> --verdict` have their stdout claimed by another contract, under which anything on it
  that is not an envelope or a verdict is the gate's own defect (gate-contract.md, What the gate
  runner reports). A refusal is not that, so it stays off the stream a runner parses and travels as
  the status and the stderr line. In every other mode the object is written, which is what lets
  `workspace setup`, reading `bin/run --list` through a pipe, tell a checkout whose tools need
  rebuilding from a project that answers nothing.
- **`-version` answers with the tool's name and its stamped source hash** — `project` and `text` in
  the payload [command-line.md#help-and-version](command-line.md#help-and-version) fixes. The hash
  is the tool's identity: it is what the staleness check compares, so the version a person reads and
  the version the tool holds itself to are one string. A source hash is not a semantic version, so
  the payload carries no `major`, `minor` or `patch`.
- **`make` is never stale.** It is compiled from the source it builds on every run, which is why it
  is the recovery every refusal names.

## Setup

1. **Wire the hooks.** Set `core.hooksPath` to `.githooks`.
2. **Check the ignores.** The committed `.gitignore` must ignore `/bin/`, `/.workspace/` and
   `/.home/`, as `git check-ignore` reports them.
   - When it does not, `setup` refuses and names each missing line.
   - It never edits `.gitignore`: that file is tracked, and the entries belong to the project, not
     to a clone.
3. **Run the project's setup steps.**

- **A second run changes nothing and says so.** Each step reports whether it changed anything.
- **Its result** in JSON is `{"hooks_path": ".githooks", "steps": [{"name": ..., "changed": bool}]}`.

`make` performs steps 1 and 2 on every run. A checkout that can build can therefore always gate its
commits, and a tool never writes scratch into a directory `git add -A` would stage.

## Writes and processes

> **A tool writes nothing outside its repository root.** Scratch goes under `.home/tmp/`, in a
> directory unique to the run, removed when the run ends; what a toolchain computes from this
> checkout goes under `.home/cache/`
> (workspace's `tooling.md`, An agent writes only where it is working, with the lifetimes in
> `tool-contract.md`'s Layout).

- **Every child runs with its scratch and its cache pointed inside the checkout** — `TMPDIR`, `TMP`,
  `TEMP` and `GOTMPDIR` at the run's scratch directory, and each toolchain's cache variable at its
  directory under `.home/cache/`. A toolchain offers no other way to be told. Setting a child's
  environment is how the tool instructs that child; nothing about it is an input to the tool.
- **A coverage profile, a build output and a batched file list are all scratch.**
- **A tool refuses to write under `.home/` or `.workspace/` when git does not ignore that
  directory.** A scratch file in a tracked path is a change to the tree a gate measures, and a file
  `git add -A` would put into the verified tree.
- **A lock a tool holds for its own run sits at `.home/`**, which tool-contract.md's Layout makes a
  floor rather than a ceiling for what a project keeps there.
- **Every child runs in its own process group, bounded by the run's context, and dies with the
  tool.**
  - The first interrupt signals the children and ends the run after the current step.
  - A second interrupt kills the groups.
  - No child outlives the tool that started it.
- **Progress lines go to stderr** as `==> <unit> <command>`, with every path repository-relative.
- **A child's stderr is attached to the tool's own stderr**, not copied through a pipe, except where
  a measurement parses it.
- **A child's stdout is captured, up to a bound this document sets at 10 MiB.** A measurement whose
  child exceeded it is incomplete, and says so. The requirement that a bound exist is flow's; its
  size is this document's.

## Gate

| Invocation | Stdout | Status |
|---|---|---|
| `gate <name> --envelope` | one envelope | 0 when an envelope was written, whatever it measured; 1 when nothing could be measured, with stdout empty |
| `gate <name>` | nothing; stderr names the gate, what it measures, and `bin/run <name>` | 2 |
| `gate --list` | human: one name per line; JSON: `{"gates": [{"name": ..., "summary": ..., "metrics": [{"name": ..., "type": ..., "unit": ...}]}]}` | 0 |
| `gate` | nothing; the brief form on stderr ([command-line.md#help-and-version](command-line.md#help-and-version)) | 2 |
| `gate -help` | its help | 0 |
| `gate -version` | its version | 0 |
| any, when stale | nothing in the protocol modes, the refusal object otherwise ([staleness](#staleness)) | the refusal status |

- **A gate leaves the tracked tree as it found it** (gate-contract.md, What a gate is). Everything a
  measurement writes is scratch under `.home/`, which the repository ignores and the subject
  therefore excludes.
- **The envelope is base's.** The library guarantees how it is produced:
  - It is written once, whole, after every measurement has returned, and carries the schema version
    base's contract declares.
  - It carries the host's `os/arch` as its target, unless the gate declares a target for the
    instance it measured.
  - It carries an incomplete reason whenever the run measured less than a full one, and never an
    empty one — a run that is incomplete with nothing to say is the one state an envelope cannot
    mean.
  - It names the gate that measured it, so a judge handed an envelope it did not produce can refuse
    one another gate wrote.
  - Each measurement carries the type and the unit the definition declared for that metric.

> **A measurement that disagrees with its declaration is an error, never a value to absorb.** The
> definition states each metric's type and unit; the envelope states what was measured. The two are
> a claim and its check, which is the rule base's contract already applies to a metric's type.

A float reported where the definition says a count, or bytes where it says a percentage, is a
different measurement wearing a declared name. The gate refuses to write such a metric and exits 1,
and the judge refuses an envelope carrying one ([run](#run)) — the sending side's checks are not
evidence to a reader that did not run it.

- **`--list` names every concept and every instance, sorted.** It answers the same set of names `run
  --list` reports as gates, rendered as objects rather than as bare names, so that what a gate
  declares of itself can grow additively. `run --list` keeps the names alone, which is the shape
  generic-projects.md's The contract fixes for it. The line form is for a person, and a program
  reads the JSON ([command-line.md#output](command-line.md#output)). This object is not base's
  manifest, whose fields and rules are that document's.
- **A composition measures each part by the path a caller asking for that part alone would take.**
  The whole cannot disagree with its parts about how anything is measured.
  - Its metrics are its parts' metrics, and its groups are its parts' groups.
  - A part that is incomplete makes the whole incomplete, naming the part.
- **An instance measures only the units it names.**
- **A gate may declare a preparation**, run once per process before any part that needs it — for
  example, building the compiler the measurements use.
  - A preparation's stdout goes to stderr.
  - Its failure makes every dependent part incomplete, with the reason. It never produces a
    measurement of zero.
- **A gate reads neither term file.** Whether a number is acceptable is the judge's question
  ([run](#run)).

## Run

| Invocation | Stdout | Status |
|---|---|---|
| `run <gate>` | human: each measurement beside every term it was judged on; JSON: `{"envelope": ..., "verdict": ...}` | 0 acceptable; 1 not acceptable, or nothing measured |
| `run <gate> --verdict` | one verdict, judged from the envelope on stdin | 0 when a verdict was written, whatever it says; 1 when no verdict can be reached, with stdout empty |
| `run <command> [arguments…]` | the command's own | the command's own, or the refusal status when that command has no binary |
| `run --list` | human: two labelled groups; JSON: `{"commands": [...], "gates": [...]}` | 0 |
| `run` | nothing; the brief form on stderr ([command-line.md#help-and-version](command-line.md#help-and-version)) | 2 |
| any, when stale | the refusal object, and nothing in the `--verdict` mode ([staleness](#staleness)) | the refusal status |

**The listing and the dispatch are generic-projects.md's run answers what the project can run,
satisfied by construction:**

- The commands come from the build set's function ([make](#make)), and the gates come from
  [the definition](#the-definition).
- A name in both is a definition defect.
- **`--list` says what the project builds, not what is built.** A name it reports whose binary is
  absent is the diagnosable state that listing exists to expose, so the listing never refuses over
  one. Only dispatching to that name does.
- A command receives its arguments verbatim.
- `run <gate>` executes `bin/gate` — with the host's executable suffix — as a process, so a gate
  broken in a way only visible across that boundary is broken where a person can see it.

**The judge:**

- **Every term a metric has is applied**, cap and baseline alike, and every one must hold.
- **An integer metric is compared as an integer.** A term that is not a whole number, for an integer
  metric, is a defect in the term. The judge cannot answer, rather than rounding.
- **A property is judged by the comparison every other measurement is judged by**, with `false`
  below `true` as base's `gate-contract.md`, What a metric declares, fixes it. A second comparison
  written for properties would be a second answer to what a term means. What the verdict states is
  `true` or `false`, never the order they were compared in.
- **An incomplete run is never acceptable.**
- **A metric with no term is reported as not judged**, and cannot fail.
- **An envelope none of whose metrics has a term cannot be judged.** `run --verdict` writes nothing
  and exits 1: `acceptable: true` over nothing would be a pass that no term granted, and a verdict
  the judge could not reach is not a refusal of the tree.
- **The envelope must be for the gate being judged.** Judging one gate's numbers against another's
  terms answers a question nobody asked.
- **The verdict carries `acceptable`, `thresholds` — every term applied, per metric — and, whenever
  `acceptable` is false, `detail`**, which is the shape gates-and-commands.md's How the judge is
  asked fixes. `detail` states the judgement, the evidence, and the gate's remediation.
  - **The evidence comes from the envelope's groups**, which say where a number came from.
  - **A judge handed no evidence says it was handed none**, rather than supplying a plausible cause
    for a measurement it did not take.

```json
{"acceptable": false,
 "thresholds": {"failed_tests": {"direction": "at_most", "cap": 0}},
 "detail": "failed_tests is 2, cap 0, in tools-build. Fix the failing tests; bin/run tested:tools-build measures that module alone."}
```

## The terms

> **A term a run may move is a baseline. A term only a person moves is a cap.** That split is why
> this implementation has both a judge and a ratchet.

**Where the two files are and what their entries hold are base's `gate-contract.md`, Caps and
baselines; what `direction` means and what a metric's `type` may be are that document's What a
metric declares.** Workspace's `tool-contract.md`, Layout, is what places the two files in a managed
project. This section defines none of it again, and does not restate it either — not the paths, not
the entry shapes, not the words `direction` takes. A format with two homes is a format that will be
written two ways, and the second home is the one that goes stale.

What is here is how this library reads that definition.

- **Nothing outside that definition is accepted.** The library reads the two files strictly, and the
  judge refuses to answer, naming the file and the entry, when an entry has an unknown key, a
  missing field, an unknown direction, or both `value` and `targets`.
- **A metric may have both a cap and a baseline**, and when it does, they agree on direction.

> **Only `verify` moves a baseline, and only forward.** After every measuring stage has passed, and
> before the tree is recorded, `verify` moves each baseline whose metric it measured completely, in
> the baseline's direction, when the run improved on it.

- **A target with no recorded value is recorded.**
- **An entry is never added or removed by a run.** Making a metric a ratchet is a person's decision,
  and tracking the file is the declaration.
- **The moved baseline is part of the tree `verify` records**, so the commit that earned the
  improvement carries it.
- **The workspace's commit guard refuses a staged baseline that moved backwards**
  (tool-contract.md, precommit-guard).
- **An incomplete run moves nothing**, and neither does a red one.

## Verify

**`verify` repairs what has one right answer, then measures what remains.** It measures by the same
gate implementations `gate` runs, called in-process, and judges the result against the same terms
`run` applies. Where a concern has both a command and a gate, they are the same rules asked two ways
(gates-and-commands.md, the concern table). A verify that ran its own separate commands would be a
third way, free to disagree with the other two.

**Stages.** Within a stage, every step runs and every failure is tallied. A stage with a failure ends
the run, and the stages after it are reported as not run. The standard stages are:

1. **`repair`** — each toolchain's formatter, in rewrite mode, over exactly the file set its
   `formatted` measurement reads. One function supplies both sets, so verify cannot repair one tree
   while the gate measures another.
2. **`builds`** — the part named `builds`, judged, where `integration` has one. Nothing a failed
   build measures is about the change.
3. **`measure`** — every other part of `integration`, judged. A project whose ratcheted metric comes
   from a gate outside `integration` adds that gate here.
4. **`ratchet`** — [the terms](#the-terms).
5. **`record`** — the verified-tree record.

A project whose failures are independent puts its steps in one stage. A project that must stop at the
first failure gives each step a stage of its own. Both are the same model.

**The record** is the writing end of tool-contract.md's precommit-guard verified-tree check:

- **It is cleared before the first stage and written only after the last**, so a red or interrupted
  run blesses nothing.
- **The tree is the one `git add -A` would stage.** It is computed over a temporary index seeded from
  a copy of the real one, whose location git reports, so a linked worktree is handled.
- **The record is written atomically.**
- **`verify` refuses when `.workspace/verified-tree` is not ignored.**
- **Outside a git checkout, recording is a reported no-op**, and a run whose stages all passed still
  exits 0.
- **The record's path is one constant in `primitives`**, imported by both ends of the contract
  (primitives.md, What belongs here).

**One `verify` runs at a time per checkout**, on a lock at `.home/verify.lock`; a second run waits and
names the run it is waiting for. **A project whose runs contend for the machine rather than for the
checkout declares a wider scope**, which tool-contract.md's The two sets names as an ordinary way for
one project's `verify` to differ from another's. Where such a lock lives is bounded by
[writes and processes](#writes-and-processes): a project's tools write inside the repository root.

**The summary always prints**, whether the run passes, fails, or is interrupted:

- every stage, and every step within it, with its status and elapsed time;
- then the evidence of every failure, with its verdict detail, so the last lines of the output carry
  all of it;
- then one final line: `✅ OK to Commit` or `❌ Verify FAILED: not safe to commit`.

In JSON the summary is `{"ok": bool, "stages": [{"name": ..., "steps": [{"name": ..., "status":
"passed" | "failed" | "not-run", "elapsed_seconds": ..., "detail": ...}]}], "tree": ...}`. `tree` is
absent when nothing was recorded. Status 0 means every stage passed, and status 1 means one did not.

**A project's own flags on `verify`** — a WebAssembly test pass, for example — are typed flags in the
definition. Each enables the steps bound to it.

## The scaffolder and this repository

**`cmd/init` emits a project that satisfies the tool contract** (primitives.md, This repository is
its own first consumer), and copies no helper `primitives` carries:

- the committed trampolines, `make` and `make.cmd`;
- the layout above: a definition returning `tooling.Standard()`, and the five `main`s;
- `tools/gates/thresholds.json` holding the standard caps, and an empty `baselines.json`;
- a `go.mod` requiring forge at the release `cmd/init` was built from, and the `go.sum`
  that pin is verified against — without it the emitted module does not build, so the
  first `./make` would fail before it compiled anything;
- `docs/index.md`, carrying this project's status query and listing `docs/org/` once as
  the directory, so a scaffolded tree satisfies the docs structure every managed project
  holds from its first commit (org/normative.md, Location). The corpus itself is not
  emitted: it reaches a project by sync, and a copy frozen into the scaffolder would be
  one no stamp checks;
- the committed wiring for the agent guard, `.claude/settings.json`
  ([blueprint.md](blueprint.md), Tools the project does not build). It emits no `.githooks/`
  wiring: the commit guard and the hook that reaches it are both `workspace setup`'s
  ([blueprint.md](blueprint.md), The commit gate hook);
- the `.gitignore` entries [setup](#setup) requires.

**This repository's `tools/build` is that layout**, with its `replace` pointing at this tree
(primitives.md, The staleness contract holds with nothing added). It is the library's first consumer:
a change to the library is measured by this repository's own gates before any project raises its pin.
