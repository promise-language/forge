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
//   - tools/build/common/                 helper package (hash, stale, exec, verify, …)
//   - tools/build/cmd/make/main.go        the meta-builder (runs via `go run`)
//   - tools/build/cmd/<tool>/main.go      one binary per dir: verify, setup, precommit, guard
//   - .githooks/pre-commit                git hook trampoline → bin/precommit
//   - .claude/settings.json               wires bin/guard as a PreToolUse hook
//   - .gitignore                          adds bin/ (built tools are never committed)
//   - CLAUDE.md                           appends a "Dev tooling" section so agents
//     discover the ./make → bin/verify workflow
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
	fmt.Println("  ./make        # compiles bin/{verify,setup,precommit,guard}")
	fmt.Println("  bin/verify    # runs the commit gate")
	fmt.Println()
	fmt.Println("The guard (bin/guard, wired in .claude/settings.json) blocks dangerous")
	fmt.Println("commands always, and when the tools are out of sync funnels the agent to")
	fmt.Println("./make — allowing only that, the existing bin/ tools, and tools/build edits.")
	fmt.Println()
	fmt.Println("Then edit tools/build/common/verify.go to run your project's real")
	fmt.Println("format / build / test commands. Drop a new dir under tools/build/cmd/")
	fmt.Println("and re-run ./make to get another bin/<tool> — no registration needed.")
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

