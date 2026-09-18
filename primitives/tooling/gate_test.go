package tooling

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/promise-language/forge/primitives"
	"github.com/promise-language/forge/primitives/command"
)

// --list names every concept and every instance, sorted, each with what it
// declares — so a program reads what a gate says of itself rather than a
// rendering written for a person.
func TestTheListingNamesEveryConceptAndEveryInstance(t *testing.T) {
	root := fixture(t, "", "tools/build")
	listing, err := GateListing(quiet(Standard(), root))
	if err != nil {
		t.Fatal(err)
	}

	var names []string
	for _, g := range listing.Gates {
		names = append(names, g.Name)
	}
	if !slices.IsSorted(names) {
		t.Errorf("the listing is not sorted: %v", names)
	}
	for _, want := range []string{"tested", "tested:root", "tested:tools-build", "fit:disk", "fit:toolchain"} {
		if !slices.Contains(names, want) {
			t.Errorf("the listing does not name %q: %v", want, names)
		}
	}
	for _, g := range listing.Gates {
		if g.Summary == "" {
			t.Errorf("%q carries no summary", g.Name)
		}
		if len(g.Metrics) == 0 {
			t.Errorf("%q declares no metric, so a reader cannot tell what it will report", g.Name)
		}
	}
}

// What each invocation writes and what it exits with, as the document tabulates
// it. The measurement is written only with --envelope: a bare run that printed
// measurements and exited 0 would be read as a pass by the first script that
// wrapped it, and a gate has no verdict to give.
func TestTheGateSurfaceIsWhatTheDocumentTabulates(t *testing.T) {
	root := fixture(t, "")
	stamp := stamped(t, root)
	p := counting("x", []Metric{Count("n")}, Measured{Metrics: []Measurement{Counted("n", 0, "")}}, nil)

	for _, c := range []struct {
		name   string
		args   []string
		status int
		stdout func(*testing.T, string)
		stderr string
	}{
		{
			name:   "a measurement, with --envelope",
			args:   []string{"x", "--envelope"},
			status: command.StatusDone,
			stdout: func(t *testing.T, body string) {
				var env Envelope
				if err := json.Unmarshal([]byte(body), &env); err != nil {
					t.Fatalf("stdout is not an envelope (%v): %q", err, body)
				}
				if env.Gate != "x" {
					t.Errorf("the envelope names %q, want the gate that measured it", env.Gate)
				}
			},
		},
		{
			name:   "without --envelope, nothing on stdout and the invocation refused",
			args:   []string{"x"},
			status: command.StatusMalformed,
			stdout: func(t *testing.T, body string) {
				if body != "" {
					t.Errorf("stdout carries %q, want nothing a script could read as a pass", body)
				}
			},
			stderr: "run x",
		},
		{
			name:   "--list, as an object a program reads",
			args:   []string{"--list", "-json"},
			status: command.StatusDone,
			stdout: func(t *testing.T, body string) {
				var listing Listing
				if err := json.Unmarshal([]byte(body), &listing); err != nil {
					t.Fatalf("stdout is not a listing (%v): %q", err, body)
				}
				for _, g := range listing.Gates {
					if g.Name == "x" {
						return
					}
				}
				t.Errorf("the listing does not name the gate this project answers: %q", body)
			},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			var out, errs strings.Builder
			status := GateTool(p, stamp).RunWith(c.args,
				command.Streams{Out: &out, Err: &errs, Dir: root})
			if status != c.status {
				t.Errorf("status = %d, want %d (stderr: %s)", status, c.status, errs.String())
			}
			c.stdout(t, out.String())
			if c.stderr != "" && !strings.Contains(errs.String(), c.stderr) {
				t.Errorf("stderr = %q, want it to say %q", errs.String(), c.stderr)
			}
		})
	}
}

