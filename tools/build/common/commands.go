package common

// What this project builds, answered the way the gate list is answered: by
// looking, not by reading a list someone maintained.
//
// The meta-builder compiles one command per directory under tools/build/cmd,
// so those directories already are the registry (cmd/make/main.go). A second
// copy naming them here would be a list to keep in step with the first, and
// the two would eventually disagree — the same argument docs/blueprint.md, The gate entry point,
// makes for discovering gates rather than declaring them.
//
// `make` is not among them. It runs from source via the ./make trampoline and
// is never compiled into bin/, so a caller asking what this project puts in
// bin/ must not be told about a binary that is never there.

import (
	"os"
	"path/filepath"
	"sort"
)

// CommandNames returns every command this project builds into bin/, sorted.
func CommandNames(repoRoot string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(repoRoot, "tools", "build", "cmd"))
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() && e.Name() != "make" {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}
