// Package primitives is the one implementation of the helpers every managed
// project's dev tooling is built from. A project imports it at a pinned version
// rather than holding a copy: identical copies are one implementation with as
// many chances to drift as there are projects, and no project has ever exercised
// the ownership the copying bought (docs/primitives.md §1, §3).
//
// What lives here:
//
//   - hash.go      SourceHash / ToolsSourceHash — the FNV-128a digest over a
//     repository's tool source that the staleness check rests on.
//   - stale.go     StaleReason, MakeCmd, CheckStale — a binary refuses to run
//     when the source it was built from has moved.
//   - platform.go  IsWindows, ExeSuffix, BinaryName, Which, Exists.
//   - exec.go      RunIn, RunOutputIn, OutputBytesIn, RunSilent — subprocess
//     helpers with attached or captured streams.
//   - args.go      NormalizeArgs — accept both -foo and --foo.
//   - help.go      HasHelpFlag, MaybeHelp — usage before anything else, so help
//     answers however stale the binary is.
//   - setup.go     RunSetup — the git-hook wiring.
//
// The membership test is whether two projects could disagree about a thing and
// both be right. A hash function has one correct answer; so does a staleness
// check, and so does whether the executable suffix is ".exe" (§2). What fails
// that test stays in each project's own tools/build/common: the verify pipeline,
// the gate set, the judging terms, anything naming one project.
//
// # No dependency, deliberately
//
// This package imports nothing outside the standard library, and that is a
// constraint rather than an accident. Every project's tools module depends on
// primitives, so anything primitives depends on is something every island tools
// module drags in — including forge's own tools module, which would then build
// against a published version of the repository it is sitting inside.
//
// # Names do not change when a helper moves here
//
// The copies were byte-identical, so their exported names are already agreed
// fleet-wide. Adopting this library is therefore a deletion and an import: the
// file goes, the import arrives, and the package qualifier changes. Where a
// signature must grow — the hash gaining the replaced directories of §4 — it
// grows variadically, so the call every project already writes keeps its
// meaning (§5).
package primitives
