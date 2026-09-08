package common

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The defining property: a gate MEASURES and does not modify what it measures.
//
// `formatted` is the one where getting this wrong is easiest and least visible,
// because the mutating form of the same tool is one flag away — verify runs
// `gofmt -w`, this must run `gofmt -l`. A gate that formatted the tree on its
// way to reporting it formatted would pass always, and its answer would
// describe a tree that did not exist when it started.
func TestGateFormattedDetectsWithoutModifying(t *testing.T) {
	root := goModuleForTest(t)
	bad := "package p\n\nfunc  Badly( ) {\n}\n"
	path := filepath.Join(root, "bad.go")
	writeFile(t, path, bad)

	err := RunGate(root, "formatted")
	if err == nil {
		t.Fatal("formatted passed on an unformatted tree")
	}
	if !strings.Contains(err.Error(), "bad.go") {
		t.Errorf("failure does not name the offending file: %v", err)
	}
	if got := readFile(t, path); got != bad {
		t.Errorf("the gate MODIFIED the file it was measuring:\n%s", got)
	}
}

func TestGateFormattedPassesOnAFormattedTree(t *testing.T) {
	root := goModuleForTest(t)
	writeFile(t, filepath.Join(root, "ok.go"), "package p\n")
	if err := RunGate(root, "formatted"); err != nil {
		t.Errorf("formatted failed on a formatted tree: %v", err)
	}
}

// gofmt exits 0 whether or not it found anything, so a runner checking only the
// exit status would pass every time. The OUTPUT is the verdict.
func TestGateFormattedDoesNotTrustExitStatus(t *testing.T) {
	root := goModuleForTest(t)
	writeFile(t, filepath.Join(root, "bad.go"), "package p\n\nfunc  X( ) {\n}\n")
	if err := RunGate(root, "formatted"); err == nil {
		t.Error("formatted read gofmt's exit status instead of its output")
	}
}

// An undeclared concept must be refused rather than run. Addressing gates by
// name is what keeps this from being an arbitrary command executor, and that
// only holds if unknown names stop here.
func TestRunGateRefusesUnknownConcepts(t *testing.T) {
	err := RunGate(t.TempDir(), "definitely-not-a-gate")
	if err == nil {
		t.Fatal("unknown concept accepted")
	}
	if !strings.Contains(err.Error(), "this project provides") {
		t.Errorf("error does not say what could have been asked for: %v", err)
	}
}

// Instances that narrow nothing must be refused rather than silently ignored.
// `formatted:root` would advertise a narrowing that does not exist, since gofmt
// walks directories and every module is under the root.
func TestRunGateRefusesInstancesOnWholeTreeGates(t *testing.T) {
	for _, name := range []string{"formatted:root", "integration:root"} {
		if err := RunGate(t.TempDir(), name); err == nil {
			t.Errorf("%s accepted; it narrows nothing", name)
		}
	}
}

func TestRunGateRefusesUnknownInstances(t *testing.T) {
	err := RunGate(t.TempDir(), "tested:nosuchmodule")
	if err == nil {
		t.Fatal("unknown instance accepted")
	}
	if !strings.Contains(err.Error(), "this project has") {
		t.Errorf("error does not list the real instances: %v", err)
	}
}

// gateNamesExpectation is the drift guard for the gaterun.go mirror.
//
// The identical literal appears in workspacetool/gaterun_test.go and is
// asserted against that package's copy of this runner. Admission consults the
// copy to decide whether a tracker-declared gate is one this project answers,
// so the two answering differently means a gate the tracker asked for is
// silently skipped there while `bin/gate` would have run it — a disagreement
// that produces no error anywhere, only a check that did not happen.
//
// Spelled out rather than derived from `modules` and `providedConcepts`: a test
// that recomputes the answer agrees with any change by construction, including
// a wrong one. Keep the two literals identical.
var gateNamesExpectation = []string{
	"builds", "builds:root", "builds:tools-build",
	"checked", "checked:root", "checked:tools-build",
	"fit",
	"formatted",
	"integration",
	"tested", "tested:root", "tested:tools-build",
}

func TestGateNamesMatchesTheDeclaredSet(t *testing.T) {
	got := GateNames()
	if len(got) != len(gateNamesExpectation) {
		t.Fatalf("GateNames() = %v\nwant %v", got, gateNamesExpectation)
	}
	for i, want := range gateNamesExpectation {
		if got[i] != want {
			t.Errorf("GateNames()[%d] = %q, want %q (full: %v)", i, got[i], want, got)
		}
	}
}

// Omitting the instance must measure EVERY module. A gate that quietly covered
// a subset would report a tree sound while part of it was never measured —
// which is worse than not having the gate, because it reads as coverage.
func TestGateNamesCoverEveryModule(t *testing.T) {
	names := GateNames()
	for _, m := range modules {
		want := "tested:" + moduleLabel(m)
		if !contains(names, want) {
			t.Errorf("no name addresses module %s (expected %q in %v)", m, want, names)
		}
	}
	// And the bare concept must be offered, since that is what runs them all.
	if !contains(names, "tested") {
		t.Error("the bare `tested` concept is not offered, so nothing runs every module")
	}
}

