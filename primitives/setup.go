package primitives

// HooksPath is where git is told to find this repository's hooks. It is one
// constant because setup writes it and setup's own result reports it, and prose
// at each end is not an agreement (docs/primitives.md, What belongs here).
const HooksPath = ".githooks"

// RunSetup wires git to use the in-repo .githooks directory. Idempotent and
// fast, so the meta-builder calls it on every run; a fresh clone gets its
// pre-commit hook on the first ./make.
func RunSetup(repoRoot string) error {
	return RunIn(repoRoot, "git", "config", "core.hooksPath", HooksPath)
}