func ensureGitignore(absTarget string) {
	path := filepath.Join(absTarget, ".gitignore")
	existing, _ := os.ReadFile(path)
	if strings.Contains(string(existing), "bin/") {
		fmt.Println("  skip   .gitignore (bin/ already ignored)")
		return
	}
	const block = "\n# forge dev tooling — bin/ holds locally-built tools, never committed\nbin/\n"
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	must(err)
	defer f.Close()
	_, err = f.WriteString(block)
	must(err)
	fmt.Println("  update .gitignore (+bin/)")
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
		{path: "tools/build/common/precommit.go", body: precommitGo},
		{path: "tools/build/common/guard.go", body: guardGo},
		{path: "tools/build/common/guard_test.go", body: guardTestGo},
		{path: "tools/build/cmd/make/main.go", body: cmdMakeGo},
		{path: "tools/build/cmd/verify/main.go", body: cmdVerifyGo},
		{path: "tools/build/cmd/setup/main.go", body: cmdSetupGo},
		{path: "tools/build/cmd/precommit/main.go", body: cmdPrecommitGo},
		{path: "tools/build/cmd/guard/main.go", body: cmdGuardGo},
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
bin/verify        # format → vet → build → test, then a pass/FAIL summary
§§§

A green "OK to Commit" line means it is safe to commit; a red FAIL means it is
not. The pre-commit hook (§bin/precommit§) also blocks staged binaries, enforces
GitHub noreply commit identities, and keeps the tree source-only.

**Edit a tool, then rebuild.** If any tool prints
§tools source has changed — run: ./make§, re-run §./make§ to rebuild §bin/§.
That is the whole loop: edit → §./make§ → use. Add a tool by dropping a new dir
under §tools/build/cmd/§ and re-running §./make§ — no registration step.
<!-- /forge:dev-tooling -->
`

const preCommitHook = `#!/usr/bin/env bash
# Git pre-commit trampoline — execs the compiled hook, or refuses the commit.
# Wired up by 'git config core.hooksPath .githooks', which ./make runs for you.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
if [ -x "$root/bin/precommit" ]; then
    exec "$root/bin/precommit"
fi
# Fail closed: the commit gate is mandatory. If bin/precommit is not built, a
# commit could slip past every check — so block it and demand a bootstrap. The
# recovery (./make) runs via 'go run' and needs nothing pre-built, so this is a
# speed bump, never a trap.
echo "pre-commit: bin/precommit not built — run ./make first, then commit" >&2
exit 1
`

// ───────────────────────── tools module ─────────────────────────

const goMod = `module __MODULE__

go 1.22
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

// RunVerify is the commit gate: format → vet → build → test. It always prints a
// summary block (even on failure) so an agent tailing the output sees the
// result without re-running, and the process exit code is the only contract.
//
// This is an EXAMPLE pipeline. For a Go project it runs real go tooling; for
// anything else it runs harmless stubs. Replace verifySteps with your project's
// real commands.
func RunVerify(repoRoot string, args []string) error {
	steps := verifySteps(repoRoot)
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

const precommitGo = `package common

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// RunPrecommit enforces fast, test-free invariants before a commit. It is the
// body of bin/precommit, which .githooks/pre-commit execs. Keep it sub-second:
// anything that needs to run tests belongs in bin/verify, which the developer
// runs explicitly — putting tests here makes commits slow and gets the hook
// disabled, which kills the whole gate.
//
// The starter checks are language-agnostic and apply to any repo:
//  1. no staged built binaries under bin/ (gitignored, built by ./make),
//  2. author and committer identities are GitHub noreply addresses,
//  3. no binary blobs or oversized files anywhere in the tree — source only.
//
// Grow it from here with project-specific checks: forbidden-pattern scans and
// ratcheted-baseline (.baselines.json) validation.
func RunPrecommit(repoRoot string) error {
	if err := checkNoStagedBinaries(repoRoot); err != nil {
		return err
	}
	if err := checkNoreplyIdentity(repoRoot); err != nil {
		return err
	}
	if err := checkNoBinaryBlobs(repoRoot); err != nil {
		return err
	}
	return nil
}

func checkNoStagedBinaries(repoRoot string) error {
	staged, err := RunOutputIn(repoRoot, "git", "diff", "--cached", "--name-only")
	if err != nil {
		return fmt.Errorf("listing staged files: %w", err)
	}
	var offenders []string
	for _, line := range strings.Split(staged, "\n") {
		f := strings.TrimSpace(line)
		if f == "bin" || strings.HasPrefix(f, "bin/") {
			offenders = append(offenders, f)
		}
	}
	if len(offenders) > 0 {
		fmt.Fprintln(os.Stderr, "❌ pre-commit: refusing to commit built binaries:")
		for _, f := range offenders {
			fmt.Fprintf(os.Stderr, "  %s\n", f)
		}
		fmt.Fprintf(os.Stderr, "bin/ is gitignored and built by ./make. Unstage with:\n  git reset HEAD %s\n",
			strings.Join(offenders, " "))
		return fmt.Errorf("%d staged binary path(s)", len(offenders))
	}
	return nil
}

// noreplyDomain is the only email domain permitted for commit identities. Using
// a GitHub noreply address keeps a personal email out of the public history.
// This is the one project-policy knob in the starter checks — change it if your
// project uses a different identity convention.
const noreplyDomain = "@users.noreply.github.com"

// checkNoreplyIdentity refuses the commit unless both the author and committer
// emails are GitHub noreply addresses. It reads the identities via 'git var',
// which resolves them exactly as the impending commit will — honoring
// GIT_AUTHOR_EMAIL / GIT_COMMITTER_EMAIL env vars and user.email config alike —
// so the check matches what would actually be recorded.
func checkNoreplyIdentity(repoRoot string) error {
	roles := []struct{ label, gitVar string }{
		{"author", "GIT_AUTHOR_IDENT"},
		{"committer", "GIT_COMMITTER_IDENT"},
	}
	for _, r := range roles {
		ident, err := RunOutputIn(repoRoot, "git", "var", r.gitVar)
		if err != nil {
			return fmt.Errorf("reading %s identity: %w", r.label, err)
		}
		email := identEmail(ident)
		if !strings.HasSuffix(strings.ToLower(email), noreplyDomain) {
			fmt.Fprintf(os.Stderr, "❌ pre-commit: %s identity %q is not a %s address.\n", r.label, email, noreplyDomain)
			fmt.Fprintf(os.Stderr, "Set a GitHub noreply email, e.g.:\n  git config user.email \"<id>+<user>%s\"\n", noreplyDomain)
			return fmt.Errorf("%s email %q lacks %s", r.label, email, noreplyDomain)
		}
	}
	return nil
}

// identEmail extracts the address from a git ident string of the form
// "Name <email> <timestamp> <tz>". It returns "" if no <…> field is present.
func identEmail(ident string) string {
	open := strings.IndexByte(ident, '<')
	close := strings.IndexByte(ident, '>')
	if open < 0 || close < open {
		return ""
	}
	return ident[open+1 : close]
}

const (
	// binaryScanPrefix is how many leading bytes of a staged blob the binary
	// check inspects. A NUL byte anywhere in this window means the blob is
	// binary, not source text — git's own heuristic, widened to 16 KiB.
	binaryScanPrefix = 16 * 1024
	// maxBlobBytes is the largest staged file permitted. Anything bigger is
	// almost certainly a generated artifact or vendored blob, not source — and
	// is rejected whether or not it scans as text.
	maxBlobBytes = 256 * 1024
)

// checkNoBinaryBlobs refuses the commit if any staged file is binary (contains
// a NUL byte in its first binaryScanPrefix bytes) or larger than maxBlobBytes.
// The repo holds source only — no compiled artifacts, images, or vendored
// blobs. It inspects the *staged* content via 'git cat-file' (':<path>' reads
// from the index), so it judges exactly what the commit would record.
func checkNoBinaryBlobs(repoRoot string) error {
	staged, err := RunOutputIn(repoRoot, "git", "diff", "--cached", "--name-status")
	if err != nil {
		return fmt.Errorf("listing staged files: %w", err)
	}
	var rejects []string
	for _, line := range strings.Split(staged, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		// Skip blank lines and deletions: a removed path commits nothing.
		if len(fields) < 2 || strings.HasPrefix(fields[0], "D") {
			continue
		}
		path := fields[len(fields)-1] // dst path (handles rename/copy "R old new")
		spec := ":" + path

		// Size first — cheap, no full read. Skip entries that aren't regular
		// blobs (e.g. submodule gitlinks), where cat-file -s errors out.
		sizeOut, err := RunOutputIn(repoRoot, "git", "cat-file", "-s", spec)
		if err != nil {
			continue
		}
		size, err := strconv.ParseInt(sizeOut, 10, 64)
		if err != nil {
			continue
		}

		var head []byte
		if size <= maxBlobBytes { // only read the bytes we need to scan
			data, err := OutputBytesIn(repoRoot, "git", "cat-file", "blob", spec)
			if err != nil {
				continue
			}
			head = data
			if len(head) > binaryScanPrefix {
				head = head[:binaryScanPrefix]
			}
		}
		if why := classifyBlob(size, head); why != "" {
			rejects = append(rejects, fmt.Sprintf("  %s — %s", path, why))
		}
	}
	if len(rejects) > 0 {
		fmt.Fprintln(os.Stderr, "❌ pre-commit: refusing to commit binary or oversized files (this repo is source-only):")
		fmt.Fprintln(os.Stderr, strings.Join(rejects, "\n"))
		fmt.Fprintln(os.Stderr, "Remove the file(s) or unstage them, e.g.:\n  git reset HEAD <path>")
		return fmt.Errorf("%d binary/oversized staged file(s)", len(rejects))
	}
	return nil
}

// classifyBlob returns a human-readable rejection reason for a staged blob, or
// "" if it is acceptable source. size is the blob's full byte count; head is
// its leading bytes (already capped to binaryScanPrefix by the caller, and left
// nil for blobs already over the size limit, which are rejected on size alone).
func classifyBlob(size int64, head []byte) string {
	if size > maxBlobBytes {
		return fmt.Sprintf("%d bytes exceeds the %d KiB limit", size, maxBlobBytes/1024)
	}
	if i := bytes.IndexByte(head, 0); i >= 0 {
		return fmt.Sprintf("NUL byte at offset %d — binary, not source text", i)
	}
	return ""
}
`

const guardGo = `package common

import (
	"encoding/json"
	"path/filepath"
	"strings"
)

// HookInput is the subset of the Claude Code PreToolUse payload the guard reads.
type HookInput struct {
	ToolName  string          ` + "`json:\"tool_name\"`" + `
	ToolInput json.RawMessage ` + "`json:\"tool_input\"`" + `
}

// GuardDecision is the guard's verdict: Allowed==true means proceed, otherwise
// Reason explains the denial (fed back to the agent).
type GuardDecision struct {
	Allowed bool
	Reason  string
}

// Guard decides whether a tool call may proceed. Two layers:
//
//  1. Dangerous commands (rm -rf, git reset --hard, force-push) are blocked in
//     every state — the "crazy things" that must never run unattended.
//  2. Staleness lockdown: when the tools are out of sync with their source, the
//     agent is funnelled toward recovery. Allowed are ` + "`./make`" + ` (rebuild),
//     running the already-built bin/ tools (the old binaries — safe, and each
//     self-checks via CheckStale), and editing the tool source (tools/build/ or
//     the make trampolines, to fix a broken build). Everything else is blocked
//     until ` + "`./make`" + ` succeeds. Read/search tools are not matched by the hook,
//     so the agent can still inspect the broken tools while locked down.
//
// The guard never gates itself via CheckStale: a stale guard must keep running
// to enforce the lockdown and permit recovery. It reads its own freshness with
// StaleReason and switches policy. The escape is always open — ` + "`./make`" + ` runs
// via 'go run' and works no matter how stale the compiled tools are — so this
// funnels the agent to recovery without ever trapping it.
func Guard(repoRoot, compiledHash string, in HookInput) GuardDecision {
	if in.ToolName == "Bash" {
		if reason := dangerReason(bashCommand(in.ToolInput)); reason != "" {
			return GuardDecision{Reason: reason}
		}
	}
	if StaleReason(repoRoot, compiledHash) == "" {
		return GuardDecision{Allowed: true} // tools current — nothing more to enforce
	}
	switch in.ToolName {
	case "Bash":
		cmd := bashCommand(in.ToolInput)
		if isRecoveryCommand(cmd) || isToolCommand(repoRoot, cmd) {
			return GuardDecision{Allowed: true}
		}
		return GuardDecision{Reason: lockMsg("only " + MakeCmd() + " and the existing bin/ tools may run")}
	case "Edit", "Write", "NotebookEdit":
		if isRecoveryPath(repoRoot, editPath(in.ToolInput)) {
			return GuardDecision{Allowed: true}
		}
		return GuardDecision{Reason: lockMsg("only edits under tools/build/ (to fix the tools) are allowed")}
	default:
		return GuardDecision{Reason: lockMsg("this action is blocked until the tools are rebuilt")}
	}
}

func lockMsg(detail string) string {
	return "tools are out of sync — " + detail + ". Run " + MakeCmd() + " to recover."
}

func bashCommand(raw json.RawMessage) string {
	var ti struct {
		Command string ` + "`json:\"command\"`" + `
	}
	_ = json.Unmarshal(raw, &ti)
	return ti.Command
}

func editPath(raw json.RawMessage) string {
	var ti struct {
		FilePath     string ` + "`json:\"file_path\"`" + `
		NotebookPath string ` + "`json:\"notebook_path\"`" + `
	}
	_ = json.Unmarshal(raw, &ti)
	if ti.FilePath != "" {
		return ti.FilePath
	}
	return ti.NotebookPath
}

// hasShellControl reports whether a command contains chaining or redirection,
// which would let an allowed prefix smuggle in other commands.
func hasShellControl(cmd string) bool {
	return strings.ContainsAny(cmd, ";&|<>` + "`" + `\n") || strings.Contains(cmd, "$(")
}

// dangerReason blocks a small set of destructive commands in every state. This
// is a starter list — extend it for your project (mass deletes, force pushes,
// history rewrites, credential exfiltration, …).
func dangerReason(cmd string) string {
	c := strings.ToLower(cmd)
	switch {
	case strings.Contains(c, "rm -rf"), strings.Contains(c, "rm -fr"),
		strings.Contains(c, "rm -r -f"), strings.Contains(c, "rm -f -r"):
		return "blocked: recursive force-delete (rm -rf) is not allowed"
	case strings.Contains(c, "git reset --hard"):
		return "blocked: git reset --hard discards work and is not allowed"
	case strings.Contains(c, "git push") && (strings.Contains(c, "--force") || strings.Contains(c, " -f")):
		return "blocked: force-push is not allowed"
	}
	return ""
}

// isRecoveryCommand reports whether a Bash command is a bare ./make invocation —
// the one command that rebuilds the tools. The meta-builder runs via 'go run',
// so it works regardless of how stale the compiled tools are. Shell chaining or
// redirection disqualifies it, so 'make' cannot be a trojan for other commands.
func isRecoveryCommand(cmd string) bool {
	c := strings.TrimSpace(cmd)
	if c == "" || hasShellControl(c) {
		return false
	}
	fields := strings.Fields(c)
	tok := fields[0]
	if (tok == "bash" || tok == "sh") && len(fields) > 1 {
		tok = fields[1]
	}
	switch tok {
	case "./make", "make", ".\\make.cmd":
		return true
	}
	return strings.HasPrefix(c, "go run") && strings.Contains(c, "cmd/make")
}

// isToolCommand reports whether a Bash command invokes one of the already-built
// tools under bin/. Those stay runnable while stale — each self-checks via
// CheckStale and points at ./make if it is itself out of date — so the lockdown
// blocks novel work, not the use of the last-known-good tools.
func isToolCommand(repoRoot, cmd string) bool {
	c := strings.TrimSpace(cmd)
	if c == "" || hasShellControl(c) {
		return false
	}
	first := strings.Fields(c)[0]
	if strings.HasPrefix(strings.TrimPrefix(first, "./"), "bin/") {
		return true
	}
	binDir := filepath.Clean(filepath.Join(repoRoot, "bin")) + string(filepath.Separator)
	return strings.HasPrefix(filepath.Clean(first), binDir)
}

// isRecoveryPath reports whether an Edit/Write target is part of the tooling a
// developer must change to fix a broken build: anything under tools/build/, or
// the make trampolines at the repo root.
func isRecoveryPath(repoRoot, path string) bool {
	if path == "" {
		return false
	}
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(repoRoot, path)
	}
	abs = filepath.Clean(abs)
	toolsDir := filepath.Clean(filepath.Join(repoRoot, "tools", "build"))
	if abs == toolsDir || strings.HasPrefix(abs, toolsDir+string(filepath.Separator)) {
		return true
	}
	base := filepath.Base(abs)
	return (base == "make" || base == "make.cmd") && filepath.Dir(abs) == filepath.Clean(repoRoot)
}
`

const guardTestGo = `package common

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func bashInput(cmd string) HookInput {
	b, _ := json.Marshal(map[string]string{"command": cmd})
	return HookInput{ToolName: "Bash", ToolInput: b}
}

func editInput(path string) HookInput {
	b, _ := json.Marshal(map[string]string{"file_path": path})
	return HookInput{ToolName: "Edit", ToolInput: b}
}

// testRepoRoot derives the repo root from the package dir (<root>/tools/build/common).
func testRepoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Clean(filepath.Join(wd, "..", "..", ".."))
}

func TestDangerReason(t *testing.T) {
	for _, c := range []string{
		"rm -rf build", "RM -RF /", "foo && rm -fr bar",
		"git reset --hard HEAD~1", "git push --force", "git push -f origin main",
	} {
		if dangerReason(c) == "" {
			t.Errorf("expected %q to be blocked", c)
		}
	}
	for _, c := range []string{"ls -la", "go build ./...", "rm file.txt", "git push origin main"} {
		if r := dangerReason(c); r != "" {
			t.Errorf("expected %q allowed, got %q", c, r)
		}
	}
}

func TestIsRecoveryCommand(t *testing.T) {
	for _, c := range []string{"./make", "make", "./make -force", "go run -C tools/build ./cmd/make"} {
		if !isRecoveryCommand(c) {
			t.Errorf("expected recovery: %q", c)
		}
	}
	for _, c := range []string{"", "ls", "./make && rm -rf /", "echo $(./make)", "./maker"} {
		if isRecoveryCommand(c) {
			t.Errorf("expected not-recovery: %q", c)
		}
	}
}

func TestIsToolCommand(t *testing.T) {
	const root = "/repo"
	for _, c := range []string{"bin/verify", "./bin/test --fast", "/repo/bin/build"} {
		if !isToolCommand(root, c) {
			t.Errorf("expected tool cmd: %q", c)
		}
	}
	for _, c := range []string{"binx/foo", "bin/verify && rm -rf /", "ls bin/"} {
		if isToolCommand(root, c) {
			t.Errorf("expected not tool cmd: %q", c)
		}
	}
}

func TestIsRecoveryPath(t *testing.T) {
	const root = "/repo"
	for _, p := range []string{"tools/build/common/verify.go", "/repo/tools/build/go.mod", "make", "make.cmd"} {
		if !isRecoveryPath(root, p) {
			t.Errorf("expected recovery path: %q", p)
		}
	}
	for _, p := range []string{"", "src/app.go", "/repo/README.md", "tools/other/x.go"} {
		if isRecoveryPath(root, p) {
			t.Errorf("expected not recovery path: %q", p)
		}
	}
}

// When stale (empty compiledHash ⇒ StaleReason non-empty), only the recovery
// surface is allowed and dangerous commands are blocked regardless.
func TestGuardLockdown(t *testing.T) {
	const root = "/repo"
	cases := []struct {
		name    string
		in      HookInput
		allowed bool
	}{
		{"make allowed", bashInput("./make"), true},
		{"old bin tool allowed", bashInput("bin/verify"), true},
		{"arbitrary bash blocked", bashInput("ls"), false},
		{"dangerous blocked", bashInput("rm -rf x"), false},
		{"tools edit allowed", editInput("/repo/tools/build/common/x.go"), true},
		{"project edit blocked", editInput("/repo/src/app.go"), false},
	}
	for _, c := range cases {
		if got := Guard(root, "", c.in); got.Allowed != c.allowed {
			t.Errorf("%s: allowed=%v want %v (reason=%q)", c.name, got.Allowed, c.allowed, got.Reason)
		}
	}
}

// When current (compiledHash matches the real source hash) safe actions are
// allowed and only dangerous commands are blocked.
func TestGuardCurrentAllows(t *testing.T) {
	root := testRepoRoot(t)
	hash, err := ToolsSourceHash(root)
	if err != nil {
		t.Skipf("cannot hash tools source from %s: %v", root, err)
	}
	if d := Guard(root, hash, bashInput("ls -la")); !d.Allowed {
		t.Errorf("safe command should be allowed when current: %q", d.Reason)
	}
	if d := Guard(root, hash, editInput(filepath.Join(root, "src/app.go"))); !d.Allowed {
		t.Errorf("project edit should be allowed when current: %q", d.Reason)
	}
	if d := Guard(root, hash, bashInput("rm -rf /")); d.Allowed {
		t.Error("dangerous command must be blocked even when current")
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

const cmdPrecommitGo = `package main

import (
	"os"

	"__MODULE__/common"
)

var (
	repoRoot   = ""
	sourceHash = ""
)

func main() {
	// CheckStale first: the commit gate is only meaningful if it runs the
	// current logic. If the tools are out of sync, this refuses the commit and
	// points at ./make — you must rebuild (and therefore fix any broken tool)
	// before committing. That is what keeps a broken bin/precommit from ever
	// being committed into an unrecoverable state. Recovery is ./make (ungated,
	// 'go run'), never a commit, so blocking here is a speed bump, not a trap.
	common.CheckStale(repoRoot, sourceHash)
	if err := common.RunPrecommit(repoRoot); err != nil {
		os.Exit(1)
	}
}
`

const cmdGuardGo = `package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"__MODULE__/common"
)

// Injected by the meta-builder via -ldflags. The guard reads these to detect
// its own staleness — but, unlike pipeline tools, it never calls CheckStale: a
// stale guard must keep running to enforce the lockdown and permit recovery.
var (
	repoRoot   = ""
	sourceHash = ""
)

func main() {
	// Fail open on any input trouble: a guard that can't read its stdin must
	// not wedge the agent. Real policy lives in common.Guard.
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		os.Exit(0)
	}
	var in common.HookInput
	if err := json.Unmarshal(data, &in); err != nil {
		os.Exit(0)
	}
	decision := common.Guard(repoRoot, sourceHash, in)
	if decision.Allowed {
		os.Exit(0)
	}
	fmt.Fprintln(os.Stderr, decision.Reason)
	os.Exit(2) // exit 2 = block the tool call; stderr is fed back to the agent.
}
`

// ───────────────────────── claude config ─────────────────────────

// settingsJSON wires bin/guard as a PreToolUse hook. The command is the bare
// binary path — NOT "... || exit 2". A missing/crashed guard exits non-2, which
// Claude Code treats as non-blocking, so the tool proceeds. That is deliberate:
// fail-closed here would block the ./make that builds the guard on a fresh
// clone — the exact one-way door this design avoids. The guard enforces the
// lockdown only when it actually runs and detects staleness.
const settingsJSON = `{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash|Edit|Write|NotebookEdit",
        "hooks": [
          {
            "type": "command",
            "command": "\"$CLAUDE_PROJECT_DIR/bin/guard\""
          }
        ]
      }
    ]
  }
}
`
