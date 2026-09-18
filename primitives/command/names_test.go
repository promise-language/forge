package command

import (
	"strings"
	"testing"
)

// Names: the alphabet, the compound subcommand name, the reserved names, the
// collisions, and a boolean's unused spelling. A name the library does not
// accept cannot be defined, and every defect is reported rather than the first.
func TestNames(t *testing.T) {
	for _, c := range []struct {
		name string
		tool Tool
		says string
	}{
		{"uppercase is not a name", toolWithFlag(Flag{Name: "Force", Type: Boolean}), `"Force" is not a flag name`},
		{"an underscore is not a separator", toolWithFlag(Flag{Name: "dry_run", Type: Boolean}), "not a flag name"},
		{"a dot is not a separator", toolWithFlag(Flag{Name: "a.b", Type: Boolean}), "not a flag name"},
		{"a flag may not be compound", toolWithFlag(Flag{Name: "tested:root", Type: Boolean}), "not a flag name"},
		{"a trailing dash is not a name", toolWithFlag(Flag{Name: "force-", Type: Boolean}), "not a flag name"},
		{"help is the library's", toolWithFlag(Flag{Name: "help", Type: Boolean}), "-help is the library's"},
		{"version is the library's", toolWithFlag(Flag{Name: "version", Type: Boolean}), "-version is the library's"},
		{"json is the library's", toolWithFlag(Flag{Name: "json", Type: Boolean}), "-json is the library's"},
		{"human is the library's", toolWithFlag(Flag{Name: "human", Type: Boolean}), "-human is the library's"},
		{"json-input is the library's", toolWithFlag(Flag{Name: "json-input", Type: Path}), "-json-input is the library's"},
		{"one flag, one name", toolWithFlag(Flag{Name: "force", Type: Boolean}, Flag{Name: "force", Type: Boolean}), "two flags named -force"},
		{"a boolean's unused spelling", toolWithFlag(Flag{Name: "fetch", Type: Boolean}, Flag{Name: "no-fetch", Type: Boolean}), "only the one that changes the default exists"},
		{"an enumeration over nothing", toolWithFlag(Flag{Name: "mode", Type: Enumeration}), "enumeration over no members"},
		{"a list of lists", toolWithFlag(Flag{Name: "many", Type: List, Elem: List}), "not an element type"},
	} {
		t.Run(c.name, func(t *testing.T) {
			defects := Check(c.tool)
			if len(defects) == 0 {
				t.Fatalf("the check accepted %s", c.name)
			}
			var said []string
			for _, d := range defects {
				said = append(said, d.Error())
			}
			if !strings.Contains(strings.Join(said, "\n"), c.says) {
				t.Errorf("the defects are %q, want one saying %q", said, c.says)
			}
		})
	}

	t.Run("a compound subcommand name is one name", func(t *testing.T) {
		tool := toolWithFlag()
		tool.Root.Children = func() []Command {
			return []Command{{Name: "tested:root:deep", Summary: "one unit", Action: answered("ok")}}
		}
		if defects := Check(tool); len(defects) > 0 {
			t.Errorf("a compound name was refused: %v", defects)
		}
		tool.Root.Children = func() []Command {
			return []Command{{Name: "tested::root", Summary: "one unit", Action: answered("ok")}}
		}
		if defects := Check(tool); len(defects) == 0 {
			t.Error("an empty segment was accepted, and an empty segment is not a name")
		}
	})

	t.Run("every defect is reported, not the first", func(t *testing.T) {
		tool := toolWithFlag(Flag{Name: "Force", Type: Boolean}, Flag{Name: "help", Type: Boolean})
		if defects := Check(tool); len(defects) < 2 {
			t.Errorf("the check reported %v, want every defect", defects)
		}
	})

	t.Run("a tool whose definition fails the check refuses to run", func(t *testing.T) {
		got := invoke(t, toolWithFlag(Flag{Name: "Force", Type: Boolean}), nil, Streams{})
		if got.status != StatusFailed {
			t.Errorf("status %d, want %d — a person must clear this", got.status, StatusFailed)
		}
		if got.out != "" {
			t.Errorf("stdout %q, want nothing: help is generated from the definition being refused", got.out)
		}
	})
}

// toolWithFlag is a one-command tool carrying whatever flags a case declares.
func toolWithFlag(flags ...Flag) Tool {
	return Tool{
		Project: "tool",
		Root:    Command{Name: "tool", Summary: "do one thing", Flags: flags, Action: answered("done")},
	}
}
