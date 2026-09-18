package tooling

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/promise-language/forge/primitives/command"

	"github.com/promise-language/forge/primitives"
)

// The standard stages run in the order the document names them. Nothing a
// failed build measures is about the change, so the build comes before
// everything the change is judged on.
func TestTheStagesRunInTheSpecifiedOrder(t *testing.T) {
	want := []string{StageRepair, StageBuilds, StageMeasure, StageRatchet, StageRecord}
	var got []string
	standard := Standard()
	for _, stage := range standard.Verify.Stages() {
		got = append(got, stage.Name)
	}
	if !slices.Equal(got, want) {
		t.Errorf("stages = %v, want %v", got, want)
	}
}

// What verify measures follows what integration is made of, from one place: a
// project that recomposed integration and left verify measuring the old set
// would have two answers to what must hold before a change may land.
func TestTheMeasureStageFollowsIntegration(t *testing.T) {
	p := Standard()
	p.Gates.Add(Gate{
		Name: "size", Summary: "bytes in the release binary",
		Metrics:     Declared(Bytes("binary_bytes")),
		Measure:     func(*Run, []Unit) (Measured, error) { return Measured{}, nil },
		Remediation: "remove what grew the binary",
	})
	p.Integration(Formatted, Builds, "size")

	var steps []string
	for _, stage := range p.Verify.Stages() {
		if stage.Name == StageMeasure {
			for _, s := range stage.Steps {
				steps = append(steps, s.Name)
			}
		}
	}
	// builds has a stage of its own, so the measure stage holds the rest.
	if !slices.Equal(steps, []string{Formatted, "size"}) {
		t.Errorf("the measure stage runs %v, want what integration is composed of without the build", steps)
	}
}

// Within a stage every step runs and every failure is tallied: whoever ran it
// learns about all of them in one round rather than one per round. A stage with
// a failure ends the run, and the stages after it are reported as not run.
func TestEveryStepInAStageRunsAndEveryFailureIsTallied(t *testing.T) {
	root := fixture(t, "")
	terms(t, root, `{"n": {"direction": "down", "cap": 0}}`, "{}")

	ran := map[string]bool{}
	p := Standard()
	p.Verify = Pipeline{}
	p.Verify.AddStage(Stage{Name: "first", Steps: []Step{
		failing("one", ran), failing("two", ran), passing("three", ran),
	}})
	p.Verify.AddStage(Stage{Name: "second", Steps: []Step{passing("four", ran)}})

	r, _ := run(t, p, root)
	got, err := RunVerify(r)
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"one", "two", "three"} {
		if !ran[name] {
			t.Errorf("%q did not run, and every step in a stage runs", name)
		}
	}
	if ran["four"] {
		t.Error("a stage after a failure ran")
	}
	if got.OK {
		t.Error("a run with two failed steps reported ok")
	}

	failed := 0
	for _, s := range got.Stages[0].Steps {
		if s.Status == StatusFailed {
			failed++
		}
	}
	if failed != 2 {
		t.Errorf("%d failures tallied, want both", failed)
	}
	for _, s := range got.Stages[1].Steps {
		if s.Status != StatusNotRun {
			t.Errorf("the stage after the failure reported %q, want %q", s.Status, StatusNotRun)
		}
	}
}

// The summary always prints, and the evidence of every failure comes last, so
// the last lines of the output carry all of it.
func TestTheEvidenceOfEveryFailureComesLast(t *testing.T) {
	result := VerifyResult{
		OK: false,
		Stages: []StageResult{{Name: "measure", Steps: []StepResult{
			{Name: "tested", Status: StatusFailed, Detail: "failed_tests is 2, cap 0"},
			{Name: "checked", Status: StatusPassed},
		}}},
	}
	var rendered strings.Builder
	if err := result.Human(&rendered); err != nil {
		t.Fatal(err)
	}
	body := rendered.String()

	if !strings.Contains(body, "❌ Verify FAILED") {
		t.Errorf("the summary does not carry the verdict line: %q", body)
	}
	where := strings.Index(body, "failed_tests is 2")
	roll := strings.Index(body, "checked")
	if where < 0 || where < roll {
		t.Errorf("the evidence is not after the roll of stages: %q", body)
	}
	if result.ExitStatus() != 1 {
		t.Errorf("status = %d, want 1 when a stage did not pass", result.ExitStatus())
	}
}

