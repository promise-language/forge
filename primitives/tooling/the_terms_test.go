package tooling

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const noBaselines = "{}"

// The files are read strictly: the judge refuses to answer, naming the file and
// the entry, when an entry has an unknown key, a missing field, an unknown
// direction, or both a value and targets. A term nobody reads is a term nobody
// applies, and silence there is what a ratchet cannot recover from.
func TestTheTermFilesAreReadStrictly(t *testing.T) {
	for _, c := range []struct {
		name       string
		thresholds string
		baselines  string
		says       string
	}{
		{"an unknown key", `{"n": {"direction": "down", "cap": 0, "note": "x"}}`, noBaselines, "note"},
		{"a missing field", `{"n": {"direction": "down"}}`, noBaselines, "states no cap"},
		{"no direction", `{"n": {"cap": 0}}`, noBaselines, "states no direction"},
		{"an unknown direction", `{"n": {"direction": "sideways", "cap": 0}}`, noBaselines, "unknown direction"},
		{"both a value and targets", `{}`,
			`{"n": {"direction": "up", "value": 1, "targets": {"linux/amd64": 2}}}`, "both a value and targets"},
		{"neither a value nor targets", `{}`, `{"n": {"direction": "up"}}`, "neither a value nor targets"},
		{"two terms that disagree on direction",
			`{"n": {"direction": "down", "cap": 0}}`, `{"n": {"direction": "up", "value": 1}}`, "agree on direction"},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			terms(t, root, c.thresholds, c.baselines)
			_, err := LoadTerms(root)
			if err == nil {
				t.Fatal("the terms were accepted")
			}
			if !strings.Contains(err.Error(), c.says) {
				t.Errorf("the refusal is %q, want it to say %q", err, c.says)
			}
		})
	}
}

// Every term a metric has is applied, cap and baseline alike, and every one
// must hold.
func TestEveryTermAMetricHasIsApplied(t *testing.T) {
	root := t.TempDir()
	terms(t, root,
		`{"n": {"direction": "down", "cap": 10}}`,
		`{"n": {"direction": "down", "value": 2}}`)
	read, err := LoadTerms(root)
	if err != nil {
		t.Fatal(err)
	}

	env := Envelope{Gate: "x", Target: HostTarget(), Metrics: []Measurement{Counted("n", 5, "")}}
	verdict, err := Judge(env, read, "")
	if err != nil {
		t.Fatal(err)
	}
	if verdict.Acceptable {
		t.Error("5 passed a baseline of 2 because the cap of 10 held")
	}
	applied := verdict.Thresholds["n"]
	if applied.Cap == nil || applied.Baseline == nil {
		t.Errorf("the verdict carries %+v, want both terms it was reached from", applied)
	}
}

