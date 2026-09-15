package common

import (
	"os"
	"path/filepath"
	"testing"
)

func cmdDirs(t *testing.T, repoRoot string, names ...string) {
	t.Helper()
	for _, n := range names {
		if err := os.MkdirAll(filepath.Join(repoRoot, "tools", "build", "cmd", n), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// Commands are discovered, not enumerated: a directory under cmd/ is a command,
// and `make` is not one because it runs via `go run` and is never built.
func TestCommandNamesIsSortedAndExcludesMake(t *testing.T) {
	repo := t.TempDir()
	cmdDirs(t, repo, "verify", "gate", "make", "run")
	if err := os.WriteFile(filepath.Join(repo, "tools", "build", "cmd", "notes.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := CommandNames(repo)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"gate", "run", "verify"}
	if len(got) != len(want) {
		t.Fatalf("CommandNames = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("CommandNames = %v, want %v", got, want)
		}
	}
}

func TestCommandNamesReportsAMissingDirectory(t *testing.T) {
	if _, err := CommandNames(t.TempDir()); err == nil {
		t.Error("a missing cmd directory was accepted")
	}
}
