package common

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// fixture lays down a two-module tree — a root module and a tools/build module —
// which is the shape every gate here measures across.
func fixture(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go toolchain")
	}
	root := t.TempDir()
	writeAt(t, root, "go.mod", "module example\n\ngo 1.26\n")
	writeAt(t, root, "a.go", "package example\n\nfunc Add(a, b int) int { return a + b }\n")
	writeAt(t, root, "a_test.go", "package example\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(1, 2) != 3 {\n\t\tt.Fatal(\"bad\")\n\t}\n}\n")
	writeAt(t, root, "tools/build/go.mod", "module example/tools/build\n\ngo 1.26\n")
	writeAt(t, root, "tools/build/b.go", "package build\n\nfunc Double(n int) int { return n * 2 }\n")
	return root
}

func writeAt(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func metric(t *testing.T, ms []Metric, name string) Metric {
	t.Helper()
	for _, m := range ms {
		if m.Name == name {
			return m
		}
	}
	t.Fatalf("no metric named %q in %v", name, ms)
	return Metric{}
}

// A gate measures and does not repair. gofmt -l LISTS what is unformatted; the
// mutating form is the single easiest way to turn this gate into a lie, so the
// file must still be unformatted afterwards.
func TestMeasureFormattedCountsAndChangesNothing(t *testing.T) {
	root := fixture(t)
	const ugly = "package example\n\nfunc  Ugly( ) int {  return 1  }\n"
	writeAt(t, root, "ugly.go", ugly)

	ms, incomplete, err := measureFormatted(root, modules(root))
	if err != nil {
		t.Fatal(err)
	}
	if incomplete != "" {
		t.Errorf("a complete run reported incomplete: %q", incomplete)
	}
	if got := metric(t, ms, "unformatted_files"); got.Int < 1 {
		t.Errorf("unformatted_files = %d, want at least 1", got.Int)
	}
	if after, _ := os.ReadFile(filepath.Join(root, "ugly.go")); string(after) != ugly {
		t.Error("the gate rewrote the file it was measuring")
	}
}

func TestMeasureFormattedOnACleanTree(t *testing.T) {
	root := fixture(t)
	ms, _, err := measureFormatted(root, modules(root))
	if err != nil {
		t.Fatal(err)
	}
	if got := metric(t, ms, "unformatted_files"); got.Int != 0 {
		t.Errorf("unformatted_files = %d on a formatted tree, want 0", got.Int)
	}
}

func TestMeasureBuildsCountsUnbuildablePackages(t *testing.T) {
	root := fixture(t)
	ms, _, err := measureBuilds(root, modules(root))
	if err != nil {
		t.Fatal(err)
	}
	if got := metric(t, ms, "unbuildable_packages"); got.Int != 0 {
		t.Fatalf("a sound tree reported %d unbuildable packages", got.Int)
	}

	writeAt(t, root, "broken.go", "package example\n\nfunc Broken() int { return \"not an int\" }\n")
	ms, _, err = measureBuilds(root, modules(root))
	if err != nil {
		t.Fatal(err)
	}
	if got := metric(t, ms, "unbuildable_packages"); got.Int < 1 {
		t.Errorf("unbuildable_packages = %d after breaking a package, want at least 1", got.Int)
	}
}

// The second module must actually be reached. A gate that silently covered only
// the root would report a tree sound while half of it was unmeasured.
func TestMeasureBuildsReachesTheSecondModule(t *testing.T) {
	root := fixture(t)
	writeAt(t, root, "tools/build/broken.go", "package build\n\nfunc B() int { return \"nope\" }\n")
	ms, _, err := measureBuilds(root, modules(root))
	if err != nil {
		t.Fatal(err)
	}
	if got := metric(t, ms, "unbuildable_packages"); got.Int < 1 {
		t.Error("a break in tools/build was not seen")
	}
}

func TestMeasureCheckedCountsVetFindings(t *testing.T) {
	root := fixture(t)
	ms, _, err := measureChecked(root, modules(root))
	if err != nil {
		t.Fatal(err)
	}
	if got := metric(t, ms, "vet_findings"); got.Int != 0 {
		t.Fatalf("a clean tree reported %d vet findings", got.Int)
	}

	writeAt(t, root, "vet.go", "package example\n\nimport \"fmt\"\n\nfunc V() { fmt.Printf(\"%d\") }\n")
	ms, _, err = measureChecked(root, modules(root))
	if err != nil {
		t.Fatal(err)
	}
	if got := metric(t, ms, "vet_findings"); got.Int < 1 {
		t.Errorf("vet_findings = %d after adding a bad Printf, want at least 1", got.Int)
	}
}

func TestMeasureTestedCountsFailures(t *testing.T) {
	root := fixture(t)
	ms, _, err := measureTested(root, modules(root))
	if err != nil {
		t.Fatal(err)
	}
	if got := metric(t, ms, "failed_tests"); got.Int != 0 {
		t.Fatalf("a green suite reported %d failed tests", got.Int)
	}

	writeAt(t, root, "fail_test.go", "package example\n\nimport \"testing\"\n\nfunc TestFails(t *testing.T) { t.Fatal(\"deliberate\") }\n")
	ms, _, err = measureTested(root, modules(root))
	if err != nil {
		t.Fatal(err)
	}
	if got := metric(t, ms, "failed_tests"); got.Int < 1 {
		t.Errorf("failed_tests = %d after adding a failing test, want at least 1", got.Int)
	}
	if got := metric(t, ms, "failed_packages"); got.Int < 1 {
		t.Errorf("failed_packages = %d, want at least 1", got.Int)
	}
}

func TestMeasureCoveredReportsAPercentage(t *testing.T) {
	root := fixture(t)
	ms, _, err := measureCovered(root, modules(root))
	if err != nil {
		t.Fatal(err)
	}
	got := metric(t, ms, "statement_coverage")
	if got.Unit != "percent" {
		t.Errorf("unit = %q, want percent", got.Unit)
	}
	if got.Float <= 0 || got.Float > 100 {
		t.Errorf("statement_coverage = %v, want a percentage in (0,100]", got.Float)
	}
}

// Percentages from two modules cannot be averaged, so the combination is done on
// statement counts read out of the profiles.
func TestCoverageCounts(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "cover.out")
	body := "mode: set\n" +
		"example/a.go:1.1,2.2 3 1\n" +
		"example/a.go:3.1,4.2 5 0\n" +
		"example/b.go:1.1,2.2 2 7\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	stmts, hit, err := coverageCounts(p)
	if err != nil {
		t.Fatal(err)
	}
	if stmts != 10 || hit != 5 {
		t.Errorf("coverageCounts = (%d, %d), want (10, 5)", stmts, hit)
	}
}

