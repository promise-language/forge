package command

import (
	"encoding/json"
	"strings"
	"testing"
)

// Help and version: -help on stdout at 0, generated from the definitions the
// parser uses; -version as a result like any other; and both refused by a
// binary that is not fit to act.
func TestHelpAndVersion(t *testing.T) {
	tool := toolWithFlag(Flag{Name: "force", Type: Boolean, Description: "overwrite what is there"})

	t.Run("-help writes to stdout and exits 0", func(t *testing.T) {
		got := invoke(t, tool, []string{"-help"}, Streams{OutIsTerminal: true})
		if got.status != StatusDone {
			t.Errorf("status %d, want 0", got.status)
		}
		if got.errs != "" {
			t.Errorf("stderr %q, want help on stdout", got.errs)
		}
		got.says(t, "help", got.out, "-force", "boolean", "overwrite what is there", "-json", "-version")
	})

	t.Run("it answers even when the other flags are wrong", func(t *testing.T) {
		got := invoke(t, tool, []string{"-help", "-nonsense"}, Streams{OutIsTerminal: true})
		if got.status != StatusDone {
			t.Errorf("status %d — a person who asked what a command takes is not served by a list of their other mistakes", got.status)
		}
	})

	t.Run("it is the surface as data through a pipe", func(t *testing.T) {
		got := invoke(t, tool, []string{"-help"}, Streams{})
		var surface struct {
			Project string `json:"project"`
			Flags   []struct {
				Name string `json:"name"`
				Type string `json:"type"`
			} `json:"flags"`
		}
		if err := json.Unmarshal([]byte(got.out), &surface); err != nil {
			t.Fatalf("help is not an object: %v (%q)", err, got.out)
		}
		if surface.Project != "tool" || len(surface.Flags) == 0 {
			t.Errorf("help = %+v, want the project and its flags", surface)
		}
	})

	t.Run("-version is a result like any other", func(t *testing.T) {
		semantic := toolWithFlag()
		semantic.Version = "v0.12.0-rc1+abc"
		got := invoke(t, semantic, []string{"-version"}, Streams{OutIsTerminal: true})
		if got.status != StatusDone || strings.TrimSpace(got.out) != "tool v0.12.0-rc1+abc" {
			t.Errorf("(%q, %d), want the human line", got.out, got.status)
		}

		got = invoke(t, semantic, []string{"-version"}, Streams{})
		var payload map[string]any
		if err := json.Unmarshal([]byte(got.out), &payload); err != nil {
			t.Fatalf("the version is not an object: %v (%q)", err, got.out)
		}
		for key, want := range map[string]any{"project": "tool", "text": "v0.12.0-rc1+abc",
			"major": float64(0), "minor": float64(12), "patch": float64(0), "prerelease": "rc1", "build": "abc"} {
			if payload[key] != want {
				t.Errorf("%s = %v, want %v", key, payload[key], want)
			}
		}
	})

	t.Run("a version that is not semantic omits the parts rather than zeroing them", func(t *testing.T) {
		hashed := toolWithFlag()
		hashed.Version = "9f1c2e7a5b0d4c31"
		got := invoke(t, hashed, []string{"-version"}, Streams{})
		if strings.Contains(got.out, "major") {
			t.Errorf("version = %q, want no major: absent means unknown, and 0 would be compared", got.out)
		}
	})

	t.Run("a build that recorded no version says so", func(t *testing.T) {
		got := invoke(t, toolWithFlag(), []string{"-version"}, Streams{OutIsTerminal: true})
		if !strings.Contains(got.out, "no version recorded") {
			t.Errorf("version = %q, want it to say so rather than report the empty string", got.out)
		}
		got = invoke(t, toolWithFlag(), []string{"-version"}, Streams{})
		if strings.Contains(got.out, `"text"`) {
			t.Errorf("version = %q, want the field absent rather than empty", got.out)
		}
	})

	t.Run("a binary that is not fit to act answers neither", func(t *testing.T) {
		unfit := toolWithFlag()
		unfit.Fit = func() *Refusal {
			return &Refusal{Refusal: Stale, Tool: "tool", Detail: "the tools source has moved", Recovery: []string{"./make"}}
		}
		for _, args := range [][]string{{"-help"}, {"-version"}} {
			got := invoke(t, unfit, args, Streams{OutIsTerminal: true})
			if got.status != StatusRefused {
				t.Errorf("`tool %s` exited %d, want the refusal status", args[0], got.status)
			}
			if got.out != "" {
				t.Errorf("stdout %q in human mode, want it empty", got.out)
			}
		}
	})
}
