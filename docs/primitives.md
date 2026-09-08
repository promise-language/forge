# Primitives

> **Tag:** `primitives` — remaining work to complete this document: the query named in
> [`docs/index.md`](index.md).

The shared library every managed project's dev tooling is built from: what belongs in it, what
does not, and how a project depends on it without giving up the guarantee that an upstream
change cannot break its build.

**Out of scope:** which tools a project must have and who builds them — that is the workspace's
[`tool-contract.md`](https://github.com/promise-language/workspace/blob/main/docs/tool-contract.md)
§2, and this document states no requirement it already carries. What the tools *do* once built
is [`blueprint.md`](blueprint.md).

---

## 1. One implementation, not one per project

> **A helper that every project's tooling needs exists once, here. A second copy of it is a
> future disagreement.**

The pattern this repository describes was adopted by copying: `cmd/init` wrote the helpers into
a target repository, and from then on that repository owned them. The reasoning was that a
project owning its tooling cannot have its build broken by an upstream change on a Sunday, and
that reasoning is sound. What it did not survive is arithmetic.

Across seven adopting repositories the copies of `hash.go`, `stale.go`, `platform.go`,
`exec.go`, `args.go` and `setup.go` are byte-identical — the same digest in every one. That is
not seven projects each owning a decision; it is one implementation with seven chances to
drift, and no project has ever exercised the ownership the copying bought.

Where a copy did grow, it grew apart. Two repositories hold the writing and reading ends of the
same record and agree on its path only in prose, because neither can import the other's
constant — and one of them says so in a comment naming the drift it cannot test for. Two others
hold a 310-line measurement file differing in two words of comment. A single trampoline exists
in four spellings, and the platform variant of it in one, so a post-build step runs on one
operating system and silently does not on the other.

> **Identical copies are the failure, not the safeguard. What the copying protected is
> protected instead by §3.**

## 2. What belongs here

> **The membership test: could two projects disagree about this and both be right? If yes, it is
> theirs. If no, it is this library's.**

A hash function has one correct answer. A staleness check has one correct answer. Whether the
executable suffix is `.exe` has one correct answer. None of these is a place a project has
standing to differ, so a project holding its own is holding a latent disagreement with every
other.

What fails the test — and therefore stays in each project's own `tools/build/common`:

- **The verify pipeline.** Only the project knows how it builds and tests itself. One project
  must build a frontend before its Go package will compile; another takes a host-wide lock;
  another tallies every failure rather than stopping at the first. A caller requires `verify` to
  pass and has no opinion on what it does to get there.
- **The gate set and the judging terms.** Which gates a project answers, and the thresholds a
  verdict rests on, are the project's own — reviewed in the tree with the code they judge.
- **Anything naming one project.** A toolchain detection, a generated-file check, a
  project-specific measurement.

> **A contract spelled at two ends is one constant, held here, imported by both.**

Where two components must agree on a value — a record's path, a sidecar's name, a directory the
workspace writes into — the value lives here and both ends import it. Prose at each end is not
an agreement; it is two statements that happen to match today, and nothing fails when they stop
matching.

## 3. The dependency is pinned, per project

> **A project depends on this library at an exact version, recorded in its own
> `tools/build/go.mod` and `go.sum`. An upstream change reaches a project when that project
> raises the version, and never before.**

This is the whole of what the copying protected, kept without the copies. A published version
is immutable and its content is verified by `go.sum`, so between two bumps a project's tooling
is exactly as fixed as a vendored copy — and unlike a copy, what it is fixed *to* is a stated
version rather than whatever a scaffolder happened to emit on the day.

**The version is raised deliberately and centrally, never automatically.** A change that must
reach every project is one act against the managed set — the same fan-out that provisions them —
and it is a change each project's own gates then measure before it lands.

**A single pinned dependency is the whole cost.** The tools module is otherwise an island by
design: it holds the build toolchain's dependencies away from the product module, and that
property is unchanged by there being exactly one entry in it.

## 4. The staleness contract holds with nothing added

> **The tools-source hash covers `go.mod` and `go.sum`. Raising the pinned version therefore
> changes the hash, every compiled tool reports itself stale, and the next `./make` rebuilds.**

This is why the dependency does not need a mechanism of its own. The rule that a binary refuses
to run when its source has moved already covers a dependency bump, because the bump is recorded
in a file the hash reads. A project that raises the version and forgets to rebuild is told so by
the next tool it runs.

> **A project whose dependency is replaced by a local path hashes the replaced tree too.**

A `replace` directive pointing at a working tree puts tool source outside anything `go.sum`
describes. Hashing only `tools/build` there would let an edit to the replaced tree leave every
binary claiming to be current — the one failure the hash exists to prevent. Such a project names
the replaced directories, and the sidecar and the value baked into each binary are computed from
the same set by construction.

This is not a hypothetical accommodation. **This repository is that case**: forge's own tools are
built against forge's own working tree, because a library whose only consumer is a published tag
of itself is a library nothing tries before release.

## 5. Moving a helper here changes no call site

> **A helper published here keeps the name it had in the copies.**

The copies are byte-identical, so their exported names are already agreed fleet-wide. Preserving
them makes adopting the library a deletion and an import rather than a rewrite: the file goes,
the import arrives, and the package qualifier changes. A rename bundled into the move would put
a mechanical, reviewable change and a judgement call in the same diff, and the judgement call
would be reviewed as though it were mechanical.

Where a signature must grow — the hash gaining the replaced directories of §4 — it grows
variadically, so the call every project already writes keeps its meaning.

## 6. This repository is its own first consumer

> **A change to what this repository prescribes is exercised by this repository's own tooling
> before it is prescribed to anyone.**

Forge is both an adopter of the pattern and the place the pattern is defined. That is a hazard
when the two are allowed to differ — a blueprint describing tooling its own repository does not
run is a blueprint nothing has tried — and an advantage when they are not: the library's first
consumer is the repository that publishes it, so a helper that does not work is a build that
does not pass here.

It is also the reason `cmd/init` is held to the same standard. **The scaffolder emits a project
that satisfies the tool contract**: the required tool set, and none of the tools a project may
not build because the workspace is accountable for them.

## Open questions

**Whether the gate and run harness follows the helpers.** The measuring and judging entry points
are project tools, and their machinery is substantially identical across projects while their
terms are not. §2's test does not settle it on its own: the harness is the part with one right
answer and the terms are the part with many, but they are currently one file. Whether the
harness can be separated cleanly enough to hold in common — or whether the separation costs more
clarity than the duplication does — decides whether a protocol conformance check is a backstop
or the only thing standing between a project and gates nothing can ask for. The workspace's
`conformance.md` carries the same question from the other side.
