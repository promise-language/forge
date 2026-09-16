package primitives

// VerifiedTreeRecord is where a project's verify records the tree it blessed:
// one git tree object id, newline terminated, in the gitignored per-checkout
// .workspace/ directory.
//
// It is one constant rather than one spelling per end because the contract has
// two ends in two repositories. A project's bin/verify writes the record; the
// workspace-delivered bin/precommit-guard refuses a commit whose staged tree
// differs from it. Neither module can import the other, so the path used to be
// typed out at every end and agreed only by coincidence — which is the case
// docs/primitives.md, What belongs here names: "A contract spelled at two ends
// is one constant, held here, imported by both… Prose at each end is not an
// agreement; it is two statements that happen to match today."
//
// Drift is a permanent, silent refusal rather than a visible failure: verify
// writes one path, the guard reads another and always finds it absent, and the
// guard's named recovery — run bin/verify — cannot clear a check that verify
// does not participate in.
//
// The value is a slash-separated path relative to a repository root, which is
// how both ends carry it in prose and how a .gitignore names it. A caller joins
// it onto the root and converts it for the host:
// filepath.Join(root, filepath.FromSlash(VerifiedTreeRecord)).
const VerifiedTreeRecord = ".workspace/verified-tree"
