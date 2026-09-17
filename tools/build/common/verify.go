package common

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/promise-language/forge/primitives"
)

// The three things a step can have been, as the summary and the JSON both name
// them (docs/project-tools.md, Verify).
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
// library writes it, in the mode the invocation selected, and reads the status
// off it.
type VerifyResult struct {
	OK     bool          `json:"ok"`
	Stages []VerifyStage `json:"stages"`
	// Tree is the id of the tree this run blessed, absent when nothing was
	// recorded.
	Tree string `json:"tree,omitempty"`
}

// VerifyStage is one stage of the run. This pipeline stops at the first
// failure, which is the shape docs/project-tools.md, Verify gives a project
// that must: each step is a stage of its own.
type VerifyStage struct {
	Name  string       `json:"name"`
	Steps []VerifyStep `json:"steps"`
}

// VerifyStep is one step, what became of it, and how long it took.
type VerifyStep struct {
	Name           string  `json:"name"`
	Status         string  `json:"status"`
	ElapsedSeconds float64 `json:"elapsed_seconds"`
	Detail         string  `json:"detail,omitempty"`
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

// Human is the summary, and it always prints — pass, fail or interrupted — so
// that whoever is tailing the output sees the result without re-running.
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
// which tree that was, so the precommit-guard can refuse a commit of any other
// one.
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
// break-on-first-failure for free — a red step leaves nothing blessed. The tree
// it recorded travels out through tree, which is the one fact the record step
// produces rather than merely does.
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
			{"format", checkFormatted},
			{"vet", func(r string, n io.Writer) error { return runAllModules(r, n, "vet") }},
			{"build", func(r string, n io.Writer) error { return runAllModules(r, n, "build") }},
			{"test", func(r string, n io.Writer) error { return runAllModules(r, n, "test") }},
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

// runAllModules runs `go <verb> ./...` in every module of the repository.
//
// A child's streams are the narration, never the tool's stdout: `go test`
// reports on stdout, and stdout carries the result and nothing else.
func runAllModules(repoRoot string, narrate io.Writer, verb string) error {
	for _, dir := range modules(repoRoot) {
		if err := primitives.RunInStreams(dir, narrate, narrate, "go", verb, "./..."); err != nil {
			return err
		}
	}
	return nil
}

// checkFormatted reports unformatted files instead of rewriting them.
//
// `gofmt -w` made this step incapable of failing: it repaired the tree and
// exited 0, so the gate reported a clean run over a change it had silently
// altered. A gate states what is true about the tree it was handed; one that
// edits first is answering about a different tree — and under CI, about one
// nobody will ever see, since the checkout is discarded. Unformatted code would
// merge with the gate green.
//
// `gofmt -l` exits 0 whether or not it lists anything, so the OUTPUT is the
// signal and the exit code carries nothing. The names are printed because
// "run gofmt" without them leaves the reader to find the files themselves.
func checkFormatted(repoRoot string, narrate io.Writer) error {
	out, err := primitives.RunOutputIn(repoRoot, "gofmt", "-l", ".")
	if err != nil {
		return fmt.Errorf("gofmt -l: %w", err)
	}
	if out == "" {
		return nil
	}
	files := strings.Split(out, "\n")
	for _, f := range files {
		fmt.Fprintf(narrate, "    unformatted: %s\n", f)
	}
	return fmt.Errorf("%d file(s) need gofmt -w", len(files))
}
