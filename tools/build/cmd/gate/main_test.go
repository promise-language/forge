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
		t.Errorf("gate's definition: %v", defect)
	}
}

// A bare invocation is malformed: nothing is written where a result goes, the
// status says nothing was done, and the brief form names the build, what is
// missing, and where the full surface is (docs/command-line.md, Help and
// version).
func TestABareInvocationAnswersTheBriefForm(t *testing.T) {
	root, hash := fitRepo(t)
	out, errs, status := invoke(t, root, hash, nil, true)

	if status != command.StatusMalformed {
		t.Errorf("status %d, want %d", status, command.StatusMalformed)
	}
	if out != "" {
		t.Errorf("stdout %q, want it empty — a script must not read this as a result", out)
	}
	lines := strings.Split(strings.TrimRight(errs, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("the brief form is %d lines:\n%s", len(lines), errs)
	}
	if lines[0] != "gate "+hash {
		t.Errorf("line 1 is %q, want the version line — a reader who stopped may hold the wrong build", lines[0])
	}
	if !strings.HasPrefix(lines[1], "expecting a subcommand:") {
		t.Errorf("line 2 is %q, want what is missing", lines[1])
	}
	if !strings.Contains(lines[1], "gate --list") {
		t.Errorf("line 2 is %q, want it to name the invocation that lists the gates", lines[1])
	}
	if !strings.Contains(lines[2], "-help") {
		t.Errorf("line 3 is %q, want the pointer to -help", lines[2])
	}
	// Not the whole vocabulary: the reader asked to do something and left a
	// word out, and the full surface is one flag away.
	for _, name := range common.GateNames(root) {
		if strings.Contains(errs, name) {
			t.Errorf("the brief form names %q; the enumeration's one home is `gate --list`", name)
		}
	}
}

// The listing picks its mode like every other result: one name per line at a
// terminal, the object through a pipe, and -json forcing it either way.
func TestTheListingAnswersInBothModes(t *testing.T) {
	root, hash := fitRepo(t)

	out, _, status := invoke(t, root, hash, []string{"--list"}, true)
	if status != command.StatusDone {
		t.Fatalf("`gate --list` exited %d", status)
	}
	if got := strings.Split(strings.TrimRight(out, "\n"), "\n"); len(got) != len(common.GateNames(root)) {
		t.Errorf("the human listing is %v, want one name per line", got)
	}

	// Through a pipe, and forced with -json at a terminal: the object either
	// way, because a program that wants it asks rather than relying on a pipe
	// being detected on its behalf.
	for _, c := range []struct {
		args     []string
		terminal bool
	}{{[]string{"--list"}, false}, {[]string{"--list", "--json"}, true}} {
		args := c.args
		out, _, status := invoke(t, root, hash, args, c.terminal)
		if status != command.StatusDone {
			t.Fatalf("`gate %s` exited %d", strings.Join(args, " "), status)
		}
		var answer struct {
			Gates []struct {
				Name    string `json:"name"`
				Summary string `json:"summary"`
			} `json:"gates"`
		}
		if err := json.Unmarshal([]byte(out), &answer); err != nil {
			t.Fatalf("`gate %s` wrote %q, which is not the listing object: %v", strings.Join(args, " "), out, err)
		}
		if len(answer.Gates) != len(common.GateNames(root)) {
			t.Errorf("the object names %d gates, want %d", len(answer.Gates), len(common.GateNames(root)))
		}
		if answer.Gates[0].Summary == "" {
			t.Errorf("%q carries no summary", answer.Gates[0].Name)
		}
	}
}

// Every tool answers -version, in both modes, with the payload Help and version
// fixes. A source hash is not semantic, so it carries no major.
func TestVersionAnswersInBothModes(t *testing.T) {
	root, hash := fitRepo(t)

	out, _, status := invoke(t, root, hash, []string{"--version"}, true)
	if status != command.StatusDone || strings.TrimSpace(out) != "gate "+hash {
		t.Errorf("`gate --version` = (%q, %d), want the one-line form", out, status)
	}

	out, _, status = invoke(t, root, hash, []string{"--version"}, false)
	if status != command.StatusDone {
		t.Fatalf("`gate --version` through a pipe exited %d", status)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("the version is not an object: %v (%q)", err, out)
	}
	if payload["project"] != "gate" || payload["text"] != hash {
		t.Errorf("version = %v, want the project and the text apart", payload)
	}
	if _, ok := payload["major"]; ok {
		t.Errorf("version = %v, want no major for a hash: absent means unknown", payload)
	}
}

// Help goes to stdout with status 0, and describes the set it does not author
// rather than listing it.
func TestHelpDescribesTheGatesRatherThanListingThem(t *testing.T) {
	root, hash := fitRepo(t)
	out, _, status := invoke(t, root, hash, []string{"-help"}, true)
	if status != command.StatusDone {
		t.Fatalf("`gate -help` exited %d", status)
	}
	if !strings.Contains(out, "<gate>") || !strings.Contains(out, "gate --list") {
		t.Errorf("help is %q, want the class and the invocation that enumerates it", out)
	}
	for _, name := range common.GateNames(root) {
		if strings.Contains(out, name+"\n") {
			t.Errorf("help enumerates %q; the enumeration's one home is `gate --list`", name)
		}
	}
}

// Fail closed, invocation by invocation. Each of these did nothing and said
// what was wrong with what it was asked.
func TestAMalformedInvocationIsRefusedBeforeAnyAction(t *testing.T) {
	root, hash := fitRepo(t)
	gate := common.GateConcepts()[0]
	for _, c := range []struct {
		name string
		args []string
		says []string
	}{
		{"an abbreviation is not a flag", []string{"-h"}, []string{"unknown flag -h", "-help"}},
		{"a misspelled flag names the nearest", []string{"--lst"}, []string{"unknown flag -lst", "did you mean -list"}},
		{"an unknown gate names the nearest", []string{gate + "x"}, []string{"unknown command", gate}},
		{"a gate with no envelope says where the result is", []string{gate},
			[]string{gate, "--envelope", "run " + gate}},
		{"a mode flag on a contract-fixed command names the contract",
			[]string{gate, "--envelope", "-human"}, []string{"gate-contract.md"}},
		{"two modes are a contradiction", []string{"--list", "-json", "-human"}, []string{"-json and -human"}},
		// gate takes no positional argument, so a stray word is a command it
		// does not have — and the flag written after it is where One order puts
		// it, not a second problem to report.
		{"a stray word is the command it is not", []string{"--list", "extra", "-json"},
			[]string{"unknown command extra"}},
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

// A stale binary refuses every invocation, -help and -version included: what it
// would print is the surface it was built with, in the one place an operator
// goes to learn what a tool is (docs/org/cli-guide.md, Exit codes).
func TestAStaleBinaryRefusesEvenHelp(t *testing.T) {
	root, _ := fitRepo(t)
	for _, args := range [][]string{nil, {"-help"}, {"--version"}, {"--list"}} {
		var out, errs strings.Builder
		status := command.Run(define(root, "not-the-hash-it-would-have"), args,
			command.Streams{Out: &out, Err: &errs, Dir: root})
		if status != command.StatusRefused {
			t.Errorf("`gate %s` exited %d, want the refusal status", strings.Join(args, " "), status)
		}
		var refusal command.Refusal
		if err := json.Unmarshal([]byte(out.String()), &refusal); err != nil {
			t.Fatalf("stdout %q is not a refusal object: %v", out.String(), err)
		}
		if refusal.Refusal != command.Stale || refusal.Tool != "gate" || len(refusal.Recovery) == 0 {
			t.Errorf("refusal = %+v, want the condition, the tool and the recovery", refusal)
		}
	}
}

// The two modes another contract claims keep their stream clean even here: a
// runner parsing stdout for an envelope must not find a refusal there.
func TestARefusalStaysOffTheStreamTheContractClaims(t *testing.T) {
	root, _ := fitRepo(t)
	var out, errs strings.Builder
	status := command.Run(define(root, "not-the-hash-it-would-have"),
		[]string{common.GateConcepts()[0], "--envelope"},
		command.Streams{Out: &out, Err: &errs, Dir: root})

	if status != command.StatusRefused {
		t.Errorf("status %d, want the refusal status", status)
	}
	if out.String() != "" {
		t.Errorf("stdout %q, want nothing at all where the envelope goes", out.String())
	}
	if !strings.Contains(errs.String(), "tools source has changed") {
		t.Errorf("stderr %q, want the refusal said in one line", errs.String())
	}
}

// invoke runs one invocation of gate against a checkout it is current for.
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
	for _, rel := range []string{"tools/build/x.go", "primitives/y.go"} {
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
