package common

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/promise-language/forge/primitives"
)

// The contract, tested from the recording end: the id verify records must
// equal the id the guard computes over the real index after `git add -A`,
// because that agreement is the whole of what the two ends share. The guard
// is a workspace tool and is not in this repository, so the comparison is
// spelled out here as `git write-tree` — exactly what the guard runs.

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// ignoreRecordDir is the .gitignore line that keeps the record out of the tree
// verify blesses. It is derived from the one constant rather than typed, so a
// fixture cannot come to name a directory the code under test does not write
// to — which would make every ignore-rule test assert about the wrong path and
// still pass.
func ignoreRecordDir() string {
	return path.Dir(primitives.VerifiedTreeRecord) + "/\n"
}

// verifyRepoForTest is a fresh checkout with an identity and the .gitignore
// every project is required to carry for .workspace/ (tool-contract's Layout).
func verifyRepoForTest(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	git(t, dir, "config", "user.email", "t@example.com")
	git(t, dir, "config", "user.name", "T")
	writeFile(t, filepath.Join(dir, ".gitignore"), ignoreRecordDir())
	return dir
}

func recordedTree(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(recordPath(dir))
	if err != nil {
		t.Fatalf("read the record: %v", err)
	}
	return strings.TrimSpace(string(data))
}

