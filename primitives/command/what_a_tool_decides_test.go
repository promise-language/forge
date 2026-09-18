package command

import (
	"strings"
	"testing"
)

// What a tool decides: the tree, the summaries, the principal commands, the
// flags and their types, the result and its rendering, and the validation. The
// prefix, the order, the help layout, the mode, the refusal and the status are
// not choices a tool makes — and the check is what a tool's own tests call, so
// a definition defect fails the project's tested gate.
func TestWhatAToolDecides(t *testing.T) {
	t.Run("the brief form names the principal commands", func(t *testing.T) {
		tool := Tool{
			Project: "tool",
			Root: Command{
				Name:    "tool",
				Summary: "do one thing",
				Children: func() []Command {
					return []Command{
						{Name: "setup", Summary: "wire it", Principal: true, Action: answered("wired")},
						{Name: "doctor", Summary: "check it", Principal: true, Action: answered("checked")},
						{Name: "internal", Summary: "a corner", Action: answered("done")},
					}
				},
			},
		}
		got := invoke(t, tool, nil, Streams{OutIsTerminal: true})
		got.says(t, "the brief form", got.errs, "setup, doctor")
		if strings.Contains(got.errs, "internal") {
			t.Errorf("the brief form names every command: %q", got.errs)
		}
		// A tool that marks none names all of them, which is the same answer
		// while a tool is small.
		plain := tool
		plain.Root.Children = func() []Command {
			return []Command{{Name: "setup", Summary: "wire it", Action: answered("wired")}}
		}
		got = invoke(t, plain, nil, Streams{OutIsTerminal: true})
		got.says(t, "the brief form", got.errs, "setup")
	})

	t.Run("a command has children, an action, or both", func(t *testing.T) {
		empty := Tool{Project: "tool", Root: Command{Name: "tool", Summary: "do one thing"}}
		if defects := Check(empty); len(defects) == 0 {
			t.Error("a command with neither children nor an action was accepted")
		}
	})

	t.Run("a described set names the invocation that enumerates it", func(t *testing.T) {
		tool := Tool{Project: "tool", Root: Command{
			Name:        "tool",
			Summary:     "do one thing",
			Children:    func() []Command { return []Command{{Name: "one", Summary: "a gate"}} },
			ChildAction: answered("measured"),
			ChildClass:  "<gate>",
		}}
		if defects := Check(tool); len(defects) == 0 {
			t.Error("a class with no enumerating invocation was accepted")
		}
	})

	t.Run("a delegating command declares no flags", func(t *testing.T) {
		tool := Tool{Project: "tool", Root: Command{
			Name:    "tool",
			Summary: "do one thing",
			Children: func() []Command {
				return []Command{{
					Name:     "verify",
					Summary:  "the project's gate",
					Flags:    []Flag{{Name: "json2", Type: Boolean, Description: "a flag the delegate owns"}},
					Delegate: func(*Call) ([]string, *Refusal) { return []string{"true"}, nil },
				}}
			},
		}}
		if defects := Check(tool); len(defects) == 0 {
			t.Error("a delegating command with a flag was accepted; every word after its name belongs to the delegate")
		}
	})

	t.Run("a positional is never a boolean, and the trailing one comes last", func(t *testing.T) {
		tool := toolWithFlag()
		tool.Root.Params = []Param{{Name: "flagged", Type: Boolean, Arity: One}}
		if defects := Check(tool); len(defects) == 0 {
			t.Error("a boolean positional was accepted, and a boolean is a flag")
		}
		tool.Root.Params = []Param{
			{Name: "rest", Type: String, Arity: Trailing},
			{Name: "after", Type: String, Arity: One},
		}
		if defects := Check(tool); len(defects) == 0 {
			t.Error("a parameter after the trailing one was accepted, and no argument can reach it")
		}
	})
}
