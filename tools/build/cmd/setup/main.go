// Command setup makes a fresh clone ready to gate its own commits.
//
// It is one call into the command library and the definition below: what it
// parses, how it reports and what it exits with are not this file's
// (docs/command-line.md, One implementation).
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/promise-language/forge/primitives"
	"github.com/promise-language/forge/primitives/command"
	"github.com/promise-language/forge/tools/build/common"
)

// Injected by the meta-builder via -ldflags at build time; empty otherwise.
var (
	repoRoot   = ""
	sourceHash = ""
)

// result is what setup answers: where git now looks for this repository's
// hooks, and what each of the project's own setup steps changed.
type result struct {
	HooksPath string `json:"hooks_path"`
	Steps     []step `json:"steps"`
}

// step is one of the project's own setup steps, and whether it changed
// anything. A second run changes nothing and says so.
type step struct {
	Name    string `json:"name"`
	Changed bool   `json:"changed"`
}

// Human is the line a person reads.
func (r result) Human(w io.Writer) error {
	if _, err := fmt.Fprintf(w, "git hooks configured (core.hooksPath = %s)\n", r.HooksPath); err != nil {
		return err
	}
	for _, s := range r.Steps {
		changed := "unchanged"
		if s.Changed {
			changed = "changed"
		}
		if _, err := fmt.Fprintf(w, "  %-12s %s\n", s.Name, changed); err != nil {
			return err
		}
	}
	return nil
}

// define is setup's whole surface.
func define(repoRoot, sourceHash string) command.Tool {
	return command.Tool{
		Project: "setup",
		Version: sourceHash,
		Fit:     common.Fit("setup", repoRoot, sourceHash),
		Root: command.Command{
			Name:    "setup",
			Summary: "point git at this repository's hooks",
			Action: func(c *command.Call) (command.Result, error) {
				if err := primitives.RunSetup(repoRoot); err != nil {
					return nil, fmt.Errorf("wiring the git hooks: %w", err)
				}
				return result{HooksPath: primitives.HooksPath, Steps: []step{}}, nil
			},
		},
	}
}

func main() {
	os.Exit(command.Run(define(repoRoot, sourceHash), os.Args[1:], command.Stdio()))
}
