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

	hit, stmts, err := coverageCounts(profile)
	if err != nil {
		t.Fatal(err)
	}
	if hit != 7 || stmts != 10 {
		t.Errorf("(hit, statements) = (%d, %d), want (7, 10)", hit, stmts)
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
