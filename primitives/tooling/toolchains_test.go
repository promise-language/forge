package tooling

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A run that failed, and reported nothing countable, could not be measured:
// "zero failures" is not a reading of a run that did not happen. The three
// concepts answer differently over the same broken unit, and the difference is
// what each toolchain actually reported.
//
// It measures a real unit with a real toolchain because the rule is about what
// `go build`, `go vet` and `go test` write when a package does not compile, and
// a fixture standing in for them would only restate what this test is here to
// check.
func TestARunThatReportedNothingCountableIsNotZero(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go toolchain")
	}
	root := fixture(t, "")
	// It parses, so `go list` loads the package and every measurement is
	// reached; it does not type-check, so none of them completes normally.
	write(t, root, "x.go", "package x\n\nfunc F() { return 1 }\n")
	git(t, root, "add", "-A")
	r, _ := run(t, Standard(), root)
	unit := Unit{Toolchain: "go"}

	// go build prefixes the package it could not compile, so there is something
	// to count and the count is the measurement.
	built, err := goBuilds(r, unit)
	if err != nil {
		t.Fatalf("a package that does not compile was reported as a failure to measure: %v", err)
	}
	if n := tally(t, built, "unbuildable_packages"); n != 1 {
		t.Errorf("unbuildable_packages = %d, want the one package that did not compile", n)
	}

	// go vet printed no JSON at all. That is not zero findings: it is a
	// toolchain that did not report, and absorbed as a zero it would read as a
	// clean tree.
	if got, err := goChecked(r, unit); err == nil {
		t.Errorf("a vet run that printed no JSON was read as %+v, want nothing measured", got.Counts)
	}

	// go test did report events, and a package that failed to build is one
	// failing package rather than a failing test.
	tested, err := goTested(r, unit)
	if err != nil {
		t.Fatalf("a test run that reported events was read as nothing measured: %v", err)
	}
	if n := tally(t, tested, "failed_packages"); n != 1 {
		t.Errorf("failed_packages = %d, want 1", n)
	}
	if n := tally(t, tested, "failed_tests"); n != 0 {
		t.Errorf("failed_tests = %d, want a build failure not counted as a failing test", n)
	}
}

// A coverage profile is scratch, so it goes under .home/tmp/ in a directory
// unique to the run. A gate that dropped it in the worktree would have modified
// the subject it was measuring, and one that dropped it in the system's
// temporary directory would have written outside the repository root.
func TestTheCoverageProfileIsScratchInsideTheCheckout(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go toolchain")
	}
	root := fixture(t, "")
	write(t, root, "x.go", "package x\n\nfunc F() int { return 1 }\n")
	write(t, root, "x_test.go", "package x\n\nimport \"testing\"\n\nfunc TestF(t *testing.T) { F() }\n")
	git(t, root, "add", "-A")
	before := git(t, root, "status", "--porcelain")
	r, _ := run(t, Standard(), root)

	res, err := goCovered(r, Unit{Toolchain: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Ratios) != 1 || res.Ratios[0].Name != "statement_coverage" || res.Ratios[0].Whole == 0 {
		t.Errorf("coverage came back as %+v, want statements counted", res.Ratios)
	}
	// Nothing went wrong, so there is no reason to give. An incomplete run is
	// never a pass, and one reported over a green run would fail it.
	if res.Incomplete != "" {
		t.Errorf("a run where everything built and passed reported %q, want no reason", res.Incomplete)
	}

	if want := filepath.Join(root, filepath.FromSlash(ScratchDir)); !strings.HasPrefix(r.ScratchRoot(), want) {
		t.Fatalf("this run's scratch is %q, want it under %s", r.ScratchRoot(), want)
	}
	if _, err := os.Stat(r.Scratch("cover-root.out")); err != nil {
		t.Errorf("the profile is not in this run's scratch: %v", err)
	}
	if after := git(t, root, "status", "--porcelain"); after != before {
		t.Errorf("the measurement left the tracked tree changed:\nbefore\n%s\nafter\n%s", before, after)
	}
}

