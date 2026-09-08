package common

import (
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
	if err := RunOneGate(dir, asSubprocess(t, "gate-clean"), "tested"); err != nil {
		t.Errorf("a measurement inside every cap was refused: %v", err)
	}
}

// The failure has to name the metric, its value and the term, or the person
// reading it has to re-run the gate to learn what went wrong.
func TestRunOneGateRefusesAMeasurementOverItsCap(t *testing.T) {
	dir := writeManifest(t, testedManifest)
	err := RunOneGate(dir, asSubprocess(t, "gate-over-cap"), "tested")
	if err == nil {
		t.Fatal("a measurement over its cap was accepted")
	}
	for _, want := range []string{"tested", "failed_tests", "3"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not name %q", err, want)
		}
	}
}

// Output that is not an envelope is not a bad result — nothing was measured,
// and the two must not be reported the same way.
func TestRunOneGateReportsOutputThatIsNotAnEnvelope(t *testing.T) {
	dir := writeManifest(t, testedManifest)
	err := RunOneGate(dir, asSubprocess(t, "gate-not-an-envelope"), "tested")
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
	err := RunOneGate(dir, asSubprocess(t, "gate-dies"), "tested")
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
	err := RunOneGate(dir, asSubprocess(t, "gate-clean"), "tested")
	if err == nil {
		t.Fatal("a gate was judged with no thresholds manifest")
	}
	if !strings.Contains(err.Error(), ManifestFile) {
		t.Errorf("the error %q does not name the manifest it could not load", err)
	}
}

// An unknown name is refused before anything is spawned: a runner asking for a
// gate this project does not have must learn that.
func TestRunOneGateRefusesAnUnknownName(t *testing.T) {
	dir := writeManifest(t, testedManifest)
	// A binary that does not exist, so a name that reached the process
	// boundary would fail differently and visibly.
	absent := filepath.Join(dir, "no-such-gate")
	err := RunOneGate(dir, absent, "not-a-gate")
	if err == nil {
		t.Fatal("an unknown gate name was measured")
	}
	if !strings.Contains(err.Error(), "not-a-gate") {
		t.Errorf("the refusal %q does not name the gate that was asked for", err)
	}
}
