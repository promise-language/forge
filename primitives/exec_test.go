package primitives

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The `dir` argument is the whole point of the *In helpers, so it is what the
// test pins: `go env GOMOD` answers about the directory it ran in.
func TestRunOutputInRunsInTheDirectoryItWasGiven(t *testing.T) {
	dir := goModule(t)
	got, err := RunOutputIn(dir, "go", "env", "GOMOD")
	if err != nil {
		t.Fatalf("RunOutputIn: %v", err)
	}
	if !strings.HasSuffix(got, "go.mod") {
		t.Errorf("go env GOMOD = %q — the command did not run in the module directory", got)
	}
	if _, err := RunOutputIn(dir, "definitely-not-a-real-command-xyz"); err == nil {
		t.Error("RunOutputIn reported success for a missing command")
	}
}

func TestRunInReportsWhatTheCommandDid(t *testing.T) {
	dir := t.TempDir()
	if err := RunIn(dir, "go", "version"); err != nil {
		t.Errorf("RunIn(go version): %v", err)
	}
	if err := RunIn(dir, "definitely-not-a-real-command-xyz"); err == nil {
		t.Error("RunIn reported success for a missing command")
	}
}

// The difference between the two capture helpers IS the trimming, so that is
// what the test asserts: a caller reading a blob gets the bytes as written,
// which is why `git cat-file` uses this one.
func TestOutputBytesInDoesNotTrim(t *testing.T) {
	dir := t.TempDir()

	raw, err := OutputBytesIn(dir, "go", "env", "GOOS")
	if err != nil {
		t.Fatalf("OutputBytesIn: %v", err)
	}
	trimmed, err := RunOutputIn(dir, "go", "env", "GOOS")
	if err != nil {
		t.Fatalf("RunOutputIn: %v", err)
	}
	if string(raw) == trimmed {
		t.Errorf("OutputBytesIn returned %q, the same as the trimming helper — it trimmed", raw)
	}
	if strings.TrimSpace(string(raw)) != trimmed {
		t.Errorf("OutputBytesIn = %q, which is not %q plus surrounding bytes", raw, trimmed)
	}
	if _, err := OutputBytesIn(dir, "definitely-not-a-real-command-xyz"); err == nil {
		t.Error("OutputBytesIn reported success for a missing command")
	}
}

func TestRunSilentDiscardsOutput(t *testing.T) {
	if err := RunSilent("go", "version"); err != nil {
		t.Errorf("RunSilent(go version): %v", err)
	}
	if err := RunSilent("definitely-not-a-real-command-xyz"); err == nil {
		t.Error("RunSilent reported success for a missing command")
	}
}

// goModule is a directory the go command recognises as a module, so a helper
// that ignored its dir argument answers visibly differently.
func goModule(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}
