// Command gate runs one named gate against this repository.
//
// `bin/gate <name>` is the fixed entry point a flow uses to ask for a
// measurement. Fixed, and deliberately not configurable: the protocol addresses
// gates by name, so a project spelling its entry point differently would have
// gates nothing could ask for.
//
// A gate measures and modifies nothing — including afterwards. That is what
// separates it from `bin/verify`, which is a command: verify repairs what has
// one right answer on its way to an answer, which is what a producing step
// wants and exactly why a landing decision may not rest on it.
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

func main() {
	common.CheckStale(repoRoot, sourceHash)

	// --envelope is protocol, not configuration: a runner asks for machine-
	// readable measurements with it, and every other invocation is a person at
	// a terminal. It is stripped here so the name reaches the same place it
	// would have without it.
	var (
		args     []string
		envelope bool
	)
	for _, a := range common.NormalizeArgs(os.Args[1:]) {
		if a == "-envelope" {
			envelope = true
			continue
		}
		args = append(args, a)
	}
	// -list is protocol too: the flow SDK discovers what this project's gates
	// are by asking the entry point (one name per line on stdout), because the
	// entry point is the only party whose answer cannot drift from what a run
	// would find. It is answered before the single-name rule below: the query
	// takes no gate name, and refusing it reads as a machine with no gates.
	if len(args) == 1 && args[0] == "-list" {
		fmt.Println(strings.Join(common.GateNames(), "\n"))
		return
	}
	if len(args) != 1 || args[0] == "-h" || args[0] == "-help" {
		usage()
		// No argument is a usage error, not a passing gate: exiting 0 here would
		// let a caller that forgot the name read silence as success.
		os.Exit(2)
	}

	// A bare invocation is refused, and does not measure. Any call without the
	// flag is a person or an agent at a terminal, and this program is not a
	// channel for them: gates-and-commands.md requires it to print nothing on
	// stdout and exit non-zero, WHATEVER the gate would have found.
	//
	// Exiting 0 on a passing gate is the precise ambiguity the three parties
	// exist to remove — the first script to wrap `bin/gate tested` reads that 0
	// as a pass, and a gate has no verdict to give. Refusing before measuring
	// rather than measuring and then failing keeps the two readings from ever
	// coexisting, and costs a person nothing: `bin/run <name>` is their path,
	// and it is the one that judges.
	if !envelope {
		fmt.Fprintf(os.Stderr, "gate: refusing to measure %q without --envelope; "+
			"run `bin/run %s` for a result meant for a person\n", args[0], args[0])
		os.Exit(1)
	}

	// Envelope mode. common.MeasureGate builds the document and keeps the gate's
	// own progress off stdout, so what follows carries the envelope and nothing
	// else. It is shared with `bin/run <gate>`, which judges the same envelope
	// without a runner in between.
	env, gerr := common.MeasureGate(repoRoot, args[0])
	if err := json.NewEncoder(os.Stdout).Encode(env); err != nil {
		// The envelope is the whole point of this mode: a run that cannot state
		// what it measured has not measured anything a caller may act on.
		fmt.Fprintln(os.Stderr, "gate: could not write the envelope:", err)
		os.Exit(2)
	}
	// A gate that measured a failure and said so exits non-zero and HAS
	// measured — the runner records this code and decides nothing with it.
	if gerr != nil {
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, "usage: bin/gate <name>\n\nGates this project answers:\n  %s\n\n",
		strings.Join(common.GateNames(), "\n  "))
	fmt.Fprint(os.Stderr, "A name is a concept, optionally with an instance: `tested:tools`.\n"+
		"Omitting the instance measures every module.\n\n"+
		"`integration` is the composition a landing decision rests on.\n"+
		"For the repairing counterpart, see bin/verify — it is a command, not a gate.\n")
}
