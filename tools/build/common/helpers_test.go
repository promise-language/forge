package common

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFile writes content to path, creating parent directories as needed (a
// no-op when they already exist), so a test can lay out a tree in one call.
//
// Ported alongside gate_test.go from the workspace repo, where it lives in
// provision_test.go — a file this repository has no use for.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// readFile returns path's content, failing the test if it cannot be read.
func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// gitRepoForTest returns a fresh git repository in a temp dir.
func gitRepoForTest(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := RunIn(dir, "git", "init", "-q"); err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	return dir
}

// git runs a git command in dir and fails the test if it does not succeed.
func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	if out, err := RunOutputIn(dir, "git", args...); err != nil {
		t.Fatalf("git %s in %s: %v (%s)", strings.Join(args, " "), dir, err, out)
	}
}
