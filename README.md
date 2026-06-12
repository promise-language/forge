# Forge

Dev tooling blueprint and scaffolding. Drop one `./make` into a project and get every dev tool compiled into `bin/`, a `bin/verify` commit gate, ratcheted quality baselines, and a Claude Code guard hook — all from a single in-repo Go module that the project owns end-to-end.

## What's here

- **[`docs/blueprint.md`](docs/blueprint.md)** — the design doc. Explains the model, the file layout, the staleness check, the verify pipeline, the gate registry, and the ratchet system. Read this first.
- **`cmd/init/`** — scaffolding tool. Lays down the file structure in a target repo (`./make`, `make.cmd`, `tools/build/` with one `cmd/<tool>/main.go` per binary, `.githooks/pre-commit`, `.claude/settings.json`, `project.toml` stub) and exits. After it runs, the target repo owns every line.
- **`primitives/`** — small, stable helper library (FNV-128a hashing, file lock, OS detection, exec helpers, ldflags-injected `repoRoot` accessor). Versioned and semver-stable. Import it as a Go dependency, *or* copy the source into your own `common/` — both are supported, neither is "the wrong way."

## Getting started

In a new or existing repo:

```bash
go run github.com/promise-language/forge/cmd/init@latest
./make
bin/verify
```

After `./make`, the project owns its tooling. Forge is not a runtime dependency unless you explicitly import `primitives/`.

## Design philosophy

The blueprint exists because a real compiler project had bash + PowerShell + Makefiles drifting across platforms. The fix was one Go module that compiles every dev tool into `bin/`, with one `./make` bootstrap. That pattern generalizes; Forge is the generalized version, plus a scaffolder so adopters don't copy-paste from the doc.

The design optimizes for:

- **Self-contained**: each project owns its tooling code. No upstream library that can break your build on a Sunday.
- **Cross-platform without drift**: one source of truth, in Go, with `runtime.GOOS` checks where behavior must differ.
- **Agent-friendly**: deterministic root resolution (baked in at link time), explicit failure modes, summary blocks that survive `tail -40`.
- **Ratcheted quality**: metrics like test count, coverage, leak count are committed to `.baselines.json` and can only move in the approved direction.

## License

Dual-licensed under either of:

- [Apache License, Version 2.0](LICENSE-APACHE) (also at <http://www.apache.org/licenses/LICENSE-2.0>)
- [MIT License](LICENSE-MIT) (also at <http://opensource.org/licenses/MIT>)

at your option. See [NOTICE](NOTICE) for attribution.

Unless you explicitly state otherwise, any contribution intentionally submitted for inclusion in the work by you, as defined in the Apache-2.0 license, shall be dual-licensed as above, without any additional terms or conditions.
