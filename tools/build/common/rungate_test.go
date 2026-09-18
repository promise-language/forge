package common

import (
	"io"
	"path/filepath"
	"strings"
	"testing"
)

// RunOneGate goes through the process boundary on purpose, so a test of it has
// to as well: this binary re-executes itself as the gate (see
// subprocess_test.go). Nothing below would be exercised by calling the
// measurement directly.

func TestRunOneGateAcceptsAMeasurementWithinItsCap(t *testing.T) {
	dir := writeManifest(t, testedManifest)
	judged, err := RunOneGate(dir, asSubprocess(t, "gate-clean"), "tested", io.Discard)
	if err != nil {
		t.Fatalf("a measurement inside every cap could not be reached: %v", err)
	}
	if !judged.Verdict.Acceptable || judged.ExitStatus() != 0 {
		t.Errorf("a measurement inside every cap was refused: %+v", judged.Verdict)
	}
}

// A measurement over its cap is an answer rather than an error: the result is
// written, and the status says the answer was no. The detail has to name the
// metric, its value and the term, or the person reading it has to re-run the
// gate to learn what went wrong.
func TestRunOneGateRefusesAMeasurementOverItsCap(t *testing.T) {
	dir := writeManifest(t, testedManifest)
	judged, err := RunOneGate(dir, asSubprocess(t, "gate-over-cap"), "tested", io.Discard)
	if err != nil {
		t.Fatalf("a measurement over its cap could not be reached: %v", err)
	}
	if judged.Verdict.Acceptable {
		t.Fatal("a measurement over its cap was accepted")
	}
	if judged.ExitStatus() != 1 {
		t.Errorf("exit status %d for a measurement over its cap, want 1", judged.ExitStatus())
	}
	for _, want := range []string{"failed_tests", "3"} {
		if !strings.Contains(judged.Verdict.Detail, want) {
			t.Errorf("the detail %q does not name %q", judged.Verdict.Detail, want)
		}
	}
}

// Output that is not an envelope is not a bad result — nothing was measured,
// and the two must not be reported the same way.
func TestRunOneGateReportsOutputThatIsNotAnEnvelope(t *testing.T) {
	dir := writeManifest(t, testedManifest)
	_, err := RunOneGate(dir, asSubprocess(t, "gate-not-an-envelope"), "tested", io.Discard)
	if err == nil {
		t.Fatal("stdout that is not an envelope was read as a measurement")
	}
	if !strings.Contains(err.Error(), "not an envelope") {
		t.Errorf("the error %q does not say the output was not an envelope", err)
	}
	// The first line is quoted so the reader sees what arrived, and only the
	// first, so a gate that printed a screenful does not become the error.
	if !strings.Contains(err.Error(), "measuring...") || strings.Contains(err.Error(), "still measuring") {
		t.Errorf("the error %q does not quote exactly the first line of what arrived", err)
	}
}

func TestRunOneGateReportsAGateThatDied(t *testing.T) {
	dir := writeManifest(t, testedManifest)
	_, err := RunOneGate(dir, asSubprocess(t, "gate-dies"), "tested", io.Discard)
	if err == nil {
		t.Fatal("a gate that exited without measuring was read as a pass")
	}
	if !strings.Contains(err.Error(), "did not measure anything") {
		t.Errorf("the error %q does not say nothing was measured", err)
	}
}

// A gate that measured cannot be judged without the terms, and the answer is an
// error rather than a verdict reached against no caps.
func TestRunOneGateNeedsTheManifest(t *testing.T) {
	dir := t.TempDir() // no thresholds.json
	_, err := RunOneGate(dir, asSubprocess(t, "gate-clean"), "tested", io.Discard)
	if err == nil {
		t.Fatal("a gate was judged with no thresholds manifest")
	}
	if !strings.Contains(err.Error(), ManifestFile) {
		t.Errorf("the error %q does not name the manifest it could not load", err)
	}
}

// WHAT A PERSON READS IS EACH MEASUREMENT BESIDE THE TERM IT WAS JUDGED ON
// (docs/project-tools.md, Run). That rendering used to be printed from inside
// RunOneGate; it now travels out on the result and the library renders it, so
// it reaches a stream only through Judged.Human. The field carrying it is
// unexported and filled in one place — dropping it from that literal leaves
// `bin/run <gate>` printing nothing a terminal, with the verdict still right
// and every other test in this file still green.
func TestJudgedHumanShowsEveryMeasurementBesideItsTerm(t *testing.T) {
	dir := writeManifest(t, testedManifest)

	judged, err := RunOneGate(dir, asSubprocess(t, "gate-clean"), "tested", io.Discard)
	if err != nil {
		t.Fatalf("a measurement inside every cap could not be reached: %v", err)
	}
	var shown strings.Builder
	if err := judged.Human(&shown); err != nil {
		t.Fatalf("rendering an acceptable measurement: %v", err)
	}
	for _, want := range []string{"failed_tests", "at_most 0", "✓"} {
		if !strings.Contains(shown.String(), want) {
			t.Errorf("the rendering does not say %q:\n%s", want, shown.String())
		}
	}

	// And a measurement over its cap says so where the person is looking,
	// rather than only in the status they have to notice.
	over, err := RunOneGate(dir, asSubprocess(t, "gate-over-cap"), "tested", io.Discard)
	if err != nil {
		t.Fatalf("a measurement over its cap could not be reached: %v", err)
	}
	shown.Reset()
	if err := over.Human(&shown); err != nil {
		t.Fatalf("rendering a measurement over its cap: %v", err)
	}
	for _, want := range []string{"failed_tests", "✗", over.Verdict.Detail} {
		if !strings.Contains(shown.String(), want) {
			t.Errorf("the rendering does not say %q:\n%s", want, shown.String())
		}
	}
}

// An unknown name is refused before anything is spawned: a runner asking for a
// gate this project does not have must learn that.
func TestRunOneGateRefusesAnUnknownName(t *testing.T) {
	dir := writeManifest(t, testedManifest)
	// A binary that does not exist, so a name that reached the process
	// boundary would fail differently and visibly.
	absent := filepath.Join(dir, "no-such-gate")
	_, err := RunOneGate(dir, absent, "not-a-gate", io.Discard)
	if err == nil {
		t.Fatal("an unknown gate name was measured")
	}
	if !strings.Contains(err.Error(), "not-a-gate") {
		t.Errorf("the refusal %q does not name the gate that was asked for", err)
	}
}
