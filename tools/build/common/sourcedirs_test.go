package common

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/promise-language/forge/primitives"
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
// this file exists (docs/primitives.md §4, §6).
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
			if reason := StaleReason(repo, baked); reason != "" {
				t.Fatalf("an untouched tree reported stale: %q", reason)
			}

			edit(t, repo, rel)

			if reason := StaleReason(repo, baked); !strings.Contains(reason, "tools source has changed") {
				t.Errorf("editing %s reported %q — the binary would claim to be current", rel, reason)
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
		if reason := StaleReason(repo, baked); reason != "" {
			t.Errorf("editing %s reported %q, want no staleness", rel, reason)
		}
	}
}

// What a tool actually does about it: an edit to either tree stops the binary
// before it can measure or repair anything with logic that has moved. This is
// the acceptance the whole file exists for, seen from the end a person hits.
func TestCheckStaleAbortsOnAnEditToEitherTree(t *testing.T) {
	for _, rel := range []string{"tools/build/common/x.go", "primitives/y.go"} {
		t.Run(rel, func(t *testing.T) {
			repo := sourceRepo(t)
			baked, err := SourceHash(repo)
			if err != nil {
				t.Fatal(err)
			}

			bin := asSubprocess(t, "check-stale")
			t.Setenv("REPO", repo)
			t.Setenv("HASH", baked)
			if out, code := runSubprocess(t, bin); code != 9 {
				t.Fatalf("a current binary exited %d (%q), want it to have been allowed to run", code, out)
			}

			edit(t, repo, rel)

			out, code := runSubprocess(t, bin)
			if code != 1 {
				t.Fatalf("after editing %s the binary exited %d, want it to abort with 1", rel, code)
			}
			for _, want := range []string{"tools source has changed", repo} {
				if !strings.Contains(out, want) {
					t.Errorf("the abort said %q, which does not name %q", out, want)
				}
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
