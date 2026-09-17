package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/promise-language/forge/primitives"
	"github.com/promise-language/forge/primitives/command"
	"github.com/promise-language/forge/tools/build/common"
)

// A definition defect fails this project's tested gate rather than the first
// invocation that reaches it (docs/command-line.md, What a tool decides).
func TestTheDefinitionPassesTheLibrarysCheck(t *testing.T) {
	for _, defect := range command.Check(define(fitRepo(t))) {
		t.Errorf("setup's definition: %v", defect)
	}
}

// A misspelled flag is refused before the hooks are wired, where it used to be
// dropped in silence and setup did its work as though it had been asked
// plainly (docs/org/cli-guide.md, Fail closed).
func TestAnUnknownFlagIsRefusedBeforeAnythingIsWired(t *testing.T) {
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

// WHAT SETUP DID IS WHAT IT REPORTS. The result is the writing end of
// docs/project-tools.md, Setup's `{"hooks_path": ..., "steps": [...]}`, and the
// path in it is the one constant git was configured with — prose at each end is
// not an agreement, so a result naming a directory git was never pointed at
// would be a green run that wired nothing a hook can find.
func TestTheResultNamesTheHooksPathItWired(t *testing.T) {
	root, hash := fitRepo(t)
	gitInit(t, root)

	var out, errs strings.Builder
	status := command.Run(define(root, hash), nil,
		command.Streams{Out: &out, Err: &errs, Dir: root})

	if status != command.StatusDone {
		t.Fatalf("status %d (%q)", status, errs.String())
	}
	var answer struct {
		HooksPath string `json:"hooks_path"`
		Steps     []struct {
			Name    string `json:"name"`
			Changed bool   `json:"changed"`
		} `json:"steps"`
	}
	if err := json.Unmarshal([]byte(out.String()), &answer); err != nil {
		t.Fatalf("stdout %q is not setup's result: %v", out.String(), err)
	}
	if answer.HooksPath != primitives.HooksPath {
		t.Errorf("the result says %q, want the one constant both ends read", answer.HooksPath)
	}
	if answer.Steps == nil {
		t.Error("steps is absent, where the shape has it present and possibly empty")
	}
	if configured := gitConfig(t, root, "core.hooksPath"); configured != answer.HooksPath {
		t.Errorf("git looks for hooks in %q and the result says %q", configured, answer.HooksPath)
	}
}

// gitInit makes root a checkout, which is the only state `git config` will
// write into.
func gitInit(t *testing.T, root string) {
	t.Helper()
	if err := primitives.RunSilent("git", "-C", root, "init"); err != nil {
		t.Skipf("git init: %v", err)
	}
}

// gitConfig reads back what setup wrote.
func gitConfig(t *testing.T, root, key string) string {
	t.Helper()
	value, err := primitives.RunOutputIn(root, "git", "config", key)
	if err != nil {
		t.Fatalf("git config %s: %v", key, err)
	}
	return value
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
