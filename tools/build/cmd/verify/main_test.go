package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/promise-language/forge/primitives/command"
	"github.com/promise-language/forge/tools/build/common"
)

// A definition defect fails this project's tested gate rather than the first
// invocation that reaches it (docs/command-line.md, What a tool decides).
func TestTheDefinitionPassesTheLibrarysCheck(t *testing.T) {
	for _, defect := range command.Check(define(fitRepo(t))) {
		t.Errorf("verify's definition: %v", defect)
	}
}

// A misspelled flag is refused before any action, where it used to be dropped
// in silence and the whole pipeline ran as though it had been asked plainly.
// This is the one place a person is most likely to be wrong about what they
// typed (docs/org/cli-guide.md, Fail closed).
func TestAnUnknownFlagIsRefusedBeforeThePipelineRuns(t *testing.T) {
	root, hash := fitRepo(t)
	var out, errs strings.Builder
	status := command.Run(define(root, hash), []string{"--bogus"},
		command.Streams{Out: &out, Err: &errs, Dir: root})

	if status != command.StatusMalformed {
		t.Errorf("status %d, want %d", status, command.StatusMalformed)
	}
	if out.String() != "" {
		t.Errorf("stdout %q, want nothing done and nothing written", out.String())
	}
	if !strings.Contains(errs.String(), "unknown flag -bogus") {
		t.Errorf("stderr %q, want it to name the flag", errs.String())
	}
}

// fitRepo is a checkout a stamped binary is current against, so that the
// staleness refusal is out of the way and what this file measures is the
// parsing.
func fitRepo(t *testing.T) (root, hash string) {
	t.Helper()
	root = t.TempDir()
	for _, dir := range []string{"tools/build", "primitives"} {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(dir)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	hash, err := common.SourceHash(root)
	if err != nil {
		t.Fatal(err)
	}
	return root, hash
}
