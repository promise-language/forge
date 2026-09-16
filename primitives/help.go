package primitives

import (
	"fmt"
	"os"
)

// HasHelpFlag reports whether args request usage.
//
// The name is -help and only -help. Both prefixes are the same flag, so
// --help and -help both match after NormalizeArgs; -h and --h do not, because
// an abbreviation is not a flag at all but unknown input (cli-guide's Flag form
// and Fail closed).
// One name per flag means the spelling help text, error messages and docs use
// is the single spelling that works.
func HasHelpFlag(args []string) bool {
	for _, a := range NormalizeArgs(args) {
		if a == "-help" {
			return true
		}
	}
	return false
}

// MaybeHelp prints usage and exits 0 when args request help; otherwise it
// returns and the caller proceeds. Call it first thing in a tool's main — help
// must work regardless of staleness, so it runs before CheckStale.
func MaybeHelp(args []string, usage string) {
	if HasHelpFlag(args) {
		fmt.Println(usage)
		os.Exit(0)
	}
}
