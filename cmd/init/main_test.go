package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
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
		"make", "make.cmd", "tools/build/go.mod", ".githooks/pre-commit",
		"tools/build/cmd/gate/main.go", "tools/build/cmd/run/main.go",
		"tools/gates/thresholds.json", "tools/gates/baselines.json",
		"docs/index.md", ".claude/settings.json",
	} {
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

// scaffold lays the tree down in a fresh temp dir and returns it.
func scaffold(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	const mod = "example/tools/build"
	for _, f := range files() {
		writeFile(dir, f, mod, false)
	}
	ensureGitignore(dir)
	return dir
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
	run(filepath.Join(dir, "bin", "verify"))
	record := strings.TrimSpace(read(t, filepath.Join(dir, ".workspace", "verified-tree")))
	if len(strings.Fields(record)) != 1 || len(record) < 20 {
		t.Errorf(".workspace/verified-tree does not hold one tree id: %q", record)
	}

	// `gate --list` is how anything outside the tree discovers what this
	// project answers, and both required names must be in it.
	list := run(filepath.Join(dir, "bin", "gate"), "--list")
	for _, want := range []string{"integration", "fit"} {
		if !slices.Contains(strings.Fields(list), want) {
			t.Errorf("bin/gate --list does not name %q:\n%s", want, list)
		}
	}
	// One name per line, because that is what an orchestrator parses. A usage
	// paragraph that happened to mention both names would satisfy the check
	// above and nothing downstream.
	for _, line := range strings.Split(strings.TrimSpace(list), "\n") {
		if len(strings.Fields(line)) != 1 {
			t.Errorf("bin/gate --list printed a line that is not one name: %q", line)
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

	// Everything above is the path where nothing is wrong. What follows is the
	// other direction, on the same built tree: a refusal, a measurement that
	// fails its term, and a red run.

	// Every way out of bin/gate that is not a complete envelope leaves stdout
	// empty and exits non-zero — a caller redirecting stdout to a parser gets an
	// envelope or nothing, and "nothing" must not look like success. The emitted
	// unit tests deliberately stop at the argument parsing and leave this to
	// cmd/gate, so the boundary is the only place it is visible.
	t.Run("a refusing gate writes nothing to stdout", func(t *testing.T) {
		for _, args := range [][]string{
			{"fit"},                        // measurements with no verdict, read as a pass by the first wrapper
			{"no-such-gate", "--envelope"}, // an empty envelope, read as a clean result
			{"-h"},                         // usage, which here goes to stderr unlike every other tool
		} {
			cmd := exec.Command(filepath.Join(dir, "bin", "gate"), args...)
			cmd.Dir = dir
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			if err := cmd.Run(); err == nil {
				t.Errorf("gate %v exited 0", args)
			}
			if stdout.Len() != 0 {
				t.Errorf("gate %v wrote %q to stdout while refusing", args, stdout.String())
			}
			if stderr.Len() == 0 {
				t.Errorf("gate %v refused without saying why", args)
			}
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
		refused.Stdin = bytes.NewReader(withMetricValue(t, stdout, "missing_prereqs", 2))
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
		if !strings.Contains(got.Detail, "missing_prereqs") {
			t.Errorf("the refusal does not name the term it rests on: %s", body.String())
		}
		if _, ok := got.Thresholds["missing_prereqs"]; !ok {
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
		out, err := red.CombinedOutput()
		if err == nil {
			t.Fatalf("verify passed on a tree that does not parse:\n%s", out)
		}
		if _, err := os.Stat(filepath.Join(dir, ".workspace", "verified-tree")); !os.IsNotExist(err) {
			t.Errorf("a failed verify left the previous run's blessing behind (stat err = %v)", err)
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

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// The hook names the workspace commit gate and no local twin of it, gives the
// recovery for a provisioned checkout, and names this project's own gate for a
// checkout that never opted in.
func TestScaffoldedHookNamesTheWorkspaceCommitGate(t *testing.T) {
	for _, want := range []string{"bin/precommit-guard", "workspace update", "bin/verify", ".workspace/project.json"} {
		if !strings.Contains(preCommitHook, want) {
			t.Errorf("the pre-commit hook does not mention %q", want)
		}
	}
	for _, twin := range []string{"bin/precommit\"", "bin/precommit ", "bin/guard"} {
		if strings.Contains(preCommitHook, twin) {
			t.Errorf("the pre-commit hook still names the local twin %q", twin)
		}
	}
}

// The hook's three branches, run as the shell runs them. Which branch a
// checkout takes is the judgement this layout rests on — fail closed where the
// project opted into a workspace, and get out of the way where it did not — and
// reading the text for substrings cannot tell a correct branch from an inverted
// one.
func TestScaffoldedHookBranchesOnWhatTheCheckoutOptedInto(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash on this machine")
	}
	// hook lays the trampoline down in a fresh root and runs it, returning the
	// exit code and what it said.
	hook := func(t *testing.T, setup func(root string)) (int, string) {
		t.Helper()
		root := t.TempDir()
		write(t, root, ".githooks/pre-commit", preCommitHook)
		if err := os.Chmod(filepath.Join(root, ".githooks", "pre-commit"), 0o755); err != nil {
			t.Fatal(err)
		}
		setup(root)
		cmd := exec.Command("bash", filepath.Join(root, ".githooks", "pre-commit"))
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		code := 0
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			code = exit.ExitCode()
		} else if err != nil {
			t.Fatalf("running the hook: %v", err)
		}
		return code, string(out)
	}

	// The guard is installed: the hook execs it and adds nothing of its own.
	code, out := hook(t, func(root string) {
		write(t, root, "bin/precommit-guard", "#!/usr/bin/env bash\necho GUARD RAN\nexit 3\n")
		if err := os.Chmod(filepath.Join(root, "bin", "precommit-guard"), 0o755); err != nil {
			t.Fatal(err)
		}
	})
	if code != 3 || !strings.Contains(out, "GUARD RAN") {
		t.Errorf("an installed guard was not exec'd: exit %d, output %q", code, out)
	}

	// Provisioned, guard missing: refuse, and name the recovery. This is the
	// fail-closed case, and it is the one an inverted condition would lose.
	code, out = hook(t, func(root string) {
		write(t, root, ".workspace/project.json", "{}\n")
	})
	if code == 0 {
		t.Errorf("a provisioned checkout with no guard allowed the commit: %q", out)
	}
	if !strings.Contains(out, "workspace update") {
		t.Errorf("the refusal does not name the recovery: %q", out)
	}

	// Never opted in: nothing was promised, so the hook names this project's own
	// gate and lets the commit through rather than demanding a tool the adopter
	// cannot obtain.
	code, out = hook(t, func(string) {})
	if code != 0 {
		t.Errorf("a checkout that adopted no workspace was blocked: exit %d, %q", code, out)
	}
	if !strings.Contains(out, "bin/verify") {
		t.Errorf("the hook does not name this project's gate: %q", out)
	}
}

// One hook text, used by this repository and by everything it scaffolds. A
// scaffolder whose output differs from what its own author runs is prescribing
// something nobody has tried (docs/primitives.md §6).
func TestScaffoldedHookIsTheHookThisRepositoryRuns(t *testing.T) {
	own := read(t, filepath.Join("..", "..", ".githooks", "pre-commit"))
	if own != preCommitHook {
		t.Errorf("this repository's .githooks/pre-commit and the emitted one differ:\n--- own ---\n%s\n--- emitted ---\n%s", own, preCommitHook)
	}
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
// fail a documentation link check on the scaffolder's own output.
func TestScaffoldedDocsAreLinkClean(t *testing.T) {
	if strings.Contains(docsIndexMd, "](") {
		t.Errorf("docs/index.md carries a link, which must resolve to a file the adopter does not have yet:\n%s", docsIndexMd)
	}
	if !strings.HasPrefix(docsIndexMd, "# ") {
		t.Errorf("docs/index.md has no title:\n%s", docsIndexMd)
	}
}
