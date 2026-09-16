package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/promise-language/forge/tools/build/common"
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

// The discovery query arrives as `--list -json`, because the caller it exists
// for is a program and Output modes has programs ask for JSON. An earlier version
// accepted only a lone `--list` and refused that pair as an unknown flag, which
// read to the workspace as "this project cannot say what it builds".
func TestWantsListAcceptsTheFlagsItIsAskedWith(t *testing.T) {
	for _, args := range [][]string{
		{"-list"},
		{"-list", "-json"},
		{"-json", "-list"},
		{"-list", "-human"},
	} {
		if !wantsList(args) {
			t.Errorf("wantsList(%q) = false, want true", args)
		}
	}
	// Flag form sanctions no aliases, so a bare `list` is a gate name that does not
	// exist rather than a second spelling of the flag.
	for _, args := range [][]string{
		{},
		{"list"},
		{"fit"},
		{"fit", "-verdict"},
	} {
		if wantsList(args) {
			t.Errorf("wantsList(%q) = true, want false", args)
		}
	}
}

// Human mode says which name is which; JSON mode is the object the workspace
// reads. Both render the same two lists.
func TestRenderListBothModes(t *testing.T) {
	answer := listAnswer{Commands: []string{"gate", "run"}, Gates: []string{"fit"}}

	var human strings.Builder
	if err := renderList(&human, answer, common.OutputHuman); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"command  gate", "command  run", "gate     fit"} {
		if !strings.Contains(human.String(), want) {
			t.Errorf("human output %q is missing %q", human.String(), want)
		}
	}

	var jsonOut strings.Builder
	if err := renderList(&jsonOut, answer, common.OutputJSON); err != nil {
		t.Fatal(err)
	}
	var got listAnswer
	if err := json.Unmarshal([]byte(jsonOut.String()), &got); err != nil {
		t.Fatalf("JSON mode did not emit one decodable object: %v", err)
	}
	if len(got.Commands) != 2 || got.Gates[0] != "fit" {
		t.Errorf("decoded %+v, want %+v", got, answer)
	}
}
