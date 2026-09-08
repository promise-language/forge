package primitives

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// StaleReason's four answers. They are not interchangeable: a caller deciding
// whether to rebuild needs "the source moved" (rebuilding fixes it) apart from
// "not built via ./make" and "the repo is gone" (rebuilding cannot).
func TestStaleReason(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "tools", "build"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "tools", "build", "x.go"), []byte("package build\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	current, err := ToolsSourceHash(repo)
	if err != nil {
		t.Fatalf("ToolsSourceHash: %v", err)
	}

	if got := StaleReason(repo, current); got != "" {
		t.Errorf("matching hash reported stale: %q", got)
	}
	if got := StaleReason(repo, "deadbeef"); !strings.Contains(got, "tools source has changed") {
		t.Errorf("changed source: %q, want it to name the change", got)
	}
	if got := StaleReason("", ""); !strings.Contains(got, "not built via") {
		t.Errorf("unstamped binary: %q, want it to say so", got)
	}
	if got := StaleReason(filepath.Join(repo, "gone"), "abc"); !strings.Contains(got, "unreachable") {
		t.Errorf("missing repo: %q, want it to report unreachability", got)
	}
}

// A caller that names the replaced tree is told about an edit to it. This is
// the staleness half of §4: the hash covering the tree is worth nothing unless
// the check asks for the same set.
func TestStaleReasonSeesEveryDirectoryNamed(t *testing.T) {
	repo := hashFixture(t)
	dirs := []string{ToolsBuildDir, "primitives"}

	current, err := SourceHash(repo, dirs...)
	if err != nil {
		t.Fatal(err)
	}
	narrow, err := SourceHash(repo)
	if err != nil {
		t.Fatal(err)
	}
	if got := StaleReason(repo, current, dirs...); got != "" {
		t.Fatalf("an untouched tree reported stale: %q", got)
	}

	write(t, repo, "primitives/y.go", "package primitives\n\nfunc F() {}\n")

	if got := StaleReason(repo, current, dirs...); !strings.Contains(got, "tools source has changed") {
		t.Errorf("an edit under the replaced tree reported %q, want the source-changed answer", got)
	}
	// And the same edit is invisible to a binary that named only tools/build —
	// which is why a project whose dependency is replaced must name the tree.
	if got := StaleReason(repo, narrow); got != "" {
		t.Errorf("the one-directory check reported %q — it cannot see the other tree, and must not claim to", got)
	}
}

// Every recovery path in this workspace ends at ./make, and it has to be spelled
// the way the host can actually run it.
func TestMakeCmdMatchesTheHost(t *testing.T) {
	got := MakeCmd()
	if IsWindows() {
		if got != ".\\make.cmd" {
			t.Errorf("MakeCmd = %q on windows", got)
		}
		return
	}
	if got != "./make" {
		t.Errorf("MakeCmd = %q, want ./make", got)
	}
}