// A measurement that could not be taken writes no envelope. A partial one is not
// a measurement and must not parse as one, so stdout stays empty and the status
// says nothing about this tree was established.
func TestAGateThatCouldNotMeasureWritesNoEnvelope(t *testing.T) {
	root := fixture(t, "")
	stamp := stamped(t, root)
	p := counting("x", []Metric{Count("n")}, Measured{}, errTest)

	var out, errs strings.Builder
	status := GateTool(p, stamp).RunWith([]string{"x", "--envelope"},
		command.Streams{Out: &out, Err: &errs, Dir: root})

	if status != command.StatusFailed {
		t.Errorf("status = %d, want %d when nothing could be measured", status, command.StatusFailed)
	}
	if out.Len() != 0 {
		t.Errorf("stdout carries %q, want nothing for a runner to parse as a measurement", out.String())
	}
}

// A declared name wins over a derived one: fit:disk is a gate of its own, not
// the fit concept narrowed to a unit called disk.
func TestADeclaredInstanceIsNotADerivedOne(t *testing.T) {
	root := fixture(t, "")
	r := quiet(Standard(), root)

	g, narrowed, err := resolveGate(r, FitDisk)
	if err != nil {
		t.Fatal(err)
	}
	if g.Name != FitDisk || narrowed != nil {
		t.Errorf("fit:disk resolved to %q over %v, want the declared gate", g.Name, labels(narrowed))
	}
	if _, _, err := resolveGate(r, "fit:root"); err == nil {
		t.Error("fit narrowed to a unit, where it divides by condition rather than by unit")
	}
}

// A measurement that disagrees with its declaration is an error, never a value
// to absorb: a metric whose type changed measured something else, and absorbed
// silently it would move a ratchet that by construction never moves back.
func TestAMeasurementThatDisagreesWithItsDeclarationIsRefused(t *testing.T) {
	root := fixture(t, "")
	for _, c := range []struct {
		name     string
		declared []Metric
		measured Measured
		says     string
	}{
		{"a float where a count was declared",
			[]Metric{Count("n")},
			Measured{Metrics: []Measurement{Quantity("n", 1.5, "")}},
			"declared int"},
		{"a unit the declaration does not carry",
			[]Metric{Bytes("n")},
			Measured{Metrics: []Measurement{Counted("n", 1, "percent")}},
			"declared in"},
		{"a name nothing declared",
			[]Metric{Count("n")},
			Measured{Metrics: []Measurement{Counted("m", 1, "")}},
			"never declared"},
		{"one name reported twice in one envelope",
			[]Metric{Count("n")},
			Measured{Metrics: []Measurement{Counted("n", 1, ""), Counted("n", 2, "")}},
			"reported twice"},
		{"a property where a count was declared",
			[]Metric{Count("n")},
			Measured{Metrics: []Measurement{Reported("n", true)}},
			"declared int"},
		{"a count where a property was declared",
			[]Metric{Property("n")},
			Measured{Metrics: []Measurement{Counted("n", 1, "")}},
			"declared bool"},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := counting("x", c.declared, c.measured, nil)
			_, err := MeasureGate(quiet(p, root), "x")
			if err == nil {
				t.Fatal("the envelope was written")
			}
			if !strings.Contains(err.Error(), c.says) {
				t.Errorf("the refusal is %q, want it to say %q", err, c.says)
			}
		})
	}
}

// A property crosses the envelope as true and false. Encoded as one and zero it
// would invite comparison and arithmetic that mean nothing, and nothing would
// distinguish it from a count that happens to be small.
func TestAPropertyCrossesTheEnvelopeAsTrueAndFalse(t *testing.T) {
	root := fixture(t, "")
	p := counting("x", []Metric{Property("builds_wasm")},
		Measured{Metrics: []Measurement{Reported("builds_wasm", true)}}, nil)

	env, err := MeasureGate(quiet(p, root), "x")
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	for _, says := range []string{`"type":"bool"`, `"value":true`} {
		if !strings.Contains(string(body), says) {
			t.Errorf("the envelope is %s, want it to carry %s", body, says)
		}
	}
	if strings.Contains(string(body), `"value":1`) {
		t.Errorf("the envelope collapsed the property to a number: %s", body)
	}

	var read Envelope
	if err := json.Unmarshal(body, &read); err != nil {
		t.Fatal(err)
	}
	if got := read.Metrics[0]; got.Type != Bool || !got.Bool || got.String() != "true" {
		t.Errorf("the property read back as %+v (%s), want the one that was measured", got, got.String())
	}
}

