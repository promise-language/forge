package tooling

// What the standard toolchains measure (docs/project-tools.md).
//
// A toolchain supplies what its units need: a formatter that rewrites and one
// that checks, a build, a check, a test run and coverage, the program it runs,
// and the directory it caches in. A toolchain with no units in the repository
// contributes nothing.
//
// A count is read from a structured report wherever the toolchain has one,
// never by matching prose meant for a person. A run that failed and reported
// nothing countable could not be measured: "zero failures" is not a reading of
// a run that did not happen.

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/promise-language/forge/primitives"
)

// Toolchain is one language's answer to each concept.
type Toolchain struct {
	// Name is how an instance spells this toolchain.
	Name string
	// Manifest is the file whose tracked presence makes a directory a unit.
	Manifest string
	// Extensions are the suffixes of this language's source files.
	Extensions []string
	// Programs are what this toolchain runs, found on PATH and nowhere else. A
	// fallback location would be a second answer to where the compiler is.
	Programs []string
	// Version is the invocation that makes this toolchain state its version. It
	// is one invocation rather than one per program, because a toolchain ships
	// its parts together and only some of them answer: `go version` reports the
	// release `gofmt` came from, and gofmt has no version of its own to give.
	Version []string
	// CacheVars are the environment variables that tell this toolchain where to
	// cache what it computes from a checkout.
	CacheVars []string
	// CacheDir is this toolchain's directory under .home/cache/.
	CacheDir string

	// Metrics are what this toolchain reports for each concept. `go vet`
	// reports vet findings and `promise check` reports check findings, so the
	// names differ and the declaration follows the toolchain that will answer.
	Metrics map[string][]Metric

	// Rewrite repairs formatting in place over a unit's source files. It is
	// verify's repair stage, and it reads the same file set the formatted
	// measurement does.
	Rewrite func(*Run, Unit, []string) error

	// Measure is this toolchain's measurement for each concept.
	Measure map[string]func(*Run, Unit) (UnitResult, error)
}

// owns reports whether a tracked path is one of this toolchain's source files.
func (t Toolchain) owns(tracked string) bool {
	for _, ext := range t.Extensions {
		if strings.HasSuffix(tracked, ext) {
			return true
		}
	}
	return false
}

// present reports why this toolchain is not on this machine, or "" when it is.
// It is what `fit:toolchain` counts.
//
// Every program is looked for on PATH, and the one that states a version is
// run: a program that is there and cannot run is as absent as one that is not,
// and a measurement needing it could not be taken either way.
func (t Toolchain) present(r *Run) string {
	for _, program := range t.Programs {
		if _, found := primitives.Which(program); !found {
			return fmt.Sprintf("%s: %s is not on PATH", t.Name, program)
		}
	}
	if len(t.Version) == 0 {
		return ""
	}
	if _, stderr, err := r.Value(Unit{Toolchain: t.Name}, t.Version[0], t.Version[1:]...); err != nil {
		return fmt.Sprintf("%s: `%s` does not state a version: %v: %s",
			t.Name, strings.Join(t.Version, " "), err, firstLine(stderr))
	}
	return ""
}

// Go is the Go toolchain.
func Go() Toolchain {
	t := Toolchain{
		Name:       "go",
		Manifest:   "go.mod",
		Extensions: []string{".go"},
		Programs:   []string{"go", "gofmt"},
		Version:    []string{"go", "version"},
		CacheVars:  []string{"GOCACHE", "GOMODCACHE"},
		CacheDir:   "go",
		Metrics: map[string][]Metric{
			Formatted: {Count("unformatted_files")},
			Builds:    {Count("unbuildable_packages")},
			Checked:   {Count("vet_findings")},
			Tested:    {Count("failed_tests"), Count("failed_packages"), Count("test_count")},
			Covered:   {Percent("statement_coverage")},
		},
		Rewrite: func(r *Run, u Unit, files []string) error {
			return overFiles(u, files, func(batch []string) error {
				return r.Attached(u, "gofmt", append([]string{"-w"}, batch...)...)
			})
		},
	}
	t.Measure = map[string]func(*Run, Unit) (UnitResult, error){
		Formatted: goFormatted,
		Builds:    goBuilds,
		Checked:   goChecked,
		Tested:    goTested,
		Covered:   goCovered,
	}
	return t
}

