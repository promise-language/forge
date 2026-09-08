package common

// This repository's tool source is in two places, and this file is the one list
// that says so.
//
// A project that pins primitives at a published version has all of its tool
// source under tools/build: the version is recorded in go.mod and go.sum, both
// are hashed, so raising the pin changes the hash and every binary reports
// itself stale until the next ./make. This repository is the other case
// (docs/primitives.md §4, §6) — tools/build/go.mod replaces the dependency with
// a path into this working tree, because a library whose only consumer is a
// published tag of itself is a library nothing tries before release. That tree
// is tool source no go.sum describes, so the hash must cover it directly.
//
// The list is here, once, because the meta-builder computes the value it bakes
// into every binary and writes to bin/.tools.hash, and each binary recomputes it
// on every run. Two lists would let those disagree, and the disagreement's shape
// is either a binary that is stale forever or one that never notices it is.

import "github.com/promise-language/forge/primitives"

// sourceDirs names every directory holding this repository's tool source,
// relative to the repo root.
func sourceDirs() []string {
	return []string{primitives.ToolsBuildDir, "primitives"}
}

// SourceHash is the digest of this repository's tool source. The meta-builder
// bakes it into each binary; StaleReason recomputes it.
func SourceHash(repoRoot string) (string, error) {
	return primitives.SourceHash(repoRoot, sourceDirs()...)
}

// StaleReason reports why this binary is out of sync with the tool source it
// was built from, or "" if it is current.
func StaleReason(repoRoot, compiledHash string) string {
	return primitives.StaleReason(repoRoot, compiledHash, sourceDirs()...)
}

// CheckStale aborts a tool whose logic has moved since it was compiled.
func CheckStale(repoRoot, compiledHash string) {
	primitives.CheckStale(repoRoot, compiledHash, sourceDirs()...)
}
