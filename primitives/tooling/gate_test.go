package tooling

import (
	"slices"
	"strings"
	"testing"
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
