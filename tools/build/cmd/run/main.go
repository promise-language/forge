// Command run asks one gate for a measurement and reaches a verdict on it.
//
// This is the by-hand path, and it takes the same route a runner takes rather
// than a parallel one: it executes bin/gate as a process and reads what came
// back. Running a single gate is not a lesser case — it is faster than
// everything that blocks a change from landing, and it is what someone
// iterating on one failure actually wants.
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

// listing is what --list answers: the two kinds of name a caller outside this
// tree can ask this project for — a command it can execute, and a gate it can
// measure. Both are discovered rather than declared, so neither can go stale,
// and one object carries them together because a caller that must not confuse
// the two needs to see the whole vocabulary at once.
//
// The shape is generic-projects.md's, and `bin/gate --list` renders the same
// gate names as objects rather than bare names.
type listing struct {
	Commands []string `json:"commands"`
	Gates    []string `json:"gates"`
}

// Human is one name per line with its kind, because the two lists answer
// different questions — what this project builds, and what it answers — and a
// reader who cannot tell which is which has to know the vocabulary already to
// use the output that exists to teach it.
func (l listing) Human(w io.Writer) error {
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

// define is run's whole surface: the gates this project answers, each judged or
// measured, and the discovery query beside them.
func define(repoRoot, sourceHash string) command.Tool {
	return command.Tool{
		Project: "run",
		Version: sourceHash,
		Fit:     common.Fit("run", repoRoot, sourceHash),
		Root: command.Command{
			Name:    "run",
			Summary: "measure one gate and judge what it measured",
			Flags: []command.Flag{{
				Name:        "list",
				Type:        command.Boolean,
				Description: "name what this project builds and what it answers",
			}},
			SelectedBy: "list",
			Action:     func(c *command.Call) (command.Result, error) { return list(repoRoot) },

			Children:     func() []command.Command { return children(repoRoot) },
			ChildClass:   "<gate>",
			ChildSummary: "a gate this project answers",
			EnumeratedBy: "run --list",
			ChildFlags: []command.Flag{{
				Name:        "verdict",
				Type:        command.Boolean,
				Description: "judge the envelope on stdin, and run no gate",
				Protocol:    "gate-contract.md",
			}},
			ChildAction: func(c *command.Call) (command.Result, error) { return measureOrJudge(repoRoot, c) },
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

// list says what this project builds and what it answers.
//
// It is the discovery query: it is how anything outside the tree learns both
// without holding a copy that can go stale.
func list(repoRoot string) (command.Result, error) {
	commands, err := common.CommandNames(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("cannot say what this project builds: %w", err)
	}
	return listing{Commands: commands, Gates: common.GateNames(repoRoot)}, nil
}

// measureOrJudge runs the gate, or judges an envelope it is handed.
//
// In the judging mode nothing is spawned: the envelope arrives on stdin from
// whoever ran the gate. That is the mode the SDK asks — the SDK spawns the
// gate, because a judge that ran its own measurement would be the runner, and
// the runner comes from outside the tree.
func measureOrJudge(repoRoot string, c *command.Call) (command.Result, error) {
	if c.Bool("verdict") {
		verdict, err := common.JudgeStdin(repoRoot, c.Name(), c.In)
		if err != nil {
			return nil, err
		}
		return verdict, nil
	}
	judged, err := common.RunOneGate(repoRoot, common.GateBinary(repoRoot), c.Name(), c.Narrate)
	if err != nil {
		return nil, err
	}
	return judged, nil
}

func main() {
	os.Exit(command.Run(define(repoRoot, sourceHash), os.Args[1:], command.Stdio()))
}
