package command

import "testing"

// Parsing: the command path, then the flags, then the positionals — and every
// problem with an invocation reported in one pass, with nothing done.
func TestParsing(t *testing.T) {
	tool := Tool{
		Project: "tool",
		Root: Command{
			Name:        "tool",
			Summary:     "do one thing",
			Flags:       []Flag{{Name: "quiet", Type: Boolean, Description: "say less"}},
			Children:    func() []Command { return []Command{{Name: "sync", Summary: "sync it"}} },
			ChildFlags:  []Flag{{Name: "timeout", Type: Duration, Description: "how long"}},
			ChildAction: answered("synced"),
		},
	}
	withParam := toolWithFlag(Flag{Name: "force", Type: Boolean, Description: "overwrite"})
	withParam.Root.Params = []Param{{Name: "target", Type: String, Arity: Optional, Description: "what to build"}}

	t.Run("a value attaches with = or comes next", func(t *testing.T) {
		for _, args := range [][]string{{"sync", "-timeout=30s"}, {"sync", "-timeout", "30s"}} {
			if got := invoke(t, tool, args, Streams{}); got.status != StatusDone {
				t.Errorf("%v exited %d (%q)", args, got.status, got.errs)
			}
		}
		got := invoke(t, tool, []string{"sync", "-timeout"}, Streams{})
		got.says(t, "stderr", got.errs, "-timeout was given no value")
	})

	t.Run("a command after the path's first flag is refused", func(t *testing.T) {
		got := invoke(t, tool, []string{"-quiet", "sync"}, Streams{})
		if got.status != StatusMalformed {
			t.Errorf("status %d, want the path to have come first", got.status)
		}
		got.says(t, "stderr", got.errs, `"sync" is a command`, "before every flag")
	})

	t.Run("a flag after the first positional is refused", func(t *testing.T) {
		got := invoke(t, withParam, []string{"web", "-force"}, Streams{})
		if got.status != StatusMalformed {
			t.Errorf("status %d, want %d", got.status, StatusMalformed)
		}
		got.says(t, "stderr", got.errs, `-force is written after the argument "web"`)
	})

	t.Run("-- ends the flags, and only there is a leading dash an argument", func(t *testing.T) {
		if got := invoke(t, withParam, []string{"--", "-report"}, Streams{}); got.status != StatusDone {
			t.Errorf("a positional after -- exited %d (%q)", got.status, got.errs)
		}
		got := invoke(t, withParam, []string{"-report"}, Streams{})
		if got.status != StatusMalformed {
			t.Errorf("a leading dash before -- exited %d, want it read as a flag", got.status)
		}
		got.says(t, "stderr", got.errs, "unknown flag -report")
	})

	t.Run("every problem is reported, and nothing is done", func(t *testing.T) {
		var ran bool
		counting := withParam
		counting.Root.Action = func(*Call) (Result, error) { ran = true; return text{Said: "ran"}, nil }
		got := invoke(t, counting, []string{"-nonsense", "-alsowrong", "one", "two"}, Streams{})
		if got.status != StatusMalformed {
			t.Fatalf("status %d, want %d", got.status, StatusMalformed)
		}
		if ran {
			t.Error("the action ran over a malformed invocation")
		}
		got.says(t, "stderr", got.errs, "-nonsense", "-alsowrong", `"two" is one argument more`)
	})

	t.Run("an unknown name names the nearest, and not the list", func(t *testing.T) {
		got := invoke(t, tool, []string{"sync", "-timeuot", "30s"}, Streams{})
		got.says(t, "stderr", got.errs, "unknown flag -timeuot", "did you mean -timeout", "-help")
		if len(got.errs) > 200 {
			t.Errorf("the error dumps the flag list: %q", got.errs)
		}
	})

	t.Run("a validation is reported in the same pass and with the same status", func(t *testing.T) {
		validated := withParam
		validated.Root.Validate = func(c *Call) []error {
			if c.Arg("target") == "" {
				return []error{errNoTarget}
			}
			return nil
		}
		got := invoke(t, validated, nil, Streams{})
		if got.status != StatusMalformed {
			t.Errorf("status %d, want %d", got.status, StatusMalformed)
		}
		got.says(t, "stderr", got.errs, errNoTarget.Error())
	})
}

var errNoTarget = errValidation("this command needs a target, and nothing typed one")

type errValidation string

func (e errValidation) Error() string { return string(e) }
