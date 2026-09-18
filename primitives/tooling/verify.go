package tooling

// Verify (docs/project-tools.md).
//
// verify repairs what has one right answer, then measures what remains. It
// measures by the same gate implementations `gate` runs, called in-process, and
// judges the result against the same terms `run` applies. A verify that ran its
// own separate commands would be a third way, free to disagree with the other
// two.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// The three things a step can have been, as the summary and the JSON both name
// them.
const (
	StatusPassed = "passed"
	StatusFailed = "failed"
	StatusNotRun = "not-run"
)

// VerifyResult is what verify answers. It is a result like any other: the
// library writes it, in the mode the invocation selected, and reads the status
// off it.
type VerifyResult struct {
	OK     bool          `json:"ok"`
	Stages []StageResult `json:"stages"`
	// Tree is the id of the tree this run blessed, absent when nothing was
	// recorded.
	Tree string `json:"tree,omitempty"`
}

// StageResult is one stage of the run and what became of its steps.
type StageResult struct {
	Name  string       `json:"name"`
	Steps []StepResult `json:"steps"`
}

// StepResult is one step, what became of it, and how long it took.
type StepResult struct {
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
//
// The evidence of every failure comes after the roll of stages, so the last
// lines of the output carry all of it.
func (r VerifyResult) Human(w io.Writer) error {
	var b strings.Builder
	b.WriteString("\n──────── verify summary ────────\n")
	var elapsed time.Duration
	var failures []StepResult
	for _, stage := range r.Stages {
		fmt.Fprintf(&b, "  %s\n", stage.Name)
		for _, s := range stage.Steps {
			elapsed += time.Duration(s.ElapsedSeconds * float64(time.Second))
			fmt.Fprintf(&b, "    %-7s  %-24s %s\n", label(s.Status), s.Name, took(s))
			if s.Status == StatusFailed {
				failures = append(failures, s)
			}
		}
	}
	fmt.Fprintf(&b, "  elapsed %s\n", elapsed.Round(time.Millisecond))
	if r.Tree != "" {
		fmt.Fprintf(&b, "  tree %s\n", r.Tree)
	}
	if len(failures) > 0 {
		b.WriteString("\n──────── what failed ────────\n")
		for _, s := range failures {
			fmt.Fprintf(&b, "  %s\n", s.Name)
			for line := range strings.SplitSeq(strings.TrimRight(s.Detail, "\n"), "\n") {
				fmt.Fprintf(&b, "    %s\n", line)
			}
		}
	}
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
	case StatusPassed:
		return "ok"
	case StatusFailed:
		return "FAIL"
	}
	return StatusNotRun
}

// took is how long a step ran, or nothing where it did not run.
func took(s StepResult) string {
	if s.Status == StatusNotRun {
		return ""
	}
	return time.Duration(s.ElapsedSeconds * float64(time.Second)).Round(time.Millisecond).String()
}

// RunVerify is the commit gate. One verify runs at a time per checkout, and a
// second run waits and names the run it is waiting for.
func RunVerify(r *Run) (VerifyResult, error) {
	release, err := takeLock(r)
	if err != nil {
		return VerifyResult{}, err
	}
	defer release()

	// A stale blessing left behind is the one outcome the verified-tree check
	// must never produce, so failing to clear fails the run outright.
	if err := ClearVerifiedTree(r.Root); err != nil {
		return VerifyResult{}, fmt.Errorf("clearing the verified-tree record: %w", err)
	}

	result := VerifyResult{OK: true}
	// A run stops for two reasons, and neither is a pass. A stage that failed
	// ends the run; so does an interrupt, after the step it arrived during. What
	// follows either is reported as not run — and a run with a stage nobody ran
	// has not established that this tree may be committed, so it is not OK.
	stopped := false
	for _, stage := range r.project.Verify.Stages() {
		if stopped || r.Interrupted() {
			stopped, result.OK = true, false
			result.Stages = append(result.Stages, notRun(stage))
			continue
		}
		reported := StageResult{Name: stage.Name}
		fmt.Fprintf(r.Narrate, "==> %s\n", stage.Name)
		// Within a stage every step runs and every failure is tallied: a
		// project whose failures are independent learns about all of them in
		// one round rather than one per round. An interrupt is the exception —
		// the first one ends the run after the current step.
		for i, step := range stage.Steps {
			reported.Steps = append(reported.Steps, runStep(r, step))
			if r.Interrupted() {
				for _, skipped := range stage.Steps[i+1:] {
					reported.Steps = append(reported.Steps,
						StepResult{Name: skipped.Name, Status: StatusNotRun})
				}
				break
			}
		}
		for _, s := range reported.Steps {
			switch s.Status {
			case StatusFailed:
				result.OK, stopped = false, true
			case StatusNotRun:
				result.OK = false
			}
		}
		result.Stages = append(result.Stages, reported)
	}
	result.Tree = r.tree
	return result, nil
}