func TestCoverageCountsRejectsAMalformedProfile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "bad.out")
	if err := os.WriteFile(p, []byte("mode: set\nnonsense\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := coverageCounts(p); err == nil {
		t.Error("a malformed profile line was accepted")
	}
	if _, _, err := coverageCounts(filepath.Join(dir, "absent.out")); err == nil {
		t.Error("a missing profile was accepted")
	}
}

func TestCountLines(t *testing.T) {
	for in, want := range map[string]int{"": 0, "a": 1, "a\nb": 2, "a\nb\n": 2, "\n\n": 0} {
		if got := countLines(in); got != want {
			t.Errorf("countLines(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestModuleLabelsAndInstances(t *testing.T) {
	root := fixture(t)
	labels := ModuleLabels(root)
	if len(labels) != 2 || labels[0] != "root" || labels[1] != "tools-build" {
		t.Fatalf("ModuleLabels = %v, want [root tools-build]", labels)
	}
	if dirs, err := modulesFor(root, "tools-build"); err != nil || len(dirs) != 1 {
		t.Errorf("modulesFor(tools-build) = (%v, %v), want one dir", dirs, err)
	}
	if dirs, err := modulesFor(root, ""); err != nil || len(dirs) != 2 {
		t.Errorf("an empty instance must mean every module, got %v", dirs)
	}
	if _, err := modulesFor(root, "nope"); err == nil {
		t.Error("an unknown instance was accepted")
	}
}

// A repository with only a root module offers only that instance: a narrowing
// that does not exist must not be advertised.
func TestModuleLabelsWithOnlyARootModule(t *testing.T) {
	root := t.TempDir()
	writeAt(t, root, "go.mod", "module example\n")
	if labels := ModuleLabels(root); len(labels) != 1 || labels[0] != "root" {
		t.Errorf("ModuleLabels = %v, want [root]", labels)
	}
}

func TestKnownGateJudgesConceptAndInstance(t *testing.T) {
	root := fixture(t)
	for _, name := range []string{"tested", "tested:root", "tested:tools-build", "fit", "integration"} {
		if !KnownGate(root, name) {
			t.Errorf("%q is not known but should be", name)
		}
	}
	for _, name := range []string{"test", "build", "tested:nope", "fit:disk", "integration:root", "formatted:root"} {
		if KnownGate(root, name) {
			t.Errorf("%q is known but should not be", name)
		}
	}
}

func TestGateSummaryCarriesThroughAnInstance(t *testing.T) {
	if GateSummary("tested") == "" {
		t.Error("a known gate has no summary")
	}
	if GateSummary("tested:root") != GateSummary("tested") {
		t.Error("an instance must carry its concept's summary")
	}
	if GateSummary("nope") != "" {
		t.Error("an unknown gate reported a summary")
	}
}

func TestSplitGateName(t *testing.T) {
	for in, want := range map[string][2]string{
		"tested":             {"tested", ""},
		"tested:root":        {"tested", "root"},
		"tested:tools-build": {"tested", "tools-build"},
	} {
		c, i := splitGateName(in)
		if c != want[0] || i != want[1] {
			t.Errorf("splitGateName(%q) = (%q,%q), want %v", in, c, i, want)
		}
	}
}

func TestMeasureGateRefusesAnInstanceOnANonNarrowingGate(t *testing.T) {
	root := fixture(t)
	for _, name := range []string{"formatted:root", "integration:root", "fit:root"} {
		if _, err := MeasureGate(root, name); err == nil {
			t.Errorf("%q was measured but takes no instance", name)
		}
	}
}

func TestMeasureGateComposesIntegrationFromItsParts(t *testing.T) {
	root := fixture(t)
	env, err := MeasureGate(root, "integration")
	if err != nil {
		t.Fatal(err)
	}
	if env.Gate != "integration" {
		t.Errorf("envelope names %q", env.Gate)
	}
	for _, want := range []string{"unformatted_files", "unbuildable_packages", "vet_findings", "failed_tests"} {
		metric(t, env.Metrics, want)
	}
}

func TestPlatformHelpers(t *testing.T) {
	if BinaryName("verify") != "verify"+ExeSuffix() {
		t.Error("BinaryName does not append the platform suffix")
	}
	if IsWindows() != (ExeSuffix() == ".exe") {
		t.Error("ExeSuffix and IsWindows disagree")
	}
	if Which("definitely-not-a-real-command-xyz") != "" {
		t.Error("Which found a command that does not exist")
	}
	if Which("go") == "" {
		t.Error("Which could not find the go toolchain")
	}
	if !Exists(t.TempDir()) || Exists(filepath.Join(t.TempDir(), "nope")) {
		t.Error("Exists is wrong about a directory or a missing path")
	}
}

func TestRunSilentDiscardsOutput(t *testing.T) {
	if err := RunSilent("go", "version"); err != nil {
		t.Errorf("RunSilent(go version): %v", err)
	}
	if err := RunSilent("definitely-not-a-real-command-xyz"); err == nil {
		t.Error("RunSilent reported success for a missing command")
	}
}

func TestIsDiagnostic(t *testing.T) {
	for line, want := range map[string]bool{
		"./a.go:5:2: Printf format %d reads arg #1": true,
		"# example":           false,
		"":                    false,
		"ok  \texample\t0.1s": false,
	} {
		if got := isDiagnostic(line); got != want {
			t.Errorf("isDiagnostic(%q) = %t, want %t", line, got, want)
		}
	}
}

func TestTotalCoverage(t *testing.T) {
	out := "example/a.go:1:\tAdd\t100.0%\ntotal:\t(statements)\t42.9%\n"
	pct, ok := totalCoverage(out)
	if !ok || pct != 42.9 {
		t.Errorf("totalCoverage = (%v, %t), want (42.9, true)", pct, ok)
	}
	if _, ok := totalCoverage("no total here"); ok {
		t.Error("a total was read out of output that has none")
	}
}