// goFormatted counts the unit's source files gofmt would rewrite. `-l` lists
// them; `-w` would repair them, which is verify's job and not a gate's.
func goFormatted(r *Run, u Unit) (UnitResult, error) {
	files, err := r.SourceFiles(u)
	if err != nil {
		return UnitResult{}, err
	}
	n, incomplete := 0, ""
	err = overFiles(u, files, func(batch []string) error {
		out, over, runErr := r.Output(u, "gofmt", append([]string{"-l"}, batch...)...)
		if runErr != nil && out == "" {
			return fmt.Errorf("gofmt -l: %w", runErr)
		}
		if over {
			incomplete = OverflowReason("gofmt -l")
		}
		n += countLines(out)
		return nil
	})
	if err != nil {
		return UnitResult{}, err
	}
	return UnitResult{
		Counts:     []Tally{{Name: "unformatted_files", N: int64(n)}},
		Incomplete: incomplete,
	}, nil
}

// goBuilds counts packages that fail to compile. `go build` prefixes each
// failing package with a "# " header line on stderr, so the headers are the
// count.
func goBuilds(r *Run, u Unit) (UnitResult, error) {
	_, stderr, err := r.Value(u, "go", "build", "./...")
	found := countPrefixed(stderr, "# ")
	if err != nil && found == 0 {
		// It failed and named no package: the failure is about the toolchain or
		// the module, not about a package in this tree.
		return UnitResult{}, fmt.Errorf("go build in %s: %w: %s", u.Label(), err, firstLine(stderr))
	}
	return UnitResult{Counts: []Tally{{Name: "unbuildable_packages", N: int64(found)}}}, nil
}

// goChecked counts vet findings out of `go vet -json`'s structured report.
//
// The report arrives on both streams: a package with nothing to say is an empty
// object on stdout, and a package with findings is an object on stderr under
// the "# package" header that names it. Reading one stream would count every
// finding or none of them, so both are read and the objects counted wherever
// they came from.
//
// A vet run that printed no JSON at all is not zero findings. It is a toolchain
// that did not report, and nothing was measured.
func goChecked(r *Run, u Unit) (UnitResult, error) {
	stdout, stderr, err := r.Value(u, "go", "vet", "-json", "./...")
	n, reported := countVetFindings(stdout + "\n" + stderr)
	if !reported {
		return UnitResult{}, fmt.Errorf("go vet in %s reported no JSON, so nothing was measured: %v: %s",
			u.Label(), err, firstLine(stderr))
	}
	return UnitResult{Counts: []Tally{{Name: "vet_findings", N: n}}}, nil
}

// countVetFindings reads `go vet -json`'s report: a stream of JSON objects
// mapping package to analyzer to diagnostics, with "# package" header lines
// between them. It reports the count and whether any object was read at all.
func countVetFindings(report string) (int64, bool) {
	var body strings.Builder
	for line := range strings.SplitSeq(report, "\n") {
		if strings.HasPrefix(line, "#") {
			continue
		}
		body.WriteString(line)
		body.WriteByte('\n')
	}
	decoder := json.NewDecoder(strings.NewReader(body.String()))
	var n int64
	read := false
	for {
		var byPackage map[string]map[string][]struct {
			Posn    string `json:"posn"`
			Message string `json:"message"`
		}
		if err := decoder.Decode(&byPackage); err != nil {
			return n, read
		}
		read = true
		for _, analyzers := range byPackage {
			for _, findings := range analyzers {
				n += int64(len(findings))
			}
		}
	}
}

// goTested counts failing tests, failing packages and tests run, out of `go
// test -json`'s event stream.
func goTested(r *Run, u Unit) (UnitResult, error) {
	out, over, err := r.Output(u, "go", "test", "-json", "./...")
	tests, packages, count, read := countTestEvents(out)
	if !read {
		// The run itself did not happen — a build failure in a test package,
		// most often. That is not "zero failing tests".
		return UnitResult{}, fmt.Errorf("go test in %s reported no events, so nothing was measured: %v: %s",
			u.Label(), err, firstLine(out))
	}
	incomplete := ""
	if over {
		incomplete = OverflowReason("go test -json")
	}
	return UnitResult{
		Counts: []Tally{
			{Name: "failed_tests", N: tests},
			{Name: "failed_packages", N: packages},
			{Name: "test_count", N: count},
		},
		Incomplete: incomplete,
	}, nil
}