// runStep runs one step and reports what became of it.
func runStep(r *Run, step Step) StepResult {
	fmt.Fprintf(r.Narrate, "--> %s\n", step.Name)
	start := time.Now()
	err := step.Run(r)
	reported := StepResult{
		Name:           step.Name,
		Status:         StatusPassed,
		ElapsedSeconds: time.Since(start).Seconds(),
	}
	if err != nil {
		reported.Status = StatusFailed
		reported.Detail = err.Error()
		fmt.Fprintf(r.Narrate, "    %s failed: %v\n", step.Name, err)
	}
	return reported
}

// notRun is a stage the run never reached. A stage with a failure ends the run,
// and the stages after it are reported as not run rather than omitted: a reader
// must be able to tell what was not checked from what passed.
func notRun(stage Stage) StageResult {
	reported := StageResult{Name: stage.Name}
	for _, step := range stage.Steps {
		reported.Steps = append(reported.Steps, StepResult{Name: step.Name, Status: StatusNotRun})
	}
	return reported
}

// standardPipeline is the five stages docs/project-tools.md, Verify names, in
// the order it names them.
func standardPipeline() Pipeline {
	var pipeline Pipeline
	pipeline.AddStage(Stage{Name: StageRepair, Steps: []Step{{
		Name:    "format",
		Summary: "rewrite what the formatter has one right answer about",
		Run:     repair,
	}}})
	// Nothing a failed build measures is about the change, so the build is a
	// stage of its own and the rest does not run behind it.
	pipeline.AddStage(Stage{Name: StageBuilds, Steps: []Step{JudgedStep(Builds)}})
	pipeline.AddStage(Stage{Name: StageMeasure})
	pipeline.AddStage(Stage{Name: StageRatchet, Steps: []Step{{
		Name:    "baselines",
		Summary: "move each baseline the run improved on",
		Run:     ratchetStep,
	}}})
	pipeline.AddStage(Stage{Name: StageRecord, Steps: []Step{{
		Name:    "tree",
		Summary: "record the tree this run blessed",
		Run:     recordStep,
	}}})
	return pipeline
}

// measureSteps is every part of integration but the build, plus the gates a
// project's ratcheted metrics come from. It is computed after the definition is
// composed, because what integration is made of is the project's to say.
func measureSteps(p *Project) []Step {
	var steps []Step
	for _, part := range p.IntegrationParts() {
		if part == Builds {
			continue
		}
		steps = append(steps, JudgedStep(part))
	}
	return steps
}

// repair runs each toolchain's formatter, in rewrite mode, over exactly the
// file set its formatted measurement reads. One function supplies both sets, so
// verify cannot repair one tree while the gate measures another.
func repair(r *Run) error {
	units, err := Units(r)
	if err != nil {
		return err
	}
	for _, u := range units {
		tc, ok := r.Toolchain(u.Toolchain)
		if !ok || tc.Rewrite == nil {
			continue
		}
		files, err := r.SourceFiles(u)
		if err != nil {
			return err
		}
		if len(files) == 0 {
			continue
		}
		if err := tc.Rewrite(r, u, files); err != nil {
			return fmt.Errorf("%s: %w", u.Label(), err)
		}
	}
	return nil
}

