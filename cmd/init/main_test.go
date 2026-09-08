package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRootModulePath(t *testing.T) {
	dir := t.TempDir()
	if got := rootModulePath(dir); got != "" {
		t.Errorf("a directory with no go.mod reported module %q", got)
	}
	write(t, dir, "go.mod", "// a comment first\n\nmodule example.com/thing\n\ngo 1.26\n")
	if got, want := rootModulePath(dir), "example.com/thing"; got != want {
		t.Errorf("rootModulePath = %q, want %q", got, want)
	}
}

// The tools module nests under the project's own module path when there is one,
// so the scaffolded imports resolve without the adopter editing anything.
func TestToolsModule(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "go.mod", "module example.com/thing\n")
	if got, want := toolsModule(dir), "example.com/thing/tools/build"; got != want {
		t.Errorf("toolsModule = %q, want %q", got, want)
	}

	// With no root module the directory name stands in, and a space in it would
	// make an unusable import path.
	bare := filepath.Join(t.TempDir(), "my project")
	if err := os.MkdirAll(bare, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, want := toolsModule(bare), "my-project/tools/build"; got != want {
		t.Errorf("toolsModule = %q, want %q", got, want)
	}
}

func TestWriteFileCreatesSubstitutesAndMarksExecutable(t *testing.T) {
	dir := t.TempDir()
	writeFile(dir, file{path: "tools/build/go.mod", body: goMod}, "example/tools/build", false)
	got := read(t, filepath.Join(dir, "tools", "build", "go.mod"))
	if strings.Contains(got, "__MODULE__") {
		t.Error("the module placeholder survived into the written file")
	}
	if !strings.Contains(got, "module example/tools/build") {
		t.Errorf("module line not substituted: %q", got)
	}

	writeFile(dir, file{path: "make", body: makeSh, exec: true}, "m", false)
	info, err := os.Stat(filepath.Join(dir, "make"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("./make was written without an executable bit: %v", info.Mode())
	}
}

// An existing file is the adopter's, and is left alone unless -force is given:
// scaffolding into a live repository must not silently replace what is there.
func TestWriteFileSkipsExistingUnlessForced(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "make", "MINE\n")

	writeFile(dir, file{path: "make", body: makeSh}, "m", false)
	if got := read(t, filepath.Join(dir, "make")); got != "MINE\n" {
		t.Errorf("an existing file was overwritten without -force: %q", got)
	}

	writeFile(dir, file{path: "make", body: makeSh}, "m", true)
	if got := read(t, filepath.Join(dir, "make")); got == "MINE\n" {
		t.Error("-force did not overwrite")
	}
}

func TestEnsureGitignoreAppendsOnceAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	ensureGitignore(dir)
	first := read(t, filepath.Join(dir, ".gitignore"))
	if !strings.Contains(first, "bin/") {
		t.Fatalf("bin/ was not ignored: %q", first)
	}
	ensureGitignore(dir)
	if second := read(t, filepath.Join(dir, ".gitignore")); second != first {
		t.Errorf("a second run changed .gitignore:\n%q\n%q", first, second)
	}
}

func TestEnsureGitignoreLeavesAnExistingRuleAlone(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".gitignore", "# mine\nbin/\n")
	ensureGitignore(dir)
	if got := read(t, filepath.Join(dir, ".gitignore")); got != "# mine\nbin/\n" {
		t.Errorf(".gitignore was edited when bin/ was already ignored: %q", got)
	}
}

// The build workflow is written to CLAUDE.md so agents discover it. It appends
// rather than replaces, because an adopter's CLAUDE.md is theirs.
func TestEnsureBuildDocAppendsAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "CLAUDE.md", "# Existing\n")
	ensureBuildDoc(dir)
	got := read(t, filepath.Join(dir, "CLAUDE.md"))
	if !strings.HasPrefix(got, "# Existing\n") {
		t.Error("existing CLAUDE.md content was lost")
	}
	if !strings.Contains(got, buildDocMarker) {
		t.Error("the dev-tooling section was not appended")
	}
	ensureBuildDoc(dir)
	if again := read(t, filepath.Join(dir, "CLAUDE.md")); again != got {
		t.Error("a second run appended the section twice")
	}
}

func TestEnsureBuildDocCreatesTheFileWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	ensureBuildDoc(dir)
	if !strings.Contains(read(t, filepath.Join(dir, "CLAUDE.md")), buildDocMarker) {
		t.Error("CLAUDE.md was not created with the dev-tooling section")
	}
}

func TestExists(t *testing.T) {
	dir := t.TempDir()
	if exists(filepath.Join(dir, "nope")) {
		t.Error("a missing path reported as existing")
	}
	if !exists(dir) {
		t.Error("an existing directory reported as missing")
	}
}

// The doc body is written with § standing in for a backtick, because the body
// itself lives inside a Go raw string literal and cannot contain one.
func TestSubstituteBackticks(t *testing.T) {
	if got, want := substituteBackticks("run §./make§"), "run `./make`"; got != want {
		t.Errorf("substituteBackticks = %q, want %q", got, want)
	}
	if strings.Contains(buildDocBlock, "§") {
		t.Error("the rendered doc block still carries a placeholder")
	}
}

func TestFilesAreDistinctAndNonEmpty(t *testing.T) {
	seen := map[string]bool{}
	for _, f := range files() {
		if f.path == "" {
			t.Error("a scaffolded file has no path")
		}
		if seen[f.path] {
			t.Errorf("%s is scaffolded twice", f.path)
		}
		seen[f.path] = true
		if strings.TrimSpace(f.body) == "" {
			t.Errorf("%s has an empty body", f.path)
		}
	}
	for _, want := range []string{"make", "make.cmd", "tools/build/go.mod", ".githooks/pre-commit"} {
		if !seen[want] {
			t.Errorf("%s is not scaffolded", want)
		}
	}
}

// The tools module declares one Go version, the same in every project, so a
// scaffolded module never disagrees with the tools that build it.
func TestScaffoldedGoModDeclaresTheFixedVersion(t *testing.T) {
	if !strings.Contains(goMod, "go 1.26") {
		t.Errorf("the scaffolded go.mod does not declare go 1.26: %q", goMod)
	}
}

// The whole point of the scaffolder is that what it writes runs. This lays the
// tree down and compiles it, which is the only check that cannot drift from
// what an adopter actually gets.
func TestScaffoldedTreeCompiles(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles the scaffolded tree; skipped under -short")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go toolchain")
	}
	dir := t.TempDir()
	const mod = "example/tools/build"
	for _, f := range files() {
		writeFile(dir, f, mod, false)
	}
	cmd := exec.Command("go", "build", "./...")
	cmd.Dir = filepath.Join(dir, "tools", "build")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the scaffolded tools module does not compile: %v\n%s", err, out)
	}
}

// A record of what the scaffolder emits today, and it is wrong in two ways that
// matter to any project the workspace manages: it omits `gate` and `run`, which
// the tool contract requires, and it emits `guard` and `precommit`, which are
// twins of workspace tools a project may not build.
//
// It is asserted rather than described so that closing it fails here. The day
// cmd/init scaffolds a conformant project, this test is what says so.
func TestKnownDefect_ScaffoldedToolSetIsNotConformant(t *testing.T) {
	have := map[string]bool{}
	for _, f := range files() {
		if rest, ok := strings.CutPrefix(f.path, "tools/build/cmd/"); ok {
			have[strings.SplitN(rest, "/", 2)[0]] = true
		}
	}
	missing := !have["gate"] && !have["run"]
	twins := have["guard"] && have["precommit"]
	if missing && twins {
		return // the defect, unchanged
	}
	t.Errorf("the scaffolded tool set has moved: gate=%t run=%t guard=%t precommit=%t — "+
		"if it is now conformant, delete this test and assert the real set instead",
		have["gate"], have["run"], have["guard"], have["precommit"])
}
