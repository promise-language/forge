package tooling

// Run (docs/project-tools.md).
//
// The judging layer, and it is a different program from the gates on purpose. A
// gate that held its own terms could be made to pass by editing the gate — and
// when the thing being measured is a change written by an agent, the agent can
// edit it. The party under judgement must not hold what judges it.
//
// It has two modes, and the difference is who ran the gate:
//
//   - `run <gate>` measures and then judges. It executes bin/gate as a process,
//     so a gate broken in a way only visible across that boundary is broken
//     where a person can see it.
//   - `run <gate> --verdict` judges an envelope it was given, on stdin, and
//     spawns nothing. That is the mode the SDK asks, and it is what keeps the
//     SDK in the runner's seat.
//
// Both reach the verdict through the same comparison, so they cannot disagree
// about what this project allows.

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/promise-language/forge/primitives"
	"github.com/promise-language/forge/primitives/command"
)

// RunListing is what `run --list` answers: the two kinds of name a caller
// outside this tree can ask this project for — a command it can execute, and a
// gate it can measure. Both are discovered rather than declared, so neither can
// go stale, and one object carries them together because a caller that must not
// confuse the two needs to see the whole vocabulary at once.
type RunListing struct {
	Commands []string `json:"commands"`
	Gates    []string `json:"gates"`
}

// Human is two labelled groups, because the two lists answer different
// questions — what this project builds, and what it answers — and a reader who
// cannot tell which is which has to know the vocabulary already to use the
// output that exists to teach it.
func (l RunListing) Human(w io.Writer) error {
	for _, c := range l.Commands {
		if _, err := fmt.Fprintf(w, "command  %s\n", c); err != nil {
			return err
		}
	}
	for _, g := range l.Gates {
		if _, err := fmt.Fprintf(w, "gate     %s\n", g); err != nil {
			return err
		}
	}
	return nil
}

// List says what this project builds and what it answers.
//
// It says what the project builds, not what is built: a name it reports whose
// binary is absent is the diagnosable state the listing exists to expose, so
// the listing never refuses over one. Only dispatching to that name does.
func List(r *Run) (RunListing, error) {
	commands, err := BuildSet(r.Root)
	if err != nil {
		return RunListing{}, fmt.Errorf("cannot say what this project builds: %w", err)
	}
	gates, err := GateNames(r)
	if err != nil {
		return RunListing{}, err
	}
	return RunListing{Commands: commands, Gates: gates}, nil
}

// Judged is a measurement and the verdict reached on it: what `run <gate>`
// answers. The verdict travels with the terms it was reached from, so a reader
// who was not there can re-check it.
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
	_, err := fmt.Fprintf(w, "\n%s\n", j.Verdict.Detail)
	return err
}

// ExitStatus is 0 when the verdict is acceptable and 1 when it is not. The
// verdict is the JSON, not the status — but a person running one gate reads the
// status, and a measurement beyond its term is a condition they must clear.
func (j Judged) ExitStatus() int {
	if j.Verdict.Acceptable {
		return 0
	}
	return 1
}

// GateBinary is where a project's gate program lives. Not configurable: the
// names are fixed, so the way to reach them is.
func GateBinary(root string) string {
	return filepath.Join(root, "bin", primitives.BinaryName("gate"))
}

// MeasureAndJudge measures one gate the way a runner would — by executing the
// gate program, not by calling into it — then judges what came back.
//
// A refusal from a child is relayed, not reinterpreted: a gate that declined to
// run has measured nothing, and reporting that as the tree's failure would name
// a repair that is not the repair.
func MeasureAndJudge(r *Run, name string) (Judged, *command.Refusal, error) {
	if !KnownGate(r, name) {
		return Judged{}, nil, unknownGate(r, name)
	}
	binary := GateBinary(r.Root)
	if _, err := os.Stat(binary); err != nil {
		return Judged{}, &command.Refusal{
			Refusal:  command.Unbuilt,
			Tool:     "run",
			Detail:   fmt.Sprintf("this project builds gate and %s is not there", binary),
			Recovery: primitives.Recovery(),
		}, nil
	}

	// The gate's progress goes straight to the narration — not into a buffer
	// printed afterwards. Gates run for minutes, and a gate that is working and
	// a gate that is wedged produce the same thing (nothing) for as long as the
	// output is held.
	out, status, err := r.child(binary, name, "--envelope")
	if err != nil {
		return Judged{}, nil, fmt.Errorf("%s did not run: %w", name, err)
	}
	if status == command.StatusRefused {
		return Judged{}, &command.Refusal{
			Refusal:  command.Stale,
			Tool:     "gate",
			Detail:   "the gate declined to run, so nothing about this tree was measured",
			Recovery: primitives.Recovery(),
		}, nil
	}

	var env Envelope
	if jsonErr := json.Unmarshal([]byte(out), &env); jsonErr != nil {
		// No readable envelope. Whether the process died or printed something
		// that is not an envelope, nothing was measured — and either way this
		// is not a report that the tree is bad.
		if status != 0 {
			return Judged{}, nil, fmt.Errorf("%s did not measure anything (it exited %d)", name, status)
		}
		return Judged{}, nil, fmt.Errorf("%s printed something that is not an envelope: %s", name, firstLine(out))
	}
	judged, err := judgeEnvelope(r, name, env)
	return judged, nil, err
}

