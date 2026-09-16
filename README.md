# Forge

Dev tooling blueprint and scaffolding. Drop one `./make` into a project and get every dev tool compiled into `bin/`, a `bin/verify` commit gate, and the `bin/gate` / `bin/run` entry points that let anything outside the tree measure the project and judge what it measured — all from a single in-repo Go module that the project owns end-to-end.

## What's here

- **[`docs/`](docs/index.md)** — the specifications: [`blueprint.md`](docs/blueprint.md), [`command-line.md`](docs/command-line.md), [`primitives.md`](docs/primitives.md) and [`project-tools.md`](docs/project-tools.md), plus the organization-wide corpus they are held to. [`docs/index.md`](docs/index.md) is the map and says what each one is for — read that first.
- **`cmd/init/`** — scaffolding tool. Lays down the file structure in a target repo (`./make`, `make.cmd`, `tools/build/` with the definition and one `main` per tool, the judging terms in `tools/gates/`, `.claude/settings.json`) and exits. After it runs, the target repo owns every line.
- **`primitives/`** — the shared library the tools are built from: hashing, the staleness check, OS detection, exec helpers, the invocation surface, and the tooling harness itself. A project depends on it at a pinned version, which is what keeps an upstream change from reaching a build nobody asked to change. See **[`docs/primitives.md`](docs/primitives.md)**.

## Status

Forge is pre-1.0 and under active development. The four specifications in `docs/` are ratified: they describe the end state, not the tree as it stands today, and the tree does not answer all of them yet. The remaining work on any one document is the query [`docs/index.md`](docs/index.md) names — its tag, as a GitHub label, over this repository's open issues — and that query is the whole of it: every gap between a specification and the implementation is covered by an open issue carrying that document's tag.

What does work today is the loop this README describes: `./make`, then `bin/verify`, on this repository and on a repository `cmd/init` has scaffolded.

## Getting started

In a new or existing repo:

```bash
go run github.com/promise-language/forge/cmd/init@latest
./make
bin/verify
```

After `./make`, the project owns its pipeline. Forge reaches it as one pinned dependency in `tools/build/go.mod`, raised deliberately.

## Design philosophy

The blueprint exists because a real compiler project had bash + PowerShell + Makefiles drifting across platforms. The fix was one Go module that compiles every dev tool into `bin/`, with one `./make` bootstrap. That pattern generalizes; Forge is the generalized version, plus a scaffolder so adopters don't copy-paste from the doc.

The design optimizes for:

- **Pinned, not copied**: each project owns its definition and the exact version of the shared library it builds against. No upstream library that can break your build on a Sunday, and no private copy of a helper that has one right answer.
- **Cross-platform without drift**: one source of truth, in Go, with `runtime.GOOS` checks where behavior must differ.
- **Agent-friendly**: deterministic root resolution (baked in at link time), explicit failure modes, summary blocks that survive `tail -40`.
- **Judged, not asserted**: every gate reports measurements and no verdict; the caps a verdict is reached against are a committed artefact in `tools/gates/`, reviewed with the code they judge.

## License

Dual-licensed under either of:

- [Apache License, Version 2.0](LICENSE-APACHE) (also at <http://www.apache.org/licenses/LICENSE-2.0>)
- [MIT License](LICENSE-MIT) (also at <http://opensource.org/licenses/MIT>)

at your option. See [NOTICE](NOTICE) for attribution.

Unless you explicitly state otherwise, any contribution intentionally submitted for inclusion in the work by you, as defined in the Apache-2.0 license, shall be dual-licensed as above, without any additional terms or conditions.