// countTestEvents reads `go test -json`'s stream. A record with a Test is about
// one test, and one without is about the package that holds them.
func countTestEvents(out string) (tests, packages, count int64, read bool) {
	decoder := json.NewDecoder(strings.NewReader(out))
	for {
		var event struct {
			Action string `json:"Action"`
			Test   string `json:"Test"`
		}
		if err := decoder.Decode(&event); err != nil {
			return tests, packages, count, read
		}
		read = true
		switch {
		case event.Action == "run" && event.Test != "":
			count++
		case event.Action == "fail" && event.Test != "":
			tests++
		case event.Action == "fail" && event.Test == "":
			packages++
		}
	}
}

// goCovered reports statement coverage over the unit.
//
// The profile is scratch, so it is written under .home/tmp/ in a directory
// unique to the run. A gate that dropped it in the worktree would have modified
// the subject it was measuring.
//
// The test run's stderr is parsed rather than attached, because why the run
// measured less than a green one is on it: an exit status alone cannot tell a
// package that failed its tests from one that never compiled.
func goCovered(r *Run, u Unit) (UnitResult, error) {
	profile := r.Scratch("cover-" + u.Label() + ".out")
	_, stderr, testErr := r.Value(u, "go", "test", "-coverprofile="+profile, "./...")
	hit, statements, err := coverageCounts(profile)
	if err != nil {
		return UnitResult{}, fmt.Errorf("coverage in %s: %w (the test run said: %v: %s)",
			u.Label(), err, testErr, firstProblem(stderr))
	}
	incomplete := ""
	if testErr != nil {
		incomplete = coverageReason(stderr)
	}
	return UnitResult{
		Ratios:     []Proportion{{Name: "statement_coverage", Part: hit, Whole: statements, Unit: "percent", Scale: 100}},
		Incomplete: incomplete,
	}, nil
}

// coverageReason says why a coverage run that exited non-zero measured less
// than a green one. A package that did not build and a package whose tests
// failed are different facts: the first exercised none of its statements, and a
// reader told the second goes looking for a failing test that does not exist.
//
// The two are told apart by the headers `go test` groups a build's diagnostics
// under, which is what `builds` already counts unbuildable packages by. Not by
// stderr being non-empty: `go test` writes `go: downloading …` there too, and a
// test failure alongside a module download is not a build failure. In the other
// direction the headers are the whole story, because `go test` gives a test
// binary's own output to its stdout — what reaches its stderr came from the
// toolchain or the build.
//
// Where both happened the build failure is what the reason names. It is the
// stronger statement, and a reader who repairs the build re-runs and sees the
// failing tests then.
func coverageReason(stderr string) string {
	if countDiagnosticModules(stderr) > 0 {
		return "some packages did not build, so their statements were not exercised at all: " +
			firstProblem(stderr)
	}
	return "some packages failed their tests, so their statements were only partly exercised"
}

// coverageCounts sums statements and covered statements out of one coverprofile.
// Every line after the mode header is
//
//	<file>:<startLine>.<startCol>,<endLine>.<endCol> <numStmt> <count>
//
// and the counts come from there rather than from `go tool cover -func`, which
// prints a percentage and not the statement counts behind it.
func coverageCounts(path string) (hit, statements int64, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, fmt.Errorf("no profile was produced: %w", err)
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "mode:") {
			continue
		}
		// Split from the right: a file path may itself contain spaces, but the
		// two trailing fields never do.
		fields := strings.Fields(line)
		if len(fields) < 3 {
			return 0, 0, fmt.Errorf("cannot read a profile line: %q", line)
		}
		n, err := strconv.ParseInt(fields[len(fields)-2], 10, 64)
		if err != nil {
			return 0, 0, fmt.Errorf("statement count in %q: %w", line, err)
		}
		count, err := strconv.ParseInt(fields[len(fields)-1], 10, 64)
		if err != nil {
			return 0, 0, fmt.Errorf("execution count in %q: %w", line, err)
		}
		statements += n
		if count > 0 {
			hit += n
		}
	}
	if statements == 0 {
		return 0, 0, fmt.Errorf("the profile named no statements")
	}
	return hit, statements, nil
}

