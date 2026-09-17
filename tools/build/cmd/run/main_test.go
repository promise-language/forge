package main

import (
	"encoding/json"
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
	root, hash := fitRepo(t)
	for _, defect := range command.Check(define(root, hash)) {
		t.Errorf("run's definition: %v", defect)
	}
}

// Bare, run answers what is missing — and the same way gate does, where the two
// tools used to disagree about the status.
func TestABareInvocationAnswersTheBriefForm(t *testing.T) {
	root, hash := fitRepo(t)
	out, errs, status := invoke(t, root, hash, nil, true)

	if status != command.StatusMalformed {
		t.Errorf("status %d, want %d", status, command.StatusMalformed)
	}
	if out != "" {
		t.Errorf("stdout %q, want it empty", out)
	}
	lines := strings.Split(strings.TrimRight(errs, "\n"), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[1], "expecting a subcommand:") {
		t.Fatalf("the brief form is:\n%s", errs)
	}
	if lines[0] != "run "+hash {
		t.Errorf("line 1 is %q, want the version line", lines[0])
	}
}

// --list keeps the shape generic-projects fixes for it: two labelled groups for
// a person, and one object with both vocabularies for anything reading.
func TestTheListingKeepsItsShape(t *testing.T) {
	root, hash := fitRepo(t)

	out, _, status := invoke(t, root, hash, []string{"--list"}, false)
	if status != command.StatusDone {
		t.Fatalf("`run --list` exited %d", status)
	}
	var answer struct {
		Commands []string `json:"commands"`
		Gates    []string `json:"gates"`
	}
	if err := json.Unmarshal([]byte(out), &answer); err != nil {
		t.Fatalf("`run --list` wrote %q, which is not the listing object: %v", out, err)
	}
	if len(answer.Gates) != len(common.GateNames(root)) {
		t.Errorf("the object names %d gates, want %d", len(answer.Gates), len(common.GateNames(root)))
	}
	if answer.Commands == nil {
		t.Error("the object names no commands at all, not even an empty set")
	}

	human, _, status := invoke(t, root, hash, []string{"--list"}, true)
	if status != command.StatusDone {
		t.Fatalf("`run --list` at a terminal exited %d", status)
	}
	if !strings.Contains(human, "gate     "+answer.Gates[0]) {
		t.Errorf("the human listing is %q, want each name with its kind", human)
	}
}

// Fail closed. Each of these did nothing and said what was wrong.
func TestAMalformedInvocationIsRefusedBeforeAnyAction(t *testing.T) {
	root, hash := fitRepo(t)
	gate := common.GateConcepts()[0]
	for _, c := range []struct {
		name string
		args []string
		says []string
	}{
		{"an unknown flag names the nearest", []string{"--lst"}, []string{"unknown flag -lst", "did you mean -list"}},
		{"an unknown gate is refused", []string{"lint"}, []string{"unknown command lint"}},
		{"a mode flag on a contract-fixed command names the contract",
			[]string{gate, "--verdict", "-json"}, []string{"gate-contract.md"}},
		{"the envelope flag belongs to gate, not to run", []string{gate, "--envelope"},
			[]string{"unknown flag -envelope"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			out, errs, status := invoke(t, root, hash, c.args, true)
			if status != command.StatusMalformed {
				t.Errorf("status %d, want %d (stderr %q)", status, command.StatusMalformed, errs)
			}
			if out != "" {
				t.Errorf("stdout %q, want nothing done and nothing written", out)
			}
			for _, says := range c.says {
				if !strings.Contains(errs, says) {
					t.Errorf("stderr %q does not say %q", errs, says)
				}
			}
		})
	}
}

// -version answers, where it used to be an unknown flag.
func TestVersionAnswers(t *testing.T) {
	root, hash := fitRepo(t)
	out, _, status := invoke(t, root, hash, []string{"--version"}, true)
	if status != command.StatusDone || strings.TrimSpace(out) != "run "+hash {
		t.Errorf("`run --version` = (%q, %d), want the one-line form at status 0", out, status)
	}
}

// A stale binary refuses, and in the mode whose stdout the gate contract claims
// it writes nothing there at all.
func TestAStaleBinaryRefuses(t *testing.T) {
	root, _ := fitRepo(t)
	stale := define(root, "not-the-hash-it-would-have")

	var out, errs strings.Builder
	status := command.Run(stale, []string{"-help"}, command.Streams{Out: &out, Err: &errs, Dir: root})
	if status != command.StatusRefused {
		t.Errorf("`run -help` exited %d, want the refusal status", status)
	}
	var refusal command.Refusal
	if err := json.Unmarshal([]byte(out.String()), &refusal); err != nil {
		t.Fatalf("stdout %q is not a refusal object: %v", out.String(), err)
	}
	if refusal.Tool != "run" {
		t.Errorf("refusal names %q, want the tool that refused", refusal.Tool)
	}

	out.Reset()
	errs.Reset()
	status = command.Run(stale, []string{common.GateConcepts()[0], "--verdict"},
		command.Streams{Out: &out, Err: &errs, Dir: root})
	if status != command.StatusRefused {
		t.Errorf("status %d, want the refusal status", status)
	}
	if out.String() != "" {
		t.Errorf("stdout %q, want nothing at all where the verdict goes", out.String())
	}
}

// invoke runs one invocation of run against a checkout it is current for.
func invoke(t *testing.T, root, hash string, args []string, terminal bool) (stdout, stderr string, status int) {
	t.Helper()
	var out, errs strings.Builder
	status = command.Run(define(root, hash), args, command.Streams{
		Out:           &out,
		Err:           &errs,
		OutIsTerminal: terminal,
		Dir:           root,
	})
	return out.String(), errs.String(), status
}

// fitRepo is a checkout a stamped binary is current against: the tool source
// the staleness check hashes, and the hash it would have been stamped with.
func fitRepo(t *testing.T) (root, hash string) {
	t.Helper()
	root = t.TempDir()
	// tools/build/cmd is what the build set is derived from, and `run --list`
	// reports it: a checkout with none is a checkout that builds nothing.
	for _, rel := range []string{"tools/build/x.go", "primitives/y.go", "tools/build/cmd/gate/main.go"} {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("package x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	hash, err := common.SourceHash(root)
	if err != nil {
		t.Fatal(err)
	}
	return root, hash
}