// A package that did not build and a package whose tests failed are different
// facts about a coverage run, and the reason names the one that happened. The
// first case is the one this rule was written from: two Go installations on
// PATH, `go tool compile` refusing to run, and no test failing at all. It is a
// condition a test cannot stage, so what the toolchain wrote is what is read
// here.
func TestABuildFailureIsNotATestFailure(t *testing.T) {
	const compilerVersions = `compile: version "go1.26.0" does not match go tool version "go1.25.5"
# internal/coverage
compile: version "go1.26.0" does not match go tool version "go1.25.5"
`
	const built = "some packages did not build, so their statements were not exercised at all: "
	const tested = "some packages failed their tests, so their statements were only partly exercised"

	for _, c := range []struct{ name, stderr, want string }{
		{
			"a toolchain that would not compile",
			compilerVersions,
			built + `compile: version "go1.26.0" does not match go tool version "go1.25.5"`,
		},
		{
			// The header names the package without saying what is wrong with it,
			// so the diagnostic under it is what a reader needs.
			"a package that does not compile",
			"# example.com/x/bad\n./bad.go:3:19: too many return values\n",
			built + "./bad.go:3:19: too many return values",
		},
		{
			"a run whose stderr said nothing",
			"",
			tested,
		},
		{
			// `go test` writes this to stderr on a run whose tests then failed.
			// Read as a build failure it would send a reader to a build that
			// worked.
			"stderr that names no package",
			"go: downloading example.com/m v1.2.3\n",
			tested,
		},
		{
			"a header and nothing else",
			"# example.com/x/bad\n",
			built + "# example.com/x/bad",
		},
		{
			// The header is still all the child said. A reason that stopped at
			// the colon would name a build failure and carry nothing to act on.
			"a header behind a blank line",
			"\n# example.com/x/bad\n",
			built + "# example.com/x/bad",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := coverageReason(c.stderr); got != c.want {
				t.Errorf("coverageReason =\n%q\nwant\n%q", got, c.want)
			}
		})
	}
}

// The packages that did build are still measured, so the run reports coverage
// and says why it is less than a green one's. It measures a real unit with a
// real toolchain because the rule is about what `go test -coverprofile` writes
// and produces when one package of several does not compile.
func TestCoverageNamesTheBuildFailureItFound(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go toolchain")
	}
	root := fixture(t, "")
	write(t, root, filepath.Join("good", "good.go"), "package good\n\nfunc F() int { return 1 }\n")
	write(t, root, filepath.Join("good", "good_test.go"),
		"package good\n\nimport \"testing\"\n\nfunc TestF(t *testing.T) { F() }\n")
	// It parses, so `go list` loads it and the run reaches it; it does not
	// type-check, so it is a package that never compiled rather than one whose
	// tests failed.
	write(t, root, filepath.Join("bad", "bad.go"), "package bad\n\nfunc G() { return 1 }\n")
	git(t, root, "add", "-A")
	r, _ := run(t, Standard(), root)

	res, err := goCovered(r, Unit{Toolchain: "go"})
	if err != nil {
		t.Fatalf("a run that produced a profile was read as nothing measured: %v", err)
	}
	if len(res.Ratios) != 1 || res.Ratios[0].Whole == 0 {
		t.Errorf("coverage came back as %+v, want the statements of the package that built", res.Ratios)
	}
	if !strings.Contains(res.Incomplete, "did not build") {
		t.Errorf("the reason is %q, want it to name the build failure", res.Incomplete)
	}
	if strings.Contains(res.Incomplete, "failed their tests") {
		t.Errorf("the reason is %q, and no test failed", res.Incomplete)
	}
	if !strings.Contains(res.Incomplete, "too many return values") {
		t.Errorf("the reason is %q, want it to carry what the compiler said", res.Incomplete)
	}
}

// A run that produced no readable profile measured nothing, and the error says
// what the child said. An exit status on its own names no repair.
func TestCoverageThatMeasuredNothingSaysWhatTheChildSaid(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go toolchain")
	}
	root := fixture(t, "")
	write(t, root, "x.go", "package x\n\nfunc F() { return 1 }\n")
	git(t, root, "add", "-A")
	r, _ := run(t, Standard(), root)

	res, err := goCovered(r, Unit{Toolchain: "go"})
	if err == nil {
		t.Fatalf("a run where nothing compiled was measured as %+v", res.Ratios)
	}
	if !strings.Contains(err.Error(), "too many return values") {
		t.Errorf("the error is %q, want it to carry what the compiler said", err)
	}
}