// Promise is the Promise toolchain.
func Promise() Toolchain {
	t := Toolchain{
		Name:       "promise",
		Manifest:   "promise.toml",
		Extensions: []string{".pr"},
		Programs:   []string{"promise"},
		Version:    []string{"promise", "--version"},
		CacheVars:  []string{"PROMISE_CACHE"},
		CacheDir:   "promise",
		Metrics: map[string][]Metric{
			Formatted: {Count("unformatted_files")},
			Builds:    {Count("unbuildable_modules")},
			Checked:   {Count("check_findings")},
			Tested:    {Count("failed_tests"), Count("leaked_tests"), Count("test_count")},
			Covered:   {Percent("block_coverage")},
		},
		Rewrite: func(r *Run, u Unit, files []string) error {
			return overFiles(u, files, func(batch []string) error {
				return r.Attached(u, "promise", append([]string{"format"}, batch...)...)
			})
		},
	}
	t.Measure = map[string]func(*Run, Unit) (UnitResult, error){
		Formatted: promiseFormatted,
		Builds:    promiseBuilds,
		Checked:   promiseChecked,
		Tested:    promiseTested,
		Covered:   promiseCovered,
	}
	return t
}

// promiseFormatted counts the unit's .pr files the formatter would rewrite, in
// batches that fit the host's command-line limit.
func promiseFormatted(r *Run, u Unit) (UnitResult, error) {
	files, err := r.SourceFiles(u)
	if err != nil {
		return UnitResult{}, err
	}
	n, incomplete := 0, ""
	err = overFiles(u, files, func(batch []string) error {
		out, over, runErr := r.Output(u, "promise", append([]string{"format", "-check"}, batch...)...)
		if runErr != nil && out == "" {
			return fmt.Errorf("promise format -check: %w", runErr)
		}
		if over {
			incomplete = OverflowReason("promise format -check")
		}
		n += countLines(out)
		return nil
	})
	if err != nil {
		return UnitResult{}, err
	}
	return UnitResult{
		Counts:     []Tally{{Name: "unformatted_files", N: int64(n)}},
		Incomplete: incomplete,
	}, nil
}

// promiseBuilds builds the unit into scratch and counts the modules that failed.
func promiseBuilds(r *Run, u Unit) (UnitResult, error) {
	into := r.Scratch("build-" + u.Label())
	_, stderr, err := r.Value(u, "promise", "build", "--out", into)
	found := countDiagnosticModules(stderr)
	if err != nil && found == 0 {
		return UnitResult{}, fmt.Errorf("promise build in %s: %w: %s", u.Label(), err, firstLine(stderr))
	}
	return UnitResult{Counts: []Tally{{Name: "unbuildable_modules", N: int64(found)}}}, nil
}

// promiseChecked counts check findings: the lines naming a source position, as
// distinct from the headers that group them.
func promiseChecked(r *Run, u Unit) (UnitResult, error) {
	_, stderr, err := r.Value(u, "promise", "check")
	found := countDiagnostics(stderr)
	if err != nil && found == 0 {
		return UnitResult{}, fmt.Errorf("promise check in %s: %w: %s", u.Label(), err, firstLine(stderr))
	}
	return UnitResult{Counts: []Tally{{Name: "check_findings", N: int64(found)}}}, nil
}

// promiseTested counts failing tests, leaked tests and tests run out of
// `promise test --json`'s record stream.
func promiseTested(r *Run, u Unit) (UnitResult, error) {
	out, over, err := r.Output(u, "promise", "test", "--json", scope(u))
	failed, leaked, count, read := countPromiseTests(out)
	if !read {
		return UnitResult{}, fmt.Errorf("promise test in %s reported no records, so nothing was measured: %v: %s",
			u.Label(), err, firstLine(out))
	}
	incomplete := ""
	if over {
		incomplete = OverflowReason("promise test --json")
	}
	return UnitResult{
		Counts: []Tally{
			{Name: "failed_tests", N: failed},
			{Name: "leaked_tests", N: leaked},
			{Name: "test_count", N: count},
		},
		Incomplete: incomplete,
	}, nil
}

// countPromiseTests reads the record stream `promise test --json` writes.
func countPromiseTests(out string) (failed, leaked, count int64, read bool) {
	decoder := json.NewDecoder(strings.NewReader(out))
	for {
		var record struct {
			Kind   string `json:"kind"`
			Result string `json:"result"`
		}
		if err := decoder.Decode(&record); err != nil {
			return failed, leaked, count, read
		}
		read = true
		if record.Kind != "test" {
			continue
		}
		count++
		switch record.Result {
		case "failed":
			failed++
		case "leaked":
			leaked++
		}
	}
}

