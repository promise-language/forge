package tooling

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
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
		{"an unknown key", `{"n": {"direction": "at_most", "cap": 0, "note": "x"}}`, noBaselines, "note"},
		{"a missing field", `{"n": {"direction": "at_most"}}`, noBaselines, "states no cap"},
		{"no direction", `{"n": {"cap": 0}}`, noBaselines, "states no direction"},
		{"an unknown direction", `{"n": {"direction": "sideways", "cap": 0}}`, noBaselines, "unknown direction"},
		// The vocabulary this library once read is refused like any other
		// unknown word, and the refusal names the two that are not. A project
		// carrying the old spelling has to be told, because the alternative —
		// reading `down` as a ceiling because that is what it used to mean —
		// is a judge with two vocabularies and no way to say which one a file
		// was written in.
		{"a cap in the former vocabulary", `{"n": {"direction": "down", "cap": 0}}`, noBaselines, "unknown direction"},
		{"a baseline in the former vocabulary", `{}`, `{"n": {"direction": "up", "value": 1}}`, "unknown direction"},
		{"both a value and targets", `{}`,
			`{"n": {"direction": "at_least", "value": 1, "targets": {"linux/amd64": 2}}}`, "both a value and targets"},
		{"neither a value nor targets", `{}`, `{"n": {"direction": "at_least"}}`, "neither a value nor targets"},
		{"two terms that disagree on direction",
			`{"n": {"direction": "at_most", "cap": 0}}`, `{"n": {"direction": "at_least", "value": 1}}`, "agree on direction"},
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

// A refused direction names the two that are accepted, whether the entry holds
// the wrong word or no word at all. The refusal is the only thing a project
// holding the wrong word ever sees, so it has to carry the right one rather
// than only the verdict that this one is wrong.
func TestARefusedDirectionNamesTheTwoThatAreAccepted(t *testing.T) {
	for _, c := range []struct {
		name       string
		thresholds string
		says       []string
	}{
		{"a word the set does not hold", `{"n": {"direction": "down", "cap": 0}}`,
			[]string{ThresholdsFile, `"n"`, `"down"`, `"at_most"`, `"at_least"`}},
		{"no word at all", `{"n": {"cap": 0}}`,
			[]string{ThresholdsFile, `"n"`, `"at_most"`, `"at_least"`}},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			terms(t, root, c.thresholds, noBaselines)
			_, err := LoadTerms(root)
			if err == nil {
				t.Fatal("the terms were accepted")
			}
			for _, says := range c.says {
				if !strings.Contains(err.Error(), says) {
					t.Errorf("the refusal is %q, want it to say %s", err, says)
				}
			}
		})
	}
}

// A term is the side of it a measurement must be on, inclusively: at_most 0
// accepts 0 and at_least 75 accepts 75. The boundary is the only place the two
// words differ from a strict comparison, and it is where a project reading the
// vocabulary for the first time decides what its cap means.
func TestAMeasurementAtItsTermIsWithinIt(t *testing.T) {
	for _, c := range []struct {
		name       string
		thresholds string
		measured   Measurement
	}{
		{"at_most accepts the cap itself",
			`{"n": {"direction": "at_most", "cap": 0}}`, Counted("n", 0, "")},
		{"at_least accepts the cap itself",
			`{"n": {"direction": "at_least", "cap": 75}}`, Quantity("n", 75, "percent")},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			terms(t, root, c.thresholds, noBaselines)
			read, err := LoadTerms(root)
			if err != nil {
				t.Fatal(err)
			}
			verdict, err := Judge(Envelope{
				Gate: "x", Target: HostTarget(),
				Metrics: []Measurement{c.measured},
			}, read, "")
			if err != nil {
				t.Fatal(err)
			}
			if !verdict.Acceptable {
				t.Errorf("a measurement at its term was refused: %s", verdict.Detail)
			}
		})
	}
}