func TestRecordMatchesRealStage(t *testing.T) {
	dir := verifyRepoForTest(t)
	writeFile(t, filepath.Join(dir, "a.txt"), "a\n")
	writeFile(t, filepath.Join(dir, "sub", "b.txt"), "b\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "base")
	writeFile(t, filepath.Join(dir, "a.txt"), "a2\n")

	if _, err := recordVerifiedTree(dir, io.Discard); err != nil {
		t.Fatalf("recordVerifiedTree: %v", err)
	}
	git(t, dir, "add", "-A")
	staged := git(t, dir, "write-tree")
	if got := recordedTree(t, dir); got != staged {
		t.Errorf("recorded %s, but git add -A stages %s — the two ends disagree", got, staged)
	}
}

func TestRecordIncludesUntracked(t *testing.T) {
	// A step whose whole output is new files must produce a committable match,
	// which is why `git stash create` (tracked modifications only) is not it.
	dir := verifyRepoForTest(t)
	writeFile(t, filepath.Join(dir, "a.txt"), "a\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "base")
	writeFile(t, filepath.Join(dir, "new.txt"), "new\n")

	if _, err := recordVerifiedTree(dir, io.Discard); err != nil {
		t.Fatalf("recordVerifiedTree: %v", err)
	}
	names := git(t, dir, "ls-tree", "-r", "--name-only", recordedTree(t, dir))
	if !strings.Contains(names, "new.txt") {
		t.Errorf("untracked non-ignored file missing from recorded tree: %q", names)
	}
}

func TestRecordRespectsIgnoreRules(t *testing.T) {
	// An ignored file stays out; a tracked-but-ignored file stays in — the
	// reason the temp index is seeded rather than left empty.
	dir := verifyRepoForTest(t)
	writeFile(t, filepath.Join(dir, ".gitignore"), ignoreRecordDir()+"ignored.txt\npinned.txt\n")
	writeFile(t, filepath.Join(dir, "pinned.txt"), "pinned\n")
	git(t, dir, "add", "-A")
	git(t, dir, "add", "-f", "pinned.txt")
	git(t, dir, "commit", "-q", "-m", "base")
	writeFile(t, filepath.Join(dir, "ignored.txt"), "ignored\n")

	if _, err := recordVerifiedTree(dir, io.Discard); err != nil {
		t.Fatalf("recordVerifiedTree: %v", err)
	}
	names := git(t, dir, "ls-tree", "-r", "--name-only", recordedTree(t, dir))
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
	writeFile(t, filepath.Join(dir, ".gitignore"), ignoreRecordDir()+"added.txt\ndropped.txt\n")
	writeFile(t, filepath.Join(dir, "dropped.txt"), "dropped\n")
	git(t, dir, "add", "-A")
	git(t, dir, "add", "-f", "dropped.txt")
	git(t, dir, "commit", "-q", "-m", "base")
	writeFile(t, filepath.Join(dir, "added.txt"), "added\n")
	git(t, dir, "add", "-f", "added.txt")
	git(t, dir, "rm", "-q", "--cached", "dropped.txt")

	if _, err := recordVerifiedTree(dir, io.Discard); err != nil {
		t.Fatalf("recordVerifiedTree: %v", err)
	}
	git(t, dir, "add", "-A")
	staged := git(t, dir, "write-tree")
	if got := recordedTree(t, dir); got != staged {
		t.Errorf("recorded %s, but git add -A stages %s — the two ends disagree", got, staged)
	}
	names := git(t, dir, "ls-tree", "-r", "--name-only", recordedTree(t, dir))
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

	before := git(t, dir, "diff", "--cached", "--name-only")
	if _, err := recordVerifiedTree(dir, io.Discard); err != nil {
		t.Fatalf("recordVerifiedTree: %v", err)
	}
	after := git(t, dir, "diff", "--cached", "--name-only")
	if before != after {
		t.Errorf("recording disturbed the real index: before %q, after %q", before, after)
	}
}

// THE FORMAT IS HALF THE CONTRACT. The guard is a workspace tool that cannot be
// imported here, so nothing but agreement on the bytes connects the two ends:
// one tree id, newline terminated, and nothing else in the file. Every other
// test in this file reads the record through a trim, so a record written
// without its newline — or with a second line of commentary — passes all of
// them and is refused by a reader this repository cannot run.
func TestRecordIsOneTreeIdNewlineTerminated(t *testing.T) {
	dir := verifyRepoForTest(t)
	writeFile(t, filepath.Join(dir, "a.txt"), "a\n")

	if _, err := recordVerifiedTree(dir, io.Discard); err != nil {
		t.Fatalf("recordVerifiedTree: %v", err)
	}
	raw, err := os.ReadFile(recordPath(dir))
	if err != nil {
		t.Fatalf("read the record: %v", err)
	}
	body := string(raw)
	if !strings.HasSuffix(body, "\n") {
		t.Errorf("record = %q, want it newline terminated", body)
	}
	id := strings.TrimSuffix(body, "\n")
	if strings.ContainsAny(id, "\n ") || id == "" {
		t.Errorf("record = %q, want exactly one bare tree id and nothing else", body)
	}
	// And it must name a tree git can resolve, not merely look like an id.
	if got := git(t, dir, "cat-file", "-t", id); got != "tree" {
		t.Errorf("recorded id is a %q, want a tree", got)
	}
}

// THE LOCATION IS THE OTHER HALF. Every other test here asks recordPath where
// the record is, which is the same question the code under test answered: a
// recordPath that stopped joining the constant would move the record and take
// all of those assertions with it, green. This one joins the constant itself,
// so the writing end is measured against the agreement rather than against its
// own arithmetic.
func TestRecordLandsOnTheContractPath(t *testing.T) {
	dir := verifyRepoForTest(t)
	writeFile(t, filepath.Join(dir, "a.txt"), "a\n")

	if _, err := recordVerifiedTree(dir, io.Discard); err != nil {
		t.Fatalf("recordVerifiedTree: %v", err)
	}
	if !primitives.Exists(filepath.Join(dir, filepath.FromSlash(primitives.VerifiedTreeRecord))) {
		t.Errorf("nothing at %s — the writing end and the path the guard reads have parted",
			primitives.VerifiedTreeRecord)
	}
}

// A RECORD THAT CANNOT BE WRITTEN FAILS THE RUN, the other way round from the
// clear below. Every other test here records successfully, so nothing measures
// what happens when the write does not land — and a nil returned from here is
// the one failure that presents as success: record is the last step, so the run
// prints OK to Commit having blessed nothing, and the guard then refuses every
// commit in the checkout while naming a recovery, re-running verify, that
// reproduces it exactly.
func TestRecordReportsAWriteItCannotMake(t *testing.T) {
	dir := verifyRepoForTest(t)
	writeFile(t, filepath.Join(dir, "a.txt"), "a\n")
	// A non-empty directory where the record belongs: the rename onto it fails
	// on every host, which the permission bits this test could set instead do
	// not (the suite may run as a user nothing refuses).
	writeFile(t, filepath.Join(recordPath(dir), "occupied"), "x\n")

	if _, err := recordVerifiedTree(dir, io.Discard); err == nil {
		t.Fatal("recordVerifiedTree reported success although the record could not be written")
	}
	// The half-written record is taken back with it. It is the one file the
	// failure path is responsible for, and it lands in the directory a project
	// ignores, so a leak here is invisible until it has happened many times.
	entries, err := os.ReadDir(filepath.Dir(recordPath(dir)))
	if err != nil {
		t.Fatalf("read the record's directory: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".verified-tree-") {
			t.Errorf("a failed record left %s behind", e.Name())
		}
	}
	// And it disturbed nothing it could not replace.
	if !primitives.Exists(filepath.Join(recordPath(dir), "occupied")) {
		t.Error("the record step removed what stood where the record belongs")
	}
}

func TestClearVerifiedTree(t *testing.T) {
	dir := t.TempDir()
	record := recordPath(dir)
	writeFile(t, record, "abc\n")
	if err := clearVerifiedTree(dir); err != nil {
		t.Fatalf("clearing an existing record: %v", err)
	}
	if primitives.Exists(record) {
		t.Error("record should be gone after clear")
	}
	if err := clearVerifiedTree(dir); err != nil {
		t.Fatalf("clearing an absent record should not be an error: %v", err)
	}
}

func TestRecordOutsideGitCheckout(t *testing.T) {
	dir := t.TempDir()
	if _, err := recordVerifiedTree(dir, io.Discard); err != nil {
		t.Fatalf("outside a checkout recording should be a no-op, not an error: %v", err)
	}
	if primitives.Exists(recordPath(dir)) {
		t.Error("no record should be written outside a git checkout")
	}
}

// The path being RIGHT is not the requirement; being held in ONE PLACE is.
// A recordPath that built the same path out of its own literals would pass
// every test above, and go on passing until the day the value moves and one end
// does not follow — which is the coincidence the constant exists to end
// (docs/primitives.md, What belongs here: "Prose at each end is not an
// agreement; it is two statements that happen to match today"). Nothing else
// here notices a copy while the copy is still correct.
//
// Both halves are needed. Without the reference, a file that types the path
// again in pieces is a second statement; without the literal scan, one that
// imports the constant and then ignores it passes on the import alone.
func TestWritingEndTakesThePathFromTheConstant(t *testing.T) {
	const src = "verifiedtree.go"
	typed, usesConstant, err := recordPathLiterals(src)
	if err != nil {
		t.Fatal(err)
	}
	if !usesConstant {
		t.Errorf("%s never names primitives.VerifiedTreeRecord — whatever path it writes to, it is not the one the guard was told about", src)
	}
	for _, lit := range typed {
		t.Errorf("%s types %s out; the writing end takes the path from primitives.VerifiedTreeRecord, and a second spelling agrees with the reading end only by coincidence", src, lit)
	}
}

// The scan itself, over a file that breaks the rule: without this the test above
// passes whether or not it can see a copy at all — including if it stopped
// parsing the file, which reads as coverage of a rule nothing was checked
// against. Both shapes a copy takes are here: the whole path, and the segments
// a filepath.Join spreads it over.
func TestRecordPathLiteralsSeesACopy(t *testing.T) {
	src := filepath.Join(t.TempDir(), "copy.go")
	writeFile(t, src, `package common

import "path/filepath"

func whole(r string) string  { return filepath.Join(r, ".workspace/verified-tree") }
func pieces(r string) string { return filepath.Join(r, ".workspace", "verified-tree") }
func near(r string) string   { return filepath.Join(r, ".verified-tree-*", "verified-tree-") }
`)
	typed, usesConstant, err := recordPathLiterals(src)
	if err != nil {
		t.Fatal(err)
	}
	if usesConstant {
		t.Error("a file that names the constant nowhere was reported as using it")
	}
	if len(typed) != 3 {
		t.Errorf("found %v, want the whole path and both of its segments", typed)
	}
	// The temp-file names next door to the record are not spellings of it.
	for _, lit := range typed {
		if strings.Contains(lit, "*") || strings.HasSuffix(lit, `-"`) {
			t.Errorf("%s is a temp-file pattern, not a copy of the path", lit)
		}
	}
}

// recordPathLiterals reports every string literal in the Go file at src that
// spells the record path or one of its segments, and whether the file names the
// constant at all. It reads the syntax rather than the text so that a comment
// naming the path — prose, which every end is free to carry — is not mistaken
// for a second statement of it.
func recordPathLiterals(src string) (typed []string, usesConstant bool, err error) {
	file, err := parser.ParseFile(token.NewFileSet(), src, nil, 0)
	if err != nil {
		return nil, false, err
	}
	spellings := map[string]bool{primitives.VerifiedTreeRecord: true}
	for _, seg := range strings.Split(primitives.VerifiedTreeRecord, "/") {
		spellings[seg] = true
	}
	ast.Inspect(file, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.BasicLit:
			if n.Kind != token.STRING {
				return true
			}
			if v, err := strconv.Unquote(n.Value); err == nil && spellings[v] {
				typed = append(typed, n.Value)
			}
		case *ast.SelectorExpr:
			if pkg, ok := n.X.(*ast.Ident); ok && pkg.Name == "primitives" && n.Sel.Name == "VerifiedTreeRecord" {
				usesConstant = true
			}
		}
		return true
	})
	return typed, usesConstant, nil
}