// A profile can also be unreadable after a run that succeeded — a unit that
// declares no statements produces one with nothing but its mode header. Nothing
// failed there, so the error names no failure: a reader told the run said
// "<nil>" goes looking for one that did not happen, which is the same wrong turn
// a build failure reported as a test failure sends them on.
func TestCoverageThatMeasuredNothingOverARunThatSucceeded(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go toolchain")
	}
	root := fixture(t, "")
	// It compiles and it has no statements to cover, so `go test` exits 0 and
	// writes a profile holding only `mode: set`.
	write(t, root, "x.go", "package x\n\ntype T struct{ A int }\n")
	git(t, root, "add", "-A")
	r, _ := run(t, Standard(), root)

	res, err := goCovered(r, Unit{Toolchain: "go"})
	if err == nil {
		t.Fatalf("a profile naming no statements was measured as %+v", res.Ratios)
	}
	if !strings.Contains(err.Error(), "named no statements") {
		t.Errorf("the error is %q, want it to name the profile it could not read", err)
	}
	if strings.Contains(err.Error(), "<nil>") {
		t.Errorf("the error is %q, and the test run did not fail", err)
	}
}

func tally(t *testing.T, res UnitResult, name string) int64 {
	t.Helper()
	for _, c := range res.Counts {
		if c.Name == name {
			return c.N
		}
	}
	t.Fatalf("the result reports no %s: %+v", name, res.Counts)
	return 0
}

// A count is read from a structured report wherever the toolchain has one,
// never by matching prose meant for a person. `go vet -json` splits its report
// across both streams — an empty object per quiet package on stdout, the
// findings under a "# package" header on stderr — so both are read and the
// objects counted wherever they came from.
func TestVetFindingsAreReadFromTheStructuredReport(t *testing.T) {
	const findings = `# example.com/x
{
	"x": {
		"printf": [
			{"posn": "x.go:5:24", "message": "wrong type"},
			{"posn": "x.go:9:2", "message": "wrong type"}
		],
		"copylocks": [{"posn": "x.go:12:1", "message": "passes a lock"}]
	}
}`
	for _, c := range []struct {
		name     string
		report   string
		want     int64
		reported bool
	}{
		{"quiet packages report empty objects", "{}\n{}\n{}\n", 0, true},
		{"findings are counted per analyzer", findings, 3, true},
		{"both streams together", "{}\n{}\n" + findings, 3, true},
		// A vet run that printed no JSON at all is not zero findings. It is a
		// toolchain that did not report, and nothing was measured.
		{"nothing countable was reported", "", 0, false},
		{"only the headers arrived", "# example.com/x\n", 0, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, reported := countVetFindings(c.report)
			if got != c.want || reported != c.reported {
				t.Errorf("countVetFindings = (%d, %v), want (%d, %v)", got, reported, c.want, c.reported)
			}
		})
	}
}

// `go test -json` reports one record per event: a record with a Test is about
// one test, and one without is about the package that holds them. One failing
// test in one package and forty in forty are different situations, and a single
// number cannot tell them apart.
func TestTestEventsAreCountedByWhatTheyAreAbout(t *testing.T) {
	const stream = `{"Action":"run","Test":"TestOne"}
{"Action":"pass","Test":"TestOne"}
{"Action":"run","Test":"TestTwo"}
{"Action":"fail","Test":"TestTwo"}
{"Action":"run","Test":"TestTwo/sub"}
{"Action":"fail","Test":"TestTwo/sub"}
{"Action":"fail","Package":"example.com/x"}
{"Action":"pass","Package":"example.com/y"}`

	tests, packages, count, read := countTestEvents(stream)
	if !read {
		t.Fatal("a stream of events reported nothing")
	}
	if tests != 2 || packages != 1 || count != 3 {
		t.Errorf("(failed, packages, count) = (%d, %d, %d), want (2, 1, 3)", tests, packages, count)
	}

	// A run that did not happen reports nothing countable, and "zero failing
	// tests" is not a reading of it.
	if _, _, _, read := countTestEvents("# example.com/x\nbuild failed\n"); read {
		t.Error("a build failure was read as a test run")
	}
}

