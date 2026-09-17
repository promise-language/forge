package command

import (
	"strings"
	"testing"
)

// The command tree: children are a closed set whether they are declared or
// computed, a command with children and no action requires a child, and a
// delegating child hands everything after its name on verbatim.
func TestTheCommandTree(t *testing.T) {
	computed := Tool{
		Project: "tool",
		Version: "9f1c2e7a",
		Root: Command{
			Name:         "tool",
			Summary:      "do one thing",
			Children:     func() []Command { return []Command{{Name: "tested:root", Summary: "one module"}} },
			ChildClass:   "<gate>",
			ChildSummary: "a gate this project answers",
			EnumeratedBy: "tool --list",
			ChildAction:  answered("measured"),
		},
	}

	t.Run("a computed set is closed like a declared one", func(t *testing.T) {
		// Its members are compound names, addressed exactly: tested:root is one
		// name and not a prefix to search under.
		if got := invoke(t, computed, []string{"tested:root"}, Streams{}); got.status != StatusDone {
			t.Errorf("`tool tested:root` exited %d (%q)", got.status, got.errs)
		}
		got := invoke(t, computed, []string{"tested"}, Streams{})
		if got.status != StatusMalformed {
			t.Errorf("a name outside the set exited %d, want %d", got.status, StatusMalformed)
		}
		got.says(t, "stderr", got.errs, "unknown command tested")
	})

	t.Run("a command with children and no action requires a child", func(t *testing.T) {
		got := invoke(t, computed, nil, Streams{})
		if got.status != StatusMalformed {
			t.Errorf("a bare invocation exited %d, want %d", got.status, StatusMalformed)
		}
		if got.out != "" {
			t.Errorf("stdout %q, want it empty so a script cannot read it as a result", got.out)
		}
		got.says(t, "the brief form", got.errs, "expecting a subcommand:", "tool 9f1c2e7a", "-help")
		// The build stamp is the first line: a reader who typed a name and
		// stopped may also be holding the wrong build, and that is the first
		// thing a bug report needs.
		lines := strings.Split(strings.TrimRight(got.errs, "\n"), "\n")
		if len(lines) != 3 || lines[0] != "tool 9f1c2e7a" {
			t.Errorf("the brief form is:\n%s", got.errs)
		}
	})

	t.Run("a flag may select the root's own action beside its children", func(t *testing.T) {
		listing := computed
		listing.Root.Flags = []Flag{{Name: "list", Type: Boolean, Description: "name them"}}
		listing.Root.SelectedBy = "list"
		listing.Root.Action = answered("every name")
		if got := invoke(t, listing, []string{"--list"}, Streams{}); got.status != StatusDone {
			t.Errorf("`tool --list` exited %d (%q)", got.status, got.errs)
		}
		if got := invoke(t, listing, nil, Streams{}); got.status != StatusMalformed {
			t.Errorf("bare, with the flag absent, exited %d, want the brief form", got.status)
		}
	})

	t.Run("a delegating child parses nothing after its name", func(t *testing.T) {
		delegating := Tool{
			Project: "tool",
			Root: Command{
				Name:    "tool",
				Summary: "do one thing",
				Children: func() []Command {
					return []Command{{
						Name:    "verify",
						Summary: "the project's own gate",
						Delegate: func(c *Call) ([]string, *Refusal) {
							return nil, &Refusal{Refusal: Unbuilt, Tool: "tool", Detail: "no binary"}
						},
					}}
				},
			},
		}
		// -help included: it belongs to the delegate, so the delegating tool
		// never answers it and never parses it.
		got := invoke(t, delegating, []string{"verify", "-help", "-nonsense"}, Streams{})
		if got.status != StatusRefused {
			t.Errorf("status %d, want the delegate's refusal to have been relayed", got.status)
		}
		if strings.Contains(got.out, "Usage") {
			t.Errorf("the delegating tool answered -help itself: %q", got.out)
		}
	})
}