// A value that is not what its type declared is refused at the boundary, in
// either direction: the envelope is a claim and its check, and a reader that
// narrowed a number into a bool would absorb a type change nothing recorded.
func TestAPropertyCarryingANumberIsRefused(t *testing.T) {
	for _, c := range []struct {
		name string
		body string
	}{
		{"a number where bool was declared",
			`{"gate":"x","metrics":[{"name":"n","type":"bool","value":1}]}`},
		{"a bool where int was declared",
			`{"gate":"x","metrics":[{"name":"n","type":"int","value":true}]}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			var env Envelope
			err := json.Unmarshal([]byte(c.body), &env)
			if err == nil {
				t.Fatalf("the envelope was read: %+v", env)
			}
			if !strings.Contains(err.Error(), "its value is not") {
				t.Errorf("the refusal is %q, want it to name the value as the disagreement", err)
			}
		})
	}
}

// The type set is closed, and widening it to bool did not open it. A type
// outside the set is refused at decode rather than absorbed, because a
// measurement whose type nothing recognises carries its value in none of the
// fields a reader looks in: absorbed, it is zero, and zero is within a cap of
// zero. A gate written in another language reaches this decoder having been
// through none of the sending side's checks, so the near-miss spelling is the
// case that matters.
func TestAMetricTypeOutsideTheSetIsRefused(t *testing.T) {
	for _, c := range []struct {
		name string
		body string
	}{
		{"a near-miss spelling",
			`{"gate":"x","metrics":[{"name":"n","type":"boolean","value":true}]}`},
		{"no type at all",
			`{"gate":"x","metrics":[{"name":"n","value":0}]}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			var env Envelope
			err := json.Unmarshal([]byte(c.body), &env)
			if err == nil {
				t.Fatalf("the envelope was read: %+v", env)
			}
			if !strings.Contains(err.Error(), "unknown type") {
				t.Errorf("the refusal is %q, want it to name the type as the disagreement", err)
			}
		})
	}
}

// A composition's metrics are its parts' metrics and its groups are its parts'
// groups, and a part that is incomplete makes the whole incomplete, naming the
// part.
func TestAnIncompletePartMakesTheWholeIncompleteAndNamesIt(t *testing.T) {
	root := fixture(t, "")
	p := counting("x", []Metric{Count("n")},
		Measured{Metrics: []Measurement{Counted("n", 3, "")}, Incomplete: "half of it did not run"}, nil)

	env, err := MeasureGate(quiet(p, root), Integration)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(env.Incomplete, "x:") || !strings.Contains(env.Incomplete, "half of it did not run") {
		t.Errorf("integration's incomplete is %q, want it to name the part and its reason", env.Incomplete)
	}
	if len(env.Metrics) != 1 || env.Metrics[0].Int != 3 {
		t.Errorf("integration's metrics are %v, want its part's", env.Metrics)
	}
}

// A preparation's failure makes every dependent part incomplete, with the
// reason. It never produces a measurement of zero.
func TestAFailedPreparationMakesThePartIncompleteRatherThanZero(t *testing.T) {
	root := fixture(t, "")
	p := counting("x", []Metric{Count("n")}, Measured{Metrics: []Measurement{Counted("n", 0, "")}}, nil)
	g, _ := p.Gates.Get("x")
	tried := 0
	g.Prepare = func(*Run) error { tried++; return errTest }
	p.Gates.Add(g)

	r := quiet(p, root)
	env, err := MeasureGate(r, "x")
	if err != nil {
		t.Fatal(err)
	}
	if env.Incomplete == "" {
		t.Error("a failed preparation produced a complete run")
	}
	if len(env.Metrics) != 0 {
		t.Errorf("a failed preparation produced %v, want no measurement at all", env.Metrics)
	}
	// Run once per process: a second part needing it is told the same thing
	// rather than paying for it again.
	if _, err := MeasureGate(r, "x"); err != nil {
		t.Fatal(err)
	}
	if tried != 1 {
		t.Errorf("the preparation ran %d times, want once per process", tried)
	}
}

