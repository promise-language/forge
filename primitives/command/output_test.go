package command

import (
	"errors"
	"io"
	"strings"
	"testing"
)

// Output: the mode is the guide's, applied by the library; the result is
// written once, whole; narration never reaches stdout; and a command whose
// output a named contract fixes has one mode.
func TestOutput(t *testing.T) {
	tool := toolWithFlag()

	t.Run("stdout decides, and the flags force", func(t *testing.T) {
		if got := invoke(t, tool, nil, Streams{OutIsTerminal: true}); got.out != "done\n" {
			t.Errorf("at a terminal = %q, want the human rendering", got.out)
		}
		if got := invoke(t, tool, nil, Streams{}); got.out != `{"said":"done"}`+"\n" {
			t.Errorf("through a pipe = %q, want the object", got.out)
		}
		if got := invoke(t, tool, []string{"-json"}, Streams{OutIsTerminal: true}); !strings.HasPrefix(got.out, "{") {
			t.Errorf("-json at a terminal = %q, want the object", got.out)
		}
		if got := invoke(t, tool, []string{"-human"}, Streams{}); got.out != "done\n" {
			t.Errorf("-human through a pipe = %q, want the rendering", got.out)
		}
	})

	t.Run("both is a contradiction", func(t *testing.T) {
		got := invoke(t, tool, []string{"-json", "-human"}, Streams{})
		if got.status != StatusMalformed {
			t.Errorf("status %d, want %d", got.status, StatusMalformed)
		}
		if got.out != "" {
			t.Errorf("stdout %q, want nothing written", got.out)
		}
	})

	t.Run("a failure to render leaves stdout untouched", func(t *testing.T) {
		broken := toolWithFlag()
		broken.Root.Action = func(*Call) (Result, error) { return unrenderable{}, nil }
		got := invoke(t, broken, []string{"-human"}, Streams{})
		if got.out != "" {
			t.Errorf("stdout %q, want nothing where the result could not be rendered", got.out)
		}
		if got.status != StatusFailed {
			t.Errorf("status %d, want %d", got.status, StatusFailed)
		}
	})

	t.Run("narration goes to stderr, and an action reaches no other stream", func(t *testing.T) {
		narrating := toolWithFlag()
		narrating.Root.Action = func(c *Call) (Result, error) {
			if _, err := c.Narrate.Write([]byte("==> working\n")); err != nil {
				return nil, err
			}
			return text{Said: "done"}, nil
		}
		got := invoke(t, narrating, nil, Streams{})
		if strings.Contains(got.out, "working") {
			t.Errorf("stdout %q carries narration", got.out)
		}
		got.says(t, "stderr", got.errs, "==> working")
	})

	t.Run("a contract-fixed command has one mode", func(t *testing.T) {
		fixed := toolWithFlag(Flag{Name: "envelope", Type: Boolean, Protocol: "gate-contract.md", Description: "write the envelope"})
		for _, mode := range []string{"-json", "-human"} {
			got := invoke(t, fixed, []string{"-envelope", mode}, Streams{OutIsTerminal: true})
			if got.status != StatusMalformed {
				t.Errorf("`tool -envelope %s` exited %d, want %d", mode, got.status, StatusMalformed)
			}
			got.says(t, "stderr", got.errs, "gate-contract.md")
		}
		// And without the mode flags it is JSON whatever stdout is, because the
		// shape belongs to that contract rather than to whoever is reading.
		if got := invoke(t, fixed, []string{"-envelope"}, Streams{OutIsTerminal: true}); !strings.HasPrefix(got.out, "{") {
			t.Errorf("stdout = %q, want the shape the contract fixes", got.out)
		}
	})
}

type unrenderable struct{}

func (unrenderable) Human(io.Writer) error { return errors.New("there is no rendering for this") }