// Statement counts come from the profile rather than from `go tool cover
// -func`, which prints a percentage and not the counts behind it. Summing the
// counts is what makes a figure spanning units truthful.
func TestCoverageCountsComeFromTheProfile(t *testing.T) {
	root := t.TempDir()
	profile := filepath.Join(root, "cover.out")
	write(t, root, "cover.out", "mode: set\n"+
		"example.com/x/a.go:1.1,2.2 5 1\n"+
		"example.com/x/a.go:3.1,4.2 3 0\n"+
		"example.com/x/b.go:1.1,2.2 2 7\n")

	hit, statements, err := coverageCounts(profile)
	if err != nil {
		t.Fatal(err)
	}
	if hit != 7 || statements != 10 {
		t.Errorf("(hit, statements) = (%d, %d), want (7, 10)", hit, statements)
	}

	for _, c := range []struct{ name, body string }{
		{"a profile naming no statements", "mode: set\n"},
		{"a line that is not a profile line", "mode: set\nnonsense\n"},
		{"a statement count that is not a number", "mode: set\na.go:1.1,2.2 x 1\n"},
		{"an execution count that is not a number", "mode: set\na.go:1.1,2.2 1 x\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			write(t, root, "bad.out", c.body)
			if _, _, err := coverageCounts(filepath.Join(root, "bad.out")); err == nil {
				t.Error("an unreadable profile was measured")
			}
		})
	}
	if _, _, err := coverageCounts(filepath.Join(root, "never-written.out")); err == nil {
		t.Error("a profile that was never produced was measured")
	}
}

// The Promise toolchain reads its own record streams, and its metric names are
// its own: a Go profile counts statements and a Promise run counts blocks, so
// one name over two counts would mean whichever toolchain last reported.
func TestThePromiseReportsAreReadFromTheirOwnRecords(t *testing.T) {
	const tests = `{"kind":"test","result":"passed"}
{"kind":"test","result":"failed"}
{"kind":"test","result":"leaked"}
{"kind":"note","result":"ignored"}`
	failed, leaked, count, read := countPromiseTests(tests)
	if !read || failed != 1 || leaked != 1 || count != 3 {
		t.Errorf("(failed, leaked, count, read) = (%d, %d, %d, %v), want (1, 1, 3, true)", failed, leaked, count, read)
	}

	const coverage = `{"kind":"coverage","covered":3,"total":4}
{"kind":"test","result":"passed"}
{"kind":"coverage","covered":1,"total":6}`
	covered, total, seen := countPromiseCoverage(coverage)
	if !seen || covered != 4 || total != 10 {
		t.Errorf("(covered, total, read) = (%d, %d, %v), want (4, 10, true)", covered, total, seen)
	}
	if _, _, seen := countPromiseCoverage(`{"kind":"test","result":"passed"}`); seen {
		t.Error("a run with no coverage records reported coverage")
	}

	if got := Promise().Metrics[Covered][0].Name; got != "block_coverage" {
		t.Errorf("Promise's coverage metric is %q, want its own name", got)
	}
	if got := Go().Metrics[Covered][0].Name; got != "statement_coverage" {
		t.Errorf("Go's coverage metric is %q, want its own name", got)
	}
}

// A diagnostic is a line naming a source position, which is the form both
// checkers print. The headers that group them are not findings.
func TestADiagnosticIsALineNamingASourcePosition(t *testing.T) {
	const output = `# example.com/x
x.pr:12:4: something is wrong
x.pr:99: only one number, so not a position
a note with no colons at all
y.pr:1:1: something else is wrong`

	if got := countDiagnostics(output); got != 2 {
		t.Errorf("countDiagnostics = %d, want the two lines naming a position", got)
	}
	if got := countDiagnosticModules(output); got != 1 {
		t.Errorf("countDiagnosticModules = %d, want the one header", got)
	}
	if !isDiagnostic("a.pr:1:2: msg") || isDiagnostic("a.pr:x:2: msg") || isDiagnostic("nope") {
		t.Error("isDiagnostic does not read a source position")
	}
	if got := countPrefixed("--- FAIL: A\n    --- FAIL: A/sub\nok\n", "--- FAIL:"); got != 2 {
		t.Errorf("countPrefixed = %d, want an indented subtest counted", got)
	}
	if got := firstLine("one\ntwo"); got != "one" {
		t.Errorf("firstLine = %q", got)
	}
}