// An integer metric is compared as an integer. A term that is not a whole
// number, for an integer metric, is a defect in the term: the judge cannot
// answer, rather than rounding.
func TestAFractionalTermOnACountIsADefectInTheTerm(t *testing.T) {
	root := t.TempDir()
	terms(t, root, `{"n": {"direction": "down", "cap": 0.5}}`, noBaselines)
	read, err := LoadTerms(root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Judge(Envelope{Gate: "x", Metrics: []Measurement{Counted("n", 1, "")}}, read, "")
	if err == nil || !strings.Contains(err.Error(), "whole number") {
		t.Errorf("the judge answered %v, want a refusal naming the term as the defect", err)
	}
}

// An incomplete run is never acceptable, even when every number is within its
// term: honest numbers that understate what was checked must not read as a good
// result.
func TestAnIncompleteRunIsNeverAcceptable(t *testing.T) {
	root := t.TempDir()
	terms(t, root, `{"n": {"direction": "down", "cap": 10}}`, noBaselines)
	read, _ := LoadTerms(root)

	verdict, err := Judge(Envelope{
		Gate:       "x",
		Metrics:    []Measurement{Counted("n", 0, "")},
		Incomplete: "half of it did not run",
	}, read, "")
	if err != nil {
		t.Fatal(err)
	}
	if verdict.Acceptable {
		t.Error("an incomplete run passed")
	}
	if !strings.Contains(verdict.Detail, "half of it did not run") {
		t.Errorf("detail = %q, want the reason carried through", verdict.Detail)
	}
}

// A metric with no term is reported as not judged, and cannot fail. An envelope
// none of whose metrics has a term cannot be judged at all: acceptable over
// nothing would be a pass no term granted.
func TestAnEnvelopeNoTermTouchesCannotBeJudged(t *testing.T) {
	root := t.TempDir()
	terms(t, root, `{"other": {"direction": "down", "cap": 0}}`, noBaselines)
	read, _ := LoadTerms(root)

	_, err := Judge(Envelope{Gate: "x", Metrics: []Measurement{Counted("n", 9, "")}}, read, "")
	if !errors.Is(err, ErrNothingJudged) {
		t.Errorf("the judge answered %v, want it to say it could reach no verdict", err)
	}
}

// A failing verdict states the judgement, the evidence and the gate's
// remediation. The evidence comes from the envelope's groups, which say where a
// number came from.
func TestAFailingVerdictCarriesTheEvidenceAndTheRemediation(t *testing.T) {
	root := t.TempDir()
	terms(t, root, `{"failed_tests": {"direction": "down", "cap": 0}}`, noBaselines)
	read, _ := LoadTerms(root)

	verdict, err := Judge(Envelope{
		Gate:    "tested",
		Metrics: []Measurement{Counted("failed_tests", 2, "")},
		Groups: []Group{
			{Name: "root", Metrics: []Measurement{Counted("failed_tests", 0, "")}},
			{Name: "tools-build", Metrics: []Measurement{Counted("failed_tests", 2, "")}},
		},
	}, read, "Fix the failing tests")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"failed_tests is 2", "cap 0", "tools-build", "Fix the failing tests"} {
		if !strings.Contains(verdict.Detail, want) {
			t.Errorf("detail = %q, want it to say %q", verdict.Detail, want)
		}
	}
	if strings.Contains(verdict.Detail, "in root") {
		t.Errorf("detail = %q, want it to name where the number came from and not where it did not", verdict.Detail)
	}
}

// Under a floor the evidence is the unit that is short, and a unit at zero is
// the strongest evidence there is. A judge that named only the units with a
// number to show would point at the one that is fine and hide the one that is
// not.
func TestTheEvidenceUnderAFloorIsTheUnitThatIsShort(t *testing.T) {
	root := t.TempDir()
	terms(t, root, `{"statement_coverage": {"direction": "up", "cap": 75}}`, noBaselines)
	read, _ := LoadTerms(root)

	verdict, err := Judge(Envelope{
		Gate:    "covered",
		Metrics: []Measurement{Quantity("statement_coverage", 40, "percent")},
		Groups: []Group{
			{Name: "root", Metrics: []Measurement{Quantity("statement_coverage", 0, "percent")}},
			{Name: "tools-build", Metrics: []Measurement{Quantity("statement_coverage", 91.5, "percent")}},
		},
	}, read, "cover what the change added")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(verdict.Detail, "root (0.0)") {
		t.Errorf("detail = %q, want it to name the unit that is short of the floor", verdict.Detail)
	}
	if strings.Contains(verdict.Detail, "tools-build") {
		t.Errorf("detail = %q, want it not to name a unit that is over the floor", verdict.Detail)
	}
}

