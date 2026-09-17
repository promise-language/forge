package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/promise-language/forge/primitives/command"
)

func mkdirs(t *testing.T, root string, names ...string) {
	t.Helper()
	for _, n := range names {
		if err := os.MkdirAll(filepath.Join(root, n), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFileHash(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a")
	if err := os.WriteFile(p, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	h1, err := fileHash(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(h1) != 64 {
		t.Errorf("hash is %d chars, want a 64-char sha256", len(h1))
	}
	if err := os.WriteFile(p, []byte("hello!"), 0o644); err != nil {
		t.Fatal(err)
	}
	h2, _ := fileHash(p)
	if h1 == h2 {
		t.Error("changing the file did not change its hash")
	}
	if _, err := fileHash(filepath.Join(dir, "absent")); err == nil {
		t.Error("a missing file hashed without error")
	}
}

// The sidecar is the staleness contract: it records the source hash and the
// hash of every binary built from it, so a binary replaced after the fact is
// not mistaken for one this build produced.
func TestUpToDate(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	mkdirs(t, dir, "bin")
	verify := filepath.Join(binDir, "verify")
	if err := os.WriteFile(verify, []byte("BINARY"), 0o755); err != nil {
		t.Fatal(err)
	}
	vh, err := fileHash(verify)
	if err != nil {
		t.Fatal(err)
	}
	sidecar := filepath.Join(binDir, ".tools.hash")
	writeSidecar := func(body string) {
		if err := os.WriteFile(sidecar, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	writeSidecar("SRCHASH\nverify:" + vh + "\n")
	if !upToDate(sidecar, "SRCHASH", binDir, []string{"verify"}) {
		t.Error("a matching sidecar reported out of date")
	}
	if upToDate(sidecar, "OTHERHASH", binDir, []string{"verify"}) {
		t.Error("a changed source hash reported up to date")
	}
	if upToDate(sidecar, "SRCHASH", binDir, []string{"verify", "gate"}) {
		t.Error("a tool with no recorded hash reported up to date")
	}

	// A binary replaced since the build must not pass.
	if err := os.WriteFile(verify, []byte("REPLACED"), 0o755); err != nil {
		t.Fatal(err)
	}
	if upToDate(sidecar, "SRCHASH", binDir, []string{"verify"}) {
		t.Error("a replaced binary reported up to date")
	}

	writeSidecar("SRCHASH\nmalformed-entry\n")
	if upToDate(sidecar, "SRCHASH", binDir, []string{"verify"}) {
		t.Error("a malformed sidecar reported up to date")
	}
	if upToDate(filepath.Join(binDir, "absent"), "SRCHASH", binDir, nil) {
		t.Error("a missing sidecar reported up to date")
	}
}

// A definition defect fails this project's tested gate rather than the first
// invocation that reaches it (docs/command-line.md, What a tool decides).
func TestTheDefinitionPassesTheLibrarysCheck(t *testing.T) {
	for _, defect := range command.Check(define()) {
		t.Errorf("make's definition: %v", defect)
	}
}

// make is the builder every refusal names, so it declares nothing to refuse on:
// a builder that could refuse because the binaries are stale would close the one
// way out (docs/project-tools.md, Staleness).
func TestTheBuilderNeverRefuses(t *testing.T) {
	if define().Fit != nil {
		t.Error("make can refuse, and it is the recovery every other refusal names")
	}
}
