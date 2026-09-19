# Documentation Index

This is the map of `docs/`. It is the one file in the root that is not a specification —
everything else there is.

**The rules are defined once, org-wide, in [org/normative.md](org/normative.md)**: which
locations bind and which do not, the tag header, where status lives and where it never does,
one fact one home, the lifecycle of a specification, and what is enforced mechanically. This
index does not restate them.

**This project's status query.** Each root document's tag is a GitHub label, spelled as the
file's basename minus `.md`, and the remaining work for a document is:

> `gh issue list --label <tag> --state open --limit 200`

`--state open` and `--limit 200` are both written out deliberately: `gh issue list` defaults to
a limit of 30, so a specification with more remaining work than that would silently
under-report and read as nearly done.

## Specifications

- [blueprint.md](blueprint.md) — The model behind the build / verify / gate tooling, and why it is
  shaped this way.
- [command-line.md](command-line.md) — The one implementation of the CLI guide that every tool
  built from a project's tools module is made from.
- [primitives.md](primitives.md) — The shared library every managed project's dev tooling is
  built from, and how a project depends on it.
- [project-tools.md](project-tools.md) — The one `make`, `setup`, `verify`, `gate` and `run`,
  and the definition in Go a project shapes them with.

## Organization wide corpus

**Binding.** Every document under `org/` binds this project exactly as a specification in the root
does. Vendored from [promise-language/org](https://github.com/promise-language/org) at the release
named in [org/stamp.json](org/stamp.json).

Never edited here: an issue about one of these documents is filed against `org`
([org/normative.md#reconciliation](org/normative.md#reconciliation)); what this project files
locally under their tags is its own compliance gaps.

The corpus should be listed here once, as the directory, with
[org/stamp.json](org/stamp.json) naming its members — `normative.md`, Location. It is listed
per-file instead because `precommit-guard`'s docs-consistency check still requires that; see
issue #2.

- [org/normative.md](org/normative.md) — What makes a document binding, and the one docs
  structure every project holds.
- [org/engineering-guide.md](org/engineering-guide.md) — How code in this organization is
  written, in any language.
- [org/engineering-guide-promise.md](org/engineering-guide-promise.md) — The engineering guide
  applied to Promise source.
- [org/engineering-guide-go.md](org/engineering-guide-go.md) — The engineering guide applied to
  Go source.
- [org/cli-guide.md](org/cli-guide.md) — How every command-line tool behaves at its invocation
  surface.
- [org/identity.md](org/identity.md) — What names a host, a guest, an arena, a tool and a
  process; who creates each and where each is kept.
- [org/logging.md](org/logging.md) — How the development tools and the orchestration system log:
  the line, the log home, the bounds and the store.
- [org/stamp.json](org/stamp.json) — The version stamp: the org release these copies came from,
  with per-file hashes.
