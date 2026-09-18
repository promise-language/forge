// Command verify is the commit gate: it repairs what has one right answer,
// then measures what remains.
//
// It is one call into the command library and the definition below: what it
// parses, how it reports and what it exits with are not this file's
// (docs/command-line.md, One implementation).
package main

import (
	"os"

	"github.com/promise-language/forge/primitives/command"
	"github.com/promise-language/forge/tools/build/common"
)

// Injected by the meta-builder via -ldflags at build time; empty otherwise.
var (
	repoRoot   = ""
	sourceHash = ""
)

// define is verify's whole surface.
func define(repoRoot, sourceHash string) command.Tool {
	return command.Tool{
		Project: "verify",
		Version: sourceHash,
		Fit:     common.Fit("verify", repoRoot, sourceHash),
		Root: command.Command{
			Name:    "verify",
			Summary: "the commit gate: repair what has one right answer, then measure what remains",
			Action: func(c *command.Call) (command.Result, error) {
				result, err := common.RunVerify(repoRoot, c.Narrate)
				if err != nil {
					return nil, err
				}
				return result, nil
			},
		},
	}
}

func main() {
	os.Exit(command.Run(define(repoRoot, sourceHash), os.Args[1:], command.Stdio()))
}
