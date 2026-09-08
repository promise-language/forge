package main

import (
	"strings"
	"testing"
)

// Exactly one argument: a list request with anything alongside it is ambiguous
// between listing and measuring, and guessing would print a list to a caller
// waiting for an envelope.
func TestIsListArg(t *testing.T) {
	for _, args := range [][]string{{"--list"}, {"-list"}, {"list"}} {
		if !isListArg(args) {
			t.Errorf("%v was not read as a list request", args)
		}
	}
	for _, args := range [][]string{{}, {"tested"}, {"--list", "tested"}, {"--envelope"}} {
		if isListArg(args) {
			t.Errorf("%v was read as a list request", args)
		}
	}
}

func TestUsageNamesTheGatesAndTheEnvelopeRule(t *testing.T) {
	u := usage()
	for _, want := range []string{"tested", "integration", "fit", "--envelope", "--list"} {
		if !strings.Contains(u, want) {
			t.Errorf("usage does not mention %q", want)
		}
	}
}
