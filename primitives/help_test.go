package primitives

import "testing"

func TestHasHelpFlag(t *testing.T) {
	help := [][]string{
		{"-help"}, {"--help"},
		{"build", "--help"}, {"-force", "-help"},
	}
	for _, args := range help {
		if !HasHelpFlag(args) {
			t.Errorf("expected help for %v", args)
		}
	}
	// An abbreviation is not a flag at all — it is unknown input, and the tool
	// that receives it says so rather than guessing at help (cli-guide §3, §8).
	notHelp := [][]string{
		nil, {}, {"-force"}, {"--force"}, {"-helper"}, {"--helpme"}, {"help"},
		{"-h"}, {"--h"}, {"-force", "-h"},
	}
	for _, args := range notHelp {
		if HasHelpFlag(args) {
			t.Errorf("expected no help for %v", args)
		}
	}
}