// A composition declares a preparation the same way a leaf does — building the
// compiler its parts measure with is exactly that gate — and a preparation the
// definition asked for and the run never performed is one the parts then measure
// without.
func TestACompositionsPreparationRuns(t *testing.T) {
	root := fixture(t, "")
	p := counting("x", []Metric{Count("n")}, Measured{Metrics: []Measurement{Counted("n", 0, "")}}, nil)
	g, _ := p.Gates.Get(Integration)
	ran := 0
	g.Prepare = func(*Run) error { ran++; return nil }
	p.Gates.Add(g)

	if _, err := MeasureGate(quiet(p, root), Integration); err != nil {
		t.Fatal(err)
	}
	if ran != 1 {
		t.Errorf("the composition's preparation ran %d times, want once", ran)
	}
}

// One metric name is reported once in one envelope, and a composition is where
// two parts can each report it. The whole is checked against what it declares
// for the same reason a leaf is: a name carried twice is two measurements under
// one term, and the term would mean whichever part the judge read last.
func TestACompositionCannotReportOneNameTwice(t *testing.T) {
	root := fixture(t, "")
	p := counting("x", []Metric{Count("n")}, Measured{Metrics: []Measurement{Counted("n", 1, "")}}, nil)
	p.Gates.Add(Gate{
		Name:    "y",
		Summary: "a second part reporting the same name",
		Metrics: Declared(Count("n")),
		Measure: func(*Run, []Unit) (Measured, error) {
			return Measured{Metrics: []Measurement{Counted("n", 2, "")}}, nil
		},
		Remediation: "there is nothing to do about a fixture",
	})
	p.Integration("x", "y")

	_, err := MeasureGate(quiet(p, root), Integration)
	if err == nil || !strings.Contains(err.Error(), "twice") {
		t.Errorf("err = %v, want a composition carrying one name twice to be refused", err)
	}
}

// fit:toolchain counts the toolchains with units in this repository whose
// program does not run. A toolchain with no units contributes nothing, so a
// Go-only checkout is fit on a machine that has never heard of Promise — and
// the same machine is not, the moment a Promise unit is tracked.
func TestFitToolchainCountsOnlyTheToolchainsWithUnits(t *testing.T) {
	if primitives.Which("promise") != "" {
		t.Skip("this machine has promise, so its absence cannot be measured")
	}

	goOnly := fixture(t, "")
	r, _ := run(t, Standard(), goOnly)
	env, err := MeasureGate(r, FitToolchain)
	if err != nil {
		t.Fatal(err)
	}
	if got := missing(t, env); got != 0 {
		t.Errorf("missing_toolchains = %d in a Go-only checkout, want a toolchain with no units to count for nothing", got)
	}

	alsoPromise := fixture(t, "")
	write(t, alsoPromise, filepath.FromSlash("app/promise.toml"), "")
	git(t, alsoPromise, "add", "-A")
	r2, narrated := run(t, Standard(), alsoPromise)
	env2, err := MeasureGate(r2, FitToolchain)
	if err != nil {
		t.Fatal(err)
	}
	if got := missing(t, env2); got != 1 {
		t.Errorf("missing_toolchains = %d with a Promise unit tracked, want 1", got)
	}
	if !strings.Contains(narrated.String(), "promise") {
		t.Errorf("the measurement did not name what is missing: %q", narrated.String())
	}
}

func missing(t *testing.T, env Envelope) int64 {
	t.Helper()
	for _, m := range env.Metrics {
		if m.Name == "missing_toolchains" {
			return m.Int
		}
	}
	t.Fatalf("the envelope reports no missing_toolchains: %+v", env.Metrics)
	return 0
}

