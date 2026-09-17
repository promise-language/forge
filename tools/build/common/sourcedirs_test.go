package common

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/promise-language/forge/primitives"
	"github.com/promise-language/forge/primitives/command"
)

// sourceRepo is a stand-in for this repository's shape: a tools/build module and
// the replaced tree beside it.
func sourceRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	for rel, body := range map[string]string{
		"tools/build/go.mod":        "module example/tools/build\n\ngo 1.26\n",
		"tools/build/common/x.go":   "package common\n",
		"primitives/y.go":           "package primitives\n",
		"primitives/hash/hash.go":   "package hash\n",
		"docs/not-tool-source.md":   "prose\n",
		"cmd/init/not-a-tool.go":    "package main\n",
		"tools/build/cmd/z/main.go": "package main\n",
	} {
		p := filepath.Join(repo, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return repo
}

func edit(t *testing.T, repo, rel string) {
	t.Helper()
	p := filepath.Join(repo, filepath.FromSlash(rel))
	body, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, append(body, []byte("\n// edited\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Both trees are tool source here, and the replaced one is the whole reason
// this file exists (docs/primitives.md, The staleness
// contract holds with nothing added and This repository is its own first consumer).
func TestSourceDirsNamesBothTrees(t *testing.T) {
	got := sourceDirs()
	for _, want := range []string{primitives.ToolsBuildDir, "primitives"} {
		if !slices.Contains(got, want) {
			t.Errorf("sourceDirs() = %v, which does not name %q", got, want)
		}
	}
}

// The one list, observed from both ends: an edit anywhere in the tool source
// makes the recomputed answer differ from the baked-in one. If the hash and the
// check read different lists, one of these two edits goes unnoticed.
func TestAnEditToEitherTreeIsStale(t *testing.T) {
	for _, rel := range []string{"tools/build/common/x.go", "primitives/y.go", "primitives/hash/hash.go"} {
		t.Run(rel, func(t *testing.T) {
			repo := sourceRepo(t)
			baked, err := SourceHash(repo)
			if err != nil {
				t.Fatal(err)
			}
			if refusal := Fit("gate", repo, baked)(); refusal != nil {
				t.Fatalf("an untouched tree refused: %+v", refusal)
			}

			edit(t, repo, rel)

			refusal := Fit("gate", repo, baked)()
			if refusal == nil || refusal.Refusal != command.Stale {
				t.Errorf("editing %s gave %+v — the binary would claim to be current", rel, refusal)
			}
		})
	}
}

// What is not tool source must not make a binary stale, or every documentation
// change would force a rebuild.
func TestAnEditOutsideTheToolSourceIsNotStale(t *testing.T) {
	repo := sourceRepo(t)
	baked, err := SourceHash(repo)
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"docs/not-tool-source.md", "cmd/init/not-a-tool.go"} {
		edit(t, repo, rel)
		if refusal := Fit("gate", repo, baked)(); refusal != nil {
			t.Errorf("editing %s gave %+v, want no refusal", rel, refusal)
		}
	}
}

// What a tool actually does about it: an edit to either tree stops the tool
// before it can measure or repair anything with logic that has moved, and it
// stops it at the refusal status with the refusal object on stdout. This is the
// acceptance the whole file exists for, seen from the end a caller hits.
func TestAnEditToEitherTreeRefusesEveryInvocation(t *testing.T) {
	for _, rel := range []string{"tools/build/common/x.go", "primitives/y.go"} {
		t.Run(rel, func(t *testing.T) {
			repo := sourceRepo(t)
			baked, err := SourceHash(repo)
			if err != nil {
				t.Fatal(err)
			}
			tool := func() command.Tool {
				return command.Tool{
					Project: "gate",
					Version: baked,
					Fit:     Fit("gate", repo, baked),
					Root: command.Command{
						Name:    "gate",
						Summary: "measure one property of this tree",
						Action: func(*command.Call) (command.Result, error) {
							return nil, nil
						},
					},
				}
			}

			var out, errs strings.Builder
			streams := command.Streams{Out: &out, Err: &errs}
			if status := command.Run(tool(), nil, streams); status != command.StatusDone {
				t.Fatalf("a current binary exited %d (%q), want it to have been allowed to run", status, errs.String())
			}

			edit(t, repo, rel)

			out.Reset()
			errs.Reset()
			// Even -help: what a stale binary would print is the surface it was
			// built with, which is exactly what is out of date.
			status := command.Run(tool(), []string{"-help"}, streams)
			if status != command.StatusRefused {
				t.Fatalf("after editing %s the tool exited %d, want the refusal status", rel, status)
			}
			var refusal command.Refusal
			if err := json.Unmarshal([]byte(out.String()), &refusal); err != nil {
				t.Fatalf("stdout %q is not a refusal object: %v", out.String(), err)
			}
			if refusal.Refusal != command.Stale {
				t.Errorf("refusal %q, want %q", refusal.Refusal, command.Stale)
			}
			if !strings.Contains(errs.String(), "tools source has changed") {
				t.Errorf("stderr said %q, which does not say what moved", errs.String())
			}
		})
	}
}

// The wrappers are the only spelling in this repository. A caller reaching past
// them to the one-directory default would miss the replaced tree entirely.
func TestSourceHashIsWiderThanToolsBuildAlone(t *testing.T) {
	repo := sourceRepo(t)
	ours, err := SourceHash(repo)
	if err != nil {
		t.Fatal(err)
	}
	toolsOnly, err := primitives.ToolsSourceHash(repo)
	if err != nil {
		t.Fatal(err)
	}
	if ours == toolsOnly {
		t.Error("this repository's hash is the tools/build-only one — the replaced tree is not covered")
	}
}
