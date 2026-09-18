//go:build !windows

package tooling

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/promise-language/forge/primitives/command"
)

// `run <gate>` executes bin/gate as a process, so a gate broken in a way only
// visible across that boundary is broken where a person can see it. These
// stand a script in for that binary, because what is under test is what the
// runner does with what came back rather than what the gate measured.
func TestTheRunnerCrossesTheProcessBoundary(t *testing.T) {
	envelope := `{"gate":"x","target":"` + HostTarget() + `","metrics":[{"name":"n","type":"int","value":2}]}`

	for _, c := range []struct {
		name    string
		gate    string
		wantErr string
		refusal string
	}{
		{
			name: "a measurement is judged against this project's terms",
			gate: "printf '%s' '" + envelope + "'",
		},
		{
			// A refusal from a child is relayed, not reinterpreted: a gate that
			// declined to run has measured nothing, and reporting that as the
			// tree's failure would name a repair that is not the repair.
			name:    "a child's refusal is relayed rather than reported as a failure",
			gate:    "echo 'gate: the tools source has changed' >&2; exit 3",
			refusal: command.Stale,
		},
		{
			name:    "a gate that printed something that is not an envelope",
			gate:    "echo 'I am not an envelope'",
			wantErr: "not an envelope",
		},
		{
			name:    "a gate that died having written nothing",
			gate:    "exit 1",
			wantErr: "did not measure anything",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := fixture(t, "")
			terms(t, root, `{"n": {"direction": "down", "cap": 0}}`, `{}`)
			standIn(t, root, c.gate)
			p := counting("x", []Metric{Count("n")}, Measured{}, nil)

			r, _ := run(t, p, root)
			judged, refusal, err := MeasureAndJudge(r, "x")

			switch {
			case c.wantErr != "":
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Errorf("err = %v, want one saying %q", err, c.wantErr)
				}
			case c.refusal != "":
				if err != nil {
					t.Fatalf("a refusal came back as an error: %v", err)
				}
				if refusal == nil {
					t.Fatal("a gate that declined to run was not relayed as a refusal")
				}
				if refusal.Refusal != c.refusal || len(refusal.Recovery) == 0 {
					t.Errorf("refusal = %+v, want %q with the recovery named", refusal, c.refusal)
				}
			default:
				if err != nil || refusal != nil {
					t.Fatalf("judging a measurement gave (%v, %+v)", err, refusal)
				}
				if judged.Verdict.Acceptable {
					t.Error("n is 2 against a cap of 0, and the verdict is acceptable")
				}
				if judged.ExitStatus() != 1 {
					t.Errorf("status = %d, want 1 when the verdict is not acceptable", judged.ExitStatus())
				}
				if judged.Envelope.Metrics[0].Int != 2 {
					t.Errorf("the envelope came back as %+v", judged.Envelope)
				}
			}
		})
	}
}

// The listing says what the project builds, not what is built, so `run <gate>`
// refuses only when it actually has to reach the binary.
func TestTheRunnerRefusesWhenTheGateIsNotBuilt(t *testing.T) {
	root := fixture(t, "")
	terms(t, root, `{"n": {"direction": "down", "cap": 0}}`, `{}`)
	p := counting("x", []Metric{Count("n")}, Measured{}, nil)

	r, _ := run(t, p, root)
	_, refusal, err := MeasureAndJudge(r, "x")
	if err != nil {
		t.Fatalf("an absent binary came back as an error: %v", err)
	}
	if refusal == nil || refusal.Refusal != command.Unbuilt {
		t.Errorf("refusal = %+v, want %q", refusal, command.Unbuilt)
	}

	// And a name this project does not answer is refused before anything is
	// spawned, whatever is or is not in bin/.
	if _, _, err := MeasureAndJudge(r, "nowhere"); err == nil {
		t.Error("a gate this project does not answer was measured")
	}
}

// standIn puts a script where the gate binary goes. Nothing here compiles a
// gate: what is under test is the runner's reading of a child, and a child that
// writes exactly what the case needs is the only way to reach the readings a
// real gate reaches rarely or never.
//
// The file's build constraint is what keeps the script off Windows, which has
// no /bin/sh to run it with.
func standIn(t *testing.T, root, body string) {
	t.Helper()
	path := GateBinary(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}
