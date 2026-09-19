package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/promise-language/forge/primitives/command"
	"github.com/promise-language/forge/primitives/tooling"
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

	// The version is the second placeholder, and it is substituted rather than
	// written into each body so go.mod and go.sum cannot name different ones.
	if strings.Contains(got, "__FORGE_VERSION__") {
		t.Error("the version placeholder survived into the written file")
	}
	if !strings.Contains(got, "require github.com/promise-language/forge "+forgeVersion) {
		t.Errorf("the pin was not substituted: %q", got)
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

// placeholderPattern is the spelling every placeholder an emitted body carries
// uses: __MODULE__, __FORGE_VERSION__.
var placeholderPattern = regexp.MustCompile(`__[A-Z][A-Z_]*__`)

// A body may only carry a placeholder writeFile substitutes. Adding one to a
// constant and not to writeFile is a silent defect of exactly the shape this
// scaffolder exists to avoid: it is not a build failure here, it is a literal
// __SOMETHING__ in an adopter's committed tree, in a file nobody re-reads after
// init prints its report.
//
// The check runs every body through writeFile rather than substituting here, so
// it measures what an adopter receives rather than a second copy of the rule.
func TestNoEmittedFileReachesAnAdopterWithAPlaceholderLeft(t *testing.T) {
	dir := t.TempDir()
	for _, f := range files() {
		if _, err := writeFile(dir, f, "example.com/project/tools/build", false); err != nil {
			t.Fatal(err)
		}
		body := read(t, filepath.Join(dir, filepath.FromSlash(f.path)))
		if left := placeholderPattern.FindString(body); left != "" {
			t.Errorf("%s reaches the adopter carrying %s, which writeFile does not substitute", f.path, left)
		}
	}
}

// The pin names this repository, and its name is declared in one place: the
// root go.mod. A rename that moved the module would leave the scaffolder
// pinning a path nothing serves — every adopter's first ./make failing on a
// module that cannot be fetched, while a test comparing against a hard-coded
// string kept passing.
func TestTheEmittedPinNamesThisRepositorysModule(t *testing.T) {
	module := rootModulePath(filepath.Join("..", ".."))
	if module == "" {
		t.Fatal("this repository declares no module path")
	}
	if !strings.Contains(goMod, "require "+module+" __FORGE_VERSION__") {
		t.Errorf("the emitted go.mod does not require %q:\n%s", module, goMod)
	}
	for _, line := range strings.Split(strings.TrimSpace(goSum), "\n") {
		if !strings.HasPrefix(line, module+" ") {
			t.Errorf("the emitted go.sum names something other than %q: %q", module, line)
		}
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

// Every per-clone path, not just bin/. A workspace refuses a checkout that
// leaves one of them un-ignored, so a partial answer here is a project that
// cannot be provisioned — and the old behaviour (append bin/ unless the file
// already mentions it anywhere) gave exactly that.
func TestEnsureGitignoreCoversEveryPerClonePath(t *testing.T) {
	dir := t.TempDir()
	ensureGitignore(dir)
	got := read(t, filepath.Join(dir, ".gitignore"))
	for _, p := range perClonePaths {
		if !strings.Contains(got, "\n"+p+"\n") {
			t.Errorf("%s is not ignored:\n%s", p, got)
		}
	}

	// An existing file keeps its content and gains only what is missing.
	partial := t.TempDir()
	write(t, partial, ".gitignore", "# mine\nbin/\n")
	ensureGitignore(partial)
	got = read(t, filepath.Join(partial, ".gitignore"))
	if !strings.HasPrefix(got, "# mine\nbin/\n") {
		t.Errorf("existing content was not preserved: %q", got)
	}
	if n := strings.Count(got, "\nbin/\n"); n != 1 {
		t.Errorf("bin/ appears %d times; a rule already present is not re-added:\n%s", n, got)
	}
	for _, p := range perClonePaths[1:] {
		if !strings.Contains(got, "\n"+p+"\n") {
			t.Errorf("%s was not added to an existing .gitignore:\n%s", p, got)
		}
	}

	// The anchored spelling ignores the same path, so a file already carrying
	// every rule that way is left exactly as it is.
	anchored := t.TempDir()
	body := "# mine\n"
	for _, p := range perClonePaths {
		body += "/" + p + "\n"
	}
	write(t, anchored, ".gitignore", body)
	ensureGitignore(anchored)
	if got := read(t, filepath.Join(anchored, ".gitignore")); got != body {
		t.Errorf("the anchored spelling was not recognized:\n%q\n%q", body, got)
	}
}

// A commented-out rule is not a rule, and a path that merely appears inside a
// longer line is not one either.
func TestMissingIgnoreRulesReadsRulesNotText(t *testing.T) {
	if got := missingIgnoreRules("# bin/\n"); len(got) != len(perClonePaths) {
		t.Errorf("a commented-out rule was counted as a rule: %v", got)
	}
	if got := missingIgnoreRules("!.workspace/\n"); len(got) != len(perClonePaths) {
		t.Errorf("a negation was read as the rule it negates: %v", got)
	}
	all := ""
	for _, p := range perClonePaths {
		all += p + "\n"
	}
	if got := missingIgnoreRules(all); len(got) != 0 {
		t.Errorf("rules already present were reported missing: %v", got)
	}
}

// A scaffolded project must pass its own `setup`, whose second step refuses
// until the committed .gitignore ignores every entry tooling.IgnoredDirs()
// names, as `git check-ignore` reports them (docs/project-tools.md, Setup).
//
// It is asked of the RULE rather than of perClonePaths, because a test derived
// from the same list the code writes agrees with that list whatever is in it —
// which is how `.home/` came to be missing from both at once. tooling holds the
// one statement of what setup requires; this asks git whether the emitted file
// satisfies it.
//
// One path per call. check-ignore exits 0 when ANY argument is ignored, so a
// single call naming all three would answer 0 with two of them missing. The
// entries are spelled as .gitignore carries them, anchored at the root, and a
// path beginning with "/" is an absolute one — never inside the repository — so
// the anchor comes off exactly as CheckIgnores takes it off. The trailing slash
// stays: a directory rule does not match the name without it.
func TestTheScaffoldedGitignoreCarriesWhatSetupRequires(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git on this machine")
	}
	ignores := scaffoldedIgnores(t, true)
	for _, entry := range tooling.IgnoredDirs() {
		out, code := ignores(strings.TrimPrefix(entry, "/"))
		if code == 1 {
			t.Errorf("a scaffolded project does not ignore %s, so its own setup refuses it", entry)
		} else if code != 0 {
			t.Fatalf("git check-ignore answered neither ignored nor tracked for %s (%d):\n%s", entry, code, out)
		}
	}
}

// One run writes both the emitted files and the .gitignore beside them, and the
// two must not disagree: what the scaffolder emits, it emits to be COMMITTED.
// `.claude/settings.json` is the case that makes this load-bearing — it is
// committed precisely so the agent guard is live in a checkout provisioning has
// never reached (docs/blueprint.md, Tools the project does not build), so a run
// that emitted it into a tree ignoring it would produce exactly the state the
// decision exists to prevent, and produce it silently: the file is on disk, the
// scaffolder reports it written, and the clone carries no wiring at all.
//
// The per-clone sibling one directory apart is why a matcher is not enough.
// `.claude/settings.local.json` IS ignored, and widening that entry to
// `.claude/` — the spelling `bin/` and `.workspace/` already carry — passes every
// other check in this file: the settings file is still emitted, no .githooks/
// appears, the wiring still parses, and every path in perClonePaths is still in
// the .gitignore, because the test for that is derived from the same list.
//
// git answers the question rather than a rule-matcher written here. A second
// reading of .gitignore semantics is the copy that agrees with the
// implementation and disagrees with the tool that actually decides.
func TestNoEmittedFileIsIgnoredByTheGitignoreTheSameRunWrites(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git on this machine")
	}
	ignores := scaffoldedIgnores(t, true)

	var emitted []string
	for _, f := range files() {
		emitted = append(emitted, f.path)
	}
	// check-ignore exits 0 when at least one path is ignored and names the ones
	// that are; 1 when none is. Anything else is git refusing the question.
	if out, code := ignores(emitted...); code == 0 {
		t.Errorf("the scaffolder emits files its own .gitignore ignores, so a clone carries none of them:\n%s", out)
	} else if code != 1 {
		t.Fatalf("git check-ignore answered neither ignored nor tracked (%d):\n%s", code, out)
	}

	// The control. Without it a run where check-ignore matched nothing for a
	// reason of its own — the wrong directory, a .gitignore that was never
	// written — reads as every emitted file being tracked, which is the shape of
	// a test that passes whatever the code does.
	if out, code := ignores(".claude/settings.local.json"); code != 0 {
		t.Errorf("the per-clone sibling is not ignored (%d), so the check above proves nothing:\n%s", code, out)
	}
}

// The control above is only a control if it answers from the .gitignore the run
// wrote. Asked of the same tree with that one step left out, it must report the
// sibling TRACKED — and on a machine whose own excludes cover the path it will
// not, which is the state this pair exists to catch. Without this test the
// control passes on such a machine whatever ensureGitignore did, so the whole
// check reads as coverage while asserting nothing about the repository.
func TestTheSiblingIsIgnoredByTheScaffoldedRuleAndNothingElse(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git on this machine")
	}
	if out, code := scaffoldedIgnores(t, false)(".claude/settings.local.json"); code != 1 {
		t.Errorf("with no .gitignore written, git still answers that the sibling is ignored (%d) — the rule came from outside the repository:\n%s", code, out)
	}
}

