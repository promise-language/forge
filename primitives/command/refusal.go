package command

import (
	"encoding/json"
	"fmt"
	"strings"
)

// The conditions a binary declines to run on. The set is closed, and what each
// names is docs/project-tools.md, Staleness.
const (
	// Stale is a stamped hash that differs from the source set's.
	Stale = "stale"
	// Unstamped is no stamp at all — built by go build or go install, not by
	// the project's builder.
	Unstamped = "unstamped"
	// RepositoryUnreachable is a stamped root that no longer exists.
	RepositoryUnreachable = "repository-unreachable"
	// Unbuilt is a command the tool lists with no binary, asked to run it.
	Unbuilt = "unbuilt"
)

// Refusal is a binary declining to act, and it is not a failure. A tool that
// exits 1 over a stale build has not measured the tree and has not judged
// anything, so a caller reporting that as the tree's failure has made a claim
// about a repository that was never put to the question. A caller must be able
// to tell the two apart without reading prose, which is why a refusal has its
// own status and its own object (docs/command-line.md, Exit status and
// refusal).
type Refusal struct {
	// Refusal is why this binary declined, from the closed set above.
	Refusal string `json:"refusal"`
	// Tool is the name of the tool that refused.
	Tool string `json:"tool"`
	// Detail is prose for a person. Nothing keys on it.
	Detail string `json:"detail"`
	// Recovery is the program and arguments that clear the condition, run from
	// the repository root and exec'd, never interpreted.
	Recovery []string `json:"recovery"`
}

// writeRefusal answers an invocation the binary declined, and returns the
// refusal status.
//
// The mode is decided without the parser: a stale binary's parser may itself be
// the thing that changed, and the refusal must come out right however little of
// the binary can still be trusted.
func writeRefusal(t Tool, r *Refusal, args []string, s Streams) int {
	protocol := false
	for _, name := range protocolFlags(t.Root) {
		if named(args, name) {
			protocol = true
		}
	}
	mode := Human
	switch {
	case named(args, flagJSON):
		mode = JSON
	case named(args, flagHuman):
		mode = Human
	case protocol:
		mode = JSON
	case !s.OutIsTerminal:
		mode = JSON
	}

	// In the two modes whose stdout another contract claims, a refusal writes
	// nothing there at all: anything on that stream which is not an envelope or
	// a verdict is, to the runner parsing it, the gate's own defect. A refusal
	// is not that, so it travels as the status and the stderr line.
	if mode == JSON && !protocol {
		if body, err := json.Marshal(r); err == nil {
			fmt.Fprintf(s.Out, "%s\n", body)
		}
	}
	line := fmt.Sprintf("%s: %s", r.Tool, r.Detail)
	if len(r.Recovery) > 0 {
		line += " — run " + strings.Join(r.Recovery, " ")
	}
	fmt.Fprintln(s.Err, line)
	return StatusRefused
}

// protocolFlags names every flag that claims stdout for another contract.
//
// It reads the declared tree only: a computed set is produced from what the
// tool knows about a repository, and a binary that has already refused must not
// go asking. A parent declares the flags its computed children carry, which is
// what makes them readable here without computing anything.
func protocolFlags(c Command) []string {
	var names []string
	for _, f := range c.Flags {
		if f.Protocol != "" {
			names = append(names, f.Name)
		}
	}
	for _, f := range c.ChildFlags {
		if f.Protocol != "" {
			names = append(names, f.Name)
		}
	}
	if c.ChildClass == "" && c.Children != nil {
		for _, child := range c.Children() {
			names = append(names, protocolFlags(child)...)
		}
	}
	return names
}

// named reports whether args carry this flag, in either prefix and with or
// without an attached value. It is the raw scan a refusal uses, and nothing
// else: every other reading of the command line goes through the parser.
func named(args []string, name string) bool {
	for _, a := range args {
		spelling, _, _ := splitFlag(a)
		if spelling == name {
			return true
		}
	}
	return false
}
