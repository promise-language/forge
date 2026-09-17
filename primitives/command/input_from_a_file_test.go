package command

import (
	"os"
	"path/filepath"
	"testing"
)

// Input from a file: -json-input supplies the same closed parameter set the
// command line does, through the same converters, with no precedence between
// the two.
func TestInputFromAFile(t *testing.T) {
	tool := toolWithFlag(
		Flag{Name: "timeout", Type: Duration, Description: "how long"},
		Flag{Name: "force", Type: Boolean, Description: "overwrite"},
		Flag{Name: "retries", Type: Integer, Description: "how many"},
		Flag{Name: "tags", Type: List, Elem: String, Description: "which tags"},
	)
	tool.Root.Params = []Param{{Name: "target", Type: String, Arity: Optional, Description: "what to build"}}
	var seen *Call
	tool.Root.Action = func(c *Call) (Result, error) { seen = c; return text{Said: "done"}, nil }

	write := func(t *testing.T, body string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "args.json")
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}

	t.Run("the keys are flag names, plus args", func(t *testing.T) {
		seen = nil
		path := write(t, `{"timeout":"30s","force":true,"retries":3,"tags":["a","b"],"args":["web"]}`)
		got := invoke(t, tool, []string{"-json-input", path}, Streams{})
		if got.status != StatusDone {
			t.Fatalf("status %d (%q)", got.status, got.errs)
		}
		if seen.Duration("timeout").String() != "30s" || !seen.Bool("force") || seen.Int("retries") != 3 {
			t.Errorf("the file supplied %v, %v, %v", seen.Duration("timeout"), seen.Bool("force"), seen.Int("retries"))
		}
		if tags := seen.Strings("tags"); len(tags) != 2 {
			t.Errorf("tags = %v, want the array as a list", tags)
		}
		if seen.Arg("target") != "web" {
			t.Errorf("args gave %q, want the positional", seen.Arg("target"))
		}
	})

	t.Run("false is what not naming it means", func(t *testing.T) {
		seen = nil
		path := write(t, `{"force":false}`)
		if got := invoke(t, tool, []string{"-json-input", path}, Streams{}); got.status != StatusDone {
			t.Fatalf("status %d (%q)", got.status, got.errs)
		}
		if seen.Bool("force") {
			t.Error("false applied the flag")
		}
	})

	t.Run("a value that fails its type fails the same way", func(t *testing.T) {
		got := invoke(t, tool, []string{"-json-input", write(t, `{"timeout":"1h30m"}`)}, Streams{})
		if got.status != StatusMalformed {
			t.Errorf("status %d, want %d", got.status, StatusMalformed)
		}
		got.says(t, "stderr", got.errs, "timeout", "1h30m", "duration")

		got = invoke(t, tool, []string{"-json-input", write(t, `{"retries":1.5}`)}, Streams{})
		got.says(t, "stderr", got.errs, "no fractional part")
	})

	t.Run("an unknown key is an unknown flag", func(t *testing.T) {
		got := invoke(t, tool, []string{"-json-input", write(t, `{"timeuot":"30s"}`)}, Streams{})
		got.says(t, "stderr", got.errs, "unknown flag -timeuot", "did you mean -timeout")
	})

	t.Run("a file that is absent, unreadable or not an object is a usage error", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "nope.json")
		got := invoke(t, tool, []string{"-json-input", missing}, Streams{})
		if got.status != StatusMalformed {
			t.Errorf("status %d, want %d", got.status, StatusMalformed)
		}
		got.says(t, "stderr", got.errs, missing)

		got = invoke(t, tool, []string{"-json-input", write(t, `["not","an","object"]`)}, Streams{})
		got.says(t, "stderr", got.errs, "not a JSON object")
	})

	t.Run("a parameter given both ways is refused", func(t *testing.T) {
		got := invoke(t, tool, []string{"-timeout", "30s", "-json-input", write(t, `{"timeout":"30s"}`)}, Streams{})
		if got.status != StatusMalformed {
			t.Errorf("status %d, want %d — there is no precedence", got.status, StatusMalformed)
		}
		got.says(t, "stderr", got.errs, "-timeout", "no precedence")

		got = invoke(t, tool, []string{"-json-input", write(t, `{"args":["web"]}`), "web"}, Streams{})
		got.says(t, "stderr", got.errs, "no precedence")

		// The reserved flags are keys like any other, so the rule reaches them
		// too rather than letting one of the two silently win.
		got = invoke(t, tool, []string{"-json", "-json-input", write(t, `{"json":true}`)}, Streams{})
		if got.status != StatusMalformed {
			t.Errorf("status %d, want %d — a mode named twice is named both ways", got.status, StatusMalformed)
		}
		got.says(t, "stderr", got.errs, "-json", "no precedence")
	})

	t.Run("the file may not set json-input, and help means -help", func(t *testing.T) {
		got := invoke(t, tool, []string{"-json-input", write(t, `{"json-input":"other.json"}`)}, Streams{})
		got.says(t, "stderr", got.errs, "may not set -json-input")

		got = invoke(t, tool, []string{"-json-input", write(t, `{"help":true}`)}, Streams{OutIsTerminal: true})
		if got.status != StatusDone {
			t.Errorf("status %d, want the help the key asked for", got.status)
		}
		got.says(t, "help", got.out, "Usage")
	})
}