// fit is required of every project — gates-and-commands.md lists it beside
// verify, integration and the judge — and a project that does not answer it is
// refused by `issue resolve` before its first step runs. So its presence is a
// contract, not a convenience.
func TestFitIsProvidedAndAnswers(t *testing.T) {
	if err := RunGate(t.TempDir(), "fit"); err != nil {
		t.Fatalf("fit must answer for this project: %v", err)
	}
	if !contains(GateNames(), "fit") {
		t.Errorf("fit is not offered by name: %v", GateNames())
	}
	if !contains(providedConcepts(), "fit") {
		t.Errorf("fit is missing from the concepts an unknown name is reported against: %v", providedConcepts())
	}
}

// fit measures the machine, so a module cannot narrow it. Its instances name
// conditions on the host (`fit:disk`), and offering `fit:root` would advertise
// a narrowing that means nothing — the same rule formatted and integration follow.
func TestFitTakesNoModuleInstance(t *testing.T) {
	if err := RunGate(t.TempDir(), "fit:root"); err == nil {
		t.Error("fit:root accepted; a module does not narrow a measurement of the host")
	}
	for _, n := range GateNames() {
		if strings.HasPrefix(n, "fit:") {
			t.Errorf("GateNames advertises %q, but fit does not divide by module", n)
		}
	}
}

// fit must NOT be part of integration. A machine that cannot build is not a
// change that may not land, so folding the two would fail an honest change for
// a fact about whichever host happened to run it.
func TestFitIsNotPartOfIntegration(t *testing.T) {
	for _, c := range measurementOrder {
		if c == GateFit {
			t.Fatal("fit is in the measurement composition; it measures the host, not the tree")
		}
	}
}

// integration is the gate a landing decision rests on, so it must exist and
// must be a composition rather than a single measurement.
func TestIntegrationRunsEveryOtherGate(t *testing.T) {
	root := goModuleForTest(t)
	// An unformatted tree must fail integration, which proves formatted is in
	// the composition rather than merely offered alongside it.
	writeFile(t, filepath.Join(root, "bad.go"), "package p\n\nfunc  X( ) {\n}\n")

	if err := RunGate(root, "integration"); err == nil {
		t.Error("integration passed on a tree that fails one of its constituents")
	}
}

// MeasureGate builds the envelope that both `bin/gate --envelope` and
// `bin/run <gate>` act on, so its shape is a contract rather than a detail.
func TestMeasureGateReportsAPassingGate(t *testing.T) {
	root := goModuleForTest(t)
	writeFile(t, filepath.Join(root, "ok.go"), "package p\n")

	env, err := MeasureGate(root, "formatted")
	if err != nil {
		t.Fatalf("MeasureGate on a formatted tree: %v", err)
	}
	if env["gate"] != "formatted" {
		t.Errorf("envelope names the wrong gate: %v", env["gate"])
	}
	if env["measured"] != true {
		t.Errorf("a passing gate must report measured=true, got %v", env["measured"])
	}
	if _, ok := env["detail"]; ok {
		t.Errorf("a passing gate carries no detail, got %v", env["detail"])
	}
	if _, ok := env["elapsed_seconds"].(float64); !ok {
		t.Errorf("envelope has no elapsed_seconds: %v", env["elapsed_seconds"])
	}
}

// A gate that measured a failure HAS measured. The distinction is the whole
// reason the envelope carries `measured` rather than an exit status: a failing
// measurement is a fact to judge, not an error that prevented judging.
func TestMeasureGateReportsAFailingGateAsMeasured(t *testing.T) {
	root := goModuleForTest(t)
	writeFile(t, filepath.Join(root, "bad.go"), "package p\n\nfunc  X( ) {\n}\n")

	env, err := MeasureGate(root, "formatted")
	if err == nil {
		t.Fatal("MeasureGate returned no error for a failing gate")
	}
	if env["measured"] != false {
		t.Errorf("a failing gate must report measured=false, got %v", env["measured"])
	}
	detail, _ := env["detail"].(string)
	if !strings.Contains(detail, "bad.go") {
		t.Errorf("detail does not name the offending file: %q", detail)
	}
}

// The load-bearing side effect: MeasureGate moves the gate's own progress off
// stdout. `bin/gate --envelope` writes the envelope to stdout and NOTHING else,
// so a gate that printed there would corrupt the document a runner parses —
// and the corruption would survive as far as the parse, not the gate.
func TestMeasureGateLeavesStdoutRestored(t *testing.T) {
	before := os.Stdout
	root := goModuleForTest(t)
	writeFile(t, filepath.Join(root, "ok.go"), "package p\n")

	if _, err := MeasureGate(root, "formatted"); err != nil {
		t.Fatalf("MeasureGate: %v", err)
	}
	if os.Stdout != before {
		t.Error("MeasureGate did not restore os.Stdout, so the caller's product would go to stderr")
	}
}

// goModuleForTest returns a directory holding a minimal Go module, so the go
// tool has something coherent to act on.
func goModuleForTest(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.test\n\ngo 1.26\n")
	return dir
}

func contains(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}
