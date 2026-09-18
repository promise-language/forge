package tooling

// The verified-tree record: the writing end of workspace's tool-contract.md
// precommit-guard check (docs/project-tools.md, Verify).
//
// bin/verify records the tree it blessed at primitives.VerifiedTreeRecord, and
// the workspace-delivered precommit-guard refuses a commit whose staged tree
// differs. The reading end is not in this repository — the guard is a workspace
// tool, built and owned there — so what the two ends share is the record's
// location and format, not code: one git tree object id, newline terminated, at
// that path. The path is imported rather than typed here, because a path typed
// at each end agrees only by coincidence and spelling it wrong is a permanent,
// silent refusal.

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/promise-language/forge/primitives"
)

// recordPath is where the record lives in the checkout at root.
func recordPath(root string) string {
	return filepath.Join(root, filepath.FromSlash(primitives.VerifiedTreeRecord))
}

// ClearVerifiedTree removes the record. Verify calls it before its first stage,
// so a run that dies mid-way leaves nothing blessed and an in-flight verify
// blesses nothing. An absent record is not an error.
func ClearVerifiedTree(root string) error {
	err := os.Remove(recordPath(root))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// RecordVerifiedTree writes the tree id of the content verify just blessed. It
// runs only after every other stage has passed, so a red run blesses nothing.
//
// The tree is computed over a temporary index so the real index is untouched,
// and it is exactly what `git add -A` would stage: seeded from a copy of the
// real one, because that is the tracked set the real `git add -A` starts from.
// Ignore rules apply only to untracked paths, so any other seed gets the
// ignored-and-tracking-state-differs cases wrong — an empty seed drops a
// tracked-but-ignored file, and a HEAD seed both drops one force-added but not
// yet committed and keeps one just `git rm --cached`ed, recording a tree no
// `git add -A` can stage and a mismatch re-running verify cannot repair.
//
// Outside a git checkout, recording is a reported no-op rather than a verify
// failure: there is no commit to gate there, and the guard still refuses on the
// absent record.
func RecordVerifiedTree(r *Run) (string, error) {
	if _, err := r.gitOutput("rev-parse", "--git-dir"); err != nil {
		fmt.Fprintln(r.Narrate, "    not a git checkout — no verified-tree record to write")
		return "", nil
	}
	if !r.Ignored(filepath.Dir(primitives.VerifiedTreeRecord) + "/") {
		return "", fmt.Errorf("this repository does not ignore %s, and verify writes the record it blesses there",
			filepath.Dir(primitives.VerifiedTreeRecord)+"/")
	}

	// The temporary index is scratch, so it goes under .home/tmp/ with
	// everything else this run writes.
	index := r.Scratch("verified-tree-index")

	// Seed from a copy of the real index; a repo before its first add has no
	// index file yet, and an empty seed is exactly its tracked set.
	realIndex, err := r.gitTrimmed("rev-parse", "--git-path", "index")
	if err != nil {
		return "", fmt.Errorf("locating the index: %w", err)
	}
	if !filepath.IsAbs(realIndex) {
		realIndex = filepath.Join(r.Root, realIndex)
	}
	if data, err := os.ReadFile(realIndex); err == nil {
		if err := os.WriteFile(index, data, 0o600); err != nil {
			return "", fmt.Errorf("seeding temp index: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("seeding temp index: %w", err)
	}

	if _, err := r.gitWithIndex(index, "add", "-A"); err != nil {
		return "", fmt.Errorf("staging into temp index: %w", err)
	}
	tree, err := r.gitWithIndex(index, "write-tree")
	if err != nil {
		return "", fmt.Errorf("computing verified tree: %w", err)
	}
	if err := writeAtomically(recordPath(r.Root), []byte(tree+"\n")); err != nil {
		return "", fmt.Errorf("writing %s: %w", primitives.VerifiedTreeRecord, err)
	}
	return tree, nil
}
