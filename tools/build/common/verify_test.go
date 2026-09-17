package common

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// modules() is the foundation of the multi-module fix: if it returns only the
// root, then runAllModules degenerates to the old single-module behaviour and
// the tools module is silently skipped.

func TestModules_ReturnsToolsBuildWhenItHasGoMod(t *testing.T) {
	root := t.TempDir()
	toolsDir := filepath.Join(root, "tools", "build")
	if err := os.MkdirAll(toolsDir, 0755); err != nil {
		t.Fatal(err)
	}
	// Root module.
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// Tools module.
	if err := os.WriteFile(filepath.Join(toolsDir, "go.mod"), []byte("module example/tools/build\n"), 0644); err != nil {
		t.Fatal(err)
	}

	dirs := modules(root)
	if len(dirs) != 2 {
		t.Fatalf("modules returned %d dirs, want 2: %v", len(dirs), dirs)
	}
	if dirs[0] != root {
		t.Errorf("dirs[0] = %q, want the repo root %q", dirs[0], root)
	}
	if dirs[1] != toolsDir {
		t.Errorf("dirs[1] = %q, want the tools dir %q", dirs[1], toolsDir)
	}
}

func TestModules_ReturnsOnlyRootWhenToolsBuildHasNoGoMod(t *testing.T) {
	root := t.TempDir()
	toolsDir := filepath.Join(root, "tools", "build")
	if err := os.MkdirAll(toolsDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// No go.mod in tools/build.

	dirs := modules(root)
	if len(dirs) != 1 {
		t.Fatalf("modules returned %d dirs, want 1 (root only): %v", len(dirs), dirs)
	}
	if dirs[0] != root {
		t.Errorf("dirs[0] = %q, want the repo root %q", dirs[0], root)
	}
}

// runAllModules must visit the second module. This test creates a two-module
// layout where the second module contains a vet error. If runAllModules only
// visited the root, the error would never be seen and the test would pass
// incorrectly.
func TestRunAllModules_ReachesSecondModule(t *testing.T) {
	root := t.TempDir()
	toolsDir := filepath.Join(root, "tools", "build")
	if err := os.MkdirAll(toolsDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Root module: valid, minimal.
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example\ngo 1.21\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package example\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Tools module: contains code that fails `go vet`.
	if err := os.WriteFile(filepath.Join(toolsDir, "go.mod"), []byte("module example/tools/build\ngo 1.21\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// Printf with a format verb and no argument: go vet reports this.
	badCode := "package build\nimport \"fmt\"\nfunc init() { fmt.Printf(\"%d\") }\n"
	if err := os.WriteFile(filepath.Join(toolsDir, "bad.go"), []byte(badCode), 0644); err != nil {
		t.Fatal(err)
	}

	err := runAllModules(root, io.Discard, "vet")
	if err == nil {
		t.Fatal("runAllModules returned nil; the second module has a vet error that should have been caught")
	}
}

// runAllModules must stop at the first failure rather than continuing. This is
// the same contract as the old single-RunIn path and as verifySteps ("break //
// stop at the first failure").
func TestRunAllModules_StopsAtFirstFailure(t *testing.T) {
	root := t.TempDir()
	// A root module with code that fails vet — no tools/build module at all.
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example\ngo 1.21\n"), 0644); err != nil {
		t.Fatal(err)
	}
	badCode := "package example\nimport \"fmt\"\nfunc init() { fmt.Printf(\"%d\") }\n"
	if err := os.WriteFile(filepath.Join(root, "bad.go"), []byte(badCode), 0644); err != nil {
		t.Fatal(err)
	}

	err := runAllModules(root, io.Discard, "vet")
	if err == nil {
		t.Fatal("runAllModules returned nil for a module that fails vet")
	}
}

// verifySteps in a Go project must include vet, build, and test — and after
// the fix, each must go through runAllModules (indirectly via the step
// closures). This test verifies the step names are present; the behaviour of
// each step is tested above.
func TestVerifySteps_GoProjectHasExpectedSteps(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example\n"), 0644); err != nil {
		t.Fatal(err)
	}

	steps := verifySteps(root)
	want := []string{"format", "vet", "build", "test"}
	if len(steps) != len(want) {
		t.Fatalf("got %d steps, want %d", len(steps), len(want))
	}
	for i, w := range want {
		if steps[i].name != w {
			t.Errorf("step[%d].name = %q, want %q", i, steps[i].name, w)
		}
	}
}

// A non-Go project gets stub steps rather than real Go tooling. The stubs must
// succeed (not error) — they are placeholders, not failures.
func TestVerifySteps_NonGoProjectGetsStubs(t *testing.T) {
	root := t.TempDir() // no go.mod

	steps := verifySteps(root)
	if len(steps) != 4 {
		t.Fatalf("got %d stub steps, want 4", len(steps))
	}
	for _, s := range steps {
		if err := s.run(root, io.Discard); err != nil {
			t.Errorf("stub step %q failed: %v", s.name, err)
		}
	}
}

// THE SUMMARY ALWAYS PRINTS, and it ends at the one line a person reads to know
// whether they may commit (docs/project-tools.md, Verify). The result now
// travels out of the pipeline and the library renders it, so the line that used
// to be a fmt.Println inside the loop is reachable only through Human — and a
// rendering nothing asserts is a rendering that can quietly lose the verdict it
// exists to state.
func TestVerifyResultHumanEndsAtTheVerdict(t *testing.T) {
	green := runVerifySteps(t.TempDir(), []step{
		{"format", func(string, io.Writer) error { return nil }},
	}, io.Discard)
	var shown strings.Builder
	if err := green.Human(&shown); err != nil {
		t.Fatalf("rendering a green run: %v", err)
	}
	if !strings.HasSuffix(shown.String(), "✅ OK to Commit\n") {
		t.Errorf("a green run ends:\n%s\nwant the OK to Commit line last", shown.String())
	}

	red := runVerifySteps(t.TempDir(), []step{
		{"format", func(string, io.Writer) error { return nil }},
		{"vet", func(string, io.Writer) error { return errors.New("3 findings in tools-build") }},
		{"test", func(string, io.Writer) error { t.Error("a step after the failure ran"); return nil }},
	}, io.Discard)

	shown.Reset()
	if err := red.Human(&shown); err != nil {
		t.Fatalf("rendering a red run: %v", err)
	}
	if !strings.HasSuffix(shown.String(), "❌ Verify FAILED: not safe to commit\n") {
		t.Errorf("a red run ends:\n%s\nwant the FAILED line last", shown.String())
	}
	// Every step, and the evidence of the failure, so the last lines of the
	// output carry all of it and nobody re-runs to learn what went wrong.
	for _, want := range []string{"format", "vet", "3 findings in tools-build", "test"} {
		if !strings.Contains(shown.String(), want) {
			t.Errorf("the summary does not say %q:\n%s", want, shown.String())
		}
	}

	// And the same evidence in the JSON a program reads. What the step failed
	// with is the summary's whole value: a status with no detail sends the
	// reader back to re-run the thing that just told them.
	failed := red.Stages[1].Steps[0]
	if failed.Status != statusFailed || failed.Detail != "3 findings in tools-build" {
		t.Errorf("the failing step is %+v, want it failed with its detail", failed)
	}
}

// A CHILD'S OUTPUT IS NARRATION, NEVER THE RESULT. `go test` reports on stdout,
// and a step that let that through would be writing test output into the stream
// a caller is parsing — the defect primitives.RunInStreams exists to prevent.
// Passing io.Discard everywhere, as the tests above do, cannot tell the two
// apart: a step reverted to primitives.RunIn writes to the process's own stdout
// and every one of them still passes.
func TestRunAllModulesWritesTheChildsOutputToTheNarration(t *testing.T) {
	root := t.TempDir()
	writeVerifyModule(t, root, "module example\n\ngo 1.26\n",
		"package example\n\nimport \"testing\"\n\nfunc TestSaysSomething(t *testing.T) { t.Log(\"measured\") }\n")

	restore := captureStdout(t)
	var narration strings.Builder
	err := runAllModules(root, &narration, "test")
	written := restore()

	if err != nil {
		t.Fatalf("go test over a passing module: %v (%s)", err, narration.String())
	}
	if !strings.Contains(narration.String(), "ok") {
		t.Errorf("the narration is %q, want the child's report in it", narration.String())
	}
	if written != "" {
		t.Errorf("the child wrote %q to the tool's own stdout, where the result goes", written)
	}
}

// writeVerifyModule lays down a one-module Go tree for the steps to run over.
func writeVerifyModule(t *testing.T, root, gomod, body string) {
	t.Helper()
	for name, content := range map[string]string{"go.mod": gomod, "example_test.go": body} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// captureStdout points os.Stdout at a pipe and returns what was written to it.
// It is the only way to see the stream a step must not reach: every other
// writer in this package is one the caller passed in.
func captureStdout(t *testing.T) func() string {
	t.Helper()
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	real := os.Stdout
	os.Stdout = write

	done := make(chan string, 1)
	go func() {
		var buf strings.Builder
		_, _ = io.Copy(&buf, read)
		done <- buf.String()
	}()

	return func() string {
		os.Stdout = real
		write.Close()
		captured := <-done
		read.Close()
		return captured
	}
}
