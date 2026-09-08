package main

import (
	"strings"
	"testing"
)

// The prompts tell an agent to look here for the gate names, so a usage text
// that omits them sends it to guess.
func TestUsageNamesTheGatesAndTheVerdictMode(t *testing.T) {
	u := usage()
	for _, want := range []string{"tested", "integration", "fit", "--verdict"} {
		if !strings.Contains(u, want) {
			t.Errorf("usage does not mention %q", want)
		}
	}
}
