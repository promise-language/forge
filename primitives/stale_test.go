package primitives

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/promise-language/forge/primitives/command"
)

// StaleRefusal's four answers. They are not interchangeable, and a caller reads
// the condition rather than the prose: "the source moved" is cleared by
// rebuilding, where "not built by ./make" and "the repository is gone" are two
// other repairs entirely.
func TestStaleRefusalNamesTheCondition(t *testing.T) {
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

	if got := StaleRefusal("gate", repo, current); got != nil {
		t.Errorf("a matching hash refused: %+v", got)
	}
	for _, c := range []struct {
		name       string
		repoRoot   string
		hash       string
		want       string
		wantDetail string
	}{
		{"changed source", repo, "deadbeef", command.Stale, "tools source has changed"},
		{"no stamp", "", "", command.Unstamped, "no stamp"},
		{"repository gone", filepath.Join(repo, "gone"), "abc", command.RepositoryUnreachable, "unreachable"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := StaleRefusal("gate", c.repoRoot, c.hash)
			if got == nil {
				t.Fatal("no refusal, want one")
			}
			if got.Refusal != c.want {
				t.Errorf("refusal %q, want %q", got.Refusal, c.want)
			}
			if got.Tool != "gate" {
				t.Errorf("tool %q, want the tool that refused", got.Tool)
			}
			if !strings.Contains(got.Detail, c.wantDetail) {
				t.Errorf("detail %q, want it to say %q", got.Detail, c.wantDetail)
			}
			if len(got.Recovery) == 0 || got.Recovery[0] != MakeCmd() {
				t.Errorf("recovery %v, want the builder every refusal names", got.Recovery)
			}
		})
	}
}

// A caller that names the replaced tree is told about an edit to it. This is
// the staleness half of The staleness contract holds with nothing added: the
// hash covering the tree is worth nothing unless the check asks for the same
// set.
func TestStaleRefusalSeesEveryDirectoryNamed(t *testing.T) {
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
	if got := StaleRefusal("verify", repo, current, dirs...); got != nil {
		t.Fatalf("an untouched tree refused: %+v", got)
	}

	write(t, repo, "primitives/y.go", "package primitives\n\nfunc F() {}\n")

	got := StaleRefusal("verify", repo, current, dirs...)
	if got == nil || got.Refusal != command.Stale {
		t.Errorf("an edit under the replaced tree gave %+v, want the stale refusal", got)
	}
	// And the same edit is invisible to a binary that named only tools/build —
	// which is why a project whose dependency is replaced must name the tree.
	if got := StaleRefusal("verify", repo, narrow); got != nil {
		t.Errorf("the one-directory check refused with %+v — it cannot see the other tree, and must not claim to", got)
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
