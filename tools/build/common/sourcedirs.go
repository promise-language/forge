package common

// This repository's tool source is in two places, and this file is the one list
// that says so.
//
// A project that pins primitives at a published version has all of its tool
// source under tools/build: the version is recorded in go.mod and go.sum, both
// are hashed, so raising the pin changes the hash and every binary reports
// itself stale until the next ./make. This repository is the other case
// (docs/primitives.md, The staleness contract holds with nothing
// added and This repository is its own first consumer) — tools/build/go.mod replaces the dependency with
// a path into this working tree, because a library whose only consumer is a
// published tag of itself is a library nothing tries before release. That tree
// is tool source no go.sum describes, so the hash must cover it directly.
//
// The list is here, once, because the meta-builder computes the value it bakes
// into every binary and writes to bin/.tools.hash, and each binary recomputes it
// on every run. Two lists would let those disagree, and the disagreement's shape
// is either a binary that is stale forever or one that never notices it is.

import (
	"github.com/promise-language/forge/primitives"
	"github.com/promise-language/forge/primitives/command"
)

// sourceDirs names every directory holding this repository's tool source,
// relative to the repo root.
func sourceDirs() []string {
	return []string{primitives.ToolsBuildDir, "primitives"}
}

// SourceHash is the digest of this repository's tool source. The meta-builder
// bakes it into each binary; Fit recomputes it.
func SourceHash(repoRoot string) (string, error) {
	return primitives.SourceHash(repoRoot, sourceDirs()...)
}

// Fit is what every tool but the meta-builder answers command.Tool.Fit with: it
// reports why this binary may not act, or nil when it may. The dirs are this
// file's list, so a tool never names them and no two tools can name different
// ones.
func Fit(tool, repoRoot, compiledHash string) func() *command.Refusal {
	return func() *command.Refusal {
		return primitives.StaleRefusal(tool, repoRoot, compiledHash, sourceDirs()...)
	}
}