// Only verify moves a baseline, and only forward.
func TestARatchetMovesOnlyForward(t *testing.T) {
	root := t.TempDir()
	terms(t, root, `{}`, `{"cov": {"direction": "up", "value": 80}}`)

	moved, err := Ratchet(root, []Envelope{{
		Gate: "covered", Target: HostTarget(),
		Metrics: []Measurement{Quantity("cov", 75, "percent")},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(moved) != 0 || readBaseline(t, root, "cov") != 80 {
		t.Errorf("a worse run moved the baseline to %v (%v)", readBaseline(t, root, "cov"), moved)
	}

	if _, err := Ratchet(root, []Envelope{{
		Gate: "covered", Target: HostTarget(),
		Metrics: []Measurement{Quantity("cov", 91, "percent")},
	}}); err != nil {
		t.Fatal(err)
	}
	if got := readBaseline(t, root, "cov"); got != 91 {
		t.Errorf("baseline = %v, want the better value the run earned", got)
	}
}

// An incomplete run moves nothing, and neither does a red one — a red run never
// reaches the ratchet stage, and an incomplete one is refused here.
func TestAnIncompleteRunMovesNoBaseline(t *testing.T) {
	root := t.TempDir()
	terms(t, root, `{}`, `{"cov": {"direction": "up", "value": 80}}`)

	moved, err := Ratchet(root, []Envelope{{
		Gate: "covered", Target: HostTarget(),
		Metrics:    []Measurement{Quantity("cov", 99, "percent")},
		Incomplete: "half of it did not run",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(moved) != 0 || readBaseline(t, root, "cov") != 80 {
		t.Errorf("an incomplete run moved the baseline to %v", readBaseline(t, root, "cov"))
	}
}

// A target with no recorded value is recorded, and a baseline recorded per
// target stays per target.
func TestATargetWithNoRecordedValueIsRecorded(t *testing.T) {
	root := t.TempDir()
	terms(t, root, `{}`, `{"cov": {"direction": "up", "targets": {"linux/amd64": 81.9}}}`)

	if _, err := Ratchet(root, []Envelope{{
		Gate: "covered", Target: "darwin/arm64",
		Metrics: []Measurement{Quantity("cov", 70, "percent")},
	}}); err != nil {
		t.Fatal(err)
	}
	read, err := loadBaselines(root)
	if err != nil {
		t.Fatal(err)
	}
	if read["cov"].Value != nil {
		t.Error("a baseline recorded per target was rewritten as one value")
	}
	if got, ok := read["cov"].For("darwin/arm64"); !ok || got != 70 {
		t.Errorf("the new target is %v (%v), want it recorded", got, ok)
	}
	if got, _ := read["cov"].For("linux/amd64"); got != 81.9 {
		t.Errorf("the other target is %v, want it untouched", got)
	}
}

// An entry is never added by a run: making a metric a ratchet is a person's
// decision, and tracking the file is the declaration.
func TestARunNeverAddsATerm(t *testing.T) {
	root := t.TempDir()
	terms(t, root, `{}`, noBaselines)

	if _, err := Ratchet(root, []Envelope{{
		Gate: "covered", Target: HostTarget(),
		Metrics: []Measurement{Quantity("cov", 99, "percent")},
	}}); err != nil {
		t.Fatal(err)
	}
	read, err := loadBaselines(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(read) != 0 {
		t.Errorf("the run added %v", read)
	}
}

// A project with no ratchet has no baselines file, and that is not a defect.
func TestAnAbsentBaselinesFileIsNotADefect(t *testing.T) {
	root := t.TempDir()
	write(t, root, filepath.FromSlash(ThresholdsFile), `{"n": {"direction": "down", "cap": 0}}`)
	read, err := LoadTerms(root)
	if err != nil {
		t.Fatalf("a project with no baselines file was refused: %v", err)
	}
	if len(read.Baselines) != 0 {
		t.Errorf("baselines = %v, want none", read.Baselines)
	}
}

func readBaseline(t *testing.T, root, name string) float64 {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(BaselinesFile)))
	if err != nil {
		t.Fatal(err)
	}
	var read map[string]Baseline
	if err := json.Unmarshal(data, &read); err != nil {
		t.Fatal(err)
	}
	got, _ := read[name].For(HostTarget())
	return got
}
