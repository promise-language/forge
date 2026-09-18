package command

import (
	"fmt"
	"os"
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

	t.Run("everything after the name reaches the program, and its status comes back", func(t *testing.T) {
		// The refusal above is the branch where no program runs. This is the
		// other one, and it is the whole of what delegating means: the words
		// arrive unparsed, and the answer is the delegate's — a relay that
		// dropped an argument, or reported 0 for a program that failed, is the
		// silent success this document's Fail closed exists to prevent.
		relaying := Tool{
			Project: "tool",
			Root: Command{
				Name:    "tool",
				Summary: "do one thing",
				Children: func() []Command {
					return []Command{{
						Name:    "verify",
						Summary: "the project's own gate",
						Delegate: func(*Call) ([]string, *Refusal) {
							return []string{os.Args[0], "-test.run=" + delegateHelper, "--"}, nil
						},
					}}
				},
			},
		}
		got := invoke(t, relaying, []string{"verify", "-json", "--", "-report"}, Streams{})
		if got.status != StatusDone {
			t.Errorf("status %d, want the delegate's own (%q)", got.status, got.errs)
		}
		// -json included: a delegating child declares none of the reserved
		// flags, so the mode flag is a word meant for the delegate.
		if handed := strings.TrimSpace(got.out); handed != "-json|--|-report" {
			t.Errorf("the delegate was handed %q, want everything after the name verbatim", handed)
		}

		failing := invoke(t, relaying, []string{"verify", "-fail"}, Streams{})
		if failing.status != 7 {
			t.Errorf("status %d, want the delegate's own", failing.status)
		}
	})

	t.Run("a delegate that names no program is a failure, not a silent success", func(t *testing.T) {
		empty := Tool{
			Project: "tool",
			Root: Command{
				Name:    "tool",
				Summary: "do one thing",
				Children: func() []Command {
					return []Command{{
						Name:     "verify",
						Summary:  "the project's own gate",
						Delegate: func(*Call) ([]string, *Refusal) { return nil, nil },
					}}
				},
			},
		}
		got := invoke(t, empty, []string{"verify"}, Streams{})
		if got.status != StatusFailed {
			t.Errorf("status %d, want %d", got.status, StatusFailed)
		}
		got.says(t, "stderr", got.errs, "names no program to run")
	})
}

// delegateHelper names the test below, which this binary re-executes to stand
// in for the program a delegating child hands its arguments to. Re-executing
// the test binary is the portable way to have a program that is certainly
// present, reports what it was given, and exits with a status the case chose.
const delegateHelper = "TestTheDelegateAProgramStandsIn"

func TestTheDelegateAProgramStandsIn(t *testing.T) {
	handed := argsAfterMarker()
	if handed == nil {
		t.Skip("this test is the program a delegating child hands its arguments to")
	}
	fmt.Println(strings.Join(handed, "|"))
	for _, a := range handed {
		if a == "-fail" {
			os.Exit(7)
		}
	}
	os.Exit(0)
}

// argsAfterMarker is what the delegating command handed over, which begins
// after the marker its Delegate ended the argv with. Nil means this process is
// an ordinary test run rather than the delegate.
func argsAfterMarker() []string {
	for i, a := range os.Args {
		if a == endOfFlagsMarker {
			return os.Args[i+1:]
		}
	}
	return nil
}
