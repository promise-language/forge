package primitives

import (
	"os/exec"
	"testing"
)

func TestRunSetupWiresTheInRepoHooks(t *testing.T) {
	root := gitRepo(t)
	if err := RunSetup(root); err != nil {
		t.Fatal(err)
	}
	got, err := RunOutputIn(root, "git", "config", "core.hooksPath")
	if err != nil {
		t.Fatal(err)
	}
	if got != ".githooks" {
		t.Errorf("core.hooksPath = %q, want .githooks", got)
	}
	// Idempotent: the meta-builder calls it on every run.
	if err := RunSetup(root); err != nil {
		t.Errorf("a second run failed: %v", err)
	}
}

// Outside a checkout there is no config to write, and the caller is told rather
// than left believing the hooks are wired.
func TestRunSetupOutsideACheckout(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	if err := RunSetup(t.TempDir()); err == nil {
		t.Error("wiring hooks outside a git checkout reported success")
	}
}

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
