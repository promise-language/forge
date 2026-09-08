// Command run is this project's judging layer: it answers whether a
// measurement is acceptable.
//
// `bin/run <gate> --verdict` is the fixed entry point a flow uses to ask.
// Fixed, and deliberately not configurable for the same reason `bin/gate` is:
// the SDK asks one way, so two callers asking about the same measurement cannot
// get different answers and both be right.
//
// The split matters. `bin/gate` MEASURES and states what it found; this decides
// whether what it found is good enough. The SDK runs both and reads neither —
// it never holds a project's numbers, and it never computes a verdict. That is
// what lets the terms live here, in the tree, where they are reviewed with the
// code they judge.
//
// The envelope arrives on stdin, exactly as `bin/gate --envelope` printed it.
//
// # The by-hand mode
//
// `bin/run <gate>`, with no --verdict, measures the gate itself and prints the
// verdict for a person: each measurement beside the term it was judged on. It
// is the path someone iterating on ONE failing area wants, and the flow's
// producing prompts now point agents at it by name.
//
// It does not weaken the split. The judging terms below are reached by both
// modes and stated once; the by-hand mode only removes the runner from the
// middle, which is safe precisely because nothing crosses a process boundary
// that a runner would have had to carry. The protocol mode remains --verdict,
// and it is the only one the SDK uses.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/promise-language/forge/tools/build/common"
)

// Injected by the meta-builder via -ldflags at build time; empty otherwise.
var (
	repoRoot   = ""
	sourceHash = ""
)

// verdict is what a caller must be handed. `acceptable` is the answer;
// `thresholds` are the terms it was judged against, carried so the decision can
// be recomputed by someone who was not there; `detail` is the reason a person
// needs.
type verdict struct {
	Acceptable bool   `json:"acceptable"`
	Thresholds any    `json:"thresholds"`
	Detail     string `json:"detail"`
}

func main() {
	common.CheckStale(repoRoot, sourceHash)

	var (
		args        []string
		wantVerdict bool
	)
	for _, a := range common.NormalizeArgs(os.Args[1:]) {
		if a == "-verdict" {
			wantVerdict = true
			continue
		}
		args = append(args, a)
	}
	if len(args) != 1 || args[0] == "-h" || args[0] == "-help" {
		usage()
		// A caller that asked for nothing gets a usage error, not a verdict:
		// exiting 0 here would let silence read as "acceptable".
		os.Exit(2)
	}
	gate := args[0]

	if !wantVerdict {
		byHand(gate)
		return
	}

	var envelope map[string]any
	if err := json.NewDecoder(os.Stdin).Decode(&envelope); err != nil {
		fmt.Fprintf(os.Stderr, "run: the envelope for gate %q could not be read: %v\n", gate, err)
		os.Exit(2)
	}
	if envelope == nil {
		fmt.Fprintf(os.Stderr, "run: the envelope for gate %q is null\n", gate)
		os.Exit(2)
	}

	v, err := judge(gate, envelope)
	if err != nil {
		fmt.Fprintln(os.Stderr, "run:", err)
		os.Exit(2)
	}
	if err := json.NewEncoder(os.Stdout).Encode(v); err != nil {
		fmt.Fprintln(os.Stderr, "run: could not write the verdict:", err)
		os.Exit(2)
	}
}

// judge applies this project's terms to one envelope.
//
// Stated in full: every gate this project answers is pass/fail, so the
// measurement is acceptable exactly when the gate measured no failure. There is
// nothing to tune, and saying so is better than implying a threshold that does
// not exist.
//
// A gate that grows a scalar — a coverage percentage, a leak count — gets its
// term added HERE, next to the others, rather than inside the gate that produced
// the number. That separation is the point of this binary: the thing that
// measures must not also decide whether it liked the answer.
//
// One function reached by both modes, so a by-hand answer and the answer the SDK
// records cannot differ. Two callers asking about the same measurement getting
// different verdicts is the exact failure the fixed entry point exists to
// prevent, and it would be no less a failure for happening inside one binary.
func judge(gate string, envelope map[string]any) (verdict, error) {
	measured, ok := envelope["measured"].(bool)
	if !ok {
		return verdict{}, fmt.Errorf(
			"the envelope for gate %q does not state `measured`, so there is nothing to judge", gate)
	}

	v := verdict{
		Acceptable: measured,
		Thresholds: map[string]any{"rule": "the " + gate + " gate must report no failure"},
		Detail:     fmt.Sprintf("the %s gate reported no failure", gate),
	}
	if !measured {
		v.Detail = fmt.Sprintf("the %s gate reported a failure", gate)
		if d, isStr := envelope["detail"].(string); isStr && d != "" {
			v.Detail += ": " + d
		}
	}
	return v, nil
}

// byHand measures the gate and reports the verdict to a person.
//
// It exits 1 on an unacceptable measurement so the mode is usable in a loop —
// `bin/run tested && ...` — and 2 only when the question could not be answered
// at all. That is the same distinction bin/gate draws: a gate that measured a
// failure HAS measured, and is not the same as one that could not run.
func byHand(gate string) {
	env, _ := common.MeasureGate(repoRoot, gate)
	v, err := judge(gate, env)
	if err != nil {
		fmt.Fprintln(os.Stderr, "run:", err)
		os.Exit(2)
	}

	// The measurement beside the term it was judged on. A verdict printed alone
	// tells someone iterating that they failed, not what they were held to.
	rule, _ := v.Thresholds.(map[string]any)["rule"].(string)
	fmt.Printf("%s\n\n", gate)
	fmt.Printf("  measured  %s\n", v.Detail)
	fmt.Printf("  term      %s\n", rule)
	if elapsed, ok := env["elapsed_seconds"].(float64); ok {
		fmt.Printf("  elapsed   %.1fs\n", elapsed)
	}
	if v.Acceptable {
		fmt.Printf("  verdict   acceptable\n")
		return
	}
	fmt.Printf("  verdict   NOT acceptable\n")
	os.Exit(1)
}

func usage() {
	fmt.Fprintf(os.Stderr, "usage: bin/run <gate>            measure the gate and judge it\n"+
		"       bin/run <gate> --verdict  judge an envelope on stdin\n\n"+
		"Gates this project answers:\n  %s\n\n",
		strings.Join(common.GateNames(), "\n  "))
	fmt.Fprint(os.Stderr, "A name is a concept, optionally with an instance: `tested:tools-build`.\n"+
		"Omitting the instance measures every module.\n\n"+
		"With --verdict it decides only, reading the envelope `bin/gate <gate>\n"+
		"--envelope` printed — the mode the SDK uses. Without it, this measures\n"+
		"the gate itself and prints each measurement beside the term it was\n"+
		"judged on.\n\n"+
		"Passing one gate is not passing the whole: only bin/verify confirms the\n"+
		"full set.\n")
}
