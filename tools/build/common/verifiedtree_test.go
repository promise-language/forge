package common

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The contract, tested from the recording end: the id verify records must
// equal the id the workspace guard computes over the real index after
// `git add -A`, because that agreement is the whole of what the two ends
// share. Ported from the workspace repo, where the reading end lives.

// verifyRepoForTest is gitRepoForTest plus an identity and the .gitignore
// every project is required to carry for .workspace/.
func verifyRepoForTest(t *testing.T) string {
	t.Helper()
	dir := gitRepoForTest(t)
	git(t, dir, "config", "user.email", "t@example.com")
	git(t, dir, "config", "user.name", "T")
	writeFile(t, filepath.Join(dir, ".gitignore"), ".workspace/\n")
	return dir
}

func recordedTree(t *testing.T, dir string) string {
	t.Helper()
	return strings.TrimSpace(readFile(t, filepath.Join(dir, ".workspace", "verified-tree")))
}

func TestRecordMatchesRealStage(t *testing.T) {
	dir := verifyRepoForTest(t)
	writeFile(t, filepath.Join(dir, "a.txt"), "a\n")
	writeFile(t, filepath.Join(dir, "sub", "b.txt"), "b\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "base")
	writeFile(t, filepath.Join(dir, "a.txt"), "a2\n")

	if err := recordVerifiedTree(dir); err != nil {
		t.Fatalf("recordVerifiedTree: %v", err)
	}
	git(t, dir, "add", "-A")
	staged, err := RunOutputIn(dir, "git", "write-tree")
	if err != nil {
		t.Fatalf("git write-tree: %v", err)
	}
	if got := recordedTree(t, dir); got != staged {
		t.Errorf("recorded %s, but git add -A stages %s — the two ends disagree", got, staged)
	}
}

func TestRecordIncludesUntracked(t *testing.T) {
	// A step whose whole output is new files must produce a committable match,
	// which is why `git stash create` (tracked modifications only) was rejected.
	dir := verifyRepoForTest(t)
	writeFile(t, filepath.Join(dir, "a.txt"), "a\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "base")
	writeFile(t, filepath.Join(dir, "new.txt"), "new\n")

	if err := recordVerifiedTree(dir); err != nil {
		t.Fatalf("recordVerifiedTree: %v", err)
	}
	names, err := RunOutputIn(dir, "git", "ls-tree", "-r", "--name-only", recordedTree(t, dir))
	if err != nil {
		t.Fatalf("git ls-tree: %v", err)
	}
	if !strings.Contains(names, "new.txt") {
		t.Errorf("untracked non-ignored file missing from recorded tree: %q", names)
	}
}

func TestRecordRespectsIgnoreRules(t *testing.T) {
	// An ignored file stays out; a tracked-but-ignored file stays in — the
	// reason the temp index is seeded rather than left empty.
	dir := verifyRepoForTest(t)
	writeFile(t, filepath.Join(dir, ".gitignore"), ".workspace/\nignored.txt\npinned.txt\n")
	writeFile(t, filepath.Join(dir, "pinned.txt"), "pinned\n")
	git(t, dir, "add", "-A")
	git(t, dir, "add", "-f", "pinned.txt")
	git(t, dir, "commit", "-q", "-m", "base")
	writeFile(t, filepath.Join(dir, "ignored.txt"), "ignored\n")

	if err := recordVerifiedTree(dir); err != nil {
		t.Fatalf("recordVerifiedTree: %v", err)
	}
	names, err := RunOutputIn(dir, "git", "ls-tree", "-r", "--name-only", recordedTree(t, dir))
	if err != nil {
		t.Fatalf("git ls-tree: %v", err)
	}
	if strings.Contains(names, "ignored.txt") {
		t.Errorf("ignored file should not be in the recorded tree: %q", names)
	}
	if !strings.Contains(names, "pinned.txt") {
		t.Errorf("tracked-but-ignored file should be in the recorded tree: %q", names)
	}
}

func TestRecordTrackedSetFollowsIndexNotHEAD(t *testing.T) {
	// The tracked set `git add -A` starts from is the real index's, not
	// HEAD's, and the two differ exactly where ignore rules bite: an ignored
	// file force-added but not yet committed must be in the blessed tree, and
	// one just `git rm --cached`ed must be out. Seeded any other way, the
	// record is a tree no `git add -A` can stage — a permanent guard refusal
	// whose named recovery, re-running verify, reproduces it.
	dir := verifyRepoForTest(t)
	writeFile(t, filepath.Join(dir, ".gitignore"), ".workspace/\nadded.txt\ndropped.txt\n")
	writeFile(t, filepath.Join(dir, "dropped.txt"), "dropped\n")
	git(t, dir, "add", "-A")
	git(t, dir, "add", "-f", "dropped.txt")
	git(t, dir, "commit", "-q", "-m", "base")
	writeFile(t, filepath.Join(dir, "added.txt"), "added\n")
	git(t, dir, "add", "-f", "added.txt")
	git(t, dir, "rm", "-q", "--cached", "dropped.txt")

	if err := recordVerifiedTree(dir); err != nil {
		t.Fatalf("recordVerifiedTree: %v", err)
	}
	git(t, dir, "add", "-A")
	staged, err := RunOutputIn(dir, "git", "write-tree")
	if err != nil {
		t.Fatalf("git write-tree: %v", err)
	}
	if got := recordedTree(t, dir); got != staged {
		t.Errorf("recorded %s, but git add -A stages %s — the two ends disagree", got, staged)
	}
	names, err := RunOutputIn(dir, "git", "ls-tree", "-r", "--name-only", recordedTree(t, dir))
	if err != nil {
		t.Fatalf("git ls-tree: %v", err)
	}
	if !strings.Contains(names, "added.txt") {
		t.Errorf("force-added file missing from recorded tree: %q", names)
	}
	if strings.Contains(names, "dropped.txt") {
		t.Errorf("rm --cached'ed file should not be in the recorded tree: %q", names)
	}
}

func TestRecordLeavesIndexAlone(t *testing.T) {
	dir := verifyRepoForTest(t)
	writeFile(t, filepath.Join(dir, "a.txt"), "a\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "base")
	writeFile(t, filepath.Join(dir, "staged.txt"), "staged\n")
	git(t, dir, "add", "staged.txt")
	writeFile(t, filepath.Join(dir, "unstaged.txt"), "unstaged\n")

	before, err := RunOutputIn(dir, "git", "diff", "--cached", "--name-only")
	if err != nil {
		t.Fatal(err)
	}
	if err := recordVerifiedTree(dir); err != nil {
		t.Fatalf("recordVerifiedTree: %v", err)
	}
	after, err := RunOutputIn(dir, "git", "diff", "--cached", "--name-only")
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Errorf("recording disturbed the real index: before %q, after %q", before, after)
	}
}

func TestClearVerifiedTree(t *testing.T) {
	dir := t.TempDir()
	record := filepath.Join(dir, ".workspace", "verified-tree")
	writeFile(t, record, "abc\n")
	if err := clearVerifiedTree(dir); err != nil {
		t.Fatalf("clearing an existing record: %v", err)
	}
	if Exists(record) {
		t.Error("record should be gone after clear")
	}
	if err := clearVerifiedTree(dir); err != nil {
		t.Fatalf("clearing an absent record should not be an error: %v", err)
	}
}

func TestRecordOutsideGitCheckout(t *testing.T) {
	dir := t.TempDir()
	if err := recordVerifiedTree(dir); err != nil {
		t.Fatalf("outside a checkout recording should be a no-op, not an error: %v", err)
	}
	if Exists(filepath.Join(dir, ".workspace", "verified-tree")) {
		t.Error("no record should be written outside a git checkout")
	}
}

func TestVerifyPipelineEndsWithRecord(t *testing.T) {
	stub := verifyPipeline(t.TempDir())
	if len(stub) == 0 || stub[len(stub)-1].name != "record" {
		t.Errorf("stub pipeline should end with record: %v", stepNames(stub))
	}
	goDir := t.TempDir()
	writeFile(t, filepath.Join(goDir, "go.mod"), "module example.test\n")
	goSteps := verifyPipeline(goDir)
	if len(goSteps) == 0 || goSteps[len(goSteps)-1].name != "record" {
		t.Errorf("go pipeline should end with record: %v", stepNames(goSteps))
	}
}

func TestRecordStepNotReachedAfterFailure(t *testing.T) {
	// The record step rides the existing break-on-first-failure, so a red step
	// blesses nothing.
	reached := false
	steps := []step{
		{"boom", func(string) error { return os.ErrInvalid }},
		{"record", func(string) error { reached = true; return nil }},
	}
	if err := runVerifySteps(t.TempDir(), steps); err == nil {
		t.Fatal("a failing step should fail the run")
	}
	if reached {
		t.Error("record step must not run after an earlier failure")
	}
}

func TestRunVerifyStubClearsAndRecords(t *testing.T) {
	// End to end through RunVerify on the stub pipeline: a stale record is
	// cleared at the start and a fresh tree id is recorded at the end.
	dir := verifyRepoForTest(t)
	writeFile(t, filepath.Join(dir, "a.txt"), "a\n")
	writeFile(t, filepath.Join(dir, ".workspace", "verified-tree"), "stale-garbage\n")
	if err := RunVerify(dir, nil); err != nil {
		t.Fatalf("RunVerify: %v", err)
	}
	got := recordedTree(t, dir)
	if got == "stale-garbage" {
		t.Fatal("stale record survived the run")
	}
	if len(got) < 40 {
		t.Errorf("record should hold one tree id, got %q", got)
	}
}

func TestRunVerifyRedRunLeavesNothingBlessed(t *testing.T) {
	// The clear at the start of RunVerify is only observable on a red run —
	// a green run overwrites the record at the end anyway, so dropping the
	// clear call is invisible to TestRunVerifyStubClearsAndRecords. A stale
	// blessing surviving a failed verify is the one outcome the check must
	// never produce: an unparseable Go file reddens the format step, and the
	// pre-seeded record has to be gone.
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.test\n")
	writeFile(t, filepath.Join(dir, "broken.go"), "package broken\nfunc {\n")
	writeFile(t, filepath.Join(dir, ".workspace", "verified-tree"), "stale-blessing\n")
	if err := RunVerify(dir, nil); err == nil {
		t.Fatal("verify over an unparseable Go file should fail")
	}
	if Exists(filepath.Join(dir, ".workspace", "verified-tree")) {
		t.Error("a red run must leave nothing blessed — the stale record survived")
	}
}

// Upstream carries a TestRecordPathAgreesWithGuard here, pinning the guard's
// spelling of the record path to this module's constant by reading
// precommitguard/verifiedtree.go. forge does not build the guard and does not
// carry its source, so that test cannot be ported; the drift it guards against
// is called out on verifiedTreeRecord itself.

func stepNames(steps []step) []string {
	var names []string
	for _, s := range steps {
		names = append(names, s.name)
	}
	return names
}
