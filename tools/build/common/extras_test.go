package common

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	root := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "u@example.com"},
		{"config", "user.name", "u"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return root
}

func TestGateBinaryIsTheFixedEntryPoint(t *testing.T) {
	if got, want := GateBinary("/repo"), filepath.Join("/repo", "bin", "gate"); got != want {
		t.Errorf("GateBinary = %q, want %q", got, want)
	}
}

// checkFormatted reports rather than repairs — the opposite obligation to
// verify's gofmt -w, on the same subject.
func TestCheckFormatted(t *testing.T) {
	if _, err := exec.LookPath("gofmt"); err != nil {
		t.Skip("no gofmt")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "ok.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := checkFormatted(root); err != nil {
		t.Errorf("a formatted tree was reported unformatted: %v", err)
	}

	const ugly = "package a\n\nfunc  F( ) {  }\n"
	if err := os.WriteFile(filepath.Join(root, "ugly.go"), []byte(ugly), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := checkFormatted(root); err == nil {
		t.Error("an unformatted tree passed")
	}
	if after, _ := os.ReadFile(filepath.Join(root, "ugly.go")); string(after) != ugly {
		t.Error("checkFormatted rewrote the file it was checking")
	}
}

// verify records the tree it blessed only after everything else passed, and
// clears it first so a run that dies mid-way leaves nothing blessed.
func TestVerifiedTreeRecordAndClear(t *testing.T) {
	root := gitRepo(t)
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := clearVerifiedTree(root); err != nil {
		t.Errorf("clearing an absent record is not an error: %v", err)
	}
	if err := recordVerifiedTree(root); err != nil {
		t.Fatal(err)
	}
	rec := filepath.Join(root, filepath.FromSlash(verifiedTreeRecord))
	body, err := os.ReadFile(rec)
	if err != nil {
		t.Fatalf("no record was written: %v", err)
	}
	id := strings.TrimSpace(string(body))
	if len(id) < 7 {
		t.Errorf("the record does not look like a tree id: %q", id)
	}

	// The id must be of the content, so changing the tree changes it.
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := recordVerifiedTree(root); err != nil {
		t.Fatal(err)
	}
	body2, _ := os.ReadFile(rec)
	if strings.TrimSpace(string(body2)) == id {
		t.Error("the recorded tree id did not change when the tree did")
	}

	if err := clearVerifiedTree(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(rec); !os.IsNotExist(err) {
		t.Error("the record survived being cleared")
	}
}

// Outside a git checkout there is no commit to gate, so recording is a reported
// no-op rather than a failure.
func TestRecordVerifiedTreeOutsideACheckout(t *testing.T) {
	if err := recordVerifiedTree(t.TempDir()); err != nil {
		t.Errorf("recording outside a checkout failed: %v", err)
	}
}

func TestVerifyStepsForAGoProjectAndAStub(t *testing.T) {
	goRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(goRoot, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := len(verifySteps(goRoot)); got != 4 {
		t.Errorf("a Go project got %d steps, want 4", got)
	}
	if got := len(verifySteps(t.TempDir())); got != 4 {
		t.Errorf("a non-Go project got %d stub steps, want 4", got)
	}
}

func TestRunAllModulesReportsAFailure(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module x\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bad.go"), []byte("package x\n\nfunc F() int { return \"no\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runAllModules(root, "build"); err == nil {
		t.Error("a module that does not build reported success")
	}
}
