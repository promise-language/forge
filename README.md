# Forge

Dev tooling blueprint and scaffolding. Drop one `./make` into a project and get every dev tool compiled into `bin/`, a `bin/verify` commit gate, and the `bin/gate` / `bin/run` entry points that let anything outside the tree measure the project and judge what it measured — all from a single in-repo Go module that the project owns end-to-end.

## What's here

- **[`docs/blueprint.md`](docs/blueprint.md)** — the design doc. Explains the model, the file layout, the staleness check, the verify pipeline, the gate and judge entry points, and the tools a project does not build because another party owns them. Read this first.
- **`cmd/init/`** — scaffolding tool. Lays down the file structure in a target repo (`./make`, `make.cmd`, `tools/build/` with one `cmd/<tool>/main.go` per binary, the judging terms in `tools/gates/`, `.githooks/pre-commit`, `.claude/settings.json`) and exits. After it runs, the target repo owns every line.
- **`primitives/`** — the shared library the tools are built from: hashing, the staleness check, OS detection, exec helpers, flag normalization, help handling, git-hook wiring. A project depends on it at a pinned version, which is what keeps an upstream change from reaching a build nobody asked to change. See **[`docs/primitives.md`](docs/primitives.md)**.

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

- **Pinned, not copied**: each project owns its pipeline and the exact version of the shared helpers it builds against. No upstream library that can break your build on a Sunday, and no private copy of a helper that has one right answer.
- **Cross-platform without drift**: one source of truth, in Go, with `runtime.GOOS` checks where behavior must differ.
- **Agent-friendly**: deterministic root resolution (baked in at link time), explicit failure modes, summary blocks that survive `tail -40`.
- **Judged, not asserted**: every gate reports measurements and no verdict; the caps a verdict is reached against are a committed artefact in `tools/gates/`, reviewed with the code they judge.

## License

Dual-licensed under either of:

- [Apache License, Version 2.0](LICENSE-APACHE) (also at <http://www.apache.org/licenses/LICENSE-2.0>)
- [MIT License](LICENSE-MIT) (also at <http://opensource.org/licenses/MIT>)

at your option. See [NOTICE](NOTICE) for attribution.

Unless you explicitly state otherwise, any contribution intentionally submitted for inclusion in the work by you, as defined in the Apache-2.0 license, shall be dual-licensed as above, without any additional terms or conditions.