// present is what `fit:toolchain` counts, and it answers on two grounds: a
// program that is not on PATH, and a program that is there and cannot state a
// version. Both are measured here against toolchains this test declares, rather
// than through the gate: the gate-level test can only measure the absence of a
// toolchain this machine happens not to have, so it skips itself on any machine
// that has Promise installed — and a rule nothing measures on a developer's own
// machine is a rule nothing measures.
func TestAToolchainIsAbsentWhenItsProgramIsMissingOrCannotStateAVersion(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go toolchain")
	}
	r, _ := run(t, Standard(), fixture(t, ""))

	for _, c := range []struct {
		name      string
		toolchain Toolchain
		names     []string // absent, and the reason says these; present when empty
	}{
		{
			name:      "a program that is not on PATH",
			toolchain: Toolchain{Name: "invented", Programs: []string{"definitely-not-a-real-program-xyz"}},
			names:     []string{"invented", "definitely-not-a-real-program-xyz", "not on PATH"},
		},
		{
			name:      "a program that is there and cannot state a version",
			toolchain: Toolchain{Name: "invented", Programs: []string{"go"}, Version: []string{"go", "definitely-not-a-subcommand"}},
			names:     []string{"invented", "does not state a version"},
		},
		{
			name:      "every program on PATH, with no version to state",
			toolchain: Toolchain{Name: "invented", Programs: []string{"go"}},
		},
		{
			name:      "a program that states its version",
			toolchain: Toolchain{Name: "invented", Programs: []string{"go"}, Version: []string{"go", "version"}},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			why := c.toolchain.present(r)
			if len(c.names) == 0 {
				if why != "" {
					t.Fatalf("a toolchain this machine has was reported missing: %q", why)
				}
				return
			}
			if why == "" {
				t.Fatal("a toolchain this machine cannot run was reported present")
			}
			// The reason is the evidence fit:toolchain narrates, so it has to
			// name which toolchain and which program, not just that one failed.
			for _, name := range c.names {
				if !strings.Contains(why, name) {
					t.Errorf("the reason %q does not name %q", why, name)
				}
			}
		})
	}
}

// A unit is addressed to a toolchain that takes a path pattern by the directory
// it sits in, and the repository root is the whole tree.
func TestAUnitIsAddressedByItsDirectory(t *testing.T) {
	if got := scope(Unit{Dir: ""}); got != "..." {
		t.Errorf("scope = %q, want the whole tree", got)
	}
	if got := scope(Unit{Dir: "lib/core"}); got != filepath.ToSlash("lib/core/...") {
		t.Errorf("scope = %q, want the unit's own tree", got)
	}
}

// A batch fits the host's command-line limit, and every file reaches the
// formatter exactly once. An empty set runs nothing: an invocation with no
// files would ask the formatter about the whole tree.
func TestFilesAreBatchedToFitTheCommandLine(t *testing.T) {
	var files []string
	for i := range 4000 {
		files = append(files, "unit/"+string(rune('a'+i%26))+"/file-with-a-fairly-long-name.go")
	}

	batches, seen := 0, 0
	err := overFiles(Unit{Dir: "unit"}, files, func(batch []string) error {
		batches++
		seen += len(batch)
		size := 0
		for _, f := range batch {
			size += len(f) + 1
		}
		if size > maxBatchBytes && len(batch) > 1 {
			t.Errorf("a batch of %d files is %d bytes, over the limit", len(batch), size)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen != len(files) || batches < 2 {
		t.Errorf("%d files reached the formatter in %d batches, want all of them in more than one", seen, batches)
	}

	ran := false
	if err := overFiles(Unit{}, nil, func([]string) error { ran = true; return nil }); err != nil {
		t.Fatal(err)
	}
	if ran {
		t.Error("an empty file set ran the formatter, which would ask it about the whole tree")
	}
}
