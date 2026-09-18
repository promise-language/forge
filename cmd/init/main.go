// Command init scaffolds the Forge dev-tooling layout into a target repository.
//
// Usage:
//
//	go run github.com/promise-language/forge/cmd/init@latest [target-dir] [-force]
//
// It lays down a runnable tooling tree:
//
//   - ./make, ./make.cmd                  bootstrap trampolines (committed shell text)
//   - tools/build/go.mod, go.sum          the tools module, pinning forge at one version
//   - tools/build/common/                 what is the project's: the verify pipeline,
//     the gate set and the judging terms
//   - tools/build/cmd/make/main.go        the meta-builder (runs via `go run`)
//   - tools/build/cmd/<tool>/main.go      one binary per dir: verify, setup, gate, run
//   - tools/gates/                        the judging terms: thresholds and baselines
//   - docs/index.md                       the map of docs/
//   - .claude/settings.json               wires bin/tool-guard on both tool-use events
//   - .gitignore                          adds every per-clone path (bin/, .workspace/, …)
//   - CLAUDE.md                           appends a "Dev tooling" section so agents
//     discover the ./make → bin/verify workflow
//
// It copies no helper primitives carries: the hash, the staleness refusal, OS
// detection, exec, the invocation surface and the hook wiring arrive as the one
// pinned dependency (docs/primitives.md, One implementation and The dependency
// is pinned).
//
// It emits no twin of a tool the workspace is accountable for, and no wiring
// that reaches one it does not own: `.claude/settings.json` names bin/tool-guard
// because a fresh clone must carry it, while `.githooks/pre-commit` is written
// by `workspace setup` alongside the bin/precommit-guard it names — one party
// owns the guard and the wiring that reaches it (docs/blueprint.md, Tools the
// project does not build and The commit gate hook).
//
// After init exits, the target repo owns every line it wrote. What it does not
// own is the machinery, which arrives at the pinned version its go.mod names.
//
// The chain has no compile-the-compiler cycle: ./make is shell text, the
// meta-builder runs via `go run` (needing only the Go toolchain), and it
// compiles every other tool into bin/ — stamping each with the tools-source
// hash and the absolute repo root via -ldflags. See docs/blueprint.md.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/promise-language/forge/primitives/command"
)

type file struct {
	path string // relative to the target dir
	body string // placeholders (see writeFile) are substituted before writing
	exec bool   // chmod +x after writing
}

// result is what init answers: where it scaffolded, the tools module it chose,
// and what became of every file it laid down.
type result struct {
	Target string    `json:"target"`
	Module string    `json:"module"`
	Files  []written `json:"files"`

	// notGit records that the target is not a git checkout yet, which changes
	// what a person has to do next and nothing about what was written.
	notGit bool
}

// written is one path and what init did with it.
type written struct {
	Path   string `json:"path"`
	Action string `json:"action"`
	Detail string `json:"detail,omitempty"`
}

// The three things init does to a path.
const (
	actionCreate = "create"
	actionUpdate = "update"
	actionSkip   = "skip"
)

// Human is the scaffolder's report, and then what to do next — which is the
// whole reason a person runs this by hand.
func (r result) Human(w io.Writer) error {
	var b strings.Builder
	for _, f := range r.Files {
		fmt.Fprintf(&b, "  %-6s %s", f.Action, f.Path)
		if f.Detail != "" {
			fmt.Fprintf(&b, " (%s)", f.Detail)
		}
		b.WriteByte('\n')
	}
	if r.notGit {
		b.WriteString("\nnote: this is not a git repository yet.\n")
		b.WriteString("      run 'git init' so ./make can point git at .githooks.\n")
	}
	b.WriteString(`
Done. Next steps:
  ./make        # compiles bin/{verify,setup,gate,run}
  bin/verify    # runs the commit gate
  bin/gate --list   # the measurements this project answers
  bin/run fit       # measure one gate and judge what it measured

This project's gate is bin/verify, and nothing runs it on 'git commit' until
provisioning has been here: 'workspace setup' installs bin/precommit-guard and
bin/tool-guard, and writes the .githooks/pre-commit that reaches the first of
them. The committed .claude/settings.json names bin/tool-guard, so a fresh
clone carries that wiring; neither binary is built here.

Then edit tools/build/common/verify.go to run your project's real
format / build / test commands, tools/build/common/gate.go for what this
project measures, and tools/gates/thresholds.json for the caps a verdict
rests on. Drop a new dir under tools/build/cmd/ and re-run ./make to get
another bin/<tool> — no registration needed.

The build workflow is written to CLAUDE.md so agents discover it.
`)
	_, err := io.WriteString(w, b.String())
	return err
}

// define is init's whole surface. It carries no version — it is run as
// `go run github.com/promise-language/forge/cmd/init@latest`, and the build
// records none — and no Fit: it is not a stamped tool, so there is no source it
// could have fallen behind.
func define() command.Tool {
	return command.Tool{
		Project: "init",
		Root: command.Command{
			Name:    "init",
			Summary: "scaffold the forge dev-tooling layout into a repository",
			Flags: []command.Flag{{
				Name:        "force",
				Type:        command.Boolean,
				Description: "overwrite a file that is already there",
			}},
			Params: []command.Param{{
				Name:        "target",
				Type:        command.Path,
				Arity:       command.Optional,
				Description: "the repository to scaffold into (default: the working directory)",
			}},
			Action: writeLayout,
		},
	}
}

// writeLayout lays the layout down and reports what it did.
func writeLayout(c *command.Call) (command.Result, error) {
	absTarget := c.Arg("target")
	if absTarget == "" {
		absTarget = c.Dir
	}
	mod := toolsModule(absTarget)

	fmt.Fprintf(c.Narrate, "forge/init: scaffolding into %s\n", absTarget)
	fmt.Fprintf(c.Narrate, "forge/init: tools module = %s\n", mod)

	answer := result{Target: absTarget, Module: mod, notGit: !exists(filepath.Join(absTarget, ".git"))}
	for _, f := range files() {
		done, err := writeFile(absTarget, f, mod, c.Bool("force"))
		if err != nil {
			return nil, err
		}
		answer.Files = append(answer.Files, done)
	}
	ignored, err := ensureGitignore(absTarget)
	if err != nil {
		return nil, err
	}
	answer.Files = append(answer.Files, ignored)

	documented, err := ensureBuildDoc(absTarget)
	if err != nil {
		return nil, err
	}
	answer.Files = append(answer.Files, documented)
	return answer, nil
}