// promiseCovered reports block coverage, summed from the per-file coverage
// records on the test run's own stream.
func promiseCovered(r *Run, u Unit) (UnitResult, error) {
	out, _, testErr := r.Output(u, "promise", "test", "-coverage", "--json", scope(u))
	covered, total, read := countPromiseCoverage(out)
	if !read {
		return UnitResult{}, fmt.Errorf("coverage in %s: the run reported no coverage records (the test run said: %v)",
			u.Label(), testErr)
	}
	incomplete := ""
	if testErr != nil {
		incomplete = "some tests failed, so their blocks were only partly exercised"
	}
	return UnitResult{
		Ratios:     []Proportion{{Name: "block_coverage", Part: covered, Whole: total, Unit: "percent", Scale: 100}},
		Incomplete: incomplete,
	}, nil
}

// countPromiseCoverage sums the per-file {"kind": "coverage", …} records.
func countPromiseCoverage(out string) (covered, total int64, read bool) {
	decoder := json.NewDecoder(strings.NewReader(out))
	for {
		var record struct {
			Kind    string `json:"kind"`
			Covered int64  `json:"covered"`
			Total   int64  `json:"total"`
		}
		if err := decoder.Decode(&record); err != nil {
			return covered, total, read
		}
		if record.Kind != "coverage" {
			continue
		}
		read = true
		covered += record.Covered
		total += record.Total
	}
}

// scope is how a unit is addressed to a toolchain that takes a path pattern.
func scope(u Unit) string {
	if u.Dir == "" {
		return "..."
	}
	return path.Join(u.Dir, "...")
}

// maxBatchBytes bounds one invocation's arguments. It is well under the
// smallest limit any supported host imposes — Windows caps a command line at
// about 32 KiB — because a batch that is refused is a measurement that did not
// happen.
const maxBatchBytes = 24 << 10

// overFiles calls run once per batch of files that fits the host's
// command-line limit. An empty set runs nothing: an invocation with no files
// would ask the formatter about the whole tree.
func overFiles(u Unit, files []string, run func([]string) error) error {
	rel := make([]string, 0, len(files))
	for _, f := range files {
		rel = append(rel, relativeTo(u.Dir, f))
	}
	for len(rel) > 0 {
		size, take := 0, 0
		for take < len(rel) {
			size += len(rel[take]) + 1
			if size > maxBatchBytes && take > 0 {
				break
			}
			take++
		}
		if err := run(rel[:take]); err != nil {
			return err
		}
		rel = rel[take:]
	}
	return nil
}

// relativeTo re-roots a repository-relative path against the unit it belongs
// to, because a child runs in its unit's directory.
func relativeTo(dir, file string) string {
	if dir == "" {
		return file
	}
	return strings.TrimPrefix(file, dir+"/")
}

func countLines(s string) int {
	n := 0
	for line := range strings.SplitSeq(s, "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

// countPrefixed counts lines starting with prefix, ignoring leading
// whitespace — `go test` indents a failing subtest under its parent, and a
// subtest that failed is a failing test.
func countPrefixed(s, prefix string) int {
	n := 0
	for line := range strings.SplitSeq(s, "\n") {
		if strings.HasPrefix(strings.TrimLeft(line, " \t"), prefix) {
			n++
		}
	}
	return n
}

// countDiagnosticModules counts the headers that group a build's diagnostics.
func countDiagnosticModules(s string) int { return countPrefixed(s, "# ") }

// countDiagnostics counts findings: lines of the form path:line:col: msg. The
// headers that group them are not findings.
func countDiagnostics(s string) int {
	n := 0
	for line := range strings.SplitSeq(s, "\n") {
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

// firstProblem is the line of a child's stderr worth carrying into a reason a
// person reads: the first one that is not a header. A header names the package a
// build's diagnostics are grouped under without saying what is wrong with it,
// and it is often the first line there is. Where every line is a header, that is
// all the child said and it is what gets carried.
func firstProblem(s string) string {
	for line := range strings.SplitSeq(s, "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "# ") {
			continue
		}
		return line
	}
	return firstLine(s)
}

// joinReasons is how several incomplete reasons become one. It is never an
// empty string where there was a reason, and never a non-empty one where there
// was none.
func joinReasons(reasons []string) string {
	return strings.Join(reasons, "; ")
}