// A part of the commit gate that no term touches is not checked at all, and
// reporting it as a pass would be a pass nobody granted.
func TestAGateNoTermJudgesFailsRatherThanPasses(t *testing.T) {
	root := fixture(t, "")
	terms(t, root, `{}`, `{}`)
	p := counting("x", []Metric{Count("n")}, Measured{Metrics: []Measurement{Counted("n", 0, "")}}, nil)

	r, _ := run(t, p, root)
	err := JudgedStep("x").Run(r)
	if err == nil {
		t.Fatal("a gate no term judges passed")
	}
	if !strings.Contains(err.Error(), ThresholdsFile) {
		t.Errorf("the failure is %q, want it to name where a term would go", err)
	}
}

// A measurement beyond its term fails the step it was measured in, and the
// verdict's own detail is the failure. The summary's evidence is what someone
// reads instead of re-running the gate by hand.
func TestAMeasurementBeyondItsTermFailsTheStep(t *testing.T) {
	root := fixture(t, "")
	terms(t, root, `{"n": {"direction": "down", "cap": 0}}`, `{}`)
	p := counting("x", []Metric{Count("n")}, Measured{
		Metrics: []Measurement{Counted("n", 2, "")},
		Groups:  []Group{{Name: "root", Metrics: []Measurement{Counted("n", 2, "")}}},
	}, nil)

	r, _ := run(t, p, root)
	err := JudgedStep("x").Run(r)
	if err == nil {
		t.Fatal("a measurement over its cap passed the step it was measured in")
	}
	for _, want := range []string{"n is 2", "cap 0", "root"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the failure is %q, want it to say %q", err, want)
		}
	}
}

// verify ratchets: after every measuring stage has passed, each baseline whose
// metric the run measured completely moves in the baseline's direction, and the
// move is on disk before the tree is recorded — so the commit that earned the
// improvement carries it.
func TestAGreenRunMovesTheBaselineItEarned(t *testing.T) {
	root := fixture(t, "")
	terms(t, root, `{}`, `{"n": {"direction": "up", "value": 3}}`)
	p := counting("x", []Metric{Count("n")}, Measured{Metrics: []Measurement{Counted("n", 7, "")}}, nil)
	p.Verify = Pipeline{}
	p.Verify.AddStage(Stage{Name: StageMeasure, Steps: []Step{JudgedStep("x")}})
	p.Verify.AddStage(Stage{Name: StageRatchet, Steps: []Step{{Name: "baselines", Run: ratchetStep}}})

	r, narrated := run(t, p, root)
	got, err := RunVerify(r)
	if err != nil {
		t.Fatal(err)
	}
	if !got.OK {
		t.Fatalf("a green run reported %+v", got.Stages)
	}

	moved, err := loadBaselines(root)
	if err != nil {
		t.Fatal(err)
	}
	if moved["n"].Value == nil || *moved["n"].Value != 7 {
		t.Errorf("the baseline is %+v, want the value the run earned", moved["n"])
	}
	if !strings.Contains(narrated.String(), "moved from 3 to 7") {
		t.Errorf("the ratchet did not report what it moved: %q", narrated.String())
	}
}

// verify refuses when .workspace/ is not ignored. A record in a tracked path is
// a file `git add -A` would put into the very tree it claims to bless, so the
// record could never match what the guard stages.
func TestRecordingRefusesWhereTheRecordWouldBeTracked(t *testing.T) {
	root := fixture(t, "")
	write(t, root, ".gitignore", "/bin/\n/.home/\n")
	git(t, root, "add", "-A")

	r, _ := run(t, Standard(), root)
	if _, err := RecordVerifiedTree(r); err == nil {
		t.Error("the record was written into a path git would stage")
	} else if !strings.Contains(err.Error(), ".workspace") {
		t.Errorf("the refusal is %q, want it to name the directory that is not ignored", err)
	}
}