// fit:disk reports both filesystems every run, even before either directory
// exists. fit runs on exactly the fresh machine — the one that has not written
// a cache yet — and an envelope whose shape varied by host is one no term can be
// written against.
func TestFitDiskReportsBothFilesystemsBeforeTheCacheExists(t *testing.T) {
	root := fixture(t, "")
	r, _ := run(t, Standard(), root)
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(CacheDir))); !os.IsNotExist(err) {
		t.Fatalf("the fixture already holds a cache directory: %v", err)
	}

	env, err := MeasureGate(r, FitDisk)
	if err != nil {
		t.Fatalf("free space could not be measured where the cache does not exist yet: %v", err)
	}
	for _, want := range []string{"worktree_free_bytes", "cache_free_bytes"} {
		found := false
		for _, m := range env.Metrics {
			if m.Name != want {
				continue
			}
			found = true
			if m.Int <= 0 || m.Unit != "bytes" {
				t.Errorf("%s = %d %q, want a positive count of bytes", want, m.Int, m.Unit)
			}
		}
		if !found {
			t.Errorf("the envelope does not report %s: %+v", want, env.Metrics)
		}
	}
}

// The envelope carries the host's os/arch as its target, unless the gate
// declared one for the instance it measured.
func TestTheEnvelopeCarriesATarget(t *testing.T) {
	root := fixture(t, "")
	p := counting("x", []Metric{Count("n")}, Measured{Metrics: []Measurement{Counted("n", 0, "")}}, nil)

	env, err := MeasureGate(quiet(p, root), "x")
	if err != nil {
		t.Fatal(err)
	}
	if env.Target != HostTarget() {
		t.Errorf("target = %q, want the host's %q", env.Target, HostTarget())
	}

	g, _ := p.Gates.Get("x")
	g.Target = "wasip1/wasm"
	p.Gates.Add(g)
	env, err = MeasureGate(quiet(p, root), "x")
	if err != nil {
		t.Fatal(err)
	}
	if env.Target != "wasip1/wasm" {
		t.Errorf("target = %q, want the one the gate declared", env.Target)
	}
}

// Each unit is a group in the envelope, which is what tells a reader where a
// number came from — and what a failing verdict's evidence is drawn from.
func TestEachUnitIsAGroup(t *testing.T) {
	root := fixture(t, "", "tools/build")
	write(t, root, "a.go", "package a\n")
	write(t, root, "tools/build/b.go", "package b\n")
	git(t, root, "add", "-A")

	r, _ := run(t, Standard(), root)
	env, err := MeasureGate(r, Formatted)
	if err != nil {
		t.Fatal(err)
	}
	var groups []string
	for _, g := range env.Groups {
		groups = append(groups, g.Name)
	}
	if !slices.Equal(groups, []string{"root", "tools-build"}) {
		t.Errorf("groups = %v, want one per unit", groups)
	}
}

// Counts sum across units, and a proportion sums its two counts before
// dividing: averaging percentages would let a small, well-tested unit hide a
// large untested one.
func TestMetricsCombineByMeaning(t *testing.T) {
	units := []Unit{{Dir: "", Toolchain: "go"}, {Dir: "lib", Toolchain: "go"}}
	got := combine(units, []UnitResult{
		{Counts: []Tally{{Name: "n", N: 2}}, Ratios: []Proportion{{Name: "p", Part: 1, Whole: 1000, Unit: "percent", Scale: 100}}},
		{Counts: []Tally{{Name: "n", N: 3}}, Ratios: []Proportion{{Name: "p", Part: 3, Whole: 3, Unit: "percent", Scale: 100}}},
	})

	if got.Metrics[0].Int != 5 {
		t.Errorf("n = %d, want the counts summed", got.Metrics[0].Int)
	}
	// 4 covered of 1003 is 0.4%. The average of 0.1% and 100% would be 50%.
	if p := got.Metrics[1].Float; p > 1 {
		t.Errorf("p = %v, want the two counts summed before dividing rather than the percentages averaged", p)
	}
}

// errTest is the failure a fixture stands in with.
var errTest = &testError{}

type testError struct{}

func (*testError) Error() string { return "a fixture failed on purpose" }