// JudgedStep is one gate, measured in-process and judged against the same terms
// `run` applies. It is exported because a project whose ratcheted metric comes
// from a gate outside integration adds that gate to the measure stage, and it
// must be the same step the standard parts are.
func JudgedStep(name string) Step {
	return Step{
		Name:    name,
		Summary: "measure " + name + " and judge what it measured",
		Run: func(r *Run) error {
			env, err := MeasureGate(r, name)
			if err != nil {
				return err
			}
			r.measured = append(r.measured, env)
			terms, err := LoadTerms(r.Root)
			if err != nil {
				return err
			}
			g, _, err := resolveGate(r, name)
			if err != nil {
				return err
			}
			verdict, err := Judge(env, terms, g.Remediation)
			if errors.Is(err, ErrNothingJudged) {
				// verify is the gate that says a tree may be committed. A part
				// of it that no term touches is not checked at all, and saying
				// so is the only answer that does not report a pass nobody
				// granted.
				return fmt.Errorf("%s reported %d measurement(s) and no term judges any of them; add one to %s or %s, or take %s out of what verify measures",
					name, len(env.Metrics), ThresholdsFile, BaselinesFile, name)
			}
			if err != nil {
				return err
			}
			if !verdict.Acceptable {
				return errors.New(verdict.Detail)
			}
			return nil
		},
	}
}

// ratchetStep moves each baseline this run earned. It runs after every
// measuring stage has passed and before the tree is recorded, so the commit
// that earned the improvement carries it.
func ratchetStep(r *Run) error {
	moved, err := Ratchet(r.Root, r.measured)
	if err != nil {
		return err
	}
	for _, line := range moved {
		fmt.Fprintf(r.Narrate, "    %s\n", line)
	}
	if len(moved) == 0 {
		fmt.Fprintln(r.Narrate, "    no baseline moved")
	}
	return nil
}

// recordStep writes the verified-tree record, last.
func recordStep(r *Run) error {
	tree, err := RecordVerifiedTree(r)
	r.tree = tree
	return err
}

// takeLock holds the one verify a checkout runs at a time. A second run waits
// and names the run it is waiting for.
//
// A lock whose holder is gone is taken rather than waited on: a run killed
// between taking the lock and releasing it would otherwise refuse every verify
// in the checkout forever, and there is no person to tell that it is safe.
func takeLock(r *Run) (func(), error) {
	path := filepath.Join(r.Root, filepath.FromSlash(VerifyLock))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("making %s: %w", filepath.Dir(VerifyLock), err)
	}
	held := fmt.Sprintf("%d %s\n", os.Getpid(), time.Now().Format(time.RFC3339))
	announced := false
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			f.WriteString(held)
			f.Close()
			return func() { os.Remove(path) }, nil
		}
		if !os.IsExist(err) {
			return nil, fmt.Errorf("taking %s: %w", VerifyLock, err)
		}
		pid, since := readLock(path)
		// processAlive answers false for a pid that names no process, a
		// malformed lock included: a lock nobody holds is a lock to take, not
		// one to wait on forever.
		if !processAlive(pid) {
			fmt.Fprintf(r.Narrate, "the verify that held %s (pid %d) is gone — taking the lock\n", VerifyLock, pid)
			os.Remove(path)
			continue
		}
		if !announced {
			fmt.Fprintf(r.Narrate, "waiting for the verify started at %s (pid %d) — one runs at a time in this checkout\n", since, pid)
			announced = true
		}
		select {
		case <-r.Context().Done():
			return nil, fmt.Errorf("gave up waiting for the verify started at %s (pid %d)", since, pid)
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// readLock reports who holds the lock and since when, so a waiting run can name
// the run it is waiting for.
func readLock(path string) (pid int, since string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, "an unreadable time"
	}
	fields := strings.Fields(string(data))
	if len(fields) < 2 {
		return 0, "an unrecorded time"
	}
	n, err := strconv.Atoi(fields[0])
	if err != nil {
		return 0, fields[len(fields)-1]
	}
	return n, fields[1]
}