// JudgeStdin judges an envelope this program did not produce.
//
// Reading the measurement rather than making it is the whole point of this
// mode. The SDK spawned the gate, so the SDK is the runner; if this entry point
// ran the gate itself, the runner would be a tree artifact, and a runner is the
// one party whose account of a vanished process nothing can check.
//
// Nothing reaches stdout on any error path. A caller reads one object or none —
// a half-written verdict beside an error message is a second channel, and the
// two could disagree.
func JudgeStdin(r *Run, name string, in io.Reader) (Verdict, error) {
	if !KnownGate(r, name) {
		return Verdict{}, unknownGate(r, name)
	}
	body, err := io.ReadAll(in)
	if err != nil {
		return Verdict{}, fmt.Errorf("reading the envelope to judge: %w", err)
	}
	var env Envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return Verdict{}, fmt.Errorf("what arrived on stdin is not an envelope: %w", err)
	}
	// The judge's check, and not the SDK's: only this layer knows which gate
	// the terms it is about to apply belong to. Judging one gate's numbers
	// against another's terms answers a question nobody asked.
	if env.Gate != name {
		return Verdict{}, fmt.Errorf("asked to judge %q against the terms for %q; a measurement is judged against its own gate's terms", env.Gate, name)
	}
	judged, err := judgeEnvelope(r, name, env)
	if err != nil {
		return Verdict{}, err
	}
	return judged.Verdict, nil
}

// judgeEnvelope is the one path to a verdict, so the two modes cannot disagree.
func judgeEnvelope(r *Run, name string, env Envelope) (Judged, error) {
	terms, err := LoadTerms(r.Root)
	if err != nil {
		return Judged{}, err
	}
	g, _, err := resolveGate(r, name)
	if err != nil {
		return Judged{}, err
	}
	verdict, err := Judge(env, terms, g.Remediation)
	if err != nil {
		return Judged{}, err
	}
	return Judged{Envelope: env, Verdict: verdict, rendered: render(env, terms)}, nil
}

// render prints each measurement beside every term it was judged on.
func render(env Envelope, terms Terms) string {
	var b strings.Builder
	width := 0
	for _, m := range env.Metrics {
		if len(m.Name) > width {
			width = len(m.Name)
		}
	}
	for _, m := range env.Metrics {
		judged, mark := "not judged", " "
		if c, ok := terms.Caps[m.Name]; ok {
			judged = fmt.Sprintf("cap %s %s", c.Direction, number(*c.Cap))
			mark = passed(!beyond(m.Number(), *c.Cap, c.Direction))
		}
		if b, ok := terms.Baselines[m.Name]; ok {
			if floor, recorded := b.For(env.Target); recorded {
				judged = fmt.Sprintf("baseline %s %s", b.Direction, number(floor))
				mark = passed(!beyond(m.Number(), floor, b.Direction))
			}
		}
		fmt.Fprintf(&b, "  %-*s  %10s  %-24s %s\n", width, m.Name, m.String()+unitSuffix(m.Unit), judged, mark)
	}
	if env.Incomplete != "" {
		fmt.Fprintf(&b, "\n  incomplete: %s\n", env.Incomplete)
		fmt.Fprintf(&b, "  an incomplete run is never a pass, and never moves a baseline\n")
	}
	return b.String()
}

func passed(ok bool) string {
	if ok {
		return "✓"
	}
	return "✗"
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
