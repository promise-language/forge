package primitives

import (
	"os"
	"path/filepath"
	"testing"
)

// hashFixture is a repository with tool source in two places: the tools/build
// every project has, and a second tree standing in for a directory a `replace`
// directive points at.
func hashFixture(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	write(t, repo, "tools/build/go.mod", "module example/tools/build\n\ngo 1.26\n")
	write(t, repo, "tools/build/common/x.go", "package common\n")
	write(t, repo, "primitives/y.go", "package primitives\n")
	return repo
}

func write(t *testing.T, repo, rel, body string) {
	t.Helper()
	p := filepath.Join(repo, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func hash(t *testing.T, repo string, dirs ...string) string {
	t.Helper()
	h, err := SourceHash(repo, dirs...)
	if err != nil {
		t.Fatalf("SourceHash(%v): %v", dirs, err)
	}
	if h == "" {
		t.Fatal("SourceHash returned an empty digest")
	}
	return h
}

// Naming no directory is the call every project that pins this library writes,
// and it must keep meaning tools/build.
func TestSourceHashDefaultsToToolsBuild(t *testing.T) {
	repo := hashFixture(t)
	bare := hash(t, repo)
	named := hash(t, repo, ToolsBuildDir)
	if bare != named {
		t.Errorf("SourceHash with no directory = %s, but with %q = %s", bare, ToolsBuildDir, named)
	}
	viaWrapper, err := ToolsSourceHash(repo)
	if err != nil {
		t.Fatal(err)
	}
	if viaWrapper != bare {
		t.Errorf("ToolsSourceHash = %s, SourceHash = %s — the two disagree", viaWrapper, bare)
	}
}

// The failure §4 exists to prevent: an edit to a replaced tree leaving every
// binary claiming to be current.
func TestSourceHashCoversEveryDirectoryNamed(t *testing.T) {
	repo := hashFixture(t)
	before := hash(t, repo, ToolsBuildDir, "primitives")

	write(t, repo, "primitives/y.go", "package primitives\n\nfunc F() {}\n")

	if after := hash(t, repo, ToolsBuildDir, "primitives"); after == before {
		t.Error("editing the replaced tree did not change the hash — every binary would report itself current")
	}
	// And the one-directory call is exactly the reason it must be named: it
	// cannot see the edit, which is correct for a project that pins.
	if hash(t, repo, ToolsBuildDir) != hash(t, repo) {
		t.Error("hashing tools/build stopped meaning what it meant")
	}
}

// The meta-builder and each binary compute from one list, but a caller that
// spells the same set in a different order must still get the same answer —
// otherwise the ordering becomes an undeclared part of the contract.
func TestSourceHashDoesNotDependOnDirectoryOrder(t *testing.T) {
	repo := hashFixture(t)
	if a, b := hash(t, repo, ToolsBuildDir, "primitives"), hash(t, repo, "primitives", ToolsBuildDir); a != b {
		t.Errorf("order changed the digest: %s vs %s", a, b)
	}
}

// Overlapping directories name some files twice. Hashing such a file twice
// would make the digest depend on how the set was spelled rather than on what
// the tree holds.
func TestSourceHashCountsAFileOnce(t *testing.T) {
	repo := hashFixture(t)
	if a, b := hash(t, repo, "tools"), hash(t, repo, "tools", ToolsBuildDir); a != b {
		t.Errorf("naming a directory and its parent changed the digest: %s vs %s", a, b)
	}
}

// A file is named by its path relative to the repo root, so the same content at
// the same offset inside two directories is two different inputs. Naming it
// relative to the directory it was found under would make these collide.
func TestSourceHashNamesFilesFromTheRepoRoot(t *testing.T) {
	repo := t.TempDir()
	write(t, repo, "one/z.go", "package a\n")
	write(t, repo, "two/z.go", "package b\n")
	here := hash(t, repo, "one", "two")

	swapped := t.TempDir()
	write(t, swapped, "one/z.go", "package b\n")
	write(t, swapped, "two/z.go", "package a\n")

	if other := hash(t, swapped, "one", "two"); other == here {
		t.Error("two directories holding each other's file hashed the same — the file names collide")
	}
}

// Only source and the module manifests are tool source. A build artefact or a
// README landing in the tree must not report every binary stale.
func TestSourceHashReadsOnlySourceAndManifests(t *testing.T) {
	repo := hashFixture(t)
	before := hash(t, repo)

	write(t, repo, "tools/build/README.md", "notes\n")
	write(t, repo, "tools/build/bin/verify", "\x7fELF binary\n")
	if after := hash(t, repo); after != before {
		t.Error("a non-source file changed the digest")
	}

	write(t, repo, "tools/build/go.sum", "example v1.0.0 h1:x=\n")
	if after := hash(t, repo); after == before {
		t.Error("go.sum is not hashed — raising a pinned version would not report a binary stale")
	}
}

// The size delimiter is what stops two files' contents from running together.
func TestSourceHashSeparatesFileBoundaries(t *testing.T) {
	split := t.TempDir()
	write(t, split, "d/a.go", "package a\n")
	write(t, split, "d/b.go", "package b\n")

	joined := t.TempDir()
	write(t, joined, "d/a.go", "package a\npackage b\n")
	write(t, joined, "d/b.go", "")

	if hash(t, split, "d") == hash(t, joined, "d") {
		t.Error("moving a byte across a file boundary did not change the digest")
	}
}

func TestSourceHashReportsAMissingDirectory(t *testing.T) {
	repo := hashFixture(t)
	if _, err := SourceHash(repo, ToolsBuildDir, "not-here"); err == nil {
		t.Error("a directory that does not exist was hashed as if it were empty")
	}
	if _, err := SourceHash(filepath.Join(repo, "gone")); err == nil {
		t.Error("a repo root that does not exist was accepted")
	}
}

// An unreadable file is not an empty one: hashing it as absent would let a
// permission change hide an edit.
func TestSourceHashReportsAnUnreadableFile(t *testing.T) {
	if IsWindows() {
		t.Skip("file modes do not withhold reads here")
	}
	if os.Geteuid() == 0 {
		t.Skip("root reads anything")
	}
	repo := hashFixture(t)
	locked := filepath.Join(repo, "tools", "build", "common", "x.go")
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o644) })

	if _, err := SourceHash(repo); err == nil {
		t.Error("an unreadable source file was hashed as if it were not there")
	}
}
