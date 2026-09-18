package command

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// Exit status and refusal: an action returns an outcome and the library maps it
// to a status, and a refusal is not a failure — it has its own status and its
// own object, so a caller tells the two apart without reading prose.
func TestExitStatusAndRefusal(t *testing.T) {
	t.Run("the four statuses", func(t *testing.T) {
		done := toolWithFlag()
		if got := invoke(t, done, nil, Streams{}); got.status != StatusDone {
			t.Errorf("a result exited %d, want 0", got.status)
		}

		failed := toolWithFlag()
		failed.Root.Action = func(*Call) (Result, error) { return nil, errors.New("could not finish") }
		if got := invoke(t, failed, nil, Streams{}); got.status != StatusFailed {
			t.Errorf("a failure exited %d, want 1", got.status)
		}

		if got := invoke(t, done, []string{"-nonsense"}, Streams{}); got.status != StatusMalformed {
			t.Errorf("a malformed invocation exited %d, want 2", got.status)
		}

		refusing := toolWithFlag()
		refusing.Fit = func() *Refusal { return staleRefusal() }
		if got := invoke(t, refusing, nil, Streams{}); got.status != StatusRefused {
			t.Errorf("a refusal exited %d, want 3", got.status)
		}
	})

	t.Run("a result may report an outcome of its own, and is still written", func(t *testing.T) {
		reporting := toolWithFlag()
		reporting.Root.Action = func(*Call) (Result, error) { return failing{Said: "not acceptable"}, nil }
		got := invoke(t, reporting, nil, Streams{OutIsTerminal: true})
		if got.status != StatusFailed {
			t.Errorf("status %d, want the result's own", got.status)
		}
		if got.out != "not acceptable\n" {
			t.Errorf("stdout %q — the caller asked a question, and a no is an answer", got.out)
		}
	})

	t.Run("the refusal object is the whole of stdout in JSON mode", func(t *testing.T) {
		refusing := toolWithFlag()
		refusing.Fit = func() *Refusal { return staleRefusal() }
		got := invoke(t, refusing, nil, Streams{})
		var refusal Refusal
		if err := json.Unmarshal([]byte(got.out), &refusal); err != nil {
			t.Fatalf("stdout %q is not a refusal object: %v", got.out, err)
		}
		if refusal.Refusal != Stale || refusal.Tool != "tool" || refusal.Recovery[0] != "./make" {
			t.Errorf("refusal = %+v, want the condition, the tool and the recovery", refusal)
		}
		got.says(t, "stderr", got.errs, "the tools source has moved")
	})

	t.Run("in human mode stdout is empty, and stderr says the same thing", func(t *testing.T) {
		refusing := toolWithFlag()
		refusing.Fit = func() *Refusal { return staleRefusal() }
		got := invoke(t, refusing, nil, Streams{OutIsTerminal: true})
		if got.out != "" {
			t.Errorf("stdout %q, want it empty", got.out)
		}
		got.says(t, "stderr", got.errs, "tool:", "the tools source has moved", "./make")
	})

	t.Run("the mode is decided without the parser", func(t *testing.T) {
		refusing := toolWithFlag(Flag{Name: "envelope", Type: Boolean, Protocol: "gate-contract.md", Description: "the envelope"})
		refusing.Fit = func() *Refusal { return staleRefusal() }

		// -json anywhere decides it, even where the parser would have refused
		// the invocation carrying it.
		got := invoke(t, refusing, []string{"-nonsense", "-json"}, Streams{OutIsTerminal: true})
		if !strings.HasPrefix(got.out, "{") {
			t.Errorf("stdout %q, want the object -json asked for", got.out)
		}
		// And a flag another contract owns keeps its stream clean.
		got = invoke(t, refusing, []string{"-envelope"}, Streams{})
		if got.out != "" {
			t.Errorf("stdout %q, want nothing at all where the envelope goes", got.out)
		}
		if got.status != StatusRefused {
			t.Errorf("status %d, want the refusal status", got.status)
		}
	})

	t.Run("a tool with nothing to refuse on never refuses", func(t *testing.T) {
		if got := invoke(t, toolWithFlag(), nil, Streams{}); got.status != StatusDone {
			t.Errorf("status %d, want the builder every refusal names to keep answering", got.status)
		}
	})
}

func staleRefusal() *Refusal {
	return &Refusal{Refusal: Stale, Tool: "tool", Detail: "the tools source has moved", Recovery: []string{"./make"}}
}
