package common

// Running one gate by hand, and reaching a verdict from what it measured.
//
// This is the judging layer, and it is a DIFFERENT PROGRAM from the gates on
// purpose. A gate that held its own thresholds could be made to pass by
// editing the gate — and when the thing being measured is a change written by
// an agent, the agent can edit it. The party under judgement must not hold
// what judges it.
//
// It is also the only layer that can render a result for a person: it holds
// the caps, so it can print a number beside the terms it was judged on. A gate
// could only ever print the left-hand column.
//
// It has TWO MODES, and the difference is who ran the gate:
//
//   - `run <gate>` measures and then judges. A person at a terminal, and no
//     decision rests on it — a result handed over by the measured party is a
//     claim, not a measurement.
//   - `run <gate> --verdict` judges an envelope it was GIVEN, on stdin, and
//     spawns nothing. This is the one the SDK asks, and it is what keeps the
//     SDK in the runner's seat: an entry point that ran the gate itself would
//     be the runner, and the runner may not come from the tree.
//
// Both reach the verdict through the same comparison, so they cannot disagree
// about what this project allows.

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Direction is the sense in which a measurement is compared to its threshold.
// The set is closed: unknown values are refused at load time.
type Direction string

const (
	AtMost  Direction = "at_most"  // value must not exceed cap (counts of bad things)
	AtLeast Direction = "at_least" // value must not fall below cap (floors like coverage)
)

// Threshold is one entry in the thresholds manifest.
type Threshold struct {
	Direction Direction `json:"direction"`
	Cap       float64   `json:"cap"`
}

// ManifestFile is the thresholds manifest, versioned with the tree it judges.
// A project with a judge must have one, and the path is fixed rather than
// configurable: it is what lets something outside the project establish that
// the terms are an artefact distinct from the judge (blueprint.md, The judge and the terms).
const ManifestFile = "tools/gates/thresholds.json"

// loadManifest reads the thresholds manifest from a repo root. An absent file
// is an error — a project with a judge must have a manifest. An unknown
// direction value is an error — the set is closed.
func loadManifest(repoRoot string) (map[string]Threshold, error) {
	data, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(ManifestFile)))
	if err != nil {
		return nil, fmt.Errorf("loading thresholds manifest: %w", err)
	}
	var manifest map[string]Threshold
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("parsing thresholds manifest: %w", err)
	}
	for name, t := range manifest {
		switch t.Direction {
		case AtMost, AtLeast:
			// known
		default:
			return nil, fmt.Errorf("thresholds manifest: metric %q has unknown direction %q (must be %q or %q)",
				name, t.Direction, AtMost, AtLeast)
		}
	}
	return manifest, nil
}

// RunOneGate measures one gate the way a runner would — by executing the gate
// program, not by calling into it — then judges what came back and prints it.
//
// Going through the process boundary is the point. This is the same path the
// SDK takes, so a gate that is broken in a way only visible across that
// boundary (prints to stdout, exits without an envelope, hangs) is broken here
// too, where a person can see it.
func RunOneGate(repoRoot, gateBin, name string, narrate io.Writer) (Judged, error) {
	if !KnownGate(repoRoot, name) {
		return Judged{}, unknownGate(repoRoot, name)
	}

	cmd := exec.Command(gateBin, name, "--envelope")
	cmd.Dir = repoRoot
	// The gate's progress goes straight to the narration — not into a buffer
	// printed afterwards. Gates run for minutes, and a gate that is working and
	// a gate that is wedged produce the same thing (nothing) for as long as the
	// output is held.
	cmd.Stderr = narrate
	out, runErr := cmd.Output()

	var env Envelope
	if jsonErr := json.Unmarshal(out, &env); jsonErr != nil {
		// No readable envelope. Whether the process died or printed something
		// that is not an envelope, nothing was measured — and either way this
		// is not a report that the tree is bad.
		if runErr != nil {
			return Judged{}, fmt.Errorf("%s did not measure anything: %w", name, runErr)
		}
		return Judged{}, fmt.Errorf("%s printed something that is not an envelope: %s",
			name, firstLine(string(out)))
	}

	manifest, err := loadManifest(repoRoot)
	if err != nil {
		return Judged{}, err
	}
	acceptable, thresholds, detail := judge(env, manifest)
	return Judged{
		Envelope: env,
		Verdict:  Verdict{Acceptable: acceptable, Thresholds: thresholds, Detail: detail},
		rendered: renderVerdict(env, manifest),
	}, nil
}

