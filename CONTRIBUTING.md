# Contributing to Forge

**Forge** is part of the **Promise Lang** project, hosted in the
`promise-language` organization and maintained under Promise Lang LLC.

## Contributor License Agreement (CLA) required

Before any pull request can be merged, you must sign the **Promise Lang
Contributor License Agreement**. When you open your first pull request, the CLA
Assistant bot will post a link to sign. You only need to sign once — it covers
all future contributions across the project.

- **Individual contributors** sign the Individual CLA.
- **Contributors acting on behalf of an employer** also have their employer sign
  the Corporate CLA.

You retain copyright in your contribution; the CLA grants Promise Lang LLC the
rights it needs to administer, distribute, and sublicense it as part of the
project.

## Licensing of contributions

Unless you state otherwise, any contribution you intentionally submit for
inclusion is dual-licensed under the [Apache License 2.0](LICENSE-APACHE) and
the [MIT License](LICENSE-MIT), with no additional terms or conditions. This is
core, LLC-covered code: contributions must **not** introduce code under a
copyleft license (GPL, LGPL, AGPL, EUPL, or similar) or code of uncertain
provenance.

## How to contribute

Forge is a dev-tooling blueprint plus a scaffolder — the `cmd/init` tool, the
stable `primitives/` helper library, and the design doc in `docs/blueprint.md`
(see the [README](README.md)).

1. Open an issue describing the change or addition you'd like to make, where
   practical — especially for changes to the blueprint or the scaffolded layout.
2. Keep `primitives/` semver-stable: it's a published dependency, so breaking
   changes to its surface need a deliberate version bump, not a drive-by edit.
3. Run `go build ./...` and `go test ./...` before submitting, and keep the
   scaffolder's output in sync with `docs/blueprint.md`.
4. Open a pull request and sign the CLA when prompted.
