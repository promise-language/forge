// Package primitives provides the small, low-churn helpers that the Forge
// blueprint's tools rely on. It is *optional*: adopters can import this
// package as a Go dependency, or they can copy the source into their own
// repo's common/ package. Both paths are supported.
//
// What lives here (planned):
//
//   - hash.go      FNV-128a hash over a directory tree (used for the
//                  staleness check that compares baked-in vs. current
//                  tools/build source).
//   - stale.go     CheckStale(repoRoot, compiledHash) — refuses to run if
//                  either input is empty, or if the baked-in repoRoot is
//                  unreachable, or if the source hash has drifted.
//   - platform.go  IsWindows(), ExeSuffix(), BinaryName(), Which().
//   - exec.go      RunIn, RunOutputIn, RunSilent — subprocess helpers
//                  with attached or captured streams.
//   - args.go      NormalizeArgs — accept both -foo and --foo.
//   - interrupt.go Interrupted() and the SIGINT handler that backs it.
//   - lock.go      File-locked critical section (wraps gofrs/flock).
//
// What does NOT live here:
//
//   - Pipeline orchestration (RunBuild, RunVerify, RunTest, ...). Those are
//     project-specific by nature — every project's verify pipeline runs a
//     different formatter, builder, test runner. They belong in each
//     adopter's own tools/build/common/.
//   - Anything project-specific (a project's WASM test pass, its toolchain
//     detection, …).
//
// The canonical implementations of these helpers are the ones cmd/init
// scaffolds into a target repo's tools/build/common/; this package factors out
// the low-churn subset for adopters who would rather import than copy.
package primitives