// The record is cleared before the first stage and written only after the last,
// so a red or interrupted run blesses nothing.
func TestARedRunBlessesNothing(t *testing.T) {
	root := fixture(t, "")
	terms(t, root, `{}`, `{}`)
	write(t, root, filepath.FromSlash(primitives.VerifiedTreeRecord), "a tree an earlier run blessed\n")

	p := Standard()
	p.Verify = Pipeline{}
	p.Verify.AddStage(Stage{Name: "measure", Steps: []Step{failing("one", map[string]bool{})}})
	p.Verify.AddStage(Stage{Name: StageRecord, Steps: []Step{{Name: "tree", Run: recordStep}}})

	r, _ := run(t, p, root)
	got, err := RunVerify(r)
	if err != nil {
		t.Fatal(err)
	}
	if got.OK || got.Tree != "" {
		t.Errorf("a red run recorded %q", got.Tree)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(primitives.VerifiedTreeRecord))); !os.IsNotExist(err) {
		t.Errorf("the earlier run's record survived a red run: %v", err)
	}
}

// The first interrupt ends the run after the current step, and an interrupted
// run is not a pass. Nothing it skipped was measured, so reporting it green
// would tell a person a tree is safe to commit on the strength of the stages
// that happened to run before they pressed a key.
func TestAnInterruptedRunIsNotAPass(t *testing.T) {
	root := fixture(t, "")
	terms(t, root, `{}`, `{}`)

	ran := map[string]bool{}
	p := Standard()
	p.Verify = Pipeline{}
	p.Verify.AddStage(Stage{Name: "measure", Steps: []Step{
		{Name: "one", Run: func(r *Run) error {
			r.mu.Lock()
			r.interrupted = true
			r.mu.Unlock()
			return nil
		}},
		passing("two", ran),
	}})
	p.Verify.AddStage(Stage{Name: StageRecord, Steps: []Step{{Name: "tree", Run: recordStep}}})

	r, _ := run(t, p, root)
	got, err := RunVerify(r)
	if err != nil {
		t.Fatal(err)
	}
	if got.OK || got.ExitStatus() != 1 {
		t.Errorf("an interrupted run reported ok=%v, status %d", got.OK, got.ExitStatus())
	}
	if ran["two"] {
		t.Error("the run did not end after the step the interrupt arrived during")
	}
	if got.Tree != "" {
		t.Errorf("an interrupted run blessed %q", got.Tree)
	}
	for _, stage := range got.Stages {
		for _, s := range stage.Steps {
			if s.Name != "one" && s.Status != StatusNotRun {
				t.Errorf("%s/%s is %q, want every step after the interrupt reported as not run",
					stage.Name, s.Name, s.Status)
			}
		}
	}
}

// A green run records the tree `git add -A` would stage, and the record is the
// one the workspace's commit guard reads.
func TestAGreenRunRecordsTheTreeGitAddWouldStage(t *testing.T) {
	root := fixture(t, "")
	terms(t, root, `{}`, `{}`)
	write(t, root, "a.go", "package a\n")

	p := Standard()
	p.Verify = Pipeline{}
	p.Verify.AddStage(Stage{Name: StageRecord, Steps: []Step{{Name: "tree", Run: recordStep}}})

	r, _ := run(t, p, root)
	got, err := RunVerify(r)
	if err != nil {
		t.Fatal(err)
	}
	if !got.OK || got.Tree == "" {
		t.Fatalf("a green run recorded nothing (ok=%v)", got.OK)
	}

	recorded, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(primitives.VerifiedTreeRecord)))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(recorded)) != got.Tree {
		t.Errorf("the record holds %q and the result says %q", recorded, got.Tree)
	}

	// The same tree `git add -A` would stage, computed over the real index.
	git(t, root, "add", "-A")
	if want := git(t, root, "write-tree"); want != got.Tree {
		t.Errorf("recorded %q, and `git add -A` stages %q", got.Tree, want)
	}
}

// Outside a git checkout, recording is a reported no-op, and a run whose stages
// all passed still exits 0.
func TestOutsideACheckoutRecordingIsAReportedNoOp(t *testing.T) {
	root := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(root))
	terms(t, root, `{}`, `{}`)

	p := Standard()
	p.Verify = Pipeline{}
	p.Verify.AddStage(Stage{Name: StageRecord, Steps: []Step{{Name: "tree", Run: recordStep}}})

	r, narrated := run(t, p, root)
	got, err := RunVerify(r)
	if err != nil {
		t.Fatal(err)
	}
	if !got.OK || got.ExitStatus() != 0 {
		t.Errorf("a green run outside a checkout exited %d", got.ExitStatus())
	}
	if got.Tree != "" {
		t.Errorf("a tree was recorded outside a checkout: %q", got.Tree)
	}
	if !strings.Contains(narrated.String(), "not a git checkout") {
		t.Errorf("the no-op was not reported: %q", narrated.String())
	}
}

