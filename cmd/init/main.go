// Command init scaffolds the Forge dev-tooling layout into a target repository.
//
// Usage:
//
//	go run github.com/promise-language/forge/cmd/init@latest [target-dir] [-force]
//
// It lays down a self-contained, runnable tooling tree:
//
//   - ./make, ./make.cmd                  bootstrap trampolines (committed shell text)
//   - tools/build/go.mod                  island Go module for the dev tools
//   - tools/build/common/                 helper package (hash, stale, exec, verify, gate, run, …)
//   - tools/build/cmd/make/main.go        the meta-builder (runs via `go run`)
//   - tools/build/cmd/<tool>/main.go      one binary per dir: verify, setup, gate, run
//   - tools/gates/                        the judging terms: thresholds and baselines
//   - docs/index.md                       the map of docs/
//   - .githooks/pre-commit                git hook trampoline → bin/precommit-guard
//   - .claude/settings.json               wires bin/tool-guard on both tool-use events
//   - .gitignore                          adds every per-clone path (bin/, .workspace/, …)
//   - CLAUDE.md                           appends a "Dev tooling" section so agents
//     discover the ./make → bin/verify workflow
//
// It emits no twin of a tool the workspace is accountable for: precommit-guard
// and tool-guard are named by the committed hooks and installed by `workspace
// setup`, never built here (docs/blueprint.md §8).
//
// After init exits, the target repo owns every file. Forge is not a runtime
// dependency unless the project explicitly imports primitives/.
//
// The chain has no compile-the-compiler cycle: ./make is shell text, the
// meta-builder runs via `go run` (needing only the Go toolchain), and it
// compiles every other tool into bin/ — stamping each with the tools-source
// hash and the absolute repo root via -ldflags. See docs/blueprint.md.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type file struct {
	path string // relative to the target dir
	body string // __MODULE__ is replaced with the tools module path
	exec bool   // chmod +x after writing
}

func main() {
	target := "."
	force := false
	for _, a := range os.Args[1:] {
		switch {
		case a == "-force" || a == "--force":
			force = true
		case strings.HasPrefix(a, "-"):
			fail("unknown flag: %s", a)
		default:
			target = a
		}
	}

	absTarget, err := filepath.Abs(target)
	must(err)
	mod := toolsModule(absTarget)

	fmt.Printf("forge/init: scaffolding into %s\n", absTarget)
	fmt.Printf("forge/init: tools module = %s\n\n", mod)

	for _, f := range files() {
		writeFile(absTarget, f, mod, force)
	}
	ensureGitignore(absTarget)
	ensureBuildDoc(absTarget)

	if !exists(filepath.Join(absTarget, ".git")) {
		fmt.Println("\nnote: this is not a git repository yet.")
		fmt.Println("      run 'git init' so ./make can wire up the pre-commit hook.")
	}

	fmt.Println("\nDone. Next steps:")
	fmt.Println("  ./make        # compiles bin/{verify,setup,gate,run}")
	fmt.Println("  bin/verify    # runs the commit gate")
	fmt.Println("  bin/gate --list   # the measurements this project answers")
	fmt.Println("  bin/run fit       # measure one gate and judge what it measured")
	fmt.Println()
	fmt.Println("Two tools the committed hooks name are NOT built here: bin/precommit-guard")
	fmt.Println("and bin/tool-guard belong to the workspace that manages a project, and")
	fmt.Println("'workspace setup' installs them. Until then .githooks/pre-commit names")
	fmt.Println("bin/verify as this project's gate, and commits are not blocked.")
	fmt.Println()
	fmt.Println("Then edit tools/build/common/verify.go to run your project's real")
	fmt.Println("format / build / test commands, tools/build/common/gate.go for what this")
	fmt.Println("project measures, and tools/gates/thresholds.json for the caps a verdict")
	fmt.Println("rests on. Drop a new dir under tools/build/cmd/ and re-run ./make to get")
	fmt.Println("another bin/<tool> — no registration needed.")
	fmt.Println()
	fmt.Println("The build workflow is written to CLAUDE.md so agents discover it.")
}

// toolsModule picks the import path for the island tools module. If the target
// already has a root go.mod, the tools module nests under it; otherwise it is
// derived from the directory name.
func toolsModule(absTarget string) string {
	if mp := rootModulePath(absTarget); mp != "" {
		return mp + "/tools/build"
	}
	base := strings.ReplaceAll(filepath.Base(absTarget), " ", "-")
	if base == "" || base == "." || base == string(filepath.Separator) {
		base = "project"
	}
	return base + "/tools/build"
}

func rootModulePath(absTarget string) string {
	data, err := os.ReadFile(filepath.Join(absTarget, "go.mod"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module"))
		}
	}
	return ""
}

func writeFile(absTarget string, f file, mod string, force bool) {
	dst := filepath.Join(absTarget, f.path)
	if exists(dst) && !force {
		fmt.Printf("  skip   %s (exists)\n", f.path)
		return
	}
	must(os.MkdirAll(filepath.Dir(dst), 0o755))
	body := strings.ReplaceAll(f.body, "__MODULE__", mod)
	must(os.WriteFile(dst, []byte(body), 0o644))
	if f.exec {
		must(os.Chmod(dst, 0o755))
	}
	fmt.Printf("  create %s\n", f.path)
}

// perClonePaths is every path this layout writes that is per-clone and
// per-host, and therefore never committed: the built tools, the flow and
// provisioning state, and the local overrides a developer or a workspace writes
// beside the tracked file it extends.
//
// The list is exhaustive rather than illustrative because a workspace REFUSES a
// checkout that leaves one of them un-ignored — a path that is written on every
// run and tracked by nobody is a permanent dirty tree. So a missing entry is not
// cosmetic: it is a project that cannot be provisioned.
var perClonePaths = []string{
	"bin/",
	".flow/",
	".workspace/",
	".mcp.json",
	".claude/settings.local.json",
	"CLAUDE.local.md",
	"make.local",
}

// ensureGitignore adds the per-clone paths the file does not already carry, and
// nothing else. An adopter's .gitignore is theirs: what is present is left
// exactly as written, including the spelling.
func ensureGitignore(absTarget string) {
	path := filepath.Join(absTarget, ".gitignore")
	existing, _ := os.ReadFile(path)
	missing := missingIgnoreRules(string(existing))
	if len(missing) == 0 {
		fmt.Println("  skip   .gitignore (every per-clone path is already ignored)")
		return
	}
	block := "\n# forge dev tooling — written per clone, never committed\n" +
		strings.Join(missing, "\n") + "\n"
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	must(err)
	defer f.Close()
	_, err = f.WriteString(block)
	must(err)
	fmt.Printf("  update .gitignore (+%s)\n", strings.Join(missing, ", "))
}

// missingIgnoreRules returns the per-clone paths the existing .gitignore does
// not already carry, in perClonePaths order.
//
// Matching is per LINE and accepts both spellings of an anchored rule — "x" and
// "/x" ignore the same path at the repo root, and a project that wrote either
// one has already made the decision. A substring test would be wrong in both
// directions: "bin/" is a substring of ".claude/settings.local.json" only by
// accident of spelling, and a commented-out rule is not a rule.
//
// It reads the file rather than asking git, because the scaffolder may run
// before `git init` and `git check-ignore` has nothing to answer from there.
func missingIgnoreRules(existing string) []string {
	have := map[string]bool{}
	for _, line := range strings.Split(existing, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		have[strings.TrimPrefix(line, "/")] = true
	}
	var missing []string
	for _, p := range perClonePaths {
		if !have[p] {
			missing = append(missing, p)
		}
	}
	return missing
}

