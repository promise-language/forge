package common

import (
	"fmt"
	"os"
)

// Two output modes, one rule (docs/org/cli-guide.md §6).
//
// The mode is decided by stdout ALONE — never stderr, never an environment
// variable. An environment variable is a mode a caller did not type and cannot
// see in the command line it is reading.
//
// This lives in tools/build/common rather than in primitives because it is a
// rule about how these tools talk, not a byte-identical helper every project
// copies (docs/primitives.md). It is small on purpose: tools/build is a module
// with no dependencies, which is what lets ./make bootstrap a broken tree.

// OutputMode is how a result is rendered.
type OutputMode int

const (
	// OutputHuman is the mode for a person at a terminal.
	OutputHuman OutputMode = iota
	// OutputJSON is the mode for anything reading the result.
	OutputJSON
)

// OutputFlags is what -json and -human were given as, before the default is
// applied. Both false is "not asked", which is the ordinary case.
type OutputFlags struct {
	JSON  bool
	Human bool
}

// TakeOutputFlags strips -json and -human from args and returns the rest.
//
// Stripping rather than parsing with the flag package: these tools take their
// one positional argument in any position and a FlagSet would stop at the first
// non-flag, so `run --list -json` and `run -json --list` would not mean the same
// thing. NormalizeArgs has already made --json and -json one name.
func TakeOutputFlags(args []string) ([]string, OutputFlags) {
	var of OutputFlags
	rest := make([]string, 0, len(args))
	for _, a := range args {
		switch a {
		case "-json":
			of.JSON = true
		case "-human":
			of.Human = true
		default:
			rest = append(rest, a)
		}
	}
	return rest, of
}

// Mode resolves the flags against the default. Passing both is a contradiction
// rather than a precedence puzzle, so it is the usage error §8 makes it —
// named, and before anything is done.
//
// The default asks stdout whether it is a character device: a terminal is, and
// a pipe or a redirect is not. Asking stdout rather than a flag is what makes
// `tool > out.json` produce JSON without anyone having to remember to say so.
func (of OutputFlags) Mode() (OutputMode, error) {
	if of.JSON && of.Human {
		return OutputHuman, fmt.Errorf("-json and -human are mutually exclusive: pass one, or neither to let stdout decide")
	}
	switch {
	case of.JSON:
		return OutputJSON, nil
	case of.Human:
		return OutputHuman, nil
	}
	fi, err := os.Stdout.Stat()
	if err != nil {
		// A stdout that cannot be described is not a terminal anyone is reading.
		// JSON is the answer that survives being piped somewhere unexamined; a
		// human rendering would be the one that silently loses structure.
		return OutputJSON, nil
	}
	if fi.Mode()&os.ModeCharDevice != 0 {
		return OutputHuman, nil
	}
	return OutputJSON, nil
}