// verify writes its result to stdout and its progress to stderr, so
// `bin/verify > result.json` and `bin/verify 2>/dev/null` both behave. A run
// that failed still writes the result — the caller asked whether this tree may
// be committed, and a no is an answer — and the status says the answer was no.
func TestVerifyWritesItsResultToStdoutAndItsProgressToStderr(t *testing.T) {
	root := fixture(t, "")
	terms(t, root, `{"n": {"direction": "down", "cap": 0}}`, `{}`)
	stamp := stamped(t, root)

	p := counting("x", []Metric{Count("n")}, Measured{Metrics: []Measurement{Counted("n", 2, "")}}, nil)
	p.Verify = Pipeline{}
	p.Verify.AddStage(Stage{Name: StageMeasure, Steps: []Step{JudgedStep("x")}})

	var out, errs strings.Builder
	status := VerifyTool(p, stamp).RunWith(nil,
		command.Streams{Out: &out, Err: &errs, Dir: root})

	if status != command.StatusFailed {
		t.Errorf("status = %d, want %d when a stage did not pass", status, command.StatusFailed)
	}
	var result VerifyResult
	if err := json.Unmarshal([]byte(out.String()), &result); err != nil {
		t.Fatalf("stdout is not the result (%v): %q", err, out.String())
	}
	if result.OK || len(result.Stages) != 1 {
		t.Errorf("the result is %+v, want a red run reporting its one stage", result)
	}
	if strings.Contains(out.String(), "==>") {
		t.Errorf("progress reached stdout: %q", out.String())
	}
	if !strings.Contains(errs.String(), "==> "+StageMeasure) {
		t.Errorf("stderr carries no progress: %q", errs.String())
	}
}

// One verify runs at a time per checkout, and a second run waits and names the
// run it is waiting for.
func TestOneVerifyRunsAtATimeAndTheSecondNamesTheFirst(t *testing.T) {
	root := fixture(t, "")
	p := Standard()
	first, _ := run(t, p, root)
	release, err := takeLock(first)
	if err != nil {
		t.Fatal(err)
	}

	second, narrated := run(t, p, root)
	waiting := make(chan error, 1)
	go func() {
		got, err := takeLock(second)
		if err == nil {
			got()
		}
		waiting <- err
	}()

	// The second run must still be waiting while the first holds the lock.
	select {
	case err := <-waiting:
		t.Fatalf("a second verify took the lock while the first held it: %v", err)
	case <-time.After(400 * time.Millisecond):
	}
	if !strings.Contains(narrated.String(), "waiting for the verify started at") {
		t.Errorf("the second run did not name what it waits for: %q", narrated.String())
	}
	if !strings.Contains(narrated.String(), fmt.Sprintf("pid %d", os.Getpid())) {
		t.Errorf("the second run did not name the run holding the lock: %q", narrated.String())
	}

	release()
	select {
	case err := <-waiting:
		if err != nil {
			t.Errorf("the second run never took the released lock: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("the second run did not take the lock after it was released")
	}
}

// A lock whose holder is gone is taken rather than waited on: a run killed
// between taking the lock and releasing it would otherwise refuse every verify
// in the checkout forever, and there is no person to tell that it is safe.
func TestALockWhoseHolderIsGoneIsTaken(t *testing.T) {
	root := fixture(t, "")
	// A pid no process has. Picking one that is merely unlikely would make the
	// test flaky; this one cannot name a process at all.
	write(t, root, filepath.FromSlash(VerifyLock), "-1 2026-09-17T00:00:00Z\n")

	r, narrated := run(t, Standard(), root)
	release, err := takeLock(r)
	if err != nil {
		t.Fatalf("a lock left by a run that is gone was waited on: %v", err)
	}
	release()
	if !strings.Contains(narrated.String(), "is gone") {
		t.Errorf("taking the abandoned lock was not reported: %q", narrated.String())
	}
}

func passing(name string, ran map[string]bool) Step {
	return Step{Name: name, Summary: name, Run: func(*Run) error { ran[name] = true; return nil }}
}

func failing(name string, ran map[string]bool) Step {
	return Step{Name: name, Summary: name, Run: func(*Run) error {
		ran[name] = true
		return fmt.Errorf("%s failed on purpose", name)
	}}
}