func main() {
	os.Exit(command.Run(define(), os.Args[1:], command.Stdio()))
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

// writeFile lays one file down, substituting the two placeholders an emitted
// body may carry: __MODULE__, the tools module path this target gets, and
// __FORGE_VERSION__, the version its go.mod pins forge at. The version is
// substituted rather than written into each body so go.mod and go.sum cannot
// name different ones — see forgeVersion.
func writeFile(absTarget string, f file, mod string, force bool) (written, error) {
	dst := filepath.Join(absTarget, f.path)
	if exists(dst) && !force {
		return written{Path: f.path, Action: actionSkip, Detail: "exists"}, nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return written{}, err
	}
	body := strings.ReplaceAll(f.body, "__MODULE__", mod)
	body = strings.ReplaceAll(body, "__FORGE_VERSION__", forgeVersion)
	if err := os.WriteFile(dst, []byte(body), 0o644); err != nil {
		return written{}, err
	}
	if f.exec {
		if err := os.Chmod(dst, 0o755); err != nil {
			return written{}, err
		}
	}
	return written{Path: f.path, Action: actionCreate}, nil
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
func ensureGitignore(absTarget string) (written, error) {
	path := filepath.Join(absTarget, ".gitignore")
	existing, _ := os.ReadFile(path)
	missing := missingIgnoreRules(string(existing))
	if len(missing) == 0 {
		return written{Path: ".gitignore", Action: actionSkip, Detail: "every per-clone path is already ignored"}, nil
	}
	block := "\n# forge dev tooling — written per clone, never committed\n" +
		strings.Join(missing, "\n") + "\n"
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return written{}, err
	}
	defer f.Close()
	if _, err := f.WriteString(block); err != nil {
		return written{}, err
	}
	return written{Path: ".gitignore", Action: actionUpdate, Detail: "+" + strings.Join(missing, ", ")}, nil
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
func ensureBuildDoc(absTarget string) (written, error) {
	path := filepath.Join(absTarget, "CLAUDE.md")
	existing, _ := os.ReadFile(path)
	if strings.Contains(string(existing), buildDocMarker) {
		return written{Path: "CLAUDE.md", Action: actionSkip, Detail: "dev tooling section already present"}, nil
	}
	body := buildDocBlock
	action := actionCreate
	if len(existing) > 0 {
		body = "\n" + body // separate from prior content
		action = actionUpdate
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return written{}, err
	}
	defer f.Close()
	if _, err := f.WriteString(body); err != nil {
		return written{}, err
	}
	return written{Path: "CLAUDE.md", Action: action, Detail: "dev tooling section"}, nil
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func files() []file {
	return []file{
		{path: "make", body: makeSh, exec: true},
		{path: "make.cmd", body: makeCmd},
		{path: "tools/build/go.mod", body: goMod},
		{path: "tools/build/go.sum", body: goSum},
		{path: "tools/build/common/commands.go", body: commandsGo},
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
The module pins §github.com/promise-language/forge§ at one exact version, which
is where the harness under these tools comes from; nothing here is a copy of it.

**Fresh clone — bootstrap once:**

§§§bash
./make            # Windows: .\make.cmd
§§§

§./make§ compiles every tool into §bin/§ and points git at §.githooks§.
It is idempotent and finishes in well under a second once built.

**Before every commit — run the gate:**

§§§bash
bin/verify        # format → vet → build → test → record, then a pass/FAIL summary
§§§

A green "OK to Commit" line means it is safe to commit; a red FAIL means it is
not. Its last step records the tree it blessed at §.workspace/verified-tree§,
which is what the commit gate (§bin/precommit-guard§, installed by the workspace
rather than built here) reads to refuse a commit of any other tree. Until
provisioning has installed that guard and written §.githooks/pre-commit§,
nothing runs on §git commit§ — §bin/verify§ is still the gate, run by hand.

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

// ───────────────────────── tools module ─────────────────────────

// forgeVersion is the version of forge a scaffolded project pins, and it is
// written down once: both go.mod and go.sum carry __FORGE_VERSION__ and
// writeFile substitutes it, so the two files cannot name different versions and
// a half-finished bump does not reach an adopter.
//
// It names an ALREADY PUBLISHED commit, and necessarily so: a pseudo-version
// does not exist until the commit it names does, so the pin can never be the
// change that raises it. Raising it is an edit here — the deliberate, central
// act docs/primitives.md, The dependency is pinned describes — together with
// the two sums below, which are facts about the published module and cannot be
// derived from the version string.
const forgeVersion = "v0.0.0-20260918010348-db3e0f44c28f"

// The tools module declares one Go version, the same in every project that
// adopts this layout. It is not derived from the target and not a floor the
// target may raise: a version that varies per project is a variation in the
// tools themselves, and the tools exist to not vary. Raising it is an edit
// here, which every project then gets by adopting it.
//
// The single require is the whole of what a project's tooling depends on. The
// module is otherwise an island by design, and that is unchanged by there being
// exactly one entry in it (docs/primitives.md, The dependency is pinned).
const goMod = `module __MODULE__

go 1.26

require github.com/promise-language/forge __FORGE_VERSION__
`

// goSum is the checksum half of the pin. It is emitted rather than left for the
// adopter to generate because `go build` refuses a module whose requirements
// have no go.sum entry, so a tree without it does not bootstrap: the first
// ./make would fail before it compiled anything.
//
// A published version is immutable and its content is verified against these
// two lines, which is what makes the pin as fixed as a vendored copy was
// (docs/primitives.md, The dependency is pinned).
const goSum = `github.com/promise-language/forge __FORGE_VERSION__ h1:ngVvT/okx86hdw3ra5zM4WfHB9ON6/UMOnmw3Dk7390=
github.com/promise-language/forge __FORGE_VERSION__/go.mod h1:8tJTV2+mUBiMoK9NioLoXsJGu0y5eKVp9OWmCg3/hk0=
`

// commandsGo answers what this project builds, by looking rather than by
// reading a list someone maintained. It is one function because two callers ask
// it — the meta-builder, to know what to compile, and `run --list`, to say what
// this project offers — and a second copy would be a list to keep in step with
// the first.
const commandsGo = `package common

import (
	"os"
	"path/filepath"
	"sort"
)

// CommandNames returns every command this project builds into bin/, sorted.
//
// The meta-builder compiles one command per directory under tools/build/cmd, so
// those directories already are the registry. make is not among them: it runs
// from source via the ./make trampoline and is never compiled into bin/, so a
// caller asking what this project puts in bin/ must not be told about a binary
// that is never there.
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
`

// verifyGo is the project's own verify pipeline — what fails the membership
// test in docs/primitives.md, What belongs here, because only the project knows
// how it builds and tests itself. What RUNS the steps is the library's.
//
// It is authored with § standing in for the backtick, as buildDocRaw is; the Go
// raw string literal holding it cannot contain one. Nothing inside it may use §
// for anything else — a section sign would be written out as a backtick.
var verifyGo = substituteBackticks(verifyGoRaw)

const verifyGoRaw = `package common

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/promise-language/forge/primitives"
)

// The three things a step can have been, as the summary and the JSON both name
// them.
const (
	statusPassed = "passed"
	statusFailed = "failed"
	statusNotRun = "not-run"
)

type step struct {
	name string
	run  func(repoRoot string, narrate io.Writer) error
}

// VerifyResult is what verify answers. It is a result like any other: the
// command library writes it, in the mode the invocation selected, and reads the
// status off it. Nothing here reaches stdout, because stdout carries the result
// and nothing else.
type VerifyResult struct {
	OK     bool          §json:"ok"§
	Stages []VerifyStage §json:"stages"§
	// Tree is the id of the tree this run blessed, absent when nothing was
	// recorded.
	Tree string §json:"tree,omitempty"§
}

// VerifyStage is one stage of the run. This pipeline stops at the first
// failure, so each step is a stage of its own.
type VerifyStage struct {
	Name  string       §json:"name"§
	Steps []VerifyStep §json:"steps"§
}

// VerifyStep is one step, what became of it, and how long it took.
type VerifyStep struct {
	Name           string  §json:"name"§
	Status         string  §json:"status"§
	ElapsedSeconds float64 §json:"elapsed_seconds"§
	Detail         string  §json:"detail,omitempty"§
}

// ExitStatus is 0 when every stage passed and 1 when one did not. The result is
// written either way: the caller asked whether this tree may be committed, and
// a no is an answer.
func (r VerifyResult) ExitStatus() int {
	if r.OK {
		return 0
	}
	return 1
}

// Human is the summary, and it always prints — pass or fail — so that whoever
// is tailing the output sees the result without re-running.
func (r VerifyResult) Human(w io.Writer) error {
	var b strings.Builder
	b.WriteString("\n──────── verify summary ────────\n")
	var elapsed time.Duration
	for _, stage := range r.Stages {
		for _, s := range stage.Steps {
			elapsed += time.Duration(s.ElapsedSeconds * float64(time.Second))
			fmt.Fprintf(&b, "  %-7s  %s\n", label(s.Status), s.Name)
			if s.Detail != "" {
				fmt.Fprintf(&b, "           %s\n", s.Detail)
			}
		}
	}
	fmt.Fprintf(&b, "  elapsed %s\n", elapsed.Round(time.Millisecond))
	b.WriteString("────────────────────────────────\n")
	if r.OK {
		b.WriteString("✅ OK to Commit\n")
	} else {
		b.WriteString("❌ Verify FAILED: not safe to commit\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// label is how a status reads in the summary.
func label(status string) string {
	switch status {
	case statusPassed:
		return "ok"
	case statusFailed:
		return "FAIL"
	}
	return statusNotRun
}

// RunVerify is the commit gate: format → vet → build → test → record.
//
// The trailing record step is the writing end of the verified-tree contract
// (verifiedtree.go): the status says the tree is sound, and the record says
// which tree that was, so the commit gate can refuse a commit of any other one.
//
// It writes nothing to stdout. Progress goes to narrate, and what the run
// became is the result it returns.
//
// This is an EXAMPLE pipeline. For a Go project it runs real go tooling; for
// anything else it runs harmless stubs. Replace verifySteps with your project's
// real commands.
func RunVerify(repoRoot string, narrate io.Writer) (VerifyResult, error) {
	// A stale blessing left behind is the one outcome the verified-tree check
	// must never produce, so failing to clear fails the run outright.
	if err := clearVerifiedTree(repoRoot); err != nil {
		return VerifyResult{}, fmt.Errorf("clearing %s: %w", primitives.VerifiedTreeRecord, err)
	}
	var tree string
	result := runVerifySteps(repoRoot, verifyPipeline(repoRoot, &tree), narrate)
	result.Tree = tree
	return result, nil
}

// runVerifySteps runs the steps in order, stopping at the first failure. Every
// step is reported, the ones after a failure as not run.
func runVerifySteps(repoRoot string, steps []step, narrate io.Writer) VerifyResult {
	result := VerifyResult{OK: true}
	for i, s := range steps {
		if !result.OK {
			result.Stages = append(result.Stages, VerifyStage{
				Name:  s.name,
				Steps: []VerifyStep{{Name: s.name, Status: statusNotRun}},
			})
			continue
		}
		fmt.Fprintf(narrate, "==> %s\n", s.name)
		start := time.Now()
		err := steps[i].run(repoRoot, narrate)
		reported := VerifyStep{
			Name:           s.name,
			Status:         statusPassed,
			ElapsedSeconds: time.Since(start).Seconds(),
		}
		if err != nil {
			result.OK = false
			reported.Status = statusFailed
			reported.Detail = err.Error()
			fmt.Fprintf(narrate, "    %s failed: %v\n", s.name, err)
		}
		result.Stages = append(result.Stages, VerifyStage{Name: s.name, Steps: []VerifyStep{reported}})
	}
	return result
}

// verifyPipeline is the full run: the project's steps, then the unconditional
// trailing record step. Appended here rather than inside verifySteps so it is
// last on the Go and stub pipelines alike, and being a step gets the
// break-on-first-failure for free — a red step leaves nothing blessed.
func verifyPipeline(repoRoot string, tree *string) []step {
	record := step{"record", func(root string, narrate io.Writer) error {
		recorded, err := recordVerifiedTree(root, narrate)
		*tree = recorded
		return err
	}}
	return append(verifySteps(repoRoot), record)
}

func verifySteps(repoRoot string) []step {
	if primitives.Exists(filepath.Join(repoRoot, "go.mod")) {
		return []step{
			{"format", func(r string, n io.Writer) error { return primitives.RunInStreams(r, n, n, "gofmt", "-w", ".") }},
			{"vet", func(r string, n io.Writer) error { return primitives.RunInStreams(r, n, n, "go", "vet", "./...") }},
			{"build", func(r string, n io.Writer) error { return primitives.RunInStreams(r, n, n, "go", "build", "./...") }},
			{"test", func(r string, n io.Writer) error { return primitives.RunInStreams(r, n, n, "go", "test", "./...") }},
		}
	}
	stub := func(label string) step {
		return step{label, func(r string, n io.Writer) error {
			fmt.Fprintf(n, "    (stub) wire up your %s command in tools/build/common/verify.go\n", label)
			return nil
		}}
	}
	return []step{stub("format"), stub("vet"), stub("build"), stub("test")}
}
`

// verifiedTreeGo is the writing end of the verified-tree contract. It is
// authored with § standing in for the backtick, as buildDocRaw is; the Go raw
// string literal holding it cannot contain one. Nothing inside it may use § for
// anything else — a section sign would be written out as a backtick.
var verifiedTreeGo = substituteBackticks(verifiedTreeGoRaw)

const verifiedTreeGoRaw = `package common

// This file is the writing end of the verified-tree contract: bin/verify
// records the tree it blessed at primitives.VerifiedTreeRecord, and the
// workspace-delivered bin/precommit-guard refuses a commit whose staged tree
// differs.
//
// The reading end is not in this repository. bin/precommit-guard is a workspace
// tool, built and owned there, and this module cannot import it. What the two
// ends share is the record's location and format, not code: one git tree object
// id, newline terminated, at that path. The path is imported rather than typed
// here, because a path typed at each end agrees only by coincidence and
// spelling it wrong is a permanent, silent refusal — verify writes one path,
// the guard reads another and always finds it absent.

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/promise-language/forge/primitives"
)

// recordPath is where the record lives in the checkout at repoRoot. Everything
// this file touches is derived from it — including the directory it is created
// in — so the temp file and the name it is renamed to cannot end up in
// different places, which is what would make the rename non-atomic.
func recordPath(repoRoot string) string {
	return filepath.Join(repoRoot, filepath.FromSlash(primitives.VerifiedTreeRecord))
}

// clearVerifiedTree removes the record. Verify calls it before its first step
// so a run that dies mid-way leaves nothing blessed and an in-flight verify
// blesses nothing. An absent record is not an error.
func clearVerifiedTree(repoRoot string) error {
	err := os.Remove(recordPath(repoRoot))
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
func recordVerifiedTree(repoRoot string, narrate io.Writer) (string, error) {
	if _, err := gitWithIndex(repoRoot, "", "rev-parse", "--git-dir"); err != nil {
		fmt.Fprintln(narrate, "    not a git checkout — no verified-tree record to write")
		return "", nil
	}

	tmpDir, err := os.MkdirTemp("", "verified-tree-")
	if err != nil {
		return "", fmt.Errorf("creating temp index dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	index := filepath.Join(tmpDir, "index")

	// Seed from a copy of the real index; a repo before its first add has no
	// index file yet, and an empty seed is exactly its tracked set.
	realIndex, err := gitWithIndex(repoRoot, "", "rev-parse", "--git-path", "index")
	if err != nil {
		return "", fmt.Errorf("locating the index: %w", err)
	}
	if !filepath.IsAbs(realIndex) {
		realIndex = filepath.Join(repoRoot, realIndex)
	}
	if data, err := os.ReadFile(realIndex); err == nil {
		if err := os.WriteFile(index, data, 0o600); err != nil {
			return "", fmt.Errorf("seeding temp index: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("seeding temp index: %w", err)
	}
	if _, err := gitWithIndex(repoRoot, index, "add", "-A"); err != nil {
		return "", fmt.Errorf("staging into temp index: %w", err)
	}
	tree, err := gitWithIndex(repoRoot, index, "write-tree")
	if err != nil {
		return "", fmt.Errorf("computing verified tree: %w", err)
	}

	record := recordPath(repoRoot)
	dir := filepath.Dir(record)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creating %s: %w", dir, err)
	}
	// Atomic: temp file + rename, so no reader ever sees a half-written record.
	// The temp file is made in the record's own directory, since a rename is
	// only atomic within one filesystem.
	tmp, err := os.CreateTemp(dir, ".verified-tree-*")
	if err != nil {
		return "", err
	}
	if _, err := tmp.WriteString(tree + "\n"); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	if err := os.Rename(tmp.Name(), record); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	return tree, nil
}

// gitWithIndex runs git in dir, with GIT_INDEX_FILE pointed at indexFile when
// one is given, and returns trimmed stdout. primitives.RunOutputIn cannot be
// used: it carries no environment, which is the one thing this needs. Stderr is
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
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/promise-language/forge/primitives"
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

// Measured is one gate's envelope as the result the command library writes.
// This type exists only to carry the envelope out of the action without the
// action reaching stdout itself.
type Measured Envelope

// Human is not reached: --envelope's stdout belongs to the gate contract, so
// the command has one mode and the library never asks for a rendering. Saying
// so loudly is what keeps a future caller from inventing a second shape for a
// wire another document owns.
func (m Measured) Human(io.Writer) error {
	return fmt.Errorf("an envelope's shape is the gate contract's, and it has no rendering for a person")
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
	if primitives.Exists(filepath.Join(repoRoot, "go.mod")) {
		dirs = append(dirs, repoRoot)
	}
	tools := filepath.Join(repoRoot, "tools", "build")
	if primitives.Exists(filepath.Join(tools, "go.mod")) {
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
		if primitives.Which(cmd) == "" {
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
	"strconv"
	"strings"

	"github.com/promise-language/forge/primitives"
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
func RunOneGate(repoRoot, gateBin, name string, narrate io.Writer) (Judged, error) {
	if !KnownGate(name) {
		return Judged{}, unknownGate(name)
	}

	cmd := exec.Command(gateBin, name, "--envelope")
	cmd.Dir = repoRoot
	// The gate's progress goes straight to the narration — not into a buffer
	// printed afterwards. Gates run for minutes, and a gate that is working and
	// a gate that is wedged produce the same thing (nothing) for as long as the
	// output is held.
	cmd.Stderr = narrate
	out, runErr := cmd.Output()

	var env Envelope
	if jsonErr := json.Unmarshal(out, &env); jsonErr != nil {
		// No readable envelope. Whether the process died or printed something
		// that is not an envelope, nothing was measured — and either way this
		// is not a report that the tree is bad.
		if runErr != nil {
			return Judged{}, fmt.Errorf("%s did not measure anything: %w", name, runErr)
		}
		return Judged{}, fmt.Errorf("%s printed something that is not an envelope: %s",
			name, firstLine(string(out)))
	}

	manifest, err := loadManifest(repoRoot)
	if err != nil {
		return Judged{}, err
	}
	acceptable, thresholds, detail := judge(env, manifest)
	return Judged{
		Envelope: env,
		Verdict:  Verdict{Acceptable: acceptable, Thresholds: thresholds, Detail: detail},
		rendered: renderVerdict(env, manifest),
	}, nil
}

// Judged is a measurement and the verdict reached on it: what §run <gate>§
// answers. The verdict travels with the terms it was reached from, so a reader
// who was not there can re-check it.
type Judged struct {
	Envelope Envelope §json:"envelope"§
	Verdict  Verdict  §json:"verdict"§

	// rendered is the human form: each measurement beside every term it was
	// judged on, which is what someone iterating on one failure is reading.
	rendered string
}

// Human writes each measurement beside the term it was judged on.
func (j Judged) Human(w io.Writer) error {
	if _, err := io.WriteString(w, j.rendered); err != nil {
		return err
	}
	if j.Verdict.Acceptable || j.Verdict.Detail == "" {
		return nil
	}
	_, err := fmt.Fprintf(w, "%s\n", j.Verdict.Detail)
	return err
}

// ExitStatus is 0 when every capped measurement is within its cap and 1 when
// one is not. The verdict is the JSON, not the status — but a person running
// one gate reads the status, and a measurement over its cap is a condition they
// must clear.
func (j Judged) ExitStatus() int {
	if j.Verdict.Acceptable {
		return 0
	}
	return 1
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

// JudgeStdin judges an envelope this program did NOT produce, and answers one
// verdict.
//
// Reading the measurement rather than making it is the whole point of this
// mode. Whoever spawned the gate is the runner; if this entry point ran the
// gate itself, the runner would be a tree artifact, and a runner is the one
// party whose account of a vanished process nothing can check.
//
// Nothing reaches out on any error path. A caller reads one object or none — a
// half-written verdict beside an error message is a second channel, and the two
// could disagree.
func JudgeStdin(repoRoot, name string, in io.Reader) (Verdict, error) {
	if !KnownGate(name) {
		return Verdict{}, unknownGate(name)
	}
	envelope, err := io.ReadAll(in)
	if err != nil {
		return Verdict{}, fmt.Errorf("reading the envelope to judge: %w", err)
	}
	var env Envelope
	if err := json.Unmarshal(envelope, &env); err != nil {
		return Verdict{}, fmt.Errorf("what arrived on stdin is not an envelope: %w", err)
	}
	// The judge's check, and not the caller's: only this layer knows which gate
	// the terms it is about to apply belong to. Judging one gate's numbers
	// against another's caps would answer a question nobody asked.
	if env.Gate != name {
		return Verdict{}, fmt.Errorf("asked to judge %q against the terms for %q; a measurement is judged against its own gate's caps", env.Gate, name)
	}
	manifest, err := loadManifest(repoRoot)
	if err != nil {
		return Verdict{}, err
	}
	acceptable, thresholds, detail := judge(env, manifest)
	return Verdict{Acceptable: acceptable, Thresholds: thresholds, Detail: detail}, nil
}

// Verdict is what the judging layer answers in --verdict mode: one JSON object,
// whole. The library writes it — marshalled before anything reaches stdout, so
// a failure there leaves the stream untouched rather than half a verdict.
//
// Thresholds is not omitempty and is never nil. A verdict handed over with the
// terms it was reached from discarded cannot be re-checked by anyone who was not
// there, which is exactly the property that lets a judge live in the tree it
// judges.
type Verdict struct {
	Acceptable bool               §json:"acceptable"§
	Thresholds map[string]float64 §json:"thresholds"§
	Detail     string             §json:"detail,omitempty"§
}

// Human is not reached: --verdict's stdout belongs to the gate contract, so the
// command has one mode and the library never asks for a rendering.
func (v Verdict) Human(io.Writer) error {
	return fmt.Errorf("a verdict's shape is the gate contract's, and it has no rendering for a person")
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
	return filepath.Join(repoRoot, "bin", primitives.BinaryName("gate"))
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
	"io"
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

func TestJudgeStdinAnswersOneVerdictCarryingItsTerms(t *testing.T) {
	root := manifestRepo(t, capMissingPrereqs)
	env, err := json.Marshal(Envelope{Gate: "fit", Metrics: []Metric{Count("missing_prereqs", 0)}})
	if err != nil {
		t.Fatal(err)
	}
	verdict, err := JudgeStdin(root, "fit", bytes.NewReader(env))
	if err != nil {
		t.Fatalf("judging a well-formed envelope failed: %v", err)
	}
	if !verdict.Acceptable {
		t.Error("0 missing prerequisites was not acceptable")
	}
	if len(verdict.Thresholds) == 0 {
		t.Error("the verdict carries no terms, so nothing can re-check it")
	}
}

// Nothing is answered on any error path: the caller gets a verdict or an error,
// never both, because a half-formed verdict beside an error message is a second
// channel and the two could disagree.
func TestJudgeStdinAnswersNothingWhenItRefuses(t *testing.T) {
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
		verdict, err := JudgeStdin(root, c.gate, bytes.NewReader(c.stdin))
		if err == nil {
			t.Errorf("%s: expected a refusal, got none", c.name)
		}
		if verdict.Acceptable || verdict.Thresholds != nil {
			t.Errorf("%s: a refusal carried a verdict: %+v", c.name, verdict)
		}
	}
}

// A verdict's shape belongs to the gate contract, so the command has one mode
// and the library never renders it. A rendering that quietly existed would be a
// second shape for a wire another document owns.
func TestAVerdictAndAnEnvelopeHaveNoHumanForm(t *testing.T) {
	if err := (Verdict{}).Human(io.Discard); err == nil {
		t.Error("a verdict rendered itself for a person")
	}
	if err := (Measured{}).Human(io.Discard); err == nil {
		t.Error("an envelope rendered itself for a person")
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
//
// Each main is a definition and one call into the command library. What it
// parses, how it renders, which mode it writes in and what it exits with are
// not a main's to decide (docs/command-line.md, One implementation) — and a
// main that read os.Args for itself would be the hand-rolled parser the library
// exists to remove.
//
// Staleness reaches every tool but the meta-builder as command.Tool.Fit: a
// refusal the library writes as a refusal object with the recovery named, where
// a check calling os.Exit could only leave prose on stderr.

var cmdMakeGo = substituteBackticks(cmdMakeGoRaw)

const cmdMakeGoRaw = `// Command make is the meta-builder. It compiles every other tool under cmd/
// into <repoRoot>/bin, stamping each binary with the tools-source hash and the
// absolute repo root via -ldflags. It is the one tool that runs via 'go run'
// (from the ./make trampoline), so it is never compiled into bin/ and never
// stale — which is what breaks the bootstrap cycle.
//
// It is also the recovery every refusal names, which is why it declares no Fit:
// a tool that refused because the binaries are stale points at this one, and a
// builder that could refuse on the same ground would close the way out.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/promise-language/forge/primitives"
	"github.com/promise-language/forge/primitives/command"

	"__MODULE__/common"
)

// result is what make answers: whether it had anything to do, and what it did.
type result struct {
	UpToDate bool     §json:"up_to_date"§
	Built    []string §json:"built"§
}

// Human is the line, or the lines, a person reads.
func (r result) Human(w io.Writer) error {
	if r.UpToDate {
		_, err := fmt.Fprintln(w, "Tools up to date")
		return err
	}
	for _, name := range r.Built {
		if _, err := fmt.Fprintf(w, "built    %s\n", name); err != nil {
			return err
		}
	}
	return nil
}

// define is make's whole surface. It carries no version: it is the one main
// without a stamp, because it runs from the source it builds.
func define() command.Tool {
	return command.Tool{
		Project: "make",
		Root: command.Command{
			Name:    "make",
			Summary: "compile every tool under tools/build/cmd into bin/",
			Flags: []command.Flag{{
				Name:        "rebuild",
				Type:        command.Boolean,
				Description: "compile every tool even where bin/ is already up to date",
			}},
			Action: build,
		},
	}
}

// build is the meta-builder's whole run.
func build(c *command.Call) (command.Result, error) {
	// 1. Resolve the repo root. The ./make trampoline cd'd go run into
	//    <root>/tools/build, so our cwd is exactly that. Two levels up is root.
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	repoRoot := filepath.Dir(filepath.Dir(cwd))
	if !filepath.IsAbs(repoRoot) {
		return nil, fmt.Errorf("resolved repo root is not absolute: %s", repoRoot)
	}

	// 2. Hash the tools source — baked into every binary below. The pinned
	//    dependency is recorded in go.mod and go.sum, and both are hashed, so
	//    raising the pin makes every binary report itself stale.
	hash, err := primitives.ToolsSourceHash(repoRoot)
	if err != nil {
		return nil, err
	}

	// 3. Enable git hooks unconditionally (idempotent, fast).
	if err := primitives.RunSetup(repoRoot); err != nil {
		fmt.Fprintf(c.Narrate, "warning: could not configure git hooks: %v\n", err)
	}

	// What this project builds, from the one function that answers that — the
	// same one §run --list§ reports from, so the binaries this writes into bin/
	// and the commands the project claims cannot drift apart.
	tools, err := common.CommandNames(repoRoot)
	if err != nil {
		return nil, err
	}

	binDir := filepath.Join(repoRoot, "bin")
	hashFile := filepath.Join(binDir, ".tools.hash")

	// 4. Up-to-date short circuit.
	if !c.Bool("rebuild") && upToDate(hashFile, hash, binDir, tools) {
		return result{UpToDate: true, Built: []string{}}, nil
	}

	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return nil, err
	}

	// 5. Build each tool, injecting repoRoot and sourceHash via ldflags.
	ldflags := fmt.Sprintf("-s -w -X main.sourceHash=%s -X main.repoRoot=%s", hash, repoRoot)
	toolsModDir := filepath.Join(repoRoot, "tools", "build")
	for _, name := range tools {
		out := filepath.Join(binDir, primitives.BinaryName(name))
		fmt.Fprintf(c.Narrate, "building %s\n", name)
		if err := primitives.RunInStreams(toolsModDir, c.Narrate, c.Narrate, "go", "build",
			"-trimpath",
			"-ldflags", ldflags,
			"-o", out,
			"./cmd/"+name,
		); err != nil {
			return nil, fmt.Errorf("building %s: %w", name, err)
		}
	}

	// 6. Write the hash sidecar — the staleness contract.
	if err := os.WriteFile(hashFile, []byte(hash+"\n"), 0o644); err != nil {
		return nil, err
	}
	return result{Built: tools}, nil
}

func upToDate(hashFile, hash, binDir string, tools []string) bool {
	data, err := os.ReadFile(hashFile)
	if err != nil || strings.TrimSpace(string(data)) != hash {
		return false
	}
	for _, name := range tools {
		if !primitives.Exists(filepath.Join(binDir, primitives.BinaryName(name))) {
			return false
		}
	}
	return true
}

func main() {
	os.Exit(command.Run(define(), os.Args[1:], command.Stdio()))
}
`

const cmdVerifyGo = `// Command verify is the commit gate: it repairs what has one right answer,
// then measures what remains.
//
// It is one call into the command library and the definition below: what it
// parses, how it reports and what it exits with are not this file's.
package main

import (
	"os"

	"github.com/promise-language/forge/primitives"
	"github.com/promise-language/forge/primitives/command"

	"__MODULE__/common"
)

// Injected by the meta-builder via -ldflags at build time; empty otherwise.
var (
	repoRoot   = ""
	sourceHash = ""
)

// define is verify's whole surface.
func define(repoRoot, sourceHash string) command.Tool {
	return command.Tool{
		Project: "verify",
		Version: sourceHash,
		Fit: func() *command.Refusal {
			return primitives.StaleRefusal("verify", repoRoot, sourceHash)
		},
		Root: command.Command{
			Name:    "verify",
			Summary: "the commit gate: repair what has one right answer, then measure what remains",
			Action: func(c *command.Call) (command.Result, error) {
				result, err := common.RunVerify(repoRoot, c.Narrate)
				if err != nil {
					return nil, err
				}
				return result, nil
			},
		},
	}
}

func main() {
	os.Exit(command.Run(define(repoRoot, sourceHash), os.Args[1:], command.Stdio()))
}
`

var cmdSetupGo = substituteBackticks(cmdSetupGoRaw)

const cmdSetupGoRaw = `// Command setup makes a fresh clone ready to gate its own commits.
//
// It is one call into the command library and the definition below: what it
// parses, how it reports and what it exits with are not this file's.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/promise-language/forge/primitives"
	"github.com/promise-language/forge/primitives/command"
)

// Injected by the meta-builder via -ldflags at build time; empty otherwise.
var (
	repoRoot   = ""
	sourceHash = ""
)

// result is what setup answers: where git now looks for this repository's
// hooks, and what each of the project's own setup steps changed.
type result struct {
	HooksPath string §json:"hooks_path"§
	Steps     []step §json:"steps"§
}

// step is one of the project's own setup steps, and whether it changed
// anything. A second run changes nothing and says so.
type step struct {
	Name    string §json:"name"§
	Changed bool   §json:"changed"§
}

// Human is the line a person reads.
func (r result) Human(w io.Writer) error {
	if _, err := fmt.Fprintf(w, "git hooks configured (core.hooksPath = %s)\n", r.HooksPath); err != nil {
		return err
	}
	for _, s := range r.Steps {
		changed := "unchanged"
		if s.Changed {
			changed = "changed"
		}
		if _, err := fmt.Fprintf(w, "  %-12s %s\n", s.Name, changed); err != nil {
			return err
		}
	}
	return nil
}

// define is setup's whole surface.
func define(repoRoot, sourceHash string) command.Tool {
	return command.Tool{
		Project: "setup",
		Version: sourceHash,
		Fit: func() *command.Refusal {
			return primitives.StaleRefusal("setup", repoRoot, sourceHash)
		},
		Root: command.Command{
			Name:    "setup",
			Summary: "point git at this repository's hooks",
			Action: func(c *command.Call) (command.Result, error) {
				if err := primitives.RunSetup(repoRoot); err != nil {
					return nil, fmt.Errorf("wiring the git hooks: %w", err)
				}
				return result{HooksPath: primitives.HooksPath, Steps: []step{}}, nil
			},
		},
	}
}

func main() {
	os.Exit(command.Run(define(repoRoot, sourceHash), os.Args[1:], command.Stdio()))
}
`

var cmdGateGo = substituteBackticks(cmdGateGoRaw)

const cmdGateGoRaw = `// Command gate measures one property of this tree and prints what it found.
//
// It is not meant to be run by hand — §run <gate>§ is that path. A gate answers
// a runner, and a runner is the only caller that can say what became of the
// run: a gate killed for memory is not alive to report it, and a gate that
// exited cleanly having printed nothing would be believed.
//
// It is one call into the command library and the definition below: what it
// parses, how it reports and what it exits with are not this file's.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/promise-language/forge/primitives"
	"github.com/promise-language/forge/primitives/command"

	"__MODULE__/common"
)

// Injected by the meta-builder via -ldflags at build time; empty otherwise.
var (
	repoRoot   = ""
	sourceHash = ""
)

// listing is what --list answers: every name this project measures. A program
// reads the JSON, where what a gate declares of itself can grow additively; the
// line form is for a person.
//
// It exists because an orchestrator must not hold a second copy of what this
// project can measure. Asking the entry point is the only way to learn it that
// cannot go stale.
type listing struct {
	Gates []listedGate §json:"gates"§
}

// listedGate is one name and what it measures.
type listedGate struct {
	Name    string §json:"name"§
	Summary string §json:"summary"§
}

// Human is one name per line, which is what a person scanning for the name they
// want reads fastest.
func (l listing) Human(w io.Writer) error {
	for _, g := range l.Gates {
		if _, err := fmt.Fprintln(w, g.Name); err != nil {
			return err
		}
	}
	return nil
}

// define is gate's whole surface: the gates this project answers, each taking
// the envelope flag the gate contract fixes, and the listing beside them.
func define(repoRoot, sourceHash string) command.Tool {
	return command.Tool{
		Project: "gate",
		Version: sourceHash,
		Fit: func() *command.Refusal {
			return primitives.StaleRefusal("gate", repoRoot, sourceHash)
		},
		Root: command.Command{
			Name:    "gate",
			Summary: "measure one property of this tree",
			Flags: []command.Flag{{
				Name:        "list",
				Type:        command.Boolean,
				Description: "name every gate this project answers",
			}},
			SelectedBy: "list",
			Action: func(c *command.Call) (command.Result, error) {
				names := common.GateNames()
				gates := make([]listedGate, 0, len(names))
				for _, n := range names {
					gates = append(gates, listedGate{Name: n, Summary: common.GateSummary(n)})
				}
				return listing{Gates: gates}, nil
			},

			// The gates are the project's vocabulary rather than this tool's, so
			// help describes them instead of listing them: §gate --list§ is that
			// enumeration's one home.
			Children:     children,
			ChildClass:   "<gate>",
			ChildSummary: "a gate this project answers",
			EnumeratedBy: "gate --list",
			ChildFlags: []command.Flag{{
				Name:        "envelope",
				Type:        command.Boolean,
				Description: "write the measurement as one envelope on stdout",
				Protocol:    "the gate contract",
			}},
			ChildValidate: requireEnvelope,
			ChildAction:   func(c *command.Call) (command.Result, error) { return measure(repoRoot, c) },
		},
	}
}

// children is the gate set this project answers.
func children() []command.Command {
	names := common.GateNames()
	set := make([]command.Command, 0, len(names))
	for _, n := range names {
		set = append(set, command.Command{Name: n, Summary: common.GateSummary(n)})
	}
	return set
}

// requireEnvelope refuses a measurement nobody can read as one.
//
// A bare run that printed measurements and exited 0 would be read as a pass by
// the first script that wrapped it, and a gate has no verdict to give.
func requireEnvelope(c *command.Call) []error {
	if c.Bool("envelope") {
		return nil
	}
	return []error{fmt.Errorf(
		"%s measures %s, and writes it only with --envelope; run §run %s§ for a result meant for a person",
		c.Name(), common.GateSummary(c.Name()), c.Name())}
}

// measure runs one gate. Nothing is written here: the envelope is returned, and
// the library writes it once, whole — which is how a reader tells "measured
// nothing" from "measured and reported" without asking the gate.
func measure(repoRoot string, c *command.Call) (command.Result, error) {
	envelope, err := common.MeasureGate(repoRoot, c.Name())
	if err != nil {
		// Nothing was measured. No envelope, because a partial one is not a
		// measurement and must not parse as one.
		return nil, err
	}
	return common.Measured(envelope), nil
}

func main() {
	os.Exit(command.Run(define(repoRoot, sourceHash), os.Args[1:], command.Stdio()))
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
//
// It is one call into the command library and the definition below: what it
// parses, how it reports and what it exits with are not this file's.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/promise-language/forge/primitives"
	"github.com/promise-language/forge/primitives/command"

	"__MODULE__/common"
)

// Injected by the meta-builder via -ldflags at build time; empty otherwise.
var (
	repoRoot   = ""
	sourceHash = ""
)

// listing is what --list answers: the two kinds of name a caller outside this
// tree can ask this project for — a command it can execute, and a gate it can
// measure. Both are discovered rather than declared, so neither can go stale,
// and one object carries them together because a caller that must not confuse
// the two needs to see the whole vocabulary at once.
type listing struct {
	Commands []string §json:"commands"§
	Gates    []string §json:"gates"§
}

// Human is one name per line with its kind, because the two lists answer
// different questions — what this project builds, and what it answers — and a
// reader who cannot tell which is which has to know the vocabulary already to
// use the output that exists to teach it.
func (l listing) Human(w io.Writer) error {
	for _, c := range l.Commands {
		if _, err := fmt.Fprintf(w, "command  %s\n", c); err != nil {
			return err
		}
	}
	for _, g := range l.Gates {
		if _, err := fmt.Fprintf(w, "gate     %s\n", g); err != nil {
			return err
		}
	}
	return nil
}

// define is run's whole surface: the gates this project answers, each judged or
// measured, and the discovery query beside them.
func define(repoRoot, sourceHash string) command.Tool {
	return command.Tool{
		Project: "run",
		Version: sourceHash,
		Fit: func() *command.Refusal {
			return primitives.StaleRefusal("run", repoRoot, sourceHash)
		},
		Root: command.Command{
			Name:    "run",
			Summary: "measure one gate and judge what it measured",
			Flags: []command.Flag{{
				Name:        "list",
				Type:        command.Boolean,
				Description: "name what this project builds and what it answers",
			}},
			SelectedBy: "list",
			Action:     func(c *command.Call) (command.Result, error) { return list(repoRoot) },

			Children:     children,
			ChildClass:   "<gate>",
			ChildSummary: "a gate this project answers",
			EnumeratedBy: "run --list",
			ChildFlags: []command.Flag{{
				Name:        "verdict",
				Type:        command.Boolean,
				Description: "judge the envelope on stdin, and run no gate",
				Protocol:    "the gate contract",
			}},
			ChildAction: func(c *command.Call) (command.Result, error) { return measureOrJudge(repoRoot, c) },
		},
	}
}

// children is the gate set this project answers.
func children() []command.Command {
	names := common.GateNames()
	set := make([]command.Command, 0, len(names))
	for _, n := range names {
		set = append(set, command.Command{Name: n, Summary: common.GateSummary(n)})
	}
	return set
}

// list says what this project builds and what it answers.
//
// It is the discovery query: it is how anything outside the tree learns both
// without holding a copy that can go stale.
func list(repoRoot string) (command.Result, error) {
	commands, err := common.CommandNames(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("cannot say what this project builds: %w", err)
	}
	return listing{Commands: commands, Gates: common.GateNames()}, nil
}

// measureOrJudge runs the gate, or judges an envelope it is handed.
//
// In the judging mode nothing is spawned: the envelope arrives on stdin from
// whoever ran the gate. That is the mode an external runner asks — it spawns
// the gate, because a judge that ran its own measurement would be the runner,
// and the runner comes from outside the tree.
func measureOrJudge(repoRoot string, c *command.Call) (command.Result, error) {
	if c.Bool("verdict") {
		verdict, err := common.JudgeStdin(repoRoot, c.Name(), c.In)
		if err != nil {
			return nil, err
		}
		return verdict, nil
	}
	judged, err := common.RunOneGate(repoRoot, common.GateBinary(repoRoot), c.Name(), c.Narrate)
	if err != nil {
		return nil, err
	}
	return judged, nil
}

func main() {
	os.Exit(command.Run(define(repoRoot, sourceHash), os.Args[1:], command.Stdio()))
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

// docsIndexMd is the map of docs/, and a scaffolded project must be born with a
// conformant one: org/normative.md, Location makes the index the one per-project
// file that names the project's STATUS QUERY, "the one per-project fact this
// shared document cannot carry". An index without it is a tree that is
// non-conformant from its first commit.
//
// It carries no relative link, so the tree it is written into cannot fail a
// documentation link check on the scaffolder's own output before the adopter has
// written anything. That is also why the corpus is named as a directory in plain
// text rather than linked: org/normative.md, Location fixes that form — the
// corpus is "listed once, as the directory, by way of the stamp it carries", so
// "a sync adds and removes members without editing a file the project owns". The
// entry is therefore already correct both before the corpus arrives and after,
// and the sync that brings it edits nothing here.
//
// What this does NOT emit is the corpus itself, the legal set or the CLA
// workflow. A vendored copy is sanctioned only where a machine checks it
// (org/normative.md, Links) — verified against its stamp — and a copy frozen
// into this scaffolder at build time is checked by nothing and stale from the
// day after it is cut. The corpus "changes at its home, and reaches the project
// by sync" (org/normative.md, Location), which is provisioning's act and not
// this one's.
var docsIndexMd = substituteBackticks(docsIndexMdRaw)

const docsIndexMdRaw = `# Documentation Index

This is the map of §docs/§. Every tracked file under it is listed here, wherever
it lives — the section an entry sits under is where its binding status is written
down. A document nobody can reach from the index is one nobody reads, and one
nobody updates.

**This project's status query.** Each root document's tag is a GitHub label,
spelled as the file's basename minus §.md§, and the remaining work for a document
is:

> §gh issue list --label <tag> --state open --limit 200§

§--state open§ and §--limit 200§ are both written out deliberately: §gh issue
list§ defaults to a limit of 30, so a specification with more remaining work than
that would silently under-report and read as nearly done.

## Specifications

_Nothing yet._ Add a line here in the same commit that adds the document; an
index brought up to date afterwards is an index that was wrong in between.

## Organization wide corpus

**Binding.** Every document under §docs/org/§ binds this project exactly as a
specification in this directory's root does. It is vendored from its home
repository and never edited here: an issue about one of those documents is filed
where the document originates, and what this project files locally under their
tags is its own compliance gaps.

The corpus is listed once, as the directory, with its §docs/org/stamp.json§
naming the release these copies came from and every member — so the corpus's map
is the corpus's own, and a sync adds or removes members without editing this
file.

- §docs/org/§ — the organization-wide corpus, at the release its stamp names.

Provisioning delivers it. A checkout that has not been provisioned has no
§docs/org/§ yet, and the entry above is what it will be when it does.
`

// ───────────────────────── claude config ─────────────────────────

// settingsJSON wires bin/tool-guard, a workspace tool this project does not
// build, on BOTH tool-use events (docs/blueprint.md, The agent guard). PreToolUse is the gate
// and fails closed; PostToolUse observes and fails quiet, because by then the
// tool has already run and an enforcing shape could only inject an error after a
// completed call.
//
// The two command strings are EXACT, not a shape. A conformance checker compares
// them byte-for-byte and reports any difference at error severity, so no wrapper
// and no conditional may be added here. This is also the ONE piece of wiring the
// scaffolder commits, where the hook that reaches the commit guard is not
// scaffolded at all; why the two differ has one home and it is not this comment
// (docs/blueprint.md, Tools the project does not build).
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
