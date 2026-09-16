package primitives

import (
	"path"
	"path/filepath"
	"strings"
	"testing"
)

// THIS IS THE ONE PLACE THE PATH IS WRITTEN OUT. Every end of the contract
// imports the constant, so no other test in the fleet would notice its value
// changing — and the end that would notice first is a guard in another
// repository that this repository's build never runs. Changing the value must
// therefore fail here, where it reads as what it is: a change to an agreement
// with a reader nothing local can ask.
func TestVerifiedTreeRecordIsTheAgreedPath(t *testing.T) {
	const agreed = ".workspace/verified-tree"
	if VerifiedTreeRecord != agreed {
		t.Errorf("VerifiedTreeRecord = %q, want %q — the workspace's precommit-guard reads %q, "+
			"and a record written anywhere else is one it always finds absent",
			VerifiedTreeRecord, agreed, agreed)
	}
}

// The shape both ends rely on when they turn the constant into a filename: each
// joins it onto a repository root and converts it for the host. A value that is
// absolute makes filepath.Join discard the root entirely, so the guard would
// read a file outside the checkout it was asked about; one carrying a host
// separator, or a "." or ".." segment, lands somewhere neither end intended.
func TestVerifiedTreeRecordJoinsOntoARepositoryRoot(t *testing.T) {
	if path.IsAbs(VerifiedTreeRecord) || filepath.IsAbs(VerifiedTreeRecord) {
		t.Errorf("VerifiedTreeRecord = %q, want a path relative to a repository root", VerifiedTreeRecord)
	}
	if strings.ContainsRune(VerifiedTreeRecord, '\\') {
		t.Errorf("VerifiedTreeRecord = %q, want it slash-separated — filepath.FromSlash is what makes it a host path", VerifiedTreeRecord)
	}
	if cleaned := path.Clean(VerifiedTreeRecord); cleaned != VerifiedTreeRecord {
		t.Errorf("VerifiedTreeRecord = %q, want it already clean (%q)", VerifiedTreeRecord, cleaned)
	}

	root := filepath.FromSlash("/tmp/checkout")
	joined := filepath.Join(root, filepath.FromSlash(VerifiedTreeRecord))
	if !strings.HasPrefix(joined, root+string(filepath.Separator)) {
		t.Errorf("joined onto %q the record is %q, which is outside the checkout", root, joined)
	}
}

// The writing end derives the directory it creates the record in from the
// constant rather than spelling it a second time, and a project's .gitignore
// names that same directory (tool-contract's Layout). Both need it to be one
// segment: a record at the repository root has no directory to ignore, and one
// nested deeper is not the .workspace/ every project is required to carry.
func TestVerifiedTreeRecordLivesDirectlyInTheWorkspaceDirectory(t *testing.T) {
	if got := path.Dir(VerifiedTreeRecord); got != ".workspace" {
		t.Errorf("path.Dir(VerifiedTreeRecord) = %q, want %q", got, ".workspace")
	}
	if got := path.Base(VerifiedTreeRecord); got == "" || got == "." {
		t.Errorf("path.Base(VerifiedTreeRecord) = %q, want a filename", got)
	}
}
