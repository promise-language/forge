package tooling

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/promise-language/forge/primitives"
)

// The tests in this package are one file per section of docs/project-tools.md,
// and one test named for what that section says: the library is the document's
// conformance suite as well as its implementation, so an amendment is a change
// here that is reviewed against the test its section names.
//
// A fixture is a real git checkout in a temporary directory, because the units
// are found by asking git and a fake index would be a second answer to what the
// repository holds.

// fixture is a checkout the tools can act on: a git repository, the ignore
// entries setup requires, and whatever units the caller asked for.
func fixture(t *testing.T, units ...string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	root := t.TempDir()
	// A temporary directory may itself sit inside a checkout, because a
	// project's tools point TMPDIR inside the repository. The ceiling stops git
	// walking up into it and reading another repository's index as this one's.
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(root))

	write(t, root, ".gitignore", "/bin/\n/.workspace/\n/.home/\n")
	for _, dir := range units {
		write(t, root, filepath.Join(dir, "go.mod"), "module example.com/x\n\ngo 1.26\n")
	}
	git(t, root, "init", "-q")
	git(t, root, "config", "user.email", "u@example.com")
	git(t, root, "config", "user.name", "u")
	git(t, root, "add", "-A")
	return root
}

// stamped is the link-time stamp a tool built in this fixture would carry, so a
// test can reach an entry point's action rather than its staleness refusal.
func stamped(t *testing.T, root string) string {
	t.Helper()
	write(t, root, filepath.FromSlash("tools/build/cmd/gate/main.go"), "package main\n")
	git(t, root, "add", "-A")
	hash, err := primitives.SourceHash(root, primitives.ToolsBuildDir)
	if err != nil {
		t.Fatal(err)
	}
	return Stamp{Root: root, Hash: hash, Dirs: []string{primitives.ToolsBuildDir}}.Encode()
}

// terms writes both term files into a fixture.
func terms(t *testing.T, root, thresholds, baselines string) {
	t.Helper()
	write(t, root, filepath.FromSlash(ThresholdsFile), thresholds)
	write(t, root, filepath.FromSlash(BaselinesFile), baselines)
}

// write puts one file into a checkout, making the directories above it.
func write(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// git runs one git command in a fixture, failing the test if it does not.
func git(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// run begins a run against a fixture, with the narration captured so a test can
// read what a person would have been told.
func run(t *testing.T, p Project, root string) (*Run, *strings.Builder) {
	t.Helper()
	var narrated strings.Builder
	r, end, err := Begin(p, root, &narrated)
	if err != nil {
		t.Fatalf("beginning a run: %v", err)
	}
	t.Cleanup(end)
	return r, &narrated
}

// quiet is a run that reads and spawns nothing of its own, for the rules that
// do not need a scratch directory.
func quiet(p Project, root string) *Run {
	return &Run{Root: root, Narrate: io.Discard, ctx: context.Background(), project: p}
}

// counting is a project with one gate that reports whatever a test tells it to,
// so the rules about envelopes, judging and stages can be exercised without
// running a toolchain.
func counting(name string, metrics []Metric, measured Measured, err error) Project {
	p := Project{Toolchains: []Toolchain{Go()}}
	p.Gates.Add(Gate{
		Name:        name,
		Summary:     "a gate a test stands in for",
		Metrics:     Declared(metrics...),
		Measure:     func(*Run, []Unit) (Measured, error) { return measured, err },
		Remediation: "there is nothing to do about a fixture",
	})
	p.Gates.Add(Gate{
		Name:        Fit,
		Summary:     "whether this machine can do this project's work",
		Metrics:     Declared(Count("missing_toolchains")),
		Measure:     func(*Run, []Unit) (Measured, error) { return Measured{}, nil },
		Remediation: "there is nothing to do about a fixture",
	})
	p.Verify = standardPipeline()
	p.Integration(name)
	return p
}
