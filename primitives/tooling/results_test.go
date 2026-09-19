package tooling

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/promise-language/forge/primitives/command"
)

// The sidecar is what the previous build recorded: the source hash, then each
// built name with its binary's digest. It is written last, so a run that died
// part-way leaves nothing claiming the tools are current.
func TestTheSidecarRecordsTheHashAndEachBinary(t *testing.T) {
	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	write(t, root, filepath.Join("bin", "gate"), "a binary\n")
	path := filepath.Join(root, filepath.FromSlash(SidecarFile))

	if err := writeSidecar(path, "the-hash", binDir, []string{"gate"}); err != nil {
		t.Fatal(err)
	}
	read, err := readSidecar(path)
	if err != nil {
		t.Fatal(err)
	}
	if read.hash != "the-hash" || read.binaries["gate"] == "" {
		t.Errorf("the sidecar reads back as %+v", read)
	}
	if err := writeSidecar(path, "h", binDir, []string{"absent"}); err == nil {
		t.Error("a sidecar was written for a binary that is not there")
	}

	write(t, root, filepath.FromSlash(SidecarFile), "the-hash\nmalformed\n")
	if _, err := readSidecar(path); err == nil {
		t.Error("a malformed sidecar entry was read")
	}
	if _, err := readSidecar(filepath.Join(root, "never-written")); err == nil {
		t.Error("an absent sidecar was read")
	}
}

// Every result renders for a person, and the two whose stdout another contract
// claims refuse to: the shape is that document's, and it has no rendering.
func TestEveryResultRendersForAPersonExceptTheWiresAnotherContractOwns(t *testing.T) {
	for _, c := range []struct {
		name   string
		result command.Result
		says   []string
	}{
		{"make had nothing to do", MakeResult{UpToDate: true}, []string{"Tools up to date"}},
		{"make built and pruned",
			MakeResult{Built: []string{"gate"}, Removed: []string{"retired"}},
			[]string{"built    gate", "removed  retired"}},
		{"setup says what each step changed",
			SetupResult{HooksPath: ".githooks", Steps: []StepChange{{Name: "hooks", Changed: true}}},
			[]string{".githooks", "hooks", "changed"}},
		{"the gate listing is one name per line",
			Listing{Gates: []ListedGate{{Name: "tested"}, {Name: "tested:root"}}},
			[]string{"tested\n", "tested:root\n"}},
		{"run's listing labels the two groups",
			RunListing{Commands: []string{"gate"}, Gates: []string{"tested"}},
			[]string{"command  gate", "gate     tested"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			var rendered strings.Builder
			if err := c.result.Human(&rendered); err != nil {
				t.Fatal(err)
			}
			for _, says := range c.says {
				if !strings.Contains(rendered.String(), says) {
					t.Errorf("rendering is %q, want it to say %q", rendered.String(), says)
				}
			}
		})
	}

	for name, result := range map[string]command.Result{"an envelope": Envelope{}, "a verdict": Verdict{}} {
		if err := result.Human(&strings.Builder{}); err == nil {
			t.Errorf("%s rendered for a person, where its shape is gate-contract.md's", name)
		}
	}
}

// A judged measurement prints beside every term it was judged on, which is what
// someone iterating on one failure is reading.
func TestAJudgedMeasurementPrintsBesideItsTerms(t *testing.T) {
	read := Terms{
		Caps: map[string]Cap{
			"failed_tests": {Direction: AtMost, Cap: ptr(0.0)},
			// A metric may have both, and both are applied — so both are shown.
			"statement_coverage": {Direction: AtLeast, Cap: ptr(75.0)},
		},
		Baselines: map[string]Baseline{"statement_coverage": {Direction: AtLeast, Value: ptr(80.0)}},
	}
	env := Envelope{
		Gate: "x", Target: HostTarget(),
		Metrics: []Measurement{
			Counted("failed_tests", 2, ""),
			Quantity("statement_coverage", 91.5, "percent"),
			Counted("test_count", 412, ""),
			Counted("worktree_free_bytes", 4096, "bytes"),
		},
		Incomplete: "half of it did not run",
	}

	body := render(env, read)
	for _, says := range []string{"failed_tests", "cap at_most 0", "✗", "91.5%",
		"cap at_least 75, baseline at_least 80", "✓",
		"test_count", "not judged", "4096 B", "incomplete", "never moves a baseline"} {
		if !strings.Contains(body, says) {
			t.Errorf("the rendering does not say %q:\n%s", says, body)
		}
	}
}

// What `run <gate>` puts in front of a person: the measurements beside their
// terms, and — only where the verdict was not acceptable — the detail that says
// what to do about it. A passing run that printed a detail would read as a
// finding; a failing one that did not would send the reader back to the gate.
func TestAJudgedResultCarriesItsDetailOnlyWhenItFailed(t *testing.T) {
	judged := Judged{
		rendered: "  failed_tests  2  cap down 0  ✗\n",
		Verdict:  Verdict{Acceptable: false, Detail: "failed_tests is 2, cap 0, in root (2). Fix the failing tests."},
	}
	var failing strings.Builder
	if err := judged.Human(&failing); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(failing.String(), "cap down 0") || !strings.Contains(failing.String(), "Fix the failing tests") {
		t.Errorf("a failing result reads %q, want the measurement and the detail", failing.String())
	}

	judged.Verdict = Verdict{Acceptable: true, Detail: "every judged measurement is within its term"}
	var passing strings.Builder
	if err := judged.Human(&passing); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(passing.String(), "within its term") {
		t.Errorf("a passing result reads %q, want no detail to read as a finding", passing.String())
	}
}

func ptr(v float64) *float64 { return &v }