// Judged is a measurement and the verdict reached on it: what `run <gate>`
// answers (docs/project-tools.md, Run). The verdict travels with the terms it
// was reached from, so a reader who was not there can re-check it.
type Judged struct {
	Envelope Envelope `json:"envelope"`
	Verdict  Verdict  `json:"verdict"`

	// rendered is the human form: each measurement beside every term it was
	// judged on, which is what someone iterating on one failure is reading.
	rendered string
}

// Human writes each measurement beside the term it was judged on.
func (j Judged) Human(w io.Writer) error {
	if _, err := io.WriteString(w, j.rendered); err != nil {
		return err
	}
	if j.Verdict.Acceptable || j.Verdict.Detail == "" {
		return nil
	}
	_, err := fmt.Fprintf(w, "%s\n", j.Verdict.Detail)
	return err
}

// ExitStatus is 0 when every capped measurement is within its cap and 1 when
// one is not. The verdict is the JSON, not the status — but a person running
// one gate reads the status, and a measurement over its cap is a condition they
// must clear.
func (j Judged) ExitStatus() int {
	if j.Verdict.Acceptable {
		return 0
	}
	return 1
}

// judge compares one envelope against the caps and reaches the verdict.
//
// This is the ONLY comparison in this program. The human path and the wire
// path both come through here, because two paths reaching a verdict separately
// would eventually disagree — and a project that answers "acceptable" to a
// runner and prints a failure to a person has two thresholds wearing one name.
//
// It returns the terms it actually applied, not the whole table: a verdict has
// to travel with what it was reached from, or nobody can re-check it, and the
// caps a measurement never mentioned had no part in it.
//
// A metric nothing caps cannot fail, and an incomplete run cannot pass: honest
// numbers that understate what was checked must not be read as a good result.
func judge(env Envelope, manifest map[string]Threshold) (acceptable bool, thresholds map[string]float64, detail string) {
	thresholds = map[string]float64{}
	var exceeded []string
	for _, m := range env.Metrics {
		t, capped := manifest[m.Name]
		if !capped {
			continue
		}
		thresholds[m.Name] = t.Cap
		var over bool
		switch t.Direction {
		case AtMost:
			over = m.Number() > t.Cap
		case AtLeast:
			over = m.Number() < t.Cap
		}
		if over {
			exceeded = append(exceeded, fmt.Sprintf("%s is %s, %s %s", m.Name, m.String(), t.Direction, number(t.Cap)))
		}
	}
	switch {
	case env.Incomplete != "":
		// Refused even when every number is within its cap. The numbers are
		// honest and describe less than a full run, which is indistinguishable
		// from an improvement unless the run says so.
		return false, thresholds, "the run measured less than a full one, and an incomplete run is never a pass: " + env.Incomplete
	case len(exceeded) > 0:
		return false, thresholds, strings.Join(exceeded, "; ")
	case len(thresholds) == 0:
		return true, thresholds, "nothing here is judged: no metric this gate reported has a threshold"
	default:
		return true, thresholds, "every judged metric is within its cap"
	}
}