// Every term a metric has is applied, cap and baseline alike, and every one
// must hold.
func TestEveryTermAMetricHasIsApplied(t *testing.T) {
	root := t.TempDir()
	terms(t, root,
		`{"n": {"direction": "at_most", "cap": 10}}`,
		`{"n": {"direction": "at_most", "value": 2}}`)
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
	terms(t, root, `{"n": {"direction": "at_most", "cap": 0.5}}`, noBaselines)
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
	terms(t, root, `{"n": {"direction": "at_most", "cap": 10}}`, noBaselines)
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
	terms(t, root, `{"other": {"direction": "at_most", "cap": 0}}`, noBaselines)
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
	terms(t, root, `{"failed_tests": {"direction": "at_most", "cap": 0}}`, noBaselines)
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
	terms(t, root, `{"statement_coverage": {"direction": "at_least", "cap": 75}}`, noBaselines)
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

// A property is judged by the same comparison every other measurement is.
// False is worse than true, so at_least is "must be true" and at_most is "must
// be false", and neither needs a rule of its own.
func TestAPropertyIsJudgedByTheOneComparison(t *testing.T) {
	for _, c := range []struct {
		name       string
		thresholds string
		measured   bool
		acceptable bool
	}{
		{"at_least 1 accepts true", `{"builds_wasm": {"direction": "at_least", "cap": 1}}`, true, true},
		{"at_least 1 refuses false", `{"builds_wasm": {"direction": "at_least", "cap": 1}}`, false, false},
		{"at_most 0 accepts false", `{"builds_wasm": {"direction": "at_most", "cap": 0}}`, false, true},
		{"at_most 0 refuses true", `{"builds_wasm": {"direction": "at_most", "cap": 0}}`, true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			terms(t, root, c.thresholds, noBaselines)
			read, err := LoadTerms(root)
			if err != nil {
				t.Fatal(err)
			}
			verdict, err := Judge(Envelope{
				Gate: "x", Target: HostTarget(),
				Metrics: []Measurement{Reported("builds_wasm", c.measured)},
			}, read, "build it for that target")
			if err != nil {
				t.Fatal(err)
			}
			if verdict.Acceptable != c.acceptable {
				t.Errorf("acceptable = %v, want %v: %s", verdict.Acceptable, c.acceptable, verdict.Detail)
			}
			// The detail says true or false, never the one and zero the
			// comparison ran on.
			if !c.acceptable && !strings.Contains(verdict.Detail, strconv.FormatBool(c.measured)) {
				t.Errorf("detail = %q, want it to state the property as it was measured", verdict.Detail)
			}
		})
	}
}

// A bool ratchet is the strictest kind there is: once a property holds, a later
// run saying it does not is a regression with no room to argue about degree.
func TestAPropertyRatchetsToTrueAndNeverBack(t *testing.T) {
	root := t.TempDir()
	terms(t, root, `{}`, `{"builds_wasm": {"direction": "at_least", "value": 0}}`)

	moved, err := Ratchet(root, []Envelope{{
		Gate: "builds", Target: HostTarget(),
		Metrics: []Measurement{Reported("builds_wasm", true)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(moved) != 1 || readBaseline(t, root, "builds_wasm") != 1 {
		t.Fatalf("the baseline is %v (%v), want the property recorded as holding",
			readBaseline(t, root, "builds_wasm"), moved)
	}

	moved, err = Ratchet(root, []Envelope{{
		Gate: "builds", Target: HostTarget(),
		Metrics: []Measurement{Reported("builds_wasm", false)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(moved) != 0 || readBaseline(t, root, "builds_wasm") != 1 {
		t.Errorf("a run that lost the property moved the baseline to %v (%v)",
			readBaseline(t, root, "builds_wasm"), moved)
	}
}

// Only verify moves a baseline, and only forward.
func TestARatchetMovesOnlyForward(t *testing.T) {
	root := t.TempDir()
	terms(t, root, `{}`, `{"cov": {"direction": "at_least", "value": 80}}`)

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

// The ratchet reads the baselines file itself, so the strict reading is its
// reading too. This is the path a project upgrading the pinned library arrives
// on with the former vocabulary still in the file, and it has to refuse: the
// comparison falls through to the one at_least makes, so a baseline written
// `up` would be moved and rewritten under a word the judge then will not read —
// a ratchet advanced past a term nobody can apply, which by construction never
// moves back.
func TestARatchetRefusesABaselineItCannotRead(t *testing.T) {
	root := t.TempDir()
	terms(t, root, `{}`, `{"cov": {"direction": "up", "value": 80}}`)

	moved, err := Ratchet(root, []Envelope{{
		Gate: "covered", Target: HostTarget(),
		Metrics: []Measurement{Quantity("cov", 91, "percent")},
	}})
	if err == nil {
		t.Fatalf("the ratchet answered %v over a baseline it cannot read", moved)
	}
	for _, says := range []string{BaselinesFile, `"up"`, `"at_least"`} {
		if !strings.Contains(err.Error(), says) {
			t.Errorf("the refusal is %q, want it to say %s", err, says)
		}
	}
	if got := readBaseline(t, root, "cov"); got != 80 {
		t.Errorf("the file was rewritten to %v", got)
	}
}

// An incomplete run moves nothing, and neither does a red one — a red run never
// reaches the ratchet stage, and an incomplete one is refused here.
func TestAnIncompleteRunMovesNoBaseline(t *testing.T) {
	root := t.TempDir()
	terms(t, root, `{}`, `{"cov": {"direction": "at_least", "value": 80}}`)

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
	terms(t, root, `{}`, `{"cov": {"direction": "at_least", "targets": {"linux/amd64": 81.9}}}`)

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
	write(t, root, filepath.FromSlash(ThresholdsFile), `{"n": {"direction": "at_most", "cap": 0}}`)
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
