package command

import (
	"path/filepath"
	"testing"
	"time"
)

// Types: one converter per row of the table, shared by the command line and the
// parameter file. An error about a value names the flag, the value it was given
// and the type it expected.
func TestTypes(t *testing.T) {
	t.Run("the duration grammar is one positive integer and one unit", func(t *testing.T) {
		for _, c := range []struct {
			raw  string
			want time.Duration
		}{
			{"250ms", 250 * time.Millisecond},
			{"30s", 30 * time.Second},
			{"90m", 90 * time.Minute},
			{"2h", 2 * time.Hour},
			{"5d", 5 * 24 * time.Hour},
		} {
			got, err := convert(Duration, String, nil, c.raw, "")
			if err != nil || got != c.want {
				t.Errorf("%q = (%v, %v), want %v", c.raw, got, err, c.want)
			}
		}
		// No fraction, no sign, no compound — 1h30m is 90m — and no other unit.
		for _, raw := range []string{"1h30m", "1.5h", "-5s", "0s", "90", "5w", "s", "ms"} {
			if got, err := convert(Duration, String, nil, raw, ""); err == nil {
				t.Errorf("%q was accepted as %v", raw, got)
			}
		}
	})

	t.Run("a path resolves where the tool was invoked", func(t *testing.T) {
		dir := t.TempDir()
		got, err := convert(Path, String, nil, "reports/out.json", dir)
		if err != nil {
			t.Fatal(err)
		}
		if got != filepath.Join(dir, "reports", "out.json") {
			t.Errorf("path = %v, want it resolved against the directory the tool was invoked from", got)
		}
	})

	t.Run("an integer is base ten", func(t *testing.T) {
		if got, _ := convert(Integer, String, nil, "42", ""); got != int64(42) {
			t.Errorf("42 = %v", got)
		}
		for _, raw := range []string{"4.2", "0x10", "one", ""} {
			if _, err := convert(Integer, String, nil, raw, ""); err == nil {
				t.Errorf("%q was accepted as an integer", raw)
			}
		}
	})

	t.Run("an enumeration is a closed set", func(t *testing.T) {
		values := []string{"fast", "thorough"}
		if _, err := convert(Enumeration, String, values, "fast", ""); err != nil {
			t.Error(err)
		}
		if _, err := convert(Enumeration, String, values, "quick", ""); err == nil {
			t.Error("a non-member was accepted")
		}
	})

	t.Run("a list checks every element as its own type", func(t *testing.T) {
		got, err := convert(List, Integer, nil, "1,2,3", "")
		if err != nil {
			t.Fatal(err)
		}
		if ints, ok := got.([]int64); !ok || len(ints) != 3 {
			t.Errorf("list = %v, want a slice of the element type", got)
		}
		// An empty element is a usage error rather than a value dropped.
		if _, err := convert(List, Integer, nil, "1,,3", ""); err == nil {
			t.Error("an empty element was accepted")
		}
	})

	t.Run("a boolean takes no value and has one spelling", func(t *testing.T) {
		tool := toolWithFlag(Flag{Name: "no-fetch", Type: Boolean, Description: "do not fetch"})
		for _, args := range [][]string{{"-no-fetch=true"}, {"-no-fetch=false"}} {
			got := invoke(t, tool, args, Streams{})
			if got.status != StatusMalformed {
				t.Errorf("%v exited %d, want %d", args, got.status, StatusMalformed)
			}
			got.says(t, "stderr", got.errs, "takes no value", "-no-fetch is the one spelling")
		}
		// The opposite spelling is not defined, and the parser refuses it as
		// unknown input naming the one that exists.
		got := invoke(t, tool, []string{"-fetch"}, Streams{})
		if got.status != StatusMalformed {
			t.Errorf("the unused spelling exited %d", got.status)
		}
		got.says(t, "stderr", got.errs, "unknown flag -fetch", "did you mean -no-fetch")
	})

	t.Run("a required flag that is absent is a usage error", func(t *testing.T) {
		tool := toolWithFlag(Flag{Name: "target", Type: String, Required: true, Description: "what to build"})
		got := invoke(t, tool, nil, Streams{})
		if got.status != StatusMalformed {
			t.Errorf("status %d, want %d", got.status, StatusMalformed)
		}
		got.says(t, "stderr", got.errs, "-target is required")
	})

	t.Run("an error about a value names the flag, the value and the type", func(t *testing.T) {
		tool := toolWithFlag(Flag{Name: "timeout", Type: Duration, Description: "how long"})
		got := invoke(t, tool, []string{"-timeout", "1h30m"}, Streams{})
		got.says(t, "stderr", got.errs, "-timeout", `"1h30m"`, "duration")
	})
}