// scaffoldedIgnores lays the emitted files down in a fresh git repository and
// returns git's answer to "does this repository ignore these paths": the output,
// and check-ignore's status. writeIgnores selects whether ensureGitignore runs,
// which is the one difference between the check above and its own control.
//
// Every query is asked with the ignore sources OUTSIDE the repository out of the
// way, because an ignore rule only counts when the repository carries it, and a
// rule from the machine's global config does not (workspace's `bootstrap.md`,
// Setup owns the installed name set). Left in, a developer's excludes file
// answers for the repository: green here and red on the next machine, and a
// failure that names a scaffolder defect nobody can reproduce.
func scaffoldedIgnores(t *testing.T, writeIgnores bool) func(paths ...string) (string, int) {
	t.Helper()
	dir := t.TempDir()
	for _, f := range files() {
		if _, err := writeFile(dir, f, "example/tools/build", false); err != nil {
			t.Fatal(err)
		}
	}
	if writeIgnores {
		if _, err := ensureGitignore(dir); err != nil {
			t.Fatal(err)
		}
	}
	// A path that does not exist carries no patterns, and it is inside the temp
	// tree so nothing outside can make it exist. Slashes, because that is the
	// spelling git config reads on every OS this repository builds for.
	noExcludes := "core.excludesFile=" + filepath.ToSlash(filepath.Join(dir, "no-such-excludes-file"))
	git := func(args ...string) (string, int) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", noExcludes}, args...)...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		var status *exec.ExitError
		if errors.As(err, &status) {
			return string(out), status.ExitCode()
		}
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return string(out), 0
	}
	if out, code := git("init"); code != 0 {
		t.Fatalf("git init failed (%d):\n%s", code, out)
	}
	// The other source that is not a file the repository carries. A fresh init
	// leaves it holding nothing but comments; emptying it states that rather than
	// depending on it.
	if err := os.WriteFile(filepath.Join(dir, ".git", "info", "exclude"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return func(paths ...string) (string, int) {
		t.Helper()
		return git(append([]string{"check-ignore", "--"}, paths...)...)
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

// An append that cannot even be opened is an error, not a written record: a
// section that never reached the disk must not be reported as one that did. A
// directory standing where the file goes is the one way to make the open fail
// identically on every platform.
//
// This covers the open only. What the close reports is
// TestWriteAndCloseReportsTheCloseAsWellAsTheWrite, because no filesystem call
// can be asked to fail a close on demand.
func TestAnAppendThatCannotHappenIsReportedAndNotRecorded(t *testing.T) {
	for _, c := range []struct {
		name   string
		file   string
		ensure func(string) (written, error)
	}{
		{".gitignore", ".gitignore", ensureGitignore},
		{"CLAUDE.md", "CLAUDE.md", ensureBuildDoc},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.Mkdir(filepath.Join(dir, c.file), 0o755); err != nil {
				t.Fatal(err)
			}
			got, err := c.ensure(dir)
			if err == nil {
				t.Fatalf("appending into a directory reported success: %+v", got)
			}
			if got != (written{}) {
				t.Errorf("a failed append still answered a record: %+v", got)
			}
		})
	}
}

// appendToFile creates what is absent and appends to what is there, and it is
// the one implementation both ensure* functions write through.
func TestAppendToFileCreatesThenAppends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.md")
	if err := appendToFile(path, "one\n"); err != nil {
		t.Fatal(err)
	}
	if err := appendToFile(path, "two\n"); err != nil {
		t.Fatal(err)
	}
	if got := read(t, path); got != "one\ntwo\n" {
		t.Errorf("appendToFile wrote %q, want %q", got, "one\ntwo\n")
	}
}

// recordingFile is a file whose write and close can be made to fail, which a
// real one cannot: a close that fails to flush is the condition the discarded
// close hid, and it is unreachable through the filesystem.
type recordingFile struct {
	written  strings.Builder
	writeErr error
	closeErr error
	closes   int
}

func (f *recordingFile) Write(p []byte) (int, error) {
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	return f.written.Write(p)
}

func (f *recordingFile) Close() error {
	f.closes++
	return f.closeErr
}