func TestVerifyPipelineEndsWithRecord(t *testing.T) {
	stub := verifyPipeline(t.TempDir(), new(string))
	if len(stub) == 0 || stub[len(stub)-1].name != "record" {
		t.Errorf("stub pipeline should end with record: %v", stepNames(stub))
	}
	goDir := t.TempDir()
	writeFile(t, filepath.Join(goDir, "go.mod"), "module example.test\n")
	goSteps := verifyPipeline(goDir, new(string))
	if len(goSteps) == 0 || goSteps[len(goSteps)-1].name != "record" {
		t.Errorf("go pipeline should end with record: %v", stepNames(goSteps))
	}
}

func TestRecordStepNotReachedAfterFailure(t *testing.T) {
	// The record step rides the existing break-on-first-failure, so a red step
	// blesses nothing.
	reached := false
	steps := []step{
		{"boom", func(string, io.Writer) error { return os.ErrInvalid }},
		{"record", func(string, io.Writer) error { reached = true; return nil }},
	}
	result := runVerifySteps(t.TempDir(), steps, io.Discard)
	if result.OK || result.ExitStatus() != 1 {
		t.Fatal("a failing step should fail the run")
	}
	if reached {
		t.Error("record step must not run after an earlier failure")
	}
	// And the step that never ran says so, rather than being left out of the
	// account of what happened.
	last := result.Stages[len(result.Stages)-1]
	if last.Name != "record" || last.Steps[0].Status != statusNotRun {
		t.Errorf("the record step is reported as %+v, want it reported as not run", last)
	}
}

