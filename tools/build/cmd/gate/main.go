// Command gate measures one property of this tree and prints what it found.
//
// It is not meant to be run by hand — `bin/run <gate>` is that path. A gate
// answers a runner, and a runner is the only caller that can say what became
// of the run: a gate killed for memory is not alive to report it, and a gate
// that exited cleanly having printed nothing would be believed.
//
// It is one call into the command library and the definition below: what it
// parses, how it reports and what it exits with are not this file's
// (docs/command-line.md, One implementation).
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/promise-language/forge/primitives/command"
	"github.com/promise-language/forge/tools/build/common"
)

// Injected by the meta-builder via -ldflags at build time; empty otherwise.
var (
	repoRoot   = ""
	sourceHash = ""
)

// listing is what --list answers: every name this project measures. A program
// reads the JSON, where what a gate declares of itself can grow additively; the
// line form is for a person (docs/project-tools.md, Gate).
//
// It exists because an orchestrator must not hold a second copy of what this
// project can measure. Asking the entry point is the only way to learn it that
// cannot go stale.
type listing struct {
	Gates []listedGate `json:"gates"`
}

// listedGate is one name and what it measures.
type listedGate struct {
	Name    string `json:"name"`
	Summary string `json:"summary"`
}

// Human is one name per line, which is what a person scanning for the name they
// want reads fastest.
func (l listing) Human(w io.Writer) error {
	for _, g := range l.Gates {
		if _, err := fmt.Fprintln(w, g.Name); err != nil {
			return err
		}
	}
	return nil
}

// define is gate's whole surface: the gates this project answers, each taking
// the envelope flag the gate contract fixes, and the listing beside them.
func define(repoRoot, sourceHash string) command.Tool {
	return command.Tool{
		Project: "gate",
		Version: sourceHash,
		Fit:     common.Fit("gate", repoRoot, sourceHash),
		Root: command.Command{
			Name:    "gate",
			Summary: "measure one property of this tree",
			Flags: []command.Flag{{
				Name:        "list",
				Type:        command.Boolean,
				Description: "name every gate this project answers",
			}},
			SelectedBy: "list",
			Action: func(c *command.Call) (command.Result, error) {
				names := common.GateNames(repoRoot)
				gates := make([]listedGate, 0, len(names))
				for _, n := range names {
					gates = append(gates, listedGate{Name: n, Summary: common.GateSummary(n)})
				}
				return listing{Gates: gates}, nil
			},

			// The gates are the project's vocabulary rather than this tool's,
			// so they are computed once per invocation and then closed like any
			// declared set — and help describes them instead of listing them,
			// because `gate --list` is that enumeration's one home.
			Children:     func() []command.Command { return children(repoRoot) },
			ChildClass:   "<gate>",
			ChildSummary: "a gate this project answers",
			EnumeratedBy: "gate --list",
			ChildFlags: []command.Flag{{
				Name:        "envelope",
				Type:        command.Boolean,
				Description: "write the measurement as one envelope on stdout",
				Protocol:    "gate-contract.md",
			}},
			ChildValidate: requireEnvelope,
			ChildAction:   func(c *command.Call) (command.Result, error) { return measure(repoRoot, c) },
		},
	}
}

// children is the gate set this project answers.
func children(repoRoot string) []command.Command {
	names := common.GateNames(repoRoot)
	set := make([]command.Command, 0, len(names))
	for _, n := range names {
		set = append(set, command.Command{Name: n, Summary: common.GateSummary(n)})
	}
	return set
}

// requireEnvelope refuses a measurement nobody can read as one.
//
// A bare run that printed measurements and exited 0 would be read as a pass by
// the first script that wrapped it, and a gate has no verdict to give.
func requireEnvelope(c *command.Call) []error {
	if c.Bool("envelope") {
		return nil
	}
	return []error{fmt.Errorf(
		"%s measures %s, and writes it only with --envelope; run `run %s` for a result meant for a person",
		c.Name(), common.GateSummary(c.Name()), c.Name())}
}

// measure runs one gate. Nothing is written here: the envelope is returned, and
// the library writes it once, whole — which is how a reader tells "measured
// nothing" from "measured and reported" without asking the gate.
func measure(repoRoot string, c *command.Call) (command.Result, error) {
	envelope, err := common.MeasureGate(repoRoot, c.Name())
	if err != nil {
		// Nothing was measured. No envelope, because a partial one is not a
		// measurement and must not parse as one.
		return nil, err
	}
	return common.Measured(envelope), nil
}

func main() {
	os.Exit(command.Run(define(repoRoot, sourceHash), os.Args[1:], command.Stdio()))
}