// JudgeStdin judges an envelope this program did NOT produce, and writes one
// verdict object to out.
//
// Reading the measurement rather than making it is the whole point of this
// mode. The SDK spawned the gate, so the SDK is the runner; if this entry
// point ran the gate itself, the runner would be a tree artifact, and a runner
// is the one party whose account of a vanished process nothing can check.
//
// Nothing reaches out on any error path. A caller reads one object or none —
// a half-written verdict beside an error message is a second channel, and the
// two could disagree.
func JudgeStdin(repoRoot, name string, in io.Reader) (Verdict, error) {
	if !KnownGate(repoRoot, name) {
		return Verdict{}, unknownGate(repoRoot, name)
	}
	envelope, err := io.ReadAll(in)
	if err != nil {
		return Verdict{}, fmt.Errorf("reading the envelope to judge: %w", err)
	}
	var env Envelope
	if err := json.Unmarshal(envelope, &env); err != nil {
		return Verdict{}, fmt.Errorf("what arrived on stdin is not an envelope: %w", err)
	}
	// The judge's check, and not the SDK's: only this layer knows which gate
	// the terms it is about to apply belong to. Judging one gate's numbers
	// against another's caps would answer a question nobody asked.
	if env.Gate != name {
		return Verdict{}, fmt.Errorf("asked to judge %q against the terms for %q; a measurement is judged against its own gate's caps", env.Gate, name)
	}
	manifest, err := loadManifest(repoRoot)
	if err != nil {
		return Verdict{}, err
	}
	acceptable, thresholds, detail := judge(env, manifest)
	return Verdict{Acceptable: acceptable, Thresholds: thresholds, Detail: detail}, nil
}

// Verdict is what the judging layer answers in --verdict mode: one JSON object,
// whole. The library writes it — marshalled before anything reaches stdout, so
// a failure there leaves the stream untouched rather than half a verdict.
//
// Thresholds is not omitempty and is never nil. A verdict handed over with the
// terms it was reached from discarded cannot be re-checked by anyone who was
// not there, which is exactly the property that lets a judge live in the tree
// it judges.
type Verdict struct {
	Acceptable bool               `json:"acceptable"`
	Thresholds map[string]float64 `json:"thresholds"`
	Detail     string             `json:"detail,omitempty"`
}

// Human is not reached: --verdict's stdout belongs to the gate contract, so the
// command has one mode and the library never asks for a rendering. Saying so
// loudly is what keeps a future caller from inventing a second shape for a
// wire another document owns.
func (v Verdict) Human(io.Writer) error {
	return fmt.Errorf("a verdict's shape is gate-contract.md's, and it has no rendering for a person")
}

// renderVerdict prints each measurement beside the term it was judged on.
func renderVerdict(env Envelope, manifest map[string]Threshold) string {
	var sb strings.Builder
	width := 0
	for _, m := range env.Metrics {
		if len(m.Name) > width {
			width = len(m.Name)
		}
	}
	for _, m := range env.Metrics {
		t, capped := manifest[m.Name]
		judged, mark := "not judged", " "
		if capped {
			judged = string(t.Direction) + " " + number(t.Cap)
			mark = "✗"
			switch t.Direction {
			case AtMost:
				if m.Number() <= t.Cap {
					mark = "✓"
				}
			case AtLeast:
				if m.Number() >= t.Cap {
					mark = "✓"
				}
			}
		}
		fmt.Fprintf(&sb, "  %-*s  %8s  %-16s %s\n",
			width, m.Name, m.String()+unitSuffix(m.Unit), judged, mark)
	}
	if env.Incomplete != "" {
		fmt.Fprintf(&sb, "\n  incomplete: %s\n", env.Incomplete)
		fmt.Fprintf(&sb, "  an incomplete run is never a pass, and never moves a baseline\n")
	}
	return sb.String()
}

func number(v float64) string {
	if v == float64(int64(v)) {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'f', 1, 64)
}

func unitSuffix(unit string) string {
	switch unit {
	case "percent":
		return "%"
	case "bytes":
		return " B"
	}
	return ""
}

// GateBinary is where a project's gate program lives, relative to the repo
// root. Not configurable: the names are fixed, so the way to reach them is.
func GateBinary(repoRoot string) string {
	return filepath.Join(repoRoot, "bin", "gate")
}

// CappedMetrics lists the metrics this layer judges, for usage text. If the
// manifest cannot be loaded (empty repoRoot), it returns an empty list — this
// is usage text only, not a judging path.
func CappedMetrics(repoRoot string) []string {
	if repoRoot == "" {
		return nil
	}
	manifest, err := loadManifest(repoRoot)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(manifest))
	for n := range manifest {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