func TestRunVerifyStubClearsAndRecords(t *testing.T) {
	// End to end through RunVerify on the stub pipeline: a stale record is
	// cleared at the start and a fresh tree id is recorded at the end.
	dir := verifyRepoForTest(t)
	writeFile(t, filepath.Join(dir, "a.txt"), "a\n")
	writeFile(t, recordPath(dir), "stale-garbage\n")
	result, err := RunVerify(dir, io.Discard)
	if err != nil {
		t.Fatalf("RunVerify: %v", err)
	}
	if !result.OK {
		t.Fatalf("the stub pipeline failed: %+v", result)
	}
	got := recordedTree(t, dir)
	// The tree it blessed travels in the result too, so a caller reading the
	// JSON learns which tree without going to the file.
	if result.Tree != got {
		t.Errorf("the result says tree %q and the record says %q", result.Tree, got)
	}
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
	// never produce: a Go-shaped tree with nothing in it cannot pass the
	// agent-turn ratchet, and an unparseable file cannot pass format, so the
	// run goes red before record — and the pre-seeded blessing has to be gone.
	dir := verifyRepoForTest(t)
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.test\n")
	writeFile(t, filepath.Join(dir, "broken.go"), "package broken\nfunc {\n")
	writeFile(t, recordPath(dir), "stale-blessing\n")
	result, err := RunVerify(dir, io.Discard)
	if err != nil {
		t.Fatalf("RunVerify: %v", err)
	}
	if result.OK {
		t.Fatal("verify over an unparseable Go file should fail")
	}
	if result.Tree != "" {
		t.Errorf("a red run reported tree %q, and it blessed nothing", result.Tree)
	}
	if primitives.Exists(recordPath(dir)) {
		t.Error("a red run must leave nothing blessed — the stale record survived")
	}
}

// A CLEAR THAT FAILS FAILS THE RUN. The clear exists so a run that dies part
// way leaves nothing blessed; if it can fail and be ignored, the case it was
// added for is exactly the case it does not cover — the record it could not
// remove survives the whole run, and a red verify hands the guard a stale
// blessing for a tree nobody checked. Nothing may run past it.
func TestRunVerifyFailsWhenTheStaleRecordCannotBeCleared(t *testing.T) {
	dir := verifyRepoForTest(t)
	writeFile(t, filepath.Join(dir, "a.txt"), "a\n")
	// A non-empty directory where the record belongs: os.Remove refuses it, the
	// one clear failure that is neither "absent" nor a permission quirk of the
	// machine the tests run on.
	writeFile(t, filepath.Join(recordPath(dir), "occupied"), "x\n")

	_, err := RunVerify(dir, io.Discard)
	if err == nil {
		t.Fatal("RunVerify passed although the stale record could not be cleared")
	}
	if !strings.Contains(err.Error(), primitives.VerifiedTreeRecord) {
		t.Errorf("err = %v, want it to name %s", err, primitives.VerifiedTreeRecord)
	}
	// And it stopped there rather than running the pipeline over it.
	if !primitives.Exists(filepath.Join(recordPath(dir), "occupied")) {
		t.Error("the run went on and disturbed what it could not clear")
	}
}

func stepNames(steps []step) []string {
	var names []string
	for _, s := range steps {
		names = append(names, s.name)
	}
	return names
}