// ensureBuildDoc makes the build workflow discoverable — by agents first. It
// appends a "Dev tooling" section to CLAUDE.md (the file Claude Code auto-loads
// into context), creating the file if absent. Idempotent via a marker comment,
// and append-rather-than-overwrite so it coexists with an existing CLAUDE.md.
func ensureBuildDoc(absTarget string) {
	path := filepath.Join(absTarget, "CLAUDE.md")
	existing, _ := os.ReadFile(path)
	if strings.Contains(string(existing), buildDocMarker) {
		fmt.Println("  skip   CLAUDE.md (dev tooling section already present)")
		return
	}
	body := buildDocBlock
	verb := "create"
	if len(existing) > 0 {
		body = "\n" + body // separate from prior content
		verb = "update"
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	must(err)
	defer f.Close()
	_, err = f.WriteString(body)
	must(err)
	fmt.Printf("  %s CLAUDE.md (dev tooling section)\n", verb)
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func must(err error) {
	if err != nil {
		fail("%v", err)
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "forge/init: "+format+"\n", args...)
	os.Exit(1)
}

func files() []file {
	return []file{
		{path: "make", body: makeSh, exec: true},
		{path: "make.cmd", body: makeCmd},
		{path: "tools/build/go.mod", body: goMod},
		{path: "tools/build/common/platform.go", body: platformGo},
		{path: "tools/build/common/exec.go", body: execGo},
		{path: "tools/build/common/args.go", body: argsGo},
		{path: "tools/build/common/hash.go", body: hashGo},
		{path: "tools/build/common/stale.go", body: staleGo},
		{path: "tools/build/common/setup.go", body: setupGo},
		{path: "tools/build/common/verify.go", body: verifyGo},
		{path: "tools/build/common/verifiedtree.go", body: verifiedTreeGo},
		{path: "tools/build/common/gate.go", body: gateGo},
		{path: "tools/build/common/run.go", body: runGo},
		{path: "tools/build/common/gate_test.go", body: gateTestGo},
		{path: "tools/build/cmd/make/main.go", body: cmdMakeGo},
		{path: "tools/build/cmd/verify/main.go", body: cmdVerifyGo},
		{path: "tools/build/cmd/setup/main.go", body: cmdSetupGo},
		{path: "tools/build/cmd/gate/main.go", body: cmdGateGo},
		{path: "tools/build/cmd/run/main.go", body: cmdRunGo},
		{path: "tools/gates/thresholds.json", body: thresholdsJSON},
		{path: "tools/gates/baselines.json", body: baselinesJSON},
		{path: "docs/index.md", body: docsIndexMd},
		{path: ".githooks/pre-commit", body: preCommitHook, exec: true},
		{path: ".claude/settings.json", body: settingsJSON},
	}
}

// ───────────────────────── trampolines ─────────────────────────

const makeSh = `#!/usr/bin/env bash
# Bootstrap trampoline. Compiles every dev tool into bin/ via the meta-builder.
#
# This file is committed shell text — nothing builds it. It runs the
# meta-builder via 'go run', which needs only the Go toolchain (no pre-built
# binary), and that meta-builder compiles every other tool into bin/.
#
# The inner cd resolves $0 via its own directory, so ./make works from any cwd,
# and pins go run's working directory to <repo>/tools/build — which is how the
# meta-builder learns the absolute repo root.
set -euo pipefail
exec go run -C "$(cd "$(dirname "$0")" && pwd)/tools/build" ./cmd/make "$@"
`

const makeCmd = `@echo off
go run -C "%~dp0tools\build" ./cmd/make %*
`

// buildDocMarker fences the dev-tooling section in CLAUDE.md so ensureBuildDoc
// stays idempotent across re-runs and never double-appends.
const buildDocMarker = "<!-- forge:dev-tooling -->"

// buildDocBlock is the dev-tooling section appended to CLAUDE.md. It is authored
// with § standing in for the backtick, since a Go raw string literal cannot
// contain one; substituteBackticks swaps them in before the text is written.
var buildDocBlock = substituteBackticks(buildDocRaw)

func substituteBackticks(s string) string { return strings.ReplaceAll(s, "§", "`") }

const buildDocRaw = buildDocMarker + `
## Dev tooling

Dev tools are compiled from a single in-repo Go module (§tools/build/§) into
§bin/§, which is gitignored — the tools are always built locally, never committed.

**Fresh clone — bootstrap once:**

§§§bash
./make            # Windows: .\make.cmd
§§§

§./make§ compiles every tool into §bin/§ and wires up the git pre-commit hook.
It is idempotent and finishes in well under a second once built.

**Before every commit — run the gate:**

§§§bash
bin/verify        # format → vet → build → test → record, then a pass/FAIL summary
§§§

A green "OK to Commit" line means it is safe to commit; a red FAIL means it is
not. Its last step records the tree it blessed at §.workspace/verified-tree§,
which is what the commit gate (§bin/precommit-guard§, installed by the workspace
rather than built here) reads to refuse a commit of any other tree.

**Measure one thing, or ask what can be measured:**

§§§bash
bin/gate --list   # every measurement this project answers
bin/run <gate>    # measure one gate and print it beside the term it is judged on
§§§

§bin/gate <name> --envelope§ prints one JSON envelope and nothing else; it holds
no threshold and reaches no verdict. §bin/run <gate> --verdict§ reads that
envelope on stdin and writes one verdict, judged against §tools/gates/thresholds.json§.
**The verdict is the JSON, not the exit status** — §--verdict§ exits 0 either way.

**Edit a tool, then rebuild.** If any tool prints
§tools source has changed — run: ./make§, re-run §./make§ to rebuild §bin/§.
That is the whole loop: edit → §./make§ → use. Add a tool by dropping a new dir
under §tools/build/cmd/§ and re-running §./make§ — no registration step.
<!-- /forge:dev-tooling -->
`

// preCommitHook is the committed git hook, and it is the SAME TEXT this
// repository runs at .githooks/pre-commit. One text, because a scaffolder whose
// output differs from what its own author uses is prescribing something nobody
// has tried (docs/primitives.md §6).
//
// It names bin/precommit-guard, a workspace tool, and builds no local twin of it
// (docs/blueprint.md §8). It fails closed onto that tool only where the checkout
// opted in — the marker .workspace/project.json — because the workspace
// repository is private, and an unconditional refusal would block a public
// adopter's first commit on a recovery they cannot perform. Where there is no
// marker the project's gate is bin/verify, and the hook says so.
const preCommitHook = `#!/usr/bin/env bash
# Git pre-commit trampoline — execs the workspace commit guard, or names the
# gate to run instead. Wired up by 'git config core.hooksPath .githooks', which
# ./make runs for you.
#
# bin/precommit-guard is a WORKSPACE tool, not one ./make builds: a project may
# not build a twin of a tool the workspace is accountable for (tool-contract §5
# — one name, one builder), so this hook names it directly and never falls back
# to a local copy.
#
# It fails closed where the project opted in, and only there. The marker
# .workspace/project.json is what provisioning writes, so its presence is this
# checkout's own statement that a workspace manages it: with the marker, a
# missing guard refuses the commit and names the recovery. Without it nothing
# was promised (tool-contract §1 — fail-closed is a consequence of having opted
# in, never a tax on a repo that did not), the project's gate is bin/verify, and
# this hook says so and gets out of the way rather than demanding a tool the
# adopter cannot obtain.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
if [ -x "$root/bin/precommit-guard" ]; then
    exec "$root/bin/precommit-guard"
fi
if [ -f "$root/.workspace/project.json" ]; then
    echo "pre-commit: this checkout is provisioned but bin/precommit-guard is missing —" >&2
    echo "            run 'workspace update' (or 'workspace setup'), then commit" >&2
    exit 1
fi
echo "pre-commit: no workspace commit guard is installed here; the gate in this project" >&2
echo "            is bin/verify — run it before committing" >&2
`

// ───────────────────────── tools module ─────────────────────────

// The tools module declares one Go version, the same in every project that
// adopts this layout. It is not derived from the target and not a floor the
// target may raise: a version that varies per project is a variation in the
// tools themselves, and the tools exist to not vary. Raising it is an edit
// here, which every project then gets by adopting it.
const goMod = `module __MODULE__

go 1.26
`

const platformGo = `package common

import (
	"os"
	"os/exec"
	"runtime"
)

// IsWindows reports whether the host OS is Windows.
func IsWindows() bool { return runtime.GOOS == "windows" }

// ExeSuffix is ".exe" on Windows, "" elsewhere.
func ExeSuffix() string {
	if IsWindows() {
		return ".exe"
	}
	return ""
}

// BinaryName appends the platform executable suffix to a tool name.
func BinaryName(name string) string { return name + ExeSuffix() }

// Which resolves a command in PATH, returning "" if it is not found.
func Which(cmd string) string {
	p, err := exec.LookPath(cmd)
	if err != nil {
		return ""
	}
	return p
}

// Exists reports whether a path exists.
func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
`

const execGo = `package common

import (
	"os"
	"os/exec"
	"strings"
)

// RunIn runs name+args in dir with stdout/stderr/stdin attached to the parent.
func RunIn(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

// RunOutputIn runs name+args in dir and returns trimmed stdout.
func RunOutputIn(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// OutputBytesIn runs name+args in dir and returns raw, untrimmed stdout bytes.
// Use this instead of RunOutputIn when the output may be binary (e.g. reading a
// blob with 'git cat-file'), where trimming whitespace would corrupt content.
func OutputBytesIn(dir, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	return cmd.Output()
}

// RunSilent runs name+args with output discarded.
func RunSilent(name string, args ...string) error {
	return exec.Command(name, args...).Run()
}
`

const argsGo = `package common

import "strings"

// NormalizeArgs collapses long flags (--foo) to short form (-foo) so callers
// can accept either spelling. Values after = are preserved.
func NormalizeArgs(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		if strings.HasPrefix(a, "--") {
			out[i] = a[1:]
		} else {
			out[i] = a
		}
	}
	return out
}

// HasHelpFlag reports whether args request usage. It normalizes first and then
// compares, so --help, -help, --h and -h are all one case rather than four
// string comparisons that each tool gets slightly differently wrong.
func HasHelpFlag(args []string) bool {
	for _, a := range NormalizeArgs(args) {
		if a == "-h" || a == "-help" {
			return true
		}
	}
	return false
}
`

const hashGo = `package common

import (
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ToolsSourceHash computes an FNV-128a hash over every .go/go.mod/go.sum file
// under <repoRoot>/tools/build. It is stable across runs and platforms, and is
// what the meta-builder bakes into each binary to drive the staleness check.
// The per-file size delimiter prevents file-boundary collisions.
func ToolsSourceHash(repoRoot string) (string, error) {
	base := filepath.Join(repoRoot, "tools", "build")
	var files []string
	err := filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		name := info.Name()
		if strings.HasSuffix(name, ".go") || name == "go.mod" || name == "go.sum" {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(files)

	h := fnv.New128a()
	for _, path := range files {
		rel, err := filepath.Rel(base, path)
		if err != nil {
			return "", err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s\n%d\n", filepath.ToSlash(rel), len(data))
		h.Write(data)
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}
`

const staleGo = `package common

import (
	"fmt"
	"os"
)

// StaleReason returns a human-readable reason this binary is out of sync with
// its tools source, or "" if it is current. repoRoot and compiledHash are
// injected via -ldflags; empty values mean the binary was built some other way
// (go install, manual go build). It never exits — callers decide whether
// staleness is fatal (pipeline tools that would otherwise produce misleading
// results) or merely a warning (the git hook, which must never block a commit).
func StaleReason(repoRoot, compiledHash string) string {
	if repoRoot == "" || compiledHash == "" {
		return "this binary was not built via ./make"
	}
	currentHash, err := ToolsSourceHash(repoRoot)
	if err != nil {
		return fmt.Sprintf("binary's repo (%s) is unreachable: %v", repoRoot, err)
	}
	if compiledHash != currentHash {
		return "tools source has changed since this binary was built"
	}
	return ""
}

// MakeCmd is the bootstrap command to print in recovery hints.
func MakeCmd() string {
	if IsWindows() {
		return ".\\make.cmd"
	}
	return "./make"
}

// CheckStale aborts a tool whose stale logic would otherwise run: pipeline
// tools (verify, build, test, …) would produce misleading results, and the
// commit gate (precommit) must never validate a commit with out-of-date logic.
// It points the caller at ./make.
//
// It is deliberately NOT a one-way door: the recovery, ./make, runs via 'go
// run' and has no staleness gate of its own, so it always works no matter how
// stale — or how broken — the compiled binaries are. Editing the tool source to
// fix a broken build is likewise permitted by the guard. So the way out is
// always fix-and-rebuild, never committing the broken state. Stale tools are a
// speed bump (re-run ./make), never a lockout.
func CheckStale(repoRoot, compiledHash string) {
	reason := StaleReason(repoRoot, compiledHash)
	if reason == "" {
		return
	}
	fmt.Fprintf(os.Stderr, "%s — run %s", reason, MakeCmd())
	if repoRoot != "" {
		fmt.Fprintf(os.Stderr, " (in %s)", repoRoot)
	}
	fmt.Fprintln(os.Stderr)
	os.Exit(1)
}
`

const setupGo = `package common

// RunSetup wires git to use the in-repo .githooks directory. Idempotent and
// fast, so the meta-builder calls it on every run; a fresh clone gets its
// pre-commit hook on the first ./make.
func RunSetup(repoRoot string) error {
	return RunIn(repoRoot, "git", "config", "core.hooksPath", ".githooks")
}
`

const verifyGo = `package common

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type step struct {
	name string
	run  func(repoRoot string) error
}

// RunVerify is the commit gate: format → vet → build → test → record. It always
// prints a summary block (even on failure) so an agent tailing the output sees
// the result without re-running, and the process exit code is the only contract.
//
// The trailing record step is the writing end of the verified-tree contract
// (verifiedtree.go): the exit status says the tree is sound, and the record says
// which tree that was, so the commit gate can refuse a commit of any other one.
//
// This is an EXAMPLE pipeline. For a Go project it runs real go tooling; for
// anything else it runs harmless stubs. Replace verifySteps with your project's
// real commands.
func RunVerify(repoRoot string, args []string) error {
	// A stale blessing left behind is the one outcome the verified-tree check
	// must never produce, so failing to clear fails the run outright.
	if err := clearVerifiedTree(repoRoot); err != nil {
		return fmt.Errorf("clearing %s: %w", verifiedTreeRecord, err)
	}
	// The record step is appended here rather than inside verifySteps so it is
	// last on the Go and stub pipelines alike, and being a step gets the
	// break-on-first-failure for free — a red step blesses nothing.
	steps := append(verifySteps(repoRoot), step{"record", recordVerifiedTree})
	start := time.Now()

	type result struct {
		name string
		ok   bool
	}
	var results []result
	failed := false

	for _, s := range steps {
		fmt.Printf("==> %s\n", s.name)
		err := s.run(repoRoot)
		results = append(results, result{s.name, err == nil})
		if err != nil {
			failed = true
			fmt.Fprintf(os.Stderr, "    %s failed: %v\n", s.name, err)
			break // stop at the first failure
		}
	}

	fmt.Println("\n──────── verify summary ────────")
	for _, r := range results {
		status := "ok"
		if !r.ok {
			status = "FAIL"
		}
		fmt.Printf("  %-4s  %s\n", status, r.name)
	}
	fmt.Printf("  elapsed %s\n", time.Since(start).Round(time.Millisecond))
	fmt.Println("────────────────────────────────")

	if failed {
		fmt.Println("❌ Verify FAILED: not safe to commit")
		return fmt.Errorf("verify failed")
	}
	fmt.Println("✅ OK to Commit")
	return nil
}

func verifySteps(repoRoot string) []step {
	if Exists(filepath.Join(repoRoot, "go.mod")) {
		return []step{
			{"format", func(r string) error { return RunIn(r, "gofmt", "-w", ".") }},
			{"vet", func(r string) error { return RunIn(r, "go", "vet", "./...") }},
			{"build", func(r string) error { return RunIn(r, "go", "build", "./...") }},
			{"test", func(r string) error { return RunIn(r, "go", "test", "./...") }},
		}
	}
	stub := func(label string) step {
		return step{label, func(r string) error {
			fmt.Printf("    (stub) wire up your %s command in tools/build/common/verify.go\n", label)
			return nil
		}}
	}
	return []step{stub("format"), stub("vet"), stub("build"), stub("test")}
}
`

// verifiedTreeGo is the writing end of the verified-tree contract. It is
// authored with § standing in for the backtick, as buildDocRaw is; the Go raw
// string literal holding it cannot contain one.
var verifiedTreeGo = substituteBackticks(verifiedTreeGoRaw)

const verifiedTreeGoRaw = `package common

// This file is the writing end of the verified-tree contract: bin/verify
// records the tree it blessed at .workspace/verified-tree, and the commit gate
// refuses a commit whose staged tree differs (docs/blueprint.md §7, §9).
//
// The reading end is not in this repository. bin/precommit-guard is a workspace
// tool, built and owned there, and this module cannot import it. What the two
// ends share is the record's location and format, not code: one git tree object
// id, newline terminated, at the path below. Spelling it wrong here is a
// permanent, silent refusal — verify writes one path, the guard reads another
// and always finds it absent — so it is a constant, named once.

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// verifiedTreeRecord is where verify records the tree it blessed, in the
// gitignored per-checkout .workspace/ directory.
const verifiedTreeRecord = ".workspace/verified-tree"

// clearVerifiedTree removes the record. Verify calls it before its first step
// so a run that dies mid-way leaves nothing blessed and an in-flight verify
// blesses nothing. An absent record is not an error.
func clearVerifiedTree(repoRoot string) error {
	err := os.Remove(filepath.Join(repoRoot, filepath.FromSlash(verifiedTreeRecord)))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// recordVerifiedTree writes the tree id of the content verify just blessed.
// It runs only after every other step has passed, so a red run blesses
// nothing.
//
// The tree is computed over a temporary index so the real index is untouched,
// and it is exactly what §git add -A§ would stage: seeded from a copy of the
// real index, because that is the tracked set the real §git add -A§ starts
// from. Ignore rules apply only to untracked paths, so any other seed gets the
// ignored-and-tracking-state-differs cases wrong — an empty seed drops a
// tracked-but-ignored file, and a HEAD seed both drops one force-added but not
// yet committed and keeps one just §git rm --cached§ed — recording a tree no
// §git add -A§ can stage, a mismatch re-running verify cannot repair.
//
// Outside a git checkout, recording is a reported no-op rather than a verify
// failure: there is no commit to gate there, and a commit gate still refuses on
// the absent record.
func recordVerifiedTree(repoRoot string) error {
	if _, err := gitWithIndex(repoRoot, "", "rev-parse", "--git-dir"); err != nil {
		fmt.Println("    not a git checkout — no verified-tree record to write")
		return nil
	}

	tmpDir, err := os.MkdirTemp("", "verified-tree-")
	if err != nil {
		return fmt.Errorf("creating temp index dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	index := filepath.Join(tmpDir, "index")

	// Seed from a copy of the real index; a repo before its first add has no
	// index file yet, and an empty seed is exactly its tracked set.
	realIndex, err := gitWithIndex(repoRoot, "", "rev-parse", "--git-path", "index")
	if err != nil {
		return fmt.Errorf("locating the index: %w", err)
	}
	if !filepath.IsAbs(realIndex) {
		realIndex = filepath.Join(repoRoot, realIndex)
	}
	if data, err := os.ReadFile(realIndex); err == nil {
		if err := os.WriteFile(index, data, 0o600); err != nil {
			return fmt.Errorf("seeding temp index: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("seeding temp index: %w", err)
	}
	if _, err := gitWithIndex(repoRoot, index, "add", "-A"); err != nil {
		return fmt.Errorf("staging into temp index: %w", err)
	}
	tree, err := gitWithIndex(repoRoot, index, "write-tree")
	if err != nil {
		return fmt.Errorf("computing verified tree: %w", err)
	}

	dir := filepath.Join(repoRoot, ".workspace")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	// Atomic: temp file + rename, so no reader ever sees a half-written record.
	tmp, err := os.CreateTemp(dir, ".verified-tree-*")
	if err != nil {
		return err
	}
	if _, err := tmp.WriteString(tree + "\n"); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), filepath.Join(repoRoot, filepath.FromSlash(verifiedTreeRecord))); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}

// gitWithIndex runs git in dir, with GIT_INDEX_FILE pointed at indexFile when
// one is given, and returns trimmed stdout. RunOutputIn cannot be used: it
// carries no environment, which is the one thing this needs. Stderr is
// captured into the error so it never leaks to the terminal.
func gitWithIndex(dir, indexFile string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if indexFile != "" {
		cmd.Env = append(os.Environ(), "GIT_INDEX_FILE="+indexFile)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("git %s: %s", args[0], msg)
		}
		return "", fmt.Errorf("git %s: %w", args[0], err)
	}
	return strings.TrimSpace(string(out)), nil
}
`

// ───────────────────────── gates and the judge ─────────────────────────

// gateGo is the measuring half: what this project measures, and how. The
// judging half is runGo, and they are separate programs on purpose — see there.
var gateGo = substituteBackticks(gateGoRaw)

const gateGoRaw = `package common

// The gates this project provides.
//
// A gate MEASURES and never modifies what it measures. That is the whole
// difference between this file and verify.go: §verify§ formats the tree with
// §gofmt -w§ on its way to an answer, which is correct for a command and
// disqualifying for a gate — an answer about a tree that was repaired first is
// not an answer about the tree anyone proposed.
//
// A gate also does not JUDGE. Nothing here compares a number to a threshold or
// returns a verdict; it reports what it found and stops. Whether
// §unformatted_files: 3§ is acceptable needs thresholds the gate deliberately
// does not hold — see run.go.
//
// Nothing in here writes to stdout. Stdout carries the envelope and nothing
// else, so every child process has its stdout captured. A child's stderr is
// passed through to the process's own stderr, where a person watching a long
// run can see progress as it happens — except in gateValue, where the caller
// needs the two streams apart.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// MetricType is what kind of number a measurement is. The set is closed.
type MetricType string

const (
	// MetricInt counts things. A count is a whole number of them.
	MetricInt MetricType = "int"
	// MetricFloat measures a quantity that is not a count.
	MetricFloat MetricType = "float"
)

// Metric is one number a gate measured. It carries no opinion about whether the
// number is good: that comparison needs a threshold, and a gate holds none.
//
// The type travels with the value, and a count is held as an integer rather
// than as a float that happens to be whole. Reporting a float where a count was
// declared is a mismatch to name rather than a widening to absorb: a metric
// whose type changed measured something else, and absorbed silently it would
// move a ratchet that by construction never moves back.
type Metric struct {
	Name string
	Type MetricType
	// Exactly one of these carries the value, chosen by Type.
	Int   int64
	Float float64
	Unit  string
}

// Count is a measurement of how many. Whole by construction.
func Count(name string, n int) Metric {
	return Metric{Name: name, Type: MetricInt, Int: int64(n)}
}

// Quantity is a measurement that is not a count.
func Quantity(name string, v float64, unit string) Metric {
	return Metric{Name: name, Type: MetricFloat, Float: v, Unit: unit}
}

// Size is a measurement of how much, in whole units of it. Neither of the two
// above fits: Count takes an int and carries no unit, and Quantity is a float.
// Bytes are whole, carry a unit, and outrun what an int holds on a 32-bit host.
func Size(name string, n int64, unit string) Metric {
	return Metric{Name: name, Type: MetricInt, Int: n, Unit: unit}
}

// Number is the value as a float, for comparison against a threshold. Widening
// is safe HERE and nowhere else: the judging layer compares, it does not store,
// so nothing downstream can mistake the widened form for what was measured.
func (m Metric) Number() float64 {
	if m.Type == MetricInt {
		return float64(m.Int)
	}
	return m.Float
}

// String renders the value in its own type — a count never grows a decimal
// point, and a quantity never loses one.
func (m Metric) String() string {
	if m.Type == MetricInt {
		return strconv.FormatInt(m.Int, 10)
	}
	return strconv.FormatFloat(m.Float, 'f', 1, 64)
}

// metricWire is the envelope form of a Metric: one "value" field, and the type
// beside it so a reader knows which kind of number it is looking at.
type metricWire struct {
	Name  string          §json:"name"§
	Type  MetricType      §json:"type"§
	Value json.RawMessage §json:"value"§
	Unit  string          §json:"unit,omitempty"§
}

func (m Metric) MarshalJSON() ([]byte, error) {
	w := metricWire{Name: m.Name, Type: m.Type, Unit: m.Unit}
	switch m.Type {
	case MetricInt:
		w.Value = json.RawMessage(strconv.FormatInt(m.Int, 10))
	case MetricFloat:
		w.Value = json.RawMessage(strconv.FormatFloat(m.Float, 'f', -1, 64))
	default:
		return nil, fmt.Errorf("metric %q has no type", m.Name)
	}
	return json.Marshal(w)
}

func (m *Metric) UnmarshalJSON(b []byte) error {
	var w metricWire
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	m.Name, m.Type, m.Unit = w.Name, w.Type, w.Unit
	switch w.Type {
	case MetricInt:
		// A count arriving with a fractional part is not a count. Refusing it
		// is the point: absorbed, it would be a type change nothing recorded.
		if err := json.Unmarshal(w.Value, &m.Int); err != nil {
			return fmt.Errorf("metric %q is declared %s but its value is not: %w", w.Name, w.Type, err)
		}
	case MetricFloat:
		if err := json.Unmarshal(w.Value, &m.Float); err != nil {
			return fmt.Errorf("metric %q is declared %s but its value is not: %w", w.Name, w.Type, err)
		}
	default:
		return fmt.Errorf("metric %q has an unknown type %q", w.Name, w.Type)
	}
	return nil
}

// Envelope is what a gate prints on stdout: one JSON object, written whole.
//
// It is written whole deliberately. A run killed part-way leaves output that
// does not parse, which is how a reader tells "measured nothing" from "measured
// and reported" without asking the gate — a gate that died is not alive to say
// so.
type Envelope struct {
	Gate    string   §json:"gate"§
	Metrics []Metric §json:"metrics"§
	// Incomplete names the reason this run measured less than a full one, and
	// is empty when it did not. A run that skipped part of its work reports
	// honest numbers that UNDERSTATE what was checked, which is
	// indistinguishable from an improvement unless the run says so — and a
	// baseline moved by such a run sets a floor no complete run can meet.
	Incomplete string §json:"incomplete,omitempty"§
}

// gateDef is either a leaf that measures, or a composition of other gates.
type gateDef struct {
	summary string
	measure func(repoRoot string, mods []string) ([]Metric, string, error)
	parts   []string
}

// gates is CLOSED. A name absent from this map is refused rather than guessed
// at, because a runner asking for a gate this project does not have must learn
// that, not receive an empty measurement that reads like a clean result.
//
// This is the starter set. Add what your project measures; the only names that
// are not yours to choose are §integration§, which is what a decision to land a
// change rests on, and §fit§, which is about the machine rather than the code.
var gates = map[string]gateDef{
	"formatted": {
		summary: "source files that gofmt would rewrite",
		measure: measureFormatted,
	},
	"builds": {
		summary: "packages that fail to compile",
		measure: measureBuilds,
	},
	"checked": {
		summary: "go vet diagnostics",
		measure: measureChecked,
	},
	"tested": {
		summary: "failing tests and failing packages",
		measure: measureTested,
	},
	// integration is what a decision rests on: the whole, measured at once.
	// Its parts stay separately runnable, and that is a requirement rather
	// than a convenience — a step fixing one failing suite should re-run that
	// suite, not pay for the formatter and every other target each round.
	"integration": {
		summary: "everything that must hold before a change may land",
		parts:   []string{"formatted", "builds", "checked", "tested"},
	},
	// fit is the one gate here whose subject is not the code: it measures the
	// machine, before work is given to it. It is deliberately NOT a part of
	// integration — a machine that cannot build is not a change that may not
	// land.
	"fit": {
		summary: "prerequisites this project's pipeline needs and cannot find",
		measure: measureFit,
	},
}

// GateConcepts returns every gate this project provides, sorted. It is the
// vocabulary, which is what usage text lists.
func GateConcepts() []string {
	names := make([]string, 0, len(gates))
	for n := range gates {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// GateNames returns every name this project answers. This is what §--list§
// prints, because a runner discovers what it may ask for by asking — so a name
// missing here is a name nothing will ever request.
//
// It is the concepts, because this starter has no gate instances. A project
// that grows them — §tested:<module>§, to re-run one suite rather than all of
// them — widens this function and leaves every caller alone.
func GateNames() []string { return GateConcepts() }

// GateSummary returns the one-line description of a gate, or "" if unknown.
func GateSummary(name string) string { return gates[name].summary }

// KnownGate reports whether this project answers that name. Checked here rather
// than at the point of measuring, so every entry point refuses the same set.
func KnownGate(name string) bool {
	_, ok := gates[name]
	return ok
}

// MeasureGate runs one gate and returns what it measured.
//
// The error return means the measurement could not be OBTAINED — a tool is
// missing, or the environment refused. It never means "the numbers are bad":
// three failing tests is a successful run of the §tested§ gate, and the
// envelope says so.
func MeasureGate(repoRoot, name string) (Envelope, error) {
	def, ok := gates[name]
	if !ok {
		return Envelope{}, unknownGate(name)
	}
	env := Envelope{Gate: name, Metrics: []Metric{}}
	if def.measure != nil {
		metrics, incomplete, err := def.measure(repoRoot, modules(repoRoot))
		if err != nil {
			return Envelope{}, err
		}
		env.Metrics = metrics
		env.Incomplete = incomplete
		return env, nil
	}
	// A composition. Each part is measured by the same path a caller asking
	// for that part alone would take, so the whole cannot disagree with its
	// parts about how anything is measured.
	var reasons []string
	for _, part := range def.parts {
		sub, err := MeasureGate(repoRoot, part)
		if err != nil {
			return Envelope{}, fmt.Errorf("%s: %w", part, err)
		}
		env.Metrics = append(env.Metrics, sub.Metrics...)
		if sub.Incomplete != "" {
			reasons = append(reasons, part+": "+sub.Incomplete)
		}
	}
	env.Incomplete = strings.Join(reasons, "; ")
	return env, nil
}

// ParseGateArgs reads a gate invocation: exactly one name, and whether the
// caller asked for an envelope.
//
// The rules it enforces are the protocol, not this program's preferences. A
// gate is asked for one way, because two callers asking the same thing must not
// be able to get different answers and both be right — so an unknown flag is
// refused rather than ignored, and a second name is refused rather than
// silently dropped.
func ParseGateArgs(args []string) (name string, envelope bool, err error) {
	for _, a := range NormalizeArgs(args) {
		switch {
		case a == "-envelope":
			envelope = true
		case strings.HasPrefix(a, "-"):
			return "", false, fmt.Errorf("use of unknown flag %q; known gates: %s", a, strings.Join(GateNames(), ", "))
		case name != "":
			return "", false, fmt.Errorf("unexpected argument %q; a gate is asked for by name, once; known gates: %s",
				a, strings.Join(GateNames(), ", "))
		default:
			name = a
		}
	}
	if name == "" {
		return "", false, fmt.Errorf("no gate named; known gates: %s", strings.Join(GateNames(), ", "))
	}
	if !KnownGate(name) {
		return "", false, unknownGate(name)
	}
	return name, envelope, nil
}

// unknownGate is the refusal every entry point gives for a name this project
// does not have. One wording, because a caller that mistyped a gate name is
// told the same thing whichever program it typed it at.
func unknownGate(name string) error {
	return fmt.Errorf("no gate named %q in this project; known gates: %s",
		name, strings.Join(GateNames(), ", "))
}

// modules lists every Go module in this repository.
//
// §./...§ is module-scoped, so one invocation at the root measures the project
// and silently skips the tools that build it — including the source of the
// gates themselves. A gate that skipped a module would report honest numbers
// about part of the subject, which is the shape of an incomplete run that does
// not know it is incomplete.
func modules(repoRoot string) []string {
	var dirs []string
	if Exists(filepath.Join(repoRoot, "go.mod")) {
		dirs = append(dirs, repoRoot)
	}
	tools := filepath.Join(repoRoot, "tools", "build")
	if Exists(filepath.Join(tools, "go.mod")) {
		dirs = append(dirs, tools)
	}
	return dirs
}

// measureFormatted counts files gofmt would rewrite. §-l§ lists them; §-w§
// would repair them, which is verify's job and not a gate's.
func measureFormatted(repoRoot string, _ []string) ([]Metric, string, error) {
	out, err := gateOutput(repoRoot, "gofmt", "-l", ".")
	if err != nil && out == "" {
		return nil, "", fmt.Errorf("gofmt: %w", err)
	}
	return []Metric{Count("unformatted_files", countLines(out))}, "", nil
}

// measureBuilds counts packages that fail to compile. §go build§ prefixes each
// failing package with a "# " header line on stderr, so the headers are the
// count.
func measureBuilds(_ string, mods []string) ([]Metric, string, error) {
	n := 0
	for _, dir := range mods {
		_, stderr, err := gateValue(dir, "go", "build", "./...")
		found := countPrefixed(stderr, "# ")
		if err != nil && found == 0 {
			// It failed and named no package: the failure is about the
			// toolchain or the module, not about a package in this tree.
			return nil, "", fmt.Errorf("go build in %s: %w: %s", dir, err, firstLine(stderr))
		}
		n += found
	}
	return []Metric{Count("unbuildable_packages", n)}, "", nil
}

// measureChecked counts go vet diagnostics — the lines naming a file and a
// position, as distinct from the "# package" headers that group them. The
// diagnostics are on stderr.
func measureChecked(_ string, mods []string) ([]Metric, string, error) {
	n := 0
	for _, dir := range mods {
		_, stderr, err := gateValue(dir, "go", "vet", "./...")
		found := countDiagnostics(stderr)
		if err != nil && found == 0 {
			return nil, "", fmt.Errorf("go vet in %s: %w: %s", dir, err, firstLine(stderr))
		}
		n += found
	}
	return []Metric{Count("vet_findings", n)}, "", nil
}

// measureTested counts failing tests and failing packages. Both are worth
// having: one failing test in one package and forty in forty are different
// situations, and a single number cannot tell them apart.
func measureTested(_ string, mods []string) ([]Metric, string, error) {
	tests, pkgs := 0, 0
	for _, dir := range mods {
		out, err := gateOutput(dir, "go", "test", "./...")
		t := countPrefixed(out, "--- FAIL:")
		p := countPrefixed(out, "FAIL\t")
		if err != nil && t == 0 && p == 0 {
			// The run itself did not happen — a build failure in a test
			// package, most often. That is not "zero failing tests".
			return nil, "", fmt.Errorf("go test in %s: %w: %s", dir, err, firstLine(out))
		}
		tests += t
		pkgs += p
	}
	return []Metric{
		Count("failed_tests", tests),
		Count("failed_packages", pkgs),
	}, "", nil
}

// prereqCommands names the commands this project's pipeline runs and cannot
// substitute for. Grow it as the pipeline grows: a prerequisite that is not
// listed is one whose absence is discovered by a step failing for a reason that
// reads like the code's fault.
func prereqCommands() []string { return []string{"go", "gofmt", "git"} }

// measureFit counts the prerequisites this machine does not have.
//
// It reports the count and stops. Whether any missing prerequisite is
// tolerable is a threshold, held by the judging layer: "is this machine fit to
// be given work" reads like a yes/no question and is not one, and a gate that
// answered it would be the threshold sitting inside the party under
// measurement.
//
// The names go to stderr, because a count alone tells an operator that the
// machine is unfit without telling them what to install.
func measureFit(_ string, _ []string) ([]Metric, string, error) {
	missing := 0
	for _, cmd := range prereqCommands() {
		if Which(cmd) == "" {
			fmt.Fprintf(os.Stderr, "==> missing prerequisite: %s\n", cmd)
			missing++
		}
	}
	return []Metric{Count("missing_prereqs", missing)}, "", nil
}

// maxToolOutput bounds what a gate runner reads from a child's stdout. 10 MiB
// is enough for any measurement output and small enough that a chatty child
// cannot exhaust the process.
const maxToolOutput = 10 << 20 // 10 MiB

// gateOutput runs a child and returns its stdout as a string. Stderr is passed
// through to os.Stderr so a person watching a long gate sees the child's
// progress as it happens — silence and a hang look the same from outside.
func gateOutput(dir, name string, args ...string) (string, error) {
	fmt.Fprintf(os.Stderr, "==> %s %s\n", name, strings.Join(args, " "))
	bw := newBoundedWriter(maxToolOutput)
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = bw
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	return string(bw.Bytes()), err
}

// gateValue runs a child whose diagnostics are on stderr, and keeps the two
// streams apart.
//
// Both are captured: stdout because our stdout carries the envelope and nothing
// else, and stderr because the caller counts what is in it. Stderr is not
// passed through here, which is the deliberate exception to the file-level
// rule.
func gateValue(dir, name string, args ...string) (stdout, stderr string, err error) {
	fmt.Fprintf(os.Stderr, "==> %s %s\n", name, strings.Join(args, " "))
	out := newBoundedWriter(maxToolOutput)
	errs := newBoundedWriter(maxToolOutput)
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = out
	cmd.Stderr = errs
	err = cmd.Run()
	return string(out.Bytes()), string(errs.Bytes()), err
}

// boundedWriter keeps the first max bytes written to it and silently discards
// the rest. Write always returns len(p), nil, so a child writing to it is never
// blocked or errored by the reader having stopped.
type boundedWriter struct {
	buf []byte
	max int
}

func newBoundedWriter(n int) *boundedWriter {
	return &boundedWriter{buf: make([]byte, 0, n), max: n}
}

func (w *boundedWriter) Write(p []byte) (int, error) {
	if keep := w.max - len(w.buf); keep > 0 {
		if keep > len(p) {
			keep = len(p)
		}
		w.buf = append(w.buf, p[:keep]...)
	}
	return len(p), nil
}

// Bytes returns the retained prefix.
func (w *boundedWriter) Bytes() []byte { return w.buf }

func countLines(s string) int {
	n := 0
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

// countPrefixed counts lines starting with prefix, ignoring leading
// whitespace — §go test§ indents a failing subtest under its parent, and a
// subtest that failed is a failing test.
func countPrefixed(s, prefix string) int {
	n := 0
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimLeft(line, " \t"), prefix) {
			n++
		}
	}
	return n
}

// countDiagnostics counts vet findings: lines of the form path:line:col: msg.
// The "# package" headers that group them are not findings.
func countDiagnostics(s string) int {
	n := 0
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}
		if isDiagnostic(line) {
			n++
		}
	}
	return n
}

// isDiagnostic reports whether a line names a source position: at least two
// colon-separated numeric fields after a path.
func isDiagnostic(line string) bool {
	parts := strings.Split(line, ":")
	if len(parts) < 3 {
		return false
	}
	if _, err := strconv.Atoi(parts[1]); err != nil {
		return false
	}
	_, err := strconv.Atoi(parts[2])
	return err == nil
}

func firstLine(s string) string {
	first, _, _ := strings.Cut(s, "\n")
	return first
}
`

// runGo is the judging half. It is a different program from the gates on
// purpose: the party under judgement must not hold what judges it.
var runGo = substituteBackticks(runGoRaw)

const runGoRaw = `package common

// Running one gate by hand, and reaching a verdict from what it measured.
//
// This is the judging layer, and it is a DIFFERENT PROGRAM from the gates on
// purpose. A gate that held its own thresholds could be made to pass by editing
// the gate — and when the thing being measured is a change written by an agent,
// the agent can edit it. The party under judgement must not hold what judges it.
//
// It is also the only layer that can render a result for a person: it holds the
// caps, so it can print a number beside the terms it was judged on. A gate could
// only ever print the left-hand column.
//
// It has TWO MODES, and the difference is who ran the gate:
//
//   - §run <gate>§ measures and then judges. A person at a terminal, and no
//     decision rests on it — a result handed over by the measured party is a
//     claim, not a measurement.
//   - §run <gate> --verdict§ judges an envelope it was GIVEN, on stdin, and
//     spawns nothing. This is the one an external runner asks, and it is what
//     keeps that runner in the runner's seat: an entry point that ran the gate
//     itself would be the runner, and the runner may not come from the tree.
//
// Both reach the verdict through the same comparison, so they cannot disagree
// about what this project allows.

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Direction is the sense in which a measurement is compared to its threshold.
// The set is closed: unknown values are refused at load time.
type Direction string

const (
	AtMost  Direction = "at_most"  // value must not exceed cap (counts of bad things)
	AtLeast Direction = "at_least" // value must not fall below cap (floors like coverage)
)

// Threshold is one entry in the thresholds manifest.
type Threshold struct {
	Direction Direction §json:"direction"§
	Cap       float64   §json:"cap"§
}

// ManifestFile is the thresholds manifest, versioned with the tree it judges.
// A project with a judge must have one, and the path is fixed rather than
// configurable: it is what lets something outside the project establish that
// the terms are an artefact distinct from the judge.
const ManifestFile = "tools/gates/thresholds.json"

// loadManifest reads the thresholds manifest from a repo root. An absent file
// is an error — a project with a judge must have a manifest. An unknown
// direction value is an error — the set is closed.
func loadManifest(repoRoot string) (map[string]Threshold, error) {
	data, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(ManifestFile)))
	if err != nil {
		return nil, fmt.Errorf("loading thresholds manifest: %w", err)
	}
	var manifest map[string]Threshold
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("parsing thresholds manifest: %w", err)
	}
	for name, t := range manifest {
		switch t.Direction {
		case AtMost, AtLeast:
			// known
		default:
			return nil, fmt.Errorf("thresholds manifest: metric %q has unknown direction %q (must be %q or %q)",
				name, t.Direction, AtMost, AtLeast)
		}
	}
	return manifest, nil
}

// RunOneGate measures one gate the way a runner would — by executing the gate
// program, not by calling into it — then judges what came back and prints it.
//
// Going through the process boundary is the point. It is the same path an
// external runner takes, so a gate that is broken in a way only visible across
// that boundary (prints to stdout, exits without an envelope, hangs) is broken
// here too, where a person can see it.
func RunOneGate(repoRoot, gateBin, name string) error {
	if !KnownGate(name) {
		return unknownGate(name)
	}

	cmd := exec.Command(gateBin, name, "--envelope")
	cmd.Dir = repoRoot
	// Stderr is the gate's progress, and it goes straight to ours — not into a
	// buffer we print afterwards. Gates run for minutes, and a gate that is
	// working and a gate that is wedged produce the same thing (nothing) for as
	// long as the output is held.
	cmd.Stderr = os.Stderr
	out, runErr := cmd.Output()

	var env Envelope
	if jsonErr := json.Unmarshal(out, &env); jsonErr != nil {
		// No readable envelope. Whether the process died or printed something
		// that is not an envelope, nothing was measured — and either way this
		// is not a report that the tree is bad.
		if runErr != nil {
			return fmt.Errorf("%s did not measure anything: %w", name, runErr)
		}
		return fmt.Errorf("%s printed something that is not an envelope: %s",
			name, firstLine(string(out)))
	}

	manifest, err := loadManifest(repoRoot)
	if err != nil {
		return err
	}
	fmt.Print(renderVerdict(env, manifest))
	acceptable, _, detail := judge(env, manifest)
	if !acceptable {
		return fmt.Errorf("%s: %s", name, detail)
	}
	return nil
}

// judge compares one envelope against the caps and reaches the verdict.
//
// This is the ONLY comparison in this program. The human path and the wire path
// both come through here, because two paths reaching a verdict separately would
// eventually disagree — and a project that answers "acceptable" to a runner and
// prints a failure to a person has two thresholds wearing one name.
//
// It returns the terms it actually applied, not the whole table: a verdict has
// to travel with what it was reached from, or nobody can re-check it, and the
// caps a measurement never mentioned had no part in it.
//
// A metric nothing caps cannot fail, and an incomplete run cannot pass: honest
// numbers that understate what was checked must not be read as a good result.
func judge(env Envelope, manifest map[string]Threshold) (acceptable bool, thresholds map[string]float64, detail string) {
	thresholds = map[string]float64{}
	var exceeded []string
	for _, m := range env.Metrics {
		t, capped := manifest[m.Name]
		if !capped {
			continue
		}
		thresholds[m.Name] = t.Cap
		var over bool
		switch t.Direction {
		case AtMost:
			over = m.Number() > t.Cap
		case AtLeast:
			over = m.Number() < t.Cap
		}
		if over {
			exceeded = append(exceeded, fmt.Sprintf("%s is %s, %s %s", m.Name, m.String(), t.Direction, number(t.Cap)))
		}
	}
	switch {
	case env.Incomplete != "":
		// Refused even when every number is within its cap. The numbers are
		// honest and describe less than a full run, which is indistinguishable
		// from an improvement unless the run says so.
		return false, thresholds, "the run measured less than a full one, and an incomplete run is never a pass: " + env.Incomplete
	case len(exceeded) > 0:
		return false, thresholds, strings.Join(exceeded, "; ")
	case len(thresholds) == 0:
		return true, thresholds, "nothing here is judged: no metric this gate reported has a threshold"
	default:
		return true, thresholds, "every judged metric is within its cap"
	}
}

// JudgeStdin judges an envelope this program did NOT produce, and writes one
// verdict object to out.
//
// Reading the measurement rather than making it is the whole point of this
// mode. Whoever spawned the gate is the runner; if this entry point ran the
// gate itself, the runner would be a tree artifact, and a runner is the one
// party whose account of a vanished process nothing can check.
//
// Nothing reaches out on any error path. A caller reads one object or none — a
// half-written verdict beside an error message is a second channel, and the two
// could disagree.
func JudgeStdin(repoRoot, name string, in io.Reader, out io.Writer) error {
	if !KnownGate(name) {
		return unknownGate(name)
	}
	envelope, err := io.ReadAll(in)
	if err != nil {
		return fmt.Errorf("reading the envelope to judge: %w", err)
	}
	var env Envelope
	if err := json.Unmarshal(envelope, &env); err != nil {
		return fmt.Errorf("what arrived on stdin is not an envelope: %w", err)
	}
	// The judge's check, and not the caller's: only this layer knows which gate
	// the terms it is about to apply belong to. Judging one gate's numbers
	// against another's caps would answer a question nobody asked.
	if env.Gate != name {
		return fmt.Errorf("asked to judge %q against the terms for %q; a measurement is judged against its own gate's caps", env.Gate, name)
	}
	manifest, err := loadManifest(repoRoot)
	if err != nil {
		return err
	}
	acceptable, thresholds, detail := judge(env, manifest)
	// Marshalled whole before anything is written, so a failure here leaves
	// stdout untouched rather than half a verdict.
	body, err := json.Marshal(verdictWire{Acceptable: acceptable, Thresholds: thresholds, Detail: detail})
	if err != nil {
		return fmt.Errorf("rendering the verdict for %s: %w", name, err)
	}
	_, err = out.Write(append(body, '\n'))
	return err
}

// verdictWire is what the judging layer prints in --verdict mode: one JSON
// object, whole.
//
// Thresholds is not omitempty and is never nil. A verdict handed over with the
// terms it was reached from discarded cannot be re-checked by anyone who was not
// there, which is exactly the property that lets a judge live in the tree it
// judges.
type verdictWire struct {
	Acceptable bool               §json:"acceptable"§
	Thresholds map[string]float64 §json:"thresholds"§
	Detail     string             §json:"detail,omitempty"§
}

// ParseRunArgs reads an invocation of this program: exactly one gate name, and
// whether the caller asked for a verdict on an envelope it is handing over.
//
// Same rules as ParseGateArgs, and for the same reason — an unknown flag is
// refused rather than ignored, and a second name refused rather than dropped. A
// caller that meant --verdict and mistyped it must not silently get the
// measuring mode, which spawns a gate.
func ParseRunArgs(args []string) (name string, verdict bool, err error) {
	for _, a := range NormalizeArgs(args) {
		switch {
		case a == "-verdict":
			verdict = true
		case strings.HasPrefix(a, "-"):
			return "", false, fmt.Errorf("use of unknown flag %q; known gates: %s", a, strings.Join(GateNames(), ", "))
		case name != "":
			return "", false, fmt.Errorf("unexpected argument %q; one gate at a time; known gates: %s",
				a, strings.Join(GateNames(), ", "))
		default:
			name = a
		}
	}
	if name == "" {
		return "", false, fmt.Errorf("no gate named; known gates: %s", strings.Join(GateNames(), ", "))
	}
	if !KnownGate(name) {
		return "", false, unknownGate(name)
	}
	return name, verdict, nil
}

// renderVerdict prints each measurement beside the term it was judged on.
func renderVerdict(env Envelope, manifest map[string]Threshold) string {
	var sb strings.Builder
	width := 0
	for _, m := range env.Metrics {
		if len(m.Name) > width {
			width = len(m.Name)
		}
	}
	for _, m := range env.Metrics {
		t, capped := manifest[m.Name]
		judged, mark := "not judged", " "
		if capped {
			judged = string(t.Direction) + " " + number(t.Cap)
			mark = "✗"
			switch t.Direction {
			case AtMost:
				if m.Number() <= t.Cap {
					mark = "✓"
				}
			case AtLeast:
				if m.Number() >= t.Cap {
					mark = "✓"
				}
			}
		}
		fmt.Fprintf(&sb, "  %-*s  %8s  %-16s %s\n",
			width, m.Name, m.String()+unitSuffix(m.Unit), judged, mark)
	}
	if env.Incomplete != "" {
		fmt.Fprintf(&sb, "\n  incomplete: %s\n", env.Incomplete)
		fmt.Fprintf(&sb, "  an incomplete run is never a pass, and never moves a baseline\n")
	}
	return sb.String()
}

func number(v float64) string {
	if v == float64(int64(v)) {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'f', 1, 64)
}

func unitSuffix(unit string) string {
	switch unit {
	case "percent":
		return "%"
	case "bytes":
		return " B"
	}
	return ""
}

// GateBinary is where this project's gate program lives, relative to the repo
// root. Not configurable: the names are fixed, so the way to reach them is.
func GateBinary(repoRoot string) string {
	return filepath.Join(repoRoot, "bin", BinaryName("gate"))
}

// CappedMetrics lists the metrics this layer judges, for usage text. If the
// manifest cannot be loaded (empty repoRoot), it returns an empty list — this is
// usage text only, not a judging path.
func CappedMetrics(repoRoot string) []string {
	if repoRoot == "" {
		return nil
	}
	manifest, err := loadManifest(repoRoot)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(manifest))
	for n := range manifest {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
`

// gateTestGo is the starter's own test file. An adopter inherits the error and
// edge paths, not just the happy one: the refusals are the protocol, and a
// refusal nothing tests is a refusal that quietly becomes an acceptance.
var gateTestGo = substituteBackticks(gateTestGoRaw)

const gateTestGoRaw = `package common

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// manifestRepo writes a thresholds manifest into a temp repo root and returns
// it. The path comes from ManifestFile so a moved manifest moves the tests too.
func manifestRepo(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, filepath.FromSlash(ManifestFile))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

const capMissingPrereqs = §{"missing_prereqs": {"direction": "at_most", "cap": 0}}§

// Every refusal names what this project does answer. A caller that mistyped a
// gate name learns the set rather than only that it was wrong.
func TestParseGateArgsRefusesWhatIsNotOneGate(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"no gate at all", nil},
		{"an unknown name", []string{"coverage"}},
		{"a second name", []string{"fit", "tested"}},
		{"an unknown flag", []string{"fit", "--json"}},
	}
	for _, c := range cases {
		_, _, err := ParseGateArgs(c.args)
		if err == nil {
			t.Errorf("%s: expected a refusal, got none", c.name)
			continue
		}
		if !strings.Contains(err.Error(), "integration") {
			t.Errorf("%s: the refusal does not name the known gates: %v", c.name, err)
		}
	}
}

// A bare name parses, and reports that no envelope was asked for. The refusal
// itself lives in cmd/gate, because printing measurements without --envelope
// would be read as a pass by the first script that wrapped it.
func TestParseGateArgsReportsWhetherAnEnvelopeWasAskedFor(t *testing.T) {
	name, envelope, err := ParseGateArgs([]string{"fit"})
	if err != nil {
		t.Fatalf("a known gate was refused: %v", err)
	}
	if name != "fit" || envelope {
		t.Errorf("ParseGateArgs = (%q, %v), want (\"fit\", false)", name, envelope)
	}
	if _, envelope, err = ParseGateArgs([]string{"fit", "--envelope"}); err != nil || !envelope {
		t.Errorf("--envelope was not read: envelope=%v err=%v", envelope, err)
	}
}

func TestMeasureGateRefusesAnUnknownName(t *testing.T) {
	env, err := MeasureGate(t.TempDir(), "coverage")
	if err == nil {
		t.Fatal("an unknown gate measured something")
	}
	// No partial envelope: a measurement that was never made must not parse as
	// one that found nothing.
	if env.Gate != "" || len(env.Metrics) != 0 {
		t.Errorf("a refused gate returned an envelope: %+v", env)
	}
}

func TestKnownGateAndNamesAgree(t *testing.T) {
	for _, n := range GateNames() {
		if !KnownGate(n) {
			t.Errorf("--list names %q but KnownGate refuses it", n)
		}
		if GateSummary(n) == "" {
			t.Errorf("gate %q has no summary", n)
		}
	}
	if KnownGate("no-such-gate") {
		t.Error("an unknown name was accepted")
	}
}

// integration is a composition, and its parts are separately addressable so a
// step fixing one failing suite can re-run that suite alone.
func TestIntegrationPartsAreThemselvesGates(t *testing.T) {
	parts := gates["integration"].parts
	if len(parts) == 0 {
		t.Fatal("integration has no parts")
	}
	for _, p := range parts {
		if !KnownGate(p) {
			t.Errorf("integration names part %q, which is not a gate", p)
		}
	}
}

func TestJudgeLeavesAnUncappedMetricAlone(t *testing.T) {
	env := Envelope{Gate: "fit", Metrics: []Metric{Count("nobody_caps_this", 99)}}
	acceptable, thresholds, detail := judge(env, map[string]Threshold{})
	if !acceptable {
		t.Errorf("an uncapped metric failed: %s", detail)
	}
	if len(thresholds) != 0 {
		t.Errorf("terms were reported that nothing applied: %v", thresholds)
	}
}

func TestJudgeFailsACappedMetricOverItsCapAndNamesTheTerm(t *testing.T) {
	manifest := map[string]Threshold{"missing_prereqs": {Direction: AtMost, Cap: 0}}
	env := Envelope{Gate: "fit", Metrics: []Metric{Count("missing_prereqs", 2)}}
	acceptable, thresholds, detail := judge(env, manifest)
	if acceptable {
		t.Fatal("2 is over a cap of 0 and was accepted")
	}
	if !strings.Contains(detail, "missing_prereqs") {
		t.Errorf("the failure does not name the term it rests on: %q", detail)
	}
	// The verdict travels with what it was reached from, or nobody who was not
	// there can re-check it.
	if _, ok := thresholds["missing_prereqs"]; !ok {
		t.Errorf("the applied term was not reported: %v", thresholds)
	}
}

func TestJudgeFailsAFloorBelowItsCap(t *testing.T) {
	manifest := map[string]Threshold{"statement_coverage": {Direction: AtLeast, Cap: 75}}
	env := Envelope{Gate: "covered", Metrics: []Metric{Quantity("statement_coverage", 40, "percent")}}
	if acceptable, _, _ := judge(env, manifest); acceptable {
		t.Error("40 is below a floor of 75 and was accepted")
	}
	env.Metrics = []Metric{Quantity("statement_coverage", 80, "percent")}
	if acceptable, _, detail := judge(env, manifest); !acceptable {
		t.Errorf("80 is above a floor of 75 and was refused: %s", detail)
	}
}

// Honest numbers that describe less than a full run are indistinguishable from
// an improvement unless the run says so, so an incomplete run is never a pass —
// whatever the numbers are.
func TestJudgeRefusesAnIncompleteRunWithEveryNumberInCap(t *testing.T) {
	manifest := map[string]Threshold{"failed_tests": {Direction: AtMost, Cap: 0}}
	env := Envelope{
		Gate:       "tested",
		Metrics:    []Metric{Count("failed_tests", 0)},
		Incomplete: "one module was skipped",
	}
	acceptable, _, detail := judge(env, manifest)
	if acceptable {
		t.Fatal("an incomplete run passed")
	}
	if !strings.Contains(detail, "one module was skipped") {
		t.Errorf("the refusal does not carry the reason: %q", detail)
	}
}

func TestJudgeStdinWritesOneVerdictCarryingItsTerms(t *testing.T) {
	root := manifestRepo(t, capMissingPrereqs)
	env, err := json.Marshal(Envelope{Gate: "fit", Metrics: []Metric{Count("missing_prereqs", 0)}})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := JudgeStdin(root, "fit", bytes.NewReader(env), &out); err != nil {
		t.Fatalf("judging a well-formed envelope failed: %v", err)
	}
	var verdict struct {
		Acceptable bool               §json:"acceptable"§
		Thresholds map[string]float64 §json:"thresholds"§
	}
	if err := json.Unmarshal(out.Bytes(), &verdict); err != nil {
		t.Fatalf("the verdict does not parse: %v (%q)", err, out.String())
	}
	if !verdict.Acceptable {
		t.Error("0 missing prerequisites was not acceptable")
	}
	if len(verdict.Thresholds) == 0 {
		t.Error("the verdict carries no terms, so nothing can re-check it")
	}
}

// Nothing reaches stdout on any error path: a caller reads one object or none,
// because a half-written verdict beside an error message is a second channel.
func TestJudgeStdinWritesNothingWhenItRefuses(t *testing.T) {
	root := manifestRepo(t, capMissingPrereqs)
	other, err := json.Marshal(Envelope{Gate: "tested", Metrics: []Metric{Count("failed_tests", 0)}})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		gate  string
		stdin []byte
	}{
		{"stdin is not an envelope", "fit", []byte("this is not JSON")},
		{"the envelope names another gate", "fit", other},
		{"the gate is unknown", "coverage", other},
	}
	for _, c := range cases {
		var out bytes.Buffer
		if err := JudgeStdin(root, c.gate, bytes.NewReader(c.stdin), &out); err == nil {
			t.Errorf("%s: expected a refusal, got none", c.name)
		}
		if out.Len() != 0 {
			t.Errorf("%s: wrote %q to stdout while refusing", c.name, out.String())
		}
	}
}

func TestLoadManifestRefusesAbsentAndUnknown(t *testing.T) {
	// A project with a judge must have a manifest: judging against no terms at
	// all would report every measurement acceptable.
	if _, err := loadManifest(t.TempDir()); err == nil {
		t.Error("an absent manifest loaded")
	}
	root := manifestRepo(t, §{"failed_tests": {"direction": "at_some_point", "cap": 0}}§)
	if _, err := loadManifest(root); err == nil {
		t.Error("an unknown direction loaded; the set is closed")
	}
}

func TestMetricRoundTripsInItsOwnType(t *testing.T) {
	for _, m := range []Metric{
		Count("failed_tests", 3),
		Quantity("statement_coverage", 62.5, "percent"),
		Size("worktree_free_bytes", 1<<40, "bytes"),
	} {
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("%s: %v", m.Name, err)
		}
		var got Metric
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatalf("%s: %v", m.Name, err)
		}
		if got != m {
			t.Errorf("%s round-tripped to %+v, want %+v", m.Name, got, m)
		}
	}
}

// A count arriving fractional is refused rather than widened: absorbed, it
// would be a type change nothing recorded, on a number a ratchet is read from.
func TestMetricRefusesAValueThatIsNotItsDeclaredType(t *testing.T) {
	for _, body := range []string{
		§{"name": "failed_tests", "type": "int", "value": 1.5}§,
		§{"name": "failed_tests", "type": "count", "value": 1}§,
	} {
		var m Metric
		if err := json.Unmarshal([]byte(body), &m); err == nil {
			t.Errorf("%s was accepted as %+v", body, m)
		}
	}
}
`

// ───────────────────────── cmd mains ─────────────────────────

const cmdMakeGo = `// Command make is the meta-builder. It compiles every other tool under cmd/
// into <repoRoot>/bin, stamping each binary with the tools-source hash and the
// absolute repo root via -ldflags. It is the one tool that runs via 'go run'
// (from the ./make trampoline), so it is never compiled into bin/ and never
// stale — which is what breaks the bootstrap cycle.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"__MODULE__/common"
)

func main() {
	force := false
	for _, a := range os.Args[1:] {
		if a == "-force" || a == "--force" {
			force = true
		}
	}

	// 1. Resolve the repo root. The ./make trampoline cd'd go run into
	//    <root>/tools/build, so our cwd is exactly that. Two levels up is root.
	cwd, err := os.Getwd()
	must(err)
	repoRoot := filepath.Dir(filepath.Dir(cwd))
	if !filepath.IsAbs(repoRoot) {
		fail("resolved repo root is not absolute: %s", repoRoot)
	}

	// 2. Hash the tools source — baked into every binary below.
	hash, err := common.ToolsSourceHash(repoRoot)
	must(err)

	// 3. Enable git hooks unconditionally (idempotent, fast).
	if err := common.RunSetup(repoRoot); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not configure git hooks: %v\n", err)
	}

	// Discover tools: every cmd/<name> except make itself.
	cmdDir := filepath.Join(repoRoot, "tools", "build", "cmd")
	entries, err := os.ReadDir(cmdDir)
	must(err)
	var tools []string
	for _, e := range entries {
		if e.IsDir() && e.Name() != "make" {
			tools = append(tools, e.Name())
		}
	}
	sort.Strings(tools)

	binDir := filepath.Join(repoRoot, "bin")
	hashFile := filepath.Join(binDir, ".tools.hash")

	// 4. Up-to-date short circuit.
	if !force && upToDate(hashFile, hash, binDir, tools) {
		fmt.Println("Tools up to date")
		return
	}

	must(os.MkdirAll(binDir, 0o755))

	// 5. Build each tool, injecting repoRoot and sourceHash via ldflags.
	ldflags := fmt.Sprintf("-s -w -X main.sourceHash=%s -X main.repoRoot=%s", hash, repoRoot)
	toolsModDir := filepath.Join(repoRoot, "tools", "build")
	for _, name := range tools {
		out := filepath.Join(binDir, common.BinaryName(name))
		fmt.Printf("building %s\n", name)
		if err := common.RunIn(toolsModDir, "go", "build",
			"-trimpath",
			"-ldflags", ldflags,
			"-o", out,
			"./cmd/"+name,
		); err != nil {
			fail("building %s: %v", name, err)
		}
	}

	// 6. Write the hash sidecar — the staleness contract.
	must(os.WriteFile(hashFile, []byte(hash+"\n"), 0o644))
	fmt.Printf("built %d tool(s) into bin/\n", len(tools))
}

func upToDate(hashFile, hash, binDir string, tools []string) bool {
	data, err := os.ReadFile(hashFile)
	if err != nil || strings.TrimSpace(string(data)) != hash {
		return false
	}
	for _, name := range tools {
		if !common.Exists(filepath.Join(binDir, common.BinaryName(name))) {
			return false
		}
	}
	return true
}

func must(err error) {
	if err != nil {
		fail("%v", err)
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "make: "+format+"\n", args...)
	os.Exit(1)
}
`

const cmdVerifyGo = `package main

import (
	"fmt"
	"os"

	"__MODULE__/common"
)

// Injected by the meta-builder via -ldflags at build time; empty otherwise.
var (
	repoRoot   = ""
	sourceHash = ""
)

func main() {
	common.CheckStale(repoRoot, sourceHash)
	if err := common.RunVerify(repoRoot, common.NormalizeArgs(os.Args[1:])); err != nil {
		fmt.Fprintln(os.Stderr, "verify failed:", err)
		os.Exit(1)
	}
}
`

const cmdSetupGo = `package main

import (
	"fmt"
	"os"

	"__MODULE__/common"
)

var (
	repoRoot   = ""
	sourceHash = ""
)

func main() {
	common.CheckStale(repoRoot, sourceHash)
	if err := common.RunSetup(repoRoot); err != nil {
		fmt.Fprintln(os.Stderr, "setup failed:", err)
		os.Exit(1)
	}
	fmt.Println("git hooks configured (core.hooksPath = .githooks)")
}
`

var cmdGateGo = substituteBackticks(cmdGateGoRaw)

const cmdGateGoRaw = `// Command gate measures one property of this tree and prints what it found.
//
// It is not meant to be run by hand — §bin/run <gate>§ is that path. A gate
// answers a runner, and a runner is the only caller that can say what became of
// the run: a gate killed for memory is not alive to report it, and a gate that
// exited cleanly having printed nothing would be believed.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"__MODULE__/common"
)

// Injected by the meta-builder via -ldflags at build time; empty otherwise.
var (
	repoRoot   = ""
	sourceHash = ""
)

func usage() string {
	var sb strings.Builder
	sb.WriteString("gate — measure one property of this tree.\n\n")
	sb.WriteString("Usage:\n  gate <name> --envelope\n  gate --list\n\n")
	sb.WriteString("Prints one JSON envelope on stdout and nothing else. Without --envelope it\n")
	sb.WriteString("prints nothing and fails: a bare run that printed measurements and exited 0\n")
	sb.WriteString("would be read as a pass by the first script that wrapped it, and a gate has\n")
	sb.WriteString("no verdict to give. Run §run <name>§ for a result meant for a person.\n\n")
	sb.WriteString("Gates:\n")
	for _, n := range common.GateConcepts() {
		fmt.Fprintf(&sb, "  %-12s %s\n", n, common.GateSummary(n))
	}
	sb.WriteString("\n§--list§ prints every name this project answers, one per line.\n")
	return sb.String()
}

// isListArg reports whether the argv asks for the gate list and nothing else.
// Exactly one argument: a request with anything alongside it is ambiguous
// between listing and measuring, and guessing would print a list to a caller
// waiting for an envelope.
func isListArg(args []string) bool {
	return len(args) == 1 && (args[0] == "--list" || args[0] == "-list" || args[0] == "list")
}

// fail prints to stderr and exits non-zero, leaving stdout untouched. Every
// path out of this program that is not a complete envelope comes through here.
func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "gate: "+format+"\n", args...)
	os.Exit(1)
}

func main() {
	args := common.NormalizeArgs(os.Args[1:])

	// --list answers "which gates does this project have?", one name per line
	// on stdout, exit 0.
	//
	// It is the only mode besides a measurement that writes to stdout, and it
	// does not break the envelope rule: a caller that asked for the list did not
	// ask for a measurement, and a name-per-line stream cannot be mistaken for
	// an envelope by anything that parses one.
	//
	// It exists because an orchestrator must not hold a second copy of what this
	// project can measure. Asking the entry point is the only way to learn it
	// that cannot go stale.
	if isListArg(args) {
		for _, n := range common.GateNames() {
			fmt.Println(n)
		}
		return
	}

	// Help goes to STDERR and exits non-zero, where every other tool here prints
	// it to stdout and exits 0. Stdout carries the envelope and nothing else — a
	// caller redirecting stdout to a parser must get an envelope or nothing, and
	// "nothing" must not look like success.
	if common.HasHelpFlag(args) {
		fmt.Fprint(os.Stderr, usage())
		os.Exit(1)
	}

	// An unknown name is refused rather than guessed at: a runner asking for a
	// gate this project does not have must learn that, not receive an empty
	// measurement that reads like a clean result.
	name, envelope, err := common.ParseGateArgs(args)
	if err != nil {
		fail("%v; run §%s -h§ for usage", err, os.Args[0])
	}
	if !envelope {
		fail("refusing to measure without --envelope; run §run %s§ for a result "+
			"meant for a person", name)
	}

	// Stale logic would measure this tree with yesterday's gates and print a
	// well-formed envelope about it, which is the one failure nothing downstream
	// could detect.
	if reason := common.StaleReason(repoRoot, sourceHash); reason != "" {
		fail("%s — run %s", reason, common.MakeCmd())
	}

	env, err := common.MeasureGate(repoRoot, name)
	if err != nil {
		// Nothing was measured. No envelope, because a partial one is not a
		// measurement and must not parse as one.
		fail("%v", err)
	}

	// The envelope is written whole, in one write. A run killed part-way leaves
	// output that does not parse, which is how a reader tells "measured nothing"
	// from "measured and reported" without asking the gate.
	out, err := json.Marshal(env)
	if err != nil {
		fail("could not encode the envelope: %v", err)
	}
	os.Stdout.Write(append(out, '\n'))
}
`

var cmdRunGo = substituteBackticks(cmdRunGoRaw)

const cmdRunGoRaw = `// Command run asks one gate for a measurement and reaches a verdict on it.
//
// This is the by-hand path, and it takes the same route an external runner
// takes rather than a parallel one: it executes bin/gate as a process and reads
// what came back. Running a single gate is not a lesser case — it is faster than
// everything that blocks a change from landing, and it is what someone
// iterating on one failure actually wants.
package main

import (
	"fmt"
	"os"
	"strings"

	"__MODULE__/common"
)

// Injected by the meta-builder via -ldflags at build time; empty otherwise.
var (
	repoRoot   = ""
	sourceHash = ""
)

func usage() string {
	var sb strings.Builder
	sb.WriteString("run — measure one gate and judge what it measured.\n\n")
	sb.WriteString("Usage:\n  run <gate> [-h | -help]\n  run <gate> --verdict < envelope\n\n")
	sb.WriteString("Runs bin/gate <gate> --envelope, then prints each measurement beside the\n")
	sb.WriteString("term it was judged on. Exit 0 means every capped measurement is within its\n")
	sb.WriteString("cap; non-zero means one is not, or that nothing could be measured.\n\n")
	sb.WriteString("With --verdict it judges an envelope it is GIVEN, on stdin, and runs no\n")
	sb.WriteString("gate: it prints one JSON verdict on stdout and nothing else. That is the\n")
	sb.WriteString("mode an external runner asks — it spawns the gate itself, because a judge\n")
	sb.WriteString("that ran its own measurement would be the runner, and the runner comes from\n")
	sb.WriteString("outside the tree. The verdict is the JSON, not the exit status.\n\n")
	sb.WriteString("Gates:\n")
	for _, n := range common.GateConcepts() {
		fmt.Fprintf(&sb, "  %-12s %s\n", n, common.GateSummary(n))
	}
	capped := common.CappedMetrics(repoRoot)
	if len(capped) > 0 {
		fmt.Fprintf(&sb, "\nJudged against a cap: %s\n", strings.Join(capped, ", "))
	} else {
		fmt.Fprintf(&sb, "\nThresholds defined in %s\n", common.ManifestFile)
	}
	sb.WriteString("Anything else is reported and not judged.\n")
	return sb.String()
}

func main() {
	args := common.NormalizeArgs(os.Args[1:])
	if common.HasHelpFlag(args) {
		fmt.Print(usage())
		os.Exit(0)
	}
	common.CheckStale(repoRoot, sourceHash)

	name, verdict, err := common.ParseRunArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "run: %v; run §%s -h§ for usage\n", err, os.Args[0])
		os.Exit(2)
	}

	// The judging mode. Nothing is spawned: the envelope arrives on stdin from
	// whoever ran the gate, and stdout carries one verdict and nothing else.
	// CheckStale has already run above, so stale tooling exits before it can
	// print a verdict rather than answering with terms nobody currently holds.
	if verdict {
		if err := common.JudgeStdin(repoRoot, name, os.Stdin, os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "run: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if err := common.RunOneGate(repoRoot, common.GateBinary(repoRoot), name); err != nil {
		fmt.Fprintf(os.Stderr, "run: %v\n", err)
		os.Exit(1)
	}
}
`

// ───────────────────────── the judging terms ─────────────────────────

// thresholdsJSON caps every metric the starter gates emit. A metric with no
// entry cannot fail, so an empty manifest is a judge that accepts everything —
// and a verdict carrying no terms is one nothing can re-check.
const thresholdsJSON = `{
  "unformatted_files": {
    "direction": "at_most",
    "cap": 0
  },
  "unbuildable_packages": {
    "direction": "at_most",
    "cap": 0
  },
  "vet_findings": {
    "direction": "at_most",
    "cap": 0
  },
  "failed_tests": {
    "direction": "at_most",
    "cap": 0
  },
  "failed_packages": {
    "direction": "at_most",
    "cap": 0
  },
  "missing_prereqs": {
    "direction": "at_most",
    "cap": 0
  }
}
`

// baselinesJSON is the ratcheted-metric artefact, empty. Tracking the file is
// the declaration that the ratchet applies to this project; an empty object is
// a complete and honest starting state, and the first metric a project ratchets
// is an edit here rather than a new file nobody reads.
const baselinesJSON = `{}
`

// ───────────────────────── docs ─────────────────────────

// docsIndexMd is the map of docs/. It carries no relative link, so the tree it
// is written into cannot fail a documentation link check on the scaffolder's
// own output before the adopter has written anything.
var docsIndexMd = substituteBackticks(docsIndexMdRaw)

const docsIndexMdRaw = `# Documentation Index

This is the map of §docs/§. Every document in this directory is listed below, so
one file answers "what is written down here?" — a document nobody can reach from
the index is one nobody reads, and one nobody updates.

## Specifications

_Nothing yet._ Add a line here in the same commit that adds the document; an
index brought up to date afterwards is an index that was wrong in between.
`

// ───────────────────────── claude config ─────────────────────────

// settingsJSON wires bin/tool-guard, a workspace tool this project does not
// build, on BOTH tool-use events (docs/blueprint.md §10). PreToolUse is the gate
// and fails closed; PostToolUse observes and fails quiet, because by then the
// tool has already run and an enforcing shape could only inject an error after a
// completed call.
//
// The two command strings are EXACT, not a shape. A conformance checker compares
// them byte-for-byte and reports any difference at error severity, so no wrapper
// and no conditional may be added here — unlike the pre-commit hook above, which
// is a script and can branch. That is also why the file is committed: a tracked
// settings file is what makes the guard live in a fresh clone, before
// `workspace setup` has ever run there.
const settingsJSON = `{
  "hooks": {
    "PostToolUse": [
      {
        "hooks": [
          {
            "command": "\"$CLAUDE_PROJECT_DIR/bin/tool-guard\" || true",
            "shell": "bash",
            "timeout": 10,
            "type": "command"
          }
        ],
        "matcher": "*"
      }
    ],
    "PreToolUse": [
      {
        "hooks": [
          {
            "command": "\"$CLAUDE_PROJECT_DIR/bin/tool-guard\" || exit 2",
            "shell": "bash",
            "timeout": 10,
            "type": "command"
          }
        ],
        "matcher": "*"
      }
    ]
  }
}
`