// A close that fails is a write that did not land, and it must reach the
// caller — init prints "update .gitignore" off a nil error, so a swallowed
// close is a report of a write that is not there. The write's own error must
// survive alongside it, and the file must be closed on either path.
func TestWriteAndCloseReportsTheCloseAsWellAsTheWrite(t *testing.T) {
	writeFailed := errors.New("no space left on device")
	closeFailed := errors.New("flushing on close failed")

	for _, c := range []struct {
		name          string
		file          recordingFile
		wants         []error
		wantsNoError  bool
		wantsRecorded string
	}{
		{name: "both succeed", wantsNoError: true, wantsRecorded: "body"},
		{
			name:          "the close fails after a write that succeeded",
			file:          recordingFile{closeErr: closeFailed},
			wants:         []error{closeFailed},
			wantsRecorded: "body",
		},
		{
			name:  "the write fails",
			file:  recordingFile{writeErr: writeFailed},
			wants: []error{writeFailed},
		},
		{
			name:  "both fail",
			file:  recordingFile{writeErr: writeFailed, closeErr: closeFailed},
			wants: []error{writeFailed, closeFailed},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := writeAndClose(&c.file, "body")
			if c.wantsNoError && err != nil {
				t.Fatalf("a write and close that both succeeded answered %v", err)
			}
			if !c.wantsNoError && err == nil {
				t.Fatal("a write or close that failed answered no error")
			}
			for _, want := range c.wants {
				if !errors.Is(err, want) {
					t.Errorf("error %v does not carry %v", err, want)
				}
			}
			if c.file.closes != 1 {
				t.Errorf("the file was closed %d times, want once", c.file.closes)
			}
			if c.file.written.String() != c.wantsRecorded {
				t.Errorf("the file received %q, want %q", c.file.written.String(), c.wantsRecorded)
			}
		})
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

// § stands in for the backtick in every constant substituteBackticks touches,
// so inside one of those it can mean nothing else. A section reference written
// there as §7 reaches the adopter's tree as `7 — a mangled comment nobody
// upstream ever reads, because the scaffolder's own source looks right.
//
// The two spellings are each other's tell, and neither needs to know which
// constants were substituted: in an emitted body a § is a section sign, so it
// is followed by a digit, and a backtick opens code, so it is not. Every body
// is checked, so the next constant authored with § inherits the check rather
// than the defect.
//
// A section is addressed by its slug and never by a number (org/normative.md,
// Sections), and no emitted body carries a § citation any more: the last two went
// with the pre-commit hook this scaffolder no longer writes. What is left here is
// the guard for the next constant authored with §.
func TestEmittedFilesUseTheSectionSignOnlyForBackticks(t *testing.T) {
	for _, f := range files() {
		for _, line := range strings.Split(f.body, "\n") {
			if next, ok := charAfter(line, "`"); ok && next >= '0' && next <= '9' {
				t.Errorf("%s: a section sign was substituted as a backtick: %q", f.path, line)
			}
			if next, ok := charAfter(line, "§"); ok && !(next >= '0' && next <= '9') {
				t.Errorf("%s: a backtick placeholder was written out unsubstituted: %q", f.path, line)
			}
		}
	}
}

// charAfter reports the byte following the first occurrence of sub in line.
func charAfter(line, sub string) (byte, bool) {
	i := strings.Index(line, sub)
	if i < 0 || i+len(sub) >= len(line) {
		return 0, false
	}
	return line[i+len(sub)], true
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
	for _, want := range []string{
		"make", "make.cmd", "tools/build/go.mod", "tools/build/go.sum",
		"tools/build/cmd/gate/main.go", "tools/build/cmd/run/main.go",
		"tools/gates/thresholds.json", "tools/gates/baselines.json",
		"docs/index.md", ".claude/settings.json",
	} {
		if !seen[want] {
			t.Errorf("%s is not scaffolded", want)
		}
	}
	// The commit-gate hook is written by `workspace setup` alongside the
	// bin/precommit-guard it names, so that one party owns both ends of the
	// guard (docs/blueprint.md, The commit gate hook). A scaffolder that emitted
	// it would put a copy of the guard's posture into every adopting repository.
	for _, refused := range []string{".githooks/pre-commit", ".githooks"} {
		if seen[refused] {
			t.Errorf("%s is scaffolded; the commit-gate hook is provisioning's to write", refused)
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

// The pin is what the copies were replaced by (docs/primitives.md, The
// dependency is pinned), so a scaffolded module must require forge at one exact
// version — and its go.sum must name the SAME one. They are two files, and a
// bump that edited one and not the other would leave a tree that does not
// build, which is why the version is substituted from one constant rather than
// typed into each body.
func TestScaffoldedModulePinsForgeAtOneVersionInBothFiles(t *testing.T) {
	require := "require github.com/promise-language/forge " + forgeVersion
	mod := strings.ReplaceAll(goMod, "__FORGE_VERSION__", forgeVersion)
	if !strings.Contains(mod, require) {
		t.Errorf("the scaffolded go.mod does not pin forge:\n%s", mod)
	}

	sum := strings.ReplaceAll(goSum, "__FORGE_VERSION__", forgeVersion)
	for _, want := range []string{
		"github.com/promise-language/forge " + forgeVersion + " h1:",
		"github.com/promise-language/forge " + forgeVersion + "/go.mod h1:",
	} {
		if !strings.Contains(sum, want) {
			t.Errorf("the scaffolded go.sum has no %q line:\n%s", want, sum)
		}
	}
	// Neither file may name a version the other does not. A stray pseudo-version
	// left behind by a half-finished bump is exactly what this catches.
	for _, body := range []struct{ name, text string }{{"go.mod", mod}, {"go.sum", sum}} {
		for _, field := range strings.Fields(body.text) {
			if strings.HasPrefix(field, "v0.0.0-") && strings.TrimSuffix(field, "/go.mod") != forgeVersion {
				t.Errorf("%s names %q, not the one pinned version %q", body.name, field, forgeVersion)
			}
		}
	}
}

// A published version is immutable and a pseudo-version cannot name a commit
// that does not exist yet, so the pin is always a commit already on the remote.
// The shape is what this checks: v0.0.0-<utc timestamp>-<12 hex digits>.
func TestThePinnedVersionIsAPseudoVersion(t *testing.T) {
	rest, ok := strings.CutPrefix(forgeVersion, "v0.0.0-")
	if !ok {
		t.Fatalf("the pinned version %q is not a pseudo-version", forgeVersion)
	}
	stamp, hash, ok := strings.Cut(rest, "-")
	if !ok {
		t.Fatalf("the pinned version %q carries no commit hash", forgeVersion)
	}
	if len(stamp) != 14 {
		t.Errorf("the pinned version's timestamp %q is not 14 digits", stamp)
	}
	if len(hash) != 12 {
		t.Errorf("the pinned version's hash %q is not 12 digits", hash)
	}
	for _, c := range stamp + hash {
		if !strings.ContainsRune("0123456789abcdef", c) {
			t.Errorf("the pinned version %q carries %q, which is not hex", forgeVersion, c)
		}
	}
}

// Every helper primitives carries is gone from what is emitted. A project that
// held its own would be holding a latent disagreement with every other one
// (docs/primitives.md, One implementation), and the copies had already drifted:
// the emitted hash named each file relative to the directory it was found under
// where primitives names it from the repo root, so the two computed different
// digests for the same tree.
func TestNoEmittedFileRedeclaresAHelperPrimitivesCarries(t *testing.T) {
	lifted := []string{
		"func ToolsSourceHash(", "func SourceHash(",
		"func StaleReason(", "func CheckStale(", "func MakeCmd(",
		"func IsWindows(", "func ExeSuffix(", "func BinaryName(",
		"func Which(", "func Exists(",
		"func RunIn(", "func RunInStreams(", "func RunOutputIn(",
		"func OutputBytesIn(", "func RunSilent(",
		"func NormalizeArgs(", "func HasHelpFlag(", "func MaybeHelp(",
		"func RunSetup(",
	}
	for _, f := range files() {
		for _, decl := range lifted {
			if strings.Contains(f.body, decl) {
				t.Errorf("%s declares %q, which primitives carries", f.path, decl)
			}
		}
	}
	// The one record both ends of the verified-tree contract must agree on is
	// primitives.VerifiedTreeRecord, imported rather than typed (docs/
	// primitives.md, What belongs here). A second spelling is a permanent,
	// silent refusal: verify writes one path and the guard reads another.
	for _, f := range files() {
		if strings.HasSuffix(f.path, ".go") && strings.Contains(f.body, `= ".workspace/verified-tree"`) {
			t.Errorf("%s types the verified-tree path instead of importing the constant", f.path)
		}
	}
}

// No emitted tool reads os.Args for itself: what it parses, how it renders and
// what it exits with are the command library's (docs/command-line.md, One
// implementation). A parser written per tool disagrees with the guide in its own
// way, and the disagreements are the kind nothing tests.
func TestEveryEmittedToolIsBuiltOnTheCommandLibrary(t *testing.T) {
	var mains int
	for _, f := range files() {
		if !strings.HasPrefix(f.path, "tools/build/cmd/") {
			continue
		}
		mains++
		if !strings.Contains(f.body, "primitives/command") {
			t.Errorf("%s is not built on the command library", f.path)
		}
		if !strings.Contains(f.body, "command.Run(define(") {
			t.Errorf("%s does not hand its invocation to the library", f.path)
		}
		// -h is an abbreviation, and the library makes an abbreviation unknown
		// input rather than a flag. A tool advertising it sends a reader to an
		// invocation that exits 2.
		if strings.Contains(f.body, `"-h"`) || strings.Contains(f.body, "§-h§") {
			t.Errorf("%s spells help -h, which the library refuses as unknown input", f.path)
		}
	}
	if mains != 5 {
		t.Errorf("checked %d mains, want the 5 every project builds", mains)
	}
}

// Staleness reaches a tool as a refusal the library writes, with the recovery
// named — not as a check that calls os.Exit and leaves prose on stderr
// (docs/command-line.md, Exit status and refusal). make is the exception: it is
// the recovery every refusal names, so a Fit on it would close the way out.
func TestEveryEmittedToolButTheBuilderRefusesWhenStale(t *testing.T) {
	for _, f := range files() {
		rest, ok := strings.CutPrefix(f.path, "tools/build/cmd/")
		if !ok {
			continue
		}
		name := strings.SplitN(rest, "/", 2)[0]
		declares := strings.Contains(f.body, "primitives.StaleRefusal(")
		if name == "make" {
			if declares {
				t.Error("the meta-builder can refuse as stale, which closes the recovery it is")
			}
			continue
		}
		if !declares {
			t.Errorf("%s does not refuse when its binary is stale", f.path)
		}
	}
}

// scaffold lays the tree down in a fresh temp dir and returns it.
func scaffold(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	const mod = "example/tools/build"
	for _, f := range files() {
		writeFile(dir, f, mod, false)
	}
	ensureGitignore(dir)
	buildAgainstThisTree(t, dir)
	return dir
}

// buildAgainstThisTree points the scaffolded module's forge dependency at this
// working tree, and it is the one thing the test does to what the scaffolder
// wrote.
//
// What an adopter gets is the pin, and TestScaffoldedModulePinsForgeAtOneVersion
// InBothFiles is what holds it. Building the temp tree against the PUBLISHED
// version instead would ask a different and weaker question — whether the
// scaffolder's output compiles against a version cut before this change — and
// would answer it only on a machine with the module cached or a network to
// fetch it. Replacing it asks the question that catches a regression: does what
// cmd/init emits today compile against the primitives in this commit. It is the
// same accommodation this repository's own tools/build makes, for the same
// reason (docs/primitives.md, This repository is its own first consumer).
func buildAgainstThisTree(t *testing.T, dir string) {
	t.Helper()
	forgeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "tools", "build", "go.mod")
	body := read(t, path)
	body += "\nreplace github.com/promise-language/forge => " + filepath.ToSlash(forgeRoot) + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	// A path replacement is not described by any go.sum, so the emitted sums
	// no longer apply to the module being built and go refuses them.
	if err := os.Remove(filepath.Join(dir, "tools", "build", "go.sum")); err != nil {
		t.Fatal(err)
	}
}

// The whole point of the scaffolder is that what it writes runs. This lays the
// tree down, builds it the way ./make does, and then exercises the entry points
// the tool contract requires — which is the only check that cannot drift from
// what an adopter actually gets.
func TestScaffoldedTreeCompiles(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles and runs the scaffolded tree; skipped under -short")
	}
	for _, tool := range []string{"go", "git"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("no %s on this machine", tool)
		}
	}
	dir := scaffold(t)
	toolsDir := filepath.Join(dir, "tools", "build")

	run := func(name string, args ...string) string {
		t.Helper()
		cmd := exec.Command(name, args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s %s failed: %v\n%s", name, strings.Join(args, " "), err, out)
		}
		return string(out)
	}

	build := exec.Command("go", "build", "./...")
	build.Dir = toolsDir
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("the scaffolded tools module does not compile: %v\n%s", err, out)
	}

	// The starter's own tests, run inside this suite: an adopter inherits them,
	// so a starter whose tests fail is one every adopter starts out red.
	test := exec.Command("go", "test", "./...")
	test.Dir = toolsDir
	if out, err := test.CombinedOutput(); err != nil {
		t.Fatalf("the scaffolded tools module does not pass its own tests: %v\n%s", err, out)
	}

	// git init, then the meta-builder exactly as ./make invokes it. The repo is
	// needed for the record step and for the hooks wiring.
	run("git", "init", "-q")
	run("go", "run", "-C", toolsDir, "./cmd/make")

	// bin/verify passes and leaves the tree it blessed behind — the reading end
	// is the workspace commit gate, which refuses a commit of any other tree.
	//
	// Its result is read off stdout rather than from the summary, because the
	// summary is the rendering for a person and the object is what a caller acts
	// on (project-tools.md, Verify fixes its fields). The blessing has two ends
	// here too: what the result SAYS was blessed, and what the record on disk
	// holds. A run that answered one tree id and wrote another would pass every
	// check that reads only one of them, and the commit gate reads the other.
	green := exec.Command(filepath.Join(dir, "bin", "verify"))
	green.Dir = dir
	greenOut, err := green.Output()
	if err != nil {
		t.Fatalf("bin/verify failed on a freshly scaffolded tree: %v\n%s", err, greenOut)
	}
	var passed verifyResult
	if err := json.Unmarshal(greenOut, &passed); err != nil {
		t.Fatalf("bin/verify through a pipe is not one JSON object: %v\n%s", err, greenOut)
	}
	if !passed.OK {
		t.Errorf("a green run did not report ok: %s", greenOut)
	}
	for _, stage := range passed.Stages {
		for _, s := range stage.Steps {
			if s.Status != "passed" {
				t.Errorf("a green run reports step %q as %q", s.Name, s.Status)
			}
		}
	}
	record := strings.TrimSpace(read(t, filepath.Join(dir, ".workspace", "verified-tree")))
	if len(strings.Fields(record)) != 1 || len(record) < 20 {
		t.Errorf(".workspace/verified-tree does not hold one tree id: %q", record)
	}
	if passed.Tree != record {
		t.Errorf("verify answered tree %q and recorded %q; the commit gate reads the record", passed.Tree, record)
	}

	// What this project builds and what it answers, discovered by asking. Nothing
	// outside the tree may hold a second copy of either list — `workspace setup`
	// reads this — so the names come back from the same functions the
	// meta-builder compiled from (project-tools.md, Run).
	t.Run("run --list says what is built and what is answered", func(t *testing.T) {
		listed := exec.Command(filepath.Join(dir, "bin", "run"), "--list")
		listed.Dir = dir
		out, err := listed.Output()
		if err != nil {
			t.Fatalf("bin/run --list failed: %v", err)
		}
		var answer struct {
			Commands []string `json:"commands"`
			Gates    []string `json:"gates"`
		}
		if err := json.Unmarshal(out, &answer); err != nil {
			t.Fatalf("bin/run --list through a pipe is not one JSON object: %v\n%s", err, out)
		}
		// Every main this scaffolder emits, except the meta-builder: make runs
		// from source via the ./make trampoline and is never in bin/, so a caller
		// asking what this project puts there must not be told about it — and a
		// caller that dispatched to the name would get the refusal for a missing
		// binary.
		want := emittedCommandNames()
		if !slices.Equal(answer.Commands, want) {
			t.Errorf("bin/run --list reports commands %v, want %v", answer.Commands, want)
		}
		if slices.Contains(answer.Commands, "make") {
			t.Error("bin/run --list names make, which is never built into bin/")
		}
		for _, want := range []string{"integration", "fit"} {
			if !slices.Contains(answer.Gates, want) {
				t.Errorf("bin/run --list does not answer %q:\n%s", want, out)
			}
		}
	})

	// setup is what points git at .githooks, and it is the reason the scaffolder
	// can emit no hook file: the directory is wired here, and `workspace setup`
	// writes the hook into it (blueprint.md, The commit gate hook). The wiring is
	// cleared first, so what this measures is setup doing it rather than the
	// ./make above having already done it.
	t.Run("setup points git at the hooks directory", func(t *testing.T) {
		run("git", "config", "--unset", "core.hooksPath")
		wired := exec.Command(filepath.Join(dir, "bin", "setup"))
		wired.Dir = dir
		out, err := wired.Output()
		if err != nil {
			t.Fatalf("bin/setup failed: %v\n%s", err, out)
		}
		var answer struct {
			HooksPath string `json:"hooks_path"`
			Steps     []struct {
				Name    string `json:"name"`
				Changed bool   `json:"changed"`
			} `json:"steps"`
		}
		if err := json.Unmarshal(out, &answer); err != nil {
			t.Fatalf("bin/setup through a pipe is not one JSON object: %v\n%s", err, out)
		}
		if answer.HooksPath != ".githooks" {
			t.Errorf("bin/setup reports hooks_path %q, want %q", answer.HooksPath, ".githooks")
		}
		// What it reported, checked against what git now holds: a result naming a
		// path git was never told about is the one failure a caller cannot see.
		if got := strings.TrimSpace(run("git", "config", "--get", "core.hooksPath")); got != answer.HooksPath {
			t.Errorf("git reads hooks from %q, and setup answered %q", got, answer.HooksPath)
		}
	})

	// `gate --list` is how anything outside the tree discovers what this project
	// answers, and both required names must be in it.
	//
	// Through a pipe it is JSON, because that is what a program reads and what
	// can grow additively; one name per line is the rendering for a person at a
	// terminal (org/cli-guide.md, Output modes). This test is the program, so it
	// parses the object rather than the lines — an orchestrator that split this
	// stream on newlines would be reading the human form it never receives.
	list := run(filepath.Join(dir, "bin", "gate"), "--list")
	var listed struct {
		Gates []struct {
			Name    string `json:"name"`
			Summary string `json:"summary"`
		} `json:"gates"`
	}
	if err := json.Unmarshal([]byte(list), &listed); err != nil {
		t.Fatalf("bin/gate --list through a pipe is not one JSON object: %v\n%s", err, list)
	}
	var names []string
	for _, g := range listed.Gates {
		if g.Summary == "" {
			t.Errorf("bin/gate --list names %q with no summary", g.Name)
		}
		names = append(names, g.Name)
	}
	for _, want := range []string{"integration", "fit"} {
		if !slices.Contains(names, want) {
			t.Errorf("bin/gate --list does not name %q:\n%s", want, list)
		}
	}

	// The envelope is ALL of stdout, so a caller redirecting stdout to a parser
	// gets one object or nothing.
	envelope := exec.Command(filepath.Join(dir, "bin", "gate"), "fit", "--envelope")
	envelope.Dir = dir
	stdout, err := envelope.Output()
	if err != nil {
		t.Fatalf("bin/gate fit --envelope failed: %v", err)
	}
	var env struct {
		Gate    string `json:"gate"`
		Metrics []struct {
			Name string `json:"name"`
		} `json:"metrics"`
	}
	if err := json.Unmarshal(stdout, &env); err != nil {
		t.Fatalf("stdout is not one envelope: %v\n%s", err, stdout)
	}
	if env.Gate != "fit" || len(env.Metrics) == 0 {
		t.Errorf("the envelope measured nothing: %s", stdout)
	}

	// That envelope, byte for byte, is what the judge is given. Nothing here
	// re-measures: this is the exchange a runner performs.
	verdict := exec.Command(filepath.Join(dir, "bin", "run"), "fit", "--verdict")
	verdict.Dir = dir
	verdict.Stdin = bytes.NewReader(stdout)
	out, err := verdict.Output()
	if err != nil {
		t.Fatalf("bin/run fit --verdict failed: %v", err)
	}
	var got struct {
		Acceptable bool               `json:"acceptable"`
		Thresholds map[string]float64 `json:"thresholds"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("the verdict is not one JSON object: %v\n%s", err, out)
	}
	if !got.Acceptable {
		t.Errorf("the scaffolded fit gate is not acceptable on this machine: %s", out)
	}
	// A verdict with no terms cannot be re-checked by anyone who was not there.
	if len(got.Thresholds) == 0 {
		t.Errorf("the verdict carries no thresholds: %s", out)
	}

	// The by-hand path, which is the other way a gate is reached: `run <gate>`
	// spawns bin/gate as a process and judges what came back. Going through the
	// process boundary is the point — it is the route an external runner takes —
	// so nothing above covers it: --verdict deliberately spawns nothing, and the
	// gate was invoked directly. A gate this could not reach (a binary named
	// without the host's suffix, an envelope it could not parse) fails only here.
	//
	// Both directions of the status, because they are one mapping and only one of
	// them is ever seen by accident: acceptable is 0, and over the cap is 1
	// (project-tools.md, Run). The second is reached by taking the prerequisites
	// away rather than by editing the terms — the judged party does not move what
	// judges it, and a machine with no toolchain is what the fit gate is for.
	t.Run("run measures through the gate binary and judges what came back", func(t *testing.T) {
		judged := exec.Command(filepath.Join(dir, "bin", "run"), "fit")
		judged.Dir = dir
		out, err := judged.Output()
		if err != nil {
			t.Fatalf("bin/run fit failed on a fit machine: %v\n%s", err, out)
		}
		var answer struct {
			Envelope struct {
				Gate    string `json:"gate"`
				Metrics []struct {
					Name string `json:"name"`
				} `json:"metrics"`
			} `json:"envelope"`
			Verdict struct {
				Acceptable bool               `json:"acceptable"`
				Thresholds map[string]float64 `json:"thresholds"`
				Detail     string             `json:"detail"`
			} `json:"verdict"`
		}
		if err := json.Unmarshal(out, &answer); err != nil {
			t.Fatalf("bin/run fit through a pipe is not one JSON object: %v\n%s", err, out)
		}
		// The measurement travels with the verdict, or a reader who was not
		// there has the answer without what it was reached from.
		if answer.Envelope.Gate != "fit" || len(answer.Envelope.Metrics) == 0 {
			t.Errorf("bin/run fit answered no measurement: %s", out)
		}
		if !answer.Verdict.Acceptable || len(answer.Verdict.Thresholds) == 0 {
			t.Errorf("a fit machine was judged unfit, or on no terms: %s", out)
		}

		unfit := exec.Command(filepath.Join(dir, "bin", "run"), "fit")
		unfit.Dir = dir
		unfit.Env = []string{"PATH="}
		var body, narration bytes.Buffer
		unfit.Stdout, unfit.Stderr = &body, &narration
		err = unfit.Run()
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("a machine missing every prerequisite passed: %v\n%s", err, body.String())
		}
		if exit.ExitCode() != 1 {
			t.Errorf("a measurement over its cap exited %d, want 1", exit.ExitCode())
		}
		// Still a measurement and a verdict, not an error: nothing failed to
		// happen here, and a caller reading .acceptable must find it.
		if err := json.Unmarshal(body.Bytes(), &answer); err != nil {
			t.Fatalf("an unacceptable measurement wrote no result: %v\n%s", err, body.String())
		}
		if answer.Verdict.Acceptable {
			t.Errorf("a machine with no toolchain was acceptable: %s", body.String())
		}
		if !strings.Contains(answer.Verdict.Detail, "missing_prerequisites") {
			t.Errorf("the refusal does not name the term it rests on: %s", body.String())
		}
	})

	// Everything above is the path where nothing is wrong. What follows is the
	// other direction, on the same built tree: a refusal, a measurement that
	// fails its term, and a red run.

	// Every way out of bin/gate that is not a complete envelope leaves stdout
	// empty and exits non-zero — a caller redirecting stdout to a parser gets an
	// envelope or nothing, and "nothing" must not look like success. The emitted
	// unit tests measure and judge, and leave the invocation surface to the
	// library, so the process boundary is the only place this is visible.
	t.Run("a refusing gate writes nothing to stdout", func(t *testing.T) {
		for _, args := range [][]string{
			{"fit"},                         // measurements with no verdict, read as a pass by the first wrapper
			{"no-such-gate", "--envelope"},  // an empty envelope, read as a clean result
			{"--bogus"},                     // an unknown flag, which must not be dropped in silence
			{"fit", "tested", "--envelope"}, // two gates, where a runner asked for one answer
		} {
			cmd := exec.Command(filepath.Join(dir, "bin", "gate"), args...)
			cmd.Dir = dir
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			err := cmd.Run()
			if err == nil {
				t.Errorf("gate %v exited 0", args)
			}
			// Each of these is an invocation the gate could not make sense of,
			// and they carry the one status that says so. A refusal that came
			// back as 1 would read to a runner as a gate that measured this tree
			// and found it wanting (project-tools.md, Gate).
			var exit *exec.ExitError
			if errors.As(err, &exit) && exit.ExitCode() != command.StatusMalformed {
				t.Errorf("gate %v exited %d, want the malformed-input status %d",
					args, exit.ExitCode(), command.StatusMalformed)
			}
			if stdout.Len() != 0 {
				t.Errorf("gate %v wrote %q to stdout while refusing", args, stdout.String())
			}
			if stderr.Len() == 0 {
				t.Errorf("gate %v refused without saying why", args)
			}
		}
	})

	// Every tool answers -help on stdout and exits 0, gate included. That is a
	// change of behaviour the library brought: a scaffolded gate used to print
	// usage to stderr and exit 1, so the one invocation a person tries when they
	// do not know a tool looked like a failure (org/cli-guide.md, Exit codes).
	// -h is not a second spelling of it — an abbreviation is unknown input.
	t.Run("every tool answers -help and refuses -h", func(t *testing.T) {
		for _, tool := range []string{"verify", "setup", "gate", "run"} {
			bin := filepath.Join(dir, "bin", tool)

			cmd := exec.Command(bin, "-help")
			cmd.Dir = dir
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			if err := cmd.Run(); err != nil {
				t.Errorf("%s -help exited non-zero: %v (%q)", tool, err, stderr.String())
			}
			if stdout.Len() == 0 {
				t.Errorf("%s -help wrote no usage to stdout", tool)
			}

			abbrev := exec.Command(bin, "-h")
			abbrev.Dir = dir
			abbrev.Stdout, abbrev.Stderr = &bytes.Buffer{}, &bytes.Buffer{}
			if err := abbrev.Run(); err == nil {
				t.Errorf("%s -h was accepted; an abbreviation is unknown input", tool)
			}
		}
	})

	// A tool whose binary no longer matches its source refuses, naming the
	// recovery, rather than measuring this tree with yesterday's logic and
	// printing a well-formed answer about it — the one failure nothing
	// downstream could detect. Emitting a copy of the staleness check was what
	// used to make this an os.Exit and a line of prose; it is now a refusal the
	// library writes, and the status is what a caller reads.
	//
	// Both halves of the rule are checked, because they differ: a refusal is an
	// object on stdout, EXCEPT where a protocol flag has claimed that stream for
	// a contract of its own, and there it travels as the status and the stderr
	// line alone — anything else on that stream would read to the runner
	// parsing it as the gate's own defect.
	t.Run("a stale binary refuses and names the recovery", func(t *testing.T) {
		touched := filepath.Join(dir, "tools", "build", "common", "stale_marker.go")
		if err := os.WriteFile(touched, []byte("package common\n\n// changed after the build\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		defer os.Remove(touched)

		refuse := func(t *testing.T, tool string, args ...string) (int, string, string) {
			t.Helper()
			cmd := exec.Command(filepath.Join(dir, "bin", tool), args...)
			cmd.Dir = dir
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			err := cmd.Run()
			var exit *exec.ExitError
			if !errors.As(err, &exit) {
				t.Fatalf("a stale %s did not refuse: %v (%q)", tool, err, stderr.String())
			}
			if exit.ExitCode() != command.StatusRefused {
				t.Errorf("a stale %s exited %d, want the refusal status %d",
					tool, exit.ExitCode(), command.StatusRefused)
			}
			return exit.ExitCode(), stdout.String(), stderr.String()
		}

		// An ordinary invocation: the refusal is the object, so a caller can tell
		// a stale toolchain from a failing tree without matching prose.
		_, stdout, stderr := refuse(t, "run", "fit")
		var refusal struct {
			Refusal  string   `json:"refusal"`
			Tool     string   `json:"tool"`
			Recovery []string `json:"recovery"`
		}
		if err := json.Unmarshal([]byte(stdout), &refusal); err != nil {
			t.Fatalf("a stale run's stdout is not a refusal object: %v\n%s", err, stdout)
		}
		if refusal.Refusal != command.Stale {
			t.Errorf("the refusal is %q, want %q", refusal.Refusal, command.Stale)
		}
		if len(refusal.Recovery) == 0 {
			t.Error("the refusal names no recovery, so nothing tells the caller what to run")
		}
		if stderr == "" {
			t.Error("the refusal said nothing on stderr, where a person reads it")
		}

		// The envelope's stream belongs to the gate contract, so a refusal
		// leaves it empty rather than putting something there that is not an
		// envelope.
		_, stdout, stderr = refuse(t, "gate", "fit", "--envelope")
		if stdout != "" {
			t.Errorf("a stale gate wrote %q to the envelope's stream", stdout)
		}
		if stderr == "" {
			t.Error("a stale gate refused without saying why")
		}
	})

	// The verdict is the JSON, not the exit status: an SDK reads .acceptable, so
	// a measurement over its cap still comes back as one object on stdout and
	// exit 0. It is also the only check that the emitted terms can refuse
	// anything — an acceptable verdict looks the same against a manifest that
	// caps nothing as against one that caps everything.
	t.Run("a measurement over its cap is a verdict, not an error", func(t *testing.T) {
		refused := exec.Command(filepath.Join(dir, "bin", "run"), "fit", "--verdict")
		refused.Dir = dir
		refused.Stdin = bytes.NewReader(withMetricValue(t, stdout, "missing_prerequisites", 2))
		var body, stderr bytes.Buffer
		refused.Stdout, refused.Stderr = &body, &stderr
		if err := refused.Run(); err != nil {
			t.Fatalf("a measurement over its cap made the judge fail: %v\n%s%s", err, body.String(), stderr.String())
		}
		var got struct {
			Acceptable bool               `json:"acceptable"`
			Thresholds map[string]float64 `json:"thresholds"`
			Detail     string             `json:"detail"`
		}
		if err := json.Unmarshal(body.Bytes(), &got); err != nil {
			t.Fatalf("the verdict is not one JSON object: %v\n%s", err, body.String())
		}
		if got.Acceptable {
			t.Errorf("2 missing prerequisites was acceptable against a cap of 0: %s", body.String())
		}
		// The term it was refused on, or nobody can tell which cap it broke.
		if !strings.Contains(got.Detail, "missing_prerequisites") {
			t.Errorf("the refusal does not name the term it rests on: %s", body.String())
		}
		if _, ok := got.Thresholds["missing_prerequisites"]; !ok {
			t.Errorf("the applied term was not reported: %s", body.String())
		}
	})

	// A red run blesses nothing. verify clears the record before its first step
	// and writes it after its last, so a tree that failed is never one the
	// commit gate lets through — and the failure direction is the one that
	// degrades into a permanent blessing without anyone noticing.
	//
	// A root go.mod is what switches the emitted pipeline from stubs to real go
	// tooling, which is what lets this run go red at all. It is written last
	// because it changes the tree every assertion above measured.
	t.Run("a failing verify leaves no blessing", func(t *testing.T) {
		write(t, dir, "go.mod", "module example\n\ngo 1.26\n")
		write(t, dir, "broken.go", "package main\n\nfunc (\n")
		red := exec.Command(filepath.Join(dir, "bin", "verify"))
		red.Dir = dir
		var out, narration bytes.Buffer
		red.Stdout, red.Stderr = &out, &narration
		err := red.Run()
		if err == nil {
			t.Fatalf("verify passed on a tree that does not parse:\n%s%s", out.String(), narration.String())
		}
		if _, err := os.Stat(filepath.Join(dir, ".workspace", "verified-tree")); !os.IsNotExist(err) {
			t.Errorf("a failed verify left the previous run's blessing behind (stat err = %v)", err)
		}

		// The result is written on the failing path too — a caller asked whether
		// this tree may be committed, and a no is an answer. What it must say is
		// which step failed and that the rest did not run: a red run that
		// reported every step as passed and only the status as 1 would leave the
		// reason for the refusal nowhere a caller can read it.
		var failed verifyResult
		if err := json.Unmarshal(out.Bytes(), &failed); err != nil {
			t.Fatalf("a failing verify wrote no result to stdout: %v\n%s", err, out.String())
		}
		if failed.OK {
			t.Errorf("a failed run reported ok: %s", out.String())
		}
		var statuses []string
		for _, stage := range failed.Stages {
			for _, s := range stage.Steps {
				statuses = append(statuses, s.Status)
				if s.Status == "failed" && s.Detail == "" {
					t.Errorf("step %q failed and says nothing about why", s.Name)
				}
			}
		}
		if n := slices.Index(statuses, "failed"); n < 0 {
			t.Errorf("a failed run names no failing step: %v", statuses)
		} else {
			// Stopping at the first failure is what the pipeline promises, and
			// "not-run" is how the summary tells a skipped step from a passing
			// one. A step after the failure reported as passed is a run claiming
			// work it never did.
			for _, status := range statuses[n+1:] {
				if status != "not-run" {
					t.Errorf("a step after the failure is %q, want not-run: %v", status, statuses)
				}
			}
		}
		// Nothing was blessed, so the result carries no tree — the field is the
		// same claim the record is, and the two must agree about its absence.
		if failed.Tree != "" {
			t.Errorf("a failed run answered tree %q, which nothing recorded", failed.Tree)
		}
	})
}

// withMetricValue returns the envelope with one metric's value replaced, so the
// judge is handed a measurement a gate really produced rather than a
// hand-written object that only resembles one.
func withMetricValue(t *testing.T, envelope []byte, metric string, value float64) []byte {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(envelope, &doc); err != nil {
		t.Fatal(err)
	}
	metrics, ok := doc["metrics"].([]any)
	if !ok {
		t.Fatalf("the envelope carries no metrics: %s", envelope)
	}
	found := false
	for _, m := range metrics {
		entry, ok := m.(map[string]any)
		if !ok || entry["name"] != metric {
			continue
		}
		entry["value"] = value
		found = true
	}
	if !found {
		t.Fatalf("the envelope does not report %s: %s", metric, envelope)
	}
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// The tool set the scaffolder emits, asserted as a set: an omission and a
// reappearing twin both fail here. `gate` and `run` are required at error
// severity, and `guard`/`precommit` are twins of tools the workspace owns and
// a project may not build.
func TestScaffoldedToolSetIsConformant(t *testing.T) {
	have := map[string]bool{}
	for _, f := range files() {
		if rest, ok := strings.CutPrefix(f.path, "tools/build/cmd/"); ok {
			have[strings.SplitN(rest, "/", 2)[0]] = true
		}
	}
	want := map[string]bool{"make": true, "verify": true, "setup": true, "gate": true, "run": true}
	if !maps.Equal(have, want) {
		t.Errorf("the scaffolded tool set is %v, want %v", keysOf(have), keysOf(want))
	}
	for _, twin := range []string{"guard", "precommit", "tool-guard", "precommit-guard", "issue", "do", "workspace"} {
		if have[twin] {
			t.Errorf("the scaffolder builds %q, a tool the workspace owns", twin)
		}
	}
}

// verifyResult is a scaffolded verify's answer — the fields project-tools.md,
// Verify fixes, as far as these tests read them. It is declared here rather than
// imported: the emitted type lives in a module this one does not build, and what
// the two share is the wire, not the struct.
type verifyResult struct {
	OK     bool `json:"ok"`
	Stages []struct {
		Name  string `json:"name"`
		Steps []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
			Detail string `json:"detail"`
		} `json:"steps"`
	} `json:"stages"`
	Tree string `json:"tree"`
}

// emittedCommandNames is every command the scaffolder emits a main for, minus
// the meta-builder, sorted — what a scaffolded `run --list` must report as
// commands. It is read off files(), so a main added or removed moves the
// expectation with it rather than leaving a list to maintain twice.
func emittedCommandNames() []string {
	var names []string
	for _, f := range files() {
		rest, ok := strings.CutPrefix(f.path, "tools/build/cmd/")
		if !ok {
			continue
		}
		if name := strings.SplitN(rest, "/", 2)[0]; name != "make" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// The wiring cmd/init commits into a target repository has one home, and it is
// the constant that emits it (docs/blueprint.md, The agent guard): "The file's
// text is the settingsJSON constant in cmd/init/main.go, which is what emits
// it; this document does not carry a second copy of it", because "a prose copy
// of the wiring is the first place the two spellings part company".
// TestScaffoldedSettingsWireTheGuardOnBothEvents fixes what that constant says;
// this one fixes that it is the only place saying it.
//
// The set is derived from files() by path prefix rather than listed here, so
// what counts as wiring follows what is emitted. That is what made removing
// .githooks/pre-commit from the emitted set narrow this check to the agent
// guard without an edit: the commit-gate hook is written by `workspace setup`
// alongside the binary it names (docs/blueprint.md, The commit gate hook), so it
// is no longer wiring this repository states at all.
//
// A pasted copy reads as documentation right up to the day the two spellings
// differ, and then it is the copy a reader believes — so nothing announces the
// drift, which is why the constraint needs a test rather than review. The
// trampolines are deliberately not in this set: docs/blueprint.md, The bootstrap
// entry point quotes ./make in full on purpose, and it is the wiring for the
// tools the project does not build that is stated once.
func TestNoDocumentCarriesASecondCopyOfTheEmittedWiring(t *testing.T) {
	wiring := emittedWiring()
	if len(wiring) == 0 {
		t.Fatal("no wiring was extracted — the constraint was not checked")
	}
	found, scanned, err := documentsCarrying(filepath.Join("..", ".."), wiring)
	if err != nil {
		t.Fatal(err)
	}
	// A walk that read nothing would pass silently, which reads as coverage of
	// a constraint nothing was checked against.
	if scanned == 0 {
		t.Fatal("no document was read — the constraint was not checked")
	}
	for _, f := range found {
		t.Errorf("%s — the wiring is stated once, in the constant cmd/init emits it from", f)
	}
}

// The walk itself, over a tree that breaks the constraint: without this the test
// above passes whether or not it can see a pasted copy at all.
func TestDocumentsCarryingReportsAPastedCopy(t *testing.T) {
	wiring := emittedWiring()
	if len(wiring) == 0 {
		t.Fatal("no wiring was extracted")
	}
	tree := t.TempDir()
	// Re-indented and fenced, which is how a document would carry it — the copy
	// a reader has to notice is never a byte-identical file.
	write(t, tree, "docs/guide.md", "Wired like this:\n\n```json\n        "+wiring[0]+"\n```\n")
	write(t, tree, "docs/prose.md", "The guard runs on both tool-use events, and its command strings are exact.\n")
	// Not read: the vendored corpus is byte-identical to its home and is not
	// this repository's to repair (org/normative.md, Location), and nothing a
	// repository publishes lives under a dot directory.
	write(t, tree, "docs/org/cli-guide.md", wiring[0]+"\n")
	write(t, tree, ".flow/notes.md", wiring[0]+"\n")

	found, scanned, err := documentsCarrying(tree, wiring)
	if err != nil {
		t.Fatal(err)
	}
	if scanned != 2 {
		t.Errorf("read %d documents, want the 2 outside the corpus and the dot directory", scanned)
	}
	if len(found) != 1 {
		t.Fatalf("reported %d copies, want the one in docs/guide.md: %v", len(found), found)
	}
	// The line is named as the report renders it — quoted, so a line carrying
	// quotes of its own reads unambiguously. Comparing against the bare text
	// only happened to work while the first wiring line was shell with nothing
	// %q escapes; the agent guard's is JSON, and every line of it has quotes.
	if !strings.Contains(found[0], "docs/guide.md") || !strings.Contains(found[0], fmt.Sprintf("%q", wiring[0])) {
		t.Errorf("the report names neither the document nor the line: %q", found[0])
	}
}

// emittedWiring returns the lines of the wiring cmd/init commits into a target
// repository — the files naming a tool ./make never builds
// (docs/blueprint.md, Tools the project does not build). It reads them off
// files(), so a wiring file that stops being emitted stops being checked with
// no edit here.
//
// Shell comments and lines under ten characters are left out. Neither is a
// spelling a document could copy and get wrong: the comments are prose a
// document may legitimately echo, and the short lines are the punctuation that
// closes a block — `}`, `fi` — which carries no wiring at all.
func emittedWiring() []string {
	var lines []string
	seen := map[string]bool{}
	for _, f := range files() {
		if !strings.HasPrefix(f.path, ".githooks/") && !strings.HasPrefix(f.path, ".claude/") {
			continue
		}
		for _, line := range strings.Split(substituteBackticks(f.body), "\n") {
			line = strings.TrimSpace(line)
			if len(line) < 10 || strings.HasPrefix(line, "#") || seen[line] {
				continue
			}
			seen[line] = true
			lines = append(lines, line)
		}
	}
	return lines
}

// documentsCarrying walks root for Markdown and reports every document carrying
// one of lines verbatim. Comparison is against the trimmed line, so re-indenting
// a pasted block does not hide it. scanned is how many documents were read, so a
// walk that reached none cannot pass as a walk that found none.
func documentsCarrying(root string, lines []string) (found []string, scanned int, err error) {
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel != "." && (strings.HasPrefix(d.Name(), ".") || d.Name() == "bin" || rel == "docs/org") {
				return fs.SkipDir
			}
			return nil
		}
		if filepath.Ext(p) != ".md" {
			return nil
		}
		b, readErr := os.ReadFile(p)
		if readErr != nil {
			return readErr
		}
		scanned++
		for _, line := range lines {
			if strings.Contains(string(b), line) {
				found = append(found, fmt.Sprintf("%s carries the wiring verbatim: %q", rel, line))
			}
		}
		return nil
	})
	return found, scanned, err
}

// The two commands are compared byte-for-byte by a conformance checker, so any
// wrapper or reordering is itself the deviation.
func TestScaffoldedSettingsWireTheGuardOnBothEvents(t *testing.T) {
	var parsed map[string]any
	if err := json.Unmarshal([]byte(settingsJSON), &parsed); err != nil {
		t.Fatalf("the emitted .claude/settings.json does not parse: %v", err)
	}
	for _, want := range []string{
		`"command": "\"$CLAUDE_PROJECT_DIR/bin/tool-guard\" || exit 2"`,
		`"command": "\"$CLAUDE_PROJECT_DIR/bin/tool-guard\" || true"`,
		`"shell": "bash"`,
		`"timeout": 10`,
		`"matcher": "*"`,
		`"PreToolUse"`,
		`"PostToolUse"`,
	} {
		if !strings.Contains(settingsJSON, want) {
			t.Errorf("the emitted settings do not carry %s", want)
		}
	}
	if strings.Contains(settingsJSON, "bin/guard") {
		t.Error("the emitted settings still name the local twin bin/guard")
	}
}

// The same decision, applied to the tree it was taken in. `.claude/settings.json`
// is provisioned AND committed, and the committing end is the project's
// (docs/blueprint.md, Tools the project does not build) — this repository
// included, since it is the first consumer of what it scaffolds
// (docs/primitives.md, This repository is its own first consumer).
//
// Untracking it here would be invisible from every direction but the one that
// matters. `workspace setup` rewrites the file on every run, so it stays on disk
// in this checkout whatever git holds; the conformance that reads it reads the
// settings in FORCE rather than the ones committed; and every other test in this
// package reads the settingsJSON constant, never the tree. Only a fresh clone
// finds out, by carrying no agent guard at all.
//
// Unlike the ignore checks above this one needs no control: the answer is
// compared against the path, so a git that answers nothing — or answers about
// something else — fails rather than passing as "tracked".
func TestThisRepositoryCommitsTheAgentGuardWiring(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git on this machine")
	}
	git := func(args ...string) (string, error) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = filepath.Join("..", "..")
		out, err := cmd.Output()
		return strings.TrimSpace(string(out)), err
	}
	// A source tree that is not a checkout — an extracted archive, a vendored
	// copy — holds no answer to this question, and a failure there would name a
	// defect that is not in the repository.
	if _, err := git("rev-parse", "--is-inside-work-tree"); err != nil {
		t.Skip("not a git checkout")
	}
	const wiring = ".claude/settings.json"
	tracked, err := git("ls-files", "--", wiring)
	if err != nil {
		t.Fatalf("git ls-files %s: %v", wiring, err)
	}
	if tracked != wiring {
		t.Errorf("git tracks %q for %s — provisioning writes that file and the project commits it, so a clone of this repository would carry no agent-guard wiring at all", tracked, wiring)
	}
}

// A metric with no entry cannot fail, and an entry naming nothing is a term
// that never applies. Either way the verdict is thinner than it reads, and a
// verdict carrying no terms is one a conformance check refuses.
func TestScaffoldedThresholdsCapEveryStarterMetric(t *testing.T) {
	var manifest map[string]struct {
		Direction string  `json:"direction"`
		Cap       float64 `json:"cap"`
	}
	if err := json.Unmarshal([]byte(thresholdsJSON), &manifest); err != nil {
		t.Fatalf("the emitted thresholds do not parse: %v", err)
	}
	emitted := map[string]bool{}
	for _, name := range starterMetricNames() {
		emitted[name] = true
		if _, ok := manifest[name]; !ok {
			t.Errorf("%s is measured but never capped", name)
		}
	}
	for name := range manifest {
		if !emitted[name] {
			t.Errorf("%s is capped but never measured", name)
		}
	}
	if err := json.Unmarshal([]byte(baselinesJSON), &manifest); err != nil {
		t.Fatalf("the emitted baselines do not parse: %v", err)
	}
}

// The terms the scaffolder emits are terms the judge reads. It is checked
// through the library's own loader rather than against a second spelling of the
// vocabulary here: a scaffolder and a judge that each held their own idea of
// what a term looks like is exactly the state this guards against, and a
// freshly scaffolded project whose first `bin/run` cannot judge its own terms
// is a project born broken.
func TestTheScaffoldedTermsAreTermsTheJudgeReads(t *testing.T) {
	root := t.TempDir()
	write(t, root, filepath.FromSlash(tooling.ThresholdsFile), thresholdsJSON)
	write(t, root, filepath.FromSlash(tooling.BaselinesFile), baselinesJSON)

	read, err := tooling.LoadTerms(root)
	if err != nil {
		t.Fatalf("the judge refuses the terms the scaffolder emits: %v", err)
	}
	if len(read.Caps) == 0 {
		t.Error("the emitted thresholds carry no cap the judge could apply")
	}
}

// starterMetricNames is every metric name the emitted gates report, read out of
// the emitted source. Reading the source rather than keeping a second list is
// what makes the check catch a metric added without a cap.
func starterMetricNames() []string {
	var names []string
	for _, call := range []string{"Count(\"", "Quantity(\"", "Size(\""} {
		rest := gateGo
		for {
			i := strings.Index(rest, call)
			if i < 0 {
				break
			}
			rest = rest[i+len(call):]
			name, _, _ := strings.Cut(rest, "\"")
			names = append(names, name)
		}
	}
	return names
}

// The emitted docs index carries no relative link, so a scaffolded tree cannot
// fail a documentation link check on the scaffolder's own output. That is also
// what lets it name docs/org/ before a sync has put anything there.
func TestScaffoldedDocsAreLinkClean(t *testing.T) {
	if strings.Contains(docsIndexMd, "](") {
		t.Errorf("docs/index.md carries a link, which must resolve to a file the adopter does not have yet:\n%s", docsIndexMd)
	}
	if !strings.HasPrefix(docsIndexMd, "# ") {
		t.Errorf("docs/index.md has no title:\n%s", docsIndexMd)
	}
}

// A scaffolded project is born conformant with the one docs structure every
// managed project holds. org/normative.md, Location makes the index name the
// project's status query — "the one per-project fact this shared document cannot
// carry" — and list docs/org/ once, as the directory. An index that named
// neither would make every tree this scaffolder writes non-conformant from its
// first commit.
func TestScaffoldedDocsIndexNamesTheStatusQueryAndTheCorpus(t *testing.T) {
	for _, want := range []string{
		"gh issue list --label",
		"--state open",
		"--limit 200",
		"## Organization wide corpus",
		"docs/org/stamp.json",
	} {
		if !strings.Contains(docsIndexMd, want) {
			t.Errorf("the emitted docs/index.md does not carry %q:\n%s", want, docsIndexMd)
		}
	}
	// Listed once, as the directory. A per-file list would be a second copy of
	// the stamp's own membership, and a sync would then have to edit a file the
	// project owns.
	for _, member := range []string{"org/normative.md", "org/cli-guide.md", "org/engineering-guide.md"} {
		if strings.Contains(docsIndexMd, member) {
			t.Errorf("the emitted docs/index.md lists corpus member %q; the stamp names the members", member)
		}
	}
}

// A definition defect fails this project's tested gate rather than the first
// invocation that reaches it (docs/command-line.md, What a tool decides).
func TestTheDefinitionPassesTheLibrarysCheck(t *testing.T) {
	for _, defect := range command.Check(define()) {
		t.Errorf("init's definition: %v", defect)
	}
}

// The scaffolder answers -help like every other tool, where it used to refuse
// the flag as unknown input and carry no usage text at all.
func TestHelpAnswersOnStdout(t *testing.T) {
	var out, errs strings.Builder
	status := command.Run(define(), []string{"-help"},
		command.Streams{Out: &out, Err: &errs, OutIsTerminal: true, Dir: t.TempDir()})

	if status != command.StatusDone {
		t.Errorf("status %d, want 0 (%q)", status, errs.String())
	}
	for _, want := range []string{"-force", "target", "path"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("help %q does not describe %q", out.String(), want)
		}
	}
}

// WHERE IT SCAFFOLDS IS WHERE IT WAS INVOKED, never where the binary lives
// (docs/command-line.md, Types). The target used to be a bare string this file
// resolved against the process's own working directory; it is now an optional
// path parameter the library resolves against the invocation's, and the
// fallback when nobody typed one is this file's. Both branches write a tree
// into a directory, so getting either wrong is a scaffold that lands somewhere
// the person never named.
func TestTheTargetIsWhereItWasInvokedOrWhereItWasTold(t *testing.T) {
	invokedFrom := t.TempDir()

	answer := scaffoldThroughTheLibrary(t, nil, invokedFrom)
	if answer.Target != invokedFrom {
		t.Errorf("with no target it scaffolded into %q, want the directory it was invoked from", answer.Target)
	}
	if !exists(filepath.Join(invokedFrom, "make")) {
		t.Error("nothing was written where it said it wrote")
	}

	// And a relative target means what it would mean to any other program run
	// from the same directory.
	answer = scaffoldThroughTheLibrary(t, []string{"nested"}, invokedFrom)
	nested := filepath.Join(invokedFrom, "nested")
	if answer.Target != nested {
		t.Errorf("`init nested` scaffolded into %q, want %q", answer.Target, nested)
	}
	if !exists(filepath.Join(nested, "tools", "build", "go.mod")) {
		t.Error("the relative target was resolved somewhere else")
	}
	// Every path it reports is one it acted on, so the report is an account of
	// what happened rather than a list of intentions.
	for _, f := range answer.Files {
		if f.Action == actionCreate && !exists(filepath.Join(nested, filepath.FromSlash(f.Path))) {
			t.Errorf("it reports creating %q, which is not there", f.Path)
		}
	}
}

// scaffoldThroughTheLibrary runs init the way a person does and returns what
// it reported.
func scaffoldThroughTheLibrary(t *testing.T, args []string, invokedFrom string) result {
	t.Helper()
	var out, errs strings.Builder
	status := command.Run(define(), args, command.Streams{Out: &out, Err: &errs, Dir: invokedFrom})
	if status != command.StatusDone {
		t.Fatalf("status %d (%q)", status, errs.String())
	}
	var answer result
	if err := json.Unmarshal([]byte(out.String()), &answer); err != nil {
		t.Fatalf("stdout %q is not init's result: %v", out.String(), err)
	}
	return answer
}

// A misspelled flag is refused, and nothing is scaffolded.
func TestAnUnknownFlagScaffoldsNothing(t *testing.T) {
	target := t.TempDir()
	var out, errs strings.Builder
	status := command.Run(define(), []string{"--fore", target},
		command.Streams{Out: &out, Err: &errs, Dir: t.TempDir()})

	if status != command.StatusMalformed {
		t.Errorf("status %d, want %d", status, command.StatusMalformed)
	}
	if !strings.Contains(errs.String(), "did you mean -force") {
		t.Errorf("stderr %q, want the nearest name", errs.String())
	}
	if entries, err := os.ReadDir(target); err != nil || len(entries) != 0 {
		t.Errorf("the target holds %v, want nothing written", entries)
	}
}
