package command

import (
	"fmt"
	"io"
	"strings"
	"testing"
)

// The tests in this package are one file per section of docs/command-line.md,
// and one test named for that section: the library is the guide's conformance
// suite as well as its implementation, so an amendment to the guide is a change
// here that is reviewed against the test its section names.
//
// Every case runs Run with injected streams. Nothing starts a process, which is
// what the injected streams, arguments and working directory are for.

// text is a result whose rendering is one line, for the cases where what the
// result says does not matter.
type text struct {
	Said string `json:"said"`
}

func (t text) Human(w io.Writer) error {
	_, err := fmt.Fprintln(w, t.Said)
	return err
}

// failing is a result that reports an outcome of its own.
type failing struct {
	Said string `json:"said"`
}

func (f failing) Human(w io.Writer) error {
	_, err := fmt.Fprintln(w, f.Said)
	return err
}

func (f failing) ExitStatus() int { return StatusFailed }

// answered is an action that returns one line.
func answered(said string) Action {
	return func(*Call) (Result, error) { return text{Said: said}, nil }
}

// invocation is what one run of a tool produced.
type invocation struct {
	out    string
	errs   string
	status int
}

// invoke runs one invocation with everything injected.
func invoke(t *testing.T, tool Tool, args []string, s Streams) invocation {
	t.Helper()
	var out, errs strings.Builder
	s.Out, s.Err = &out, &errs
	status := Run(tool, args, s)
	return invocation{out: out.String(), errs: errs.String(), status: status}
}

// says fails unless the output carries every phrase.
func (i invocation) says(t *testing.T, where, output string, phrases ...string) {
	t.Helper()
	for _, phrase := range phrases {
		if !strings.Contains(output, phrase) {
			t.Errorf("%s = %q, which does not say %q", where, output, phrase)
		}
	}
}
