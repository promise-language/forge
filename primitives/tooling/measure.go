package tooling

// What a measurement is, and how measurements combine across units.
//
// Metrics combine by meaning (docs/project-tools.md, What the standard
// toolchains measure): counts sum, and a proportion sums its numerator and its
// denominator before dividing. Averaging percentages would let a small,
// well-tested unit hide a large untested one, so the ratio travels as its two
// counts and is divided once, at the end.

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// Measurement is one value a gate measured, with the type and the unit its
// declaration gave it.
type Measurement struct {
	Name  string
	Type  MetricType
	Int   int64
	Float float64
	Bool  bool
	Unit  string
}

// Counted is a measurement of how many.
func Counted(name string, n int64, unit string) Measurement {
	return Measurement{Name: name, Type: Int, Int: n, Unit: unit}
}

// Measured is a measurement that is not a count.
func Quantity(name string, v float64, unit string) Measurement {
	return Measurement{Name: name, Type: Float, Float: v, Unit: unit}
}

// Reported is a measurement of whether a property holds.
func Reported(name string, v bool) Measurement {
	return Measurement{Name: name, Type: Bool, Bool: v}
}

// Number is the value as a float, for comparison against a term. Widening is
// safe here and nowhere else: the judge compares, it does not store, so nothing
// downstream can mistake the widened form for what was measured.
//
// False is worse than true, so a property is one and zero in that order and in
// this one place. That is what lets at_least mean "must be true" and at_most
// "must be false" through the single comparison the judge already makes, rather
// than through a second one written for bools.
func (m Measurement) Number() float64 {
	switch m.Type {
	case Int:
		return float64(m.Int)
	case Bool:
		if m.Bool {
			return 1
		}
		return 0
	}
	return m.Float
}

// String renders the value in its own type — a count never grows a decimal
// point, a quantity never loses one, and a property is true or false rather
// than the one and zero it is compared as.
func (m Measurement) String() string {
	switch m.Type {
	case Int:
		return strconv.FormatInt(m.Int, 10)
	case Bool:
		return strconv.FormatBool(m.Bool)
	}
	return strconv.FormatFloat(m.Float, 'f', 1, 64)
}

// Declares reports whether this measurement is the one its declaration
// described. A float reported where the definition says a count, or bytes where
// it says a percentage, is a different measurement wearing a declared name.
func (m Measurement) Declares(d Metric) error {
	if m.Type != d.Type {
		return fmt.Errorf("%s is declared %s and was measured as %s", m.Name, d.Type, m.Type)
	}
	if m.Unit != d.Unit {
		return fmt.Errorf("%s is declared in %q and was measured in %q", m.Name, d.Unit, m.Unit)
	}
	return nil
}

// measurementWire is the envelope form: one "value" field, and the type beside
// it so a reader knows which kind of number it is looking at.
type measurementWire struct {
	Name  string          `json:"name"`
	Type  MetricType      `json:"type"`
	Value json.RawMessage `json:"value"`
	Unit  string          `json:"unit,omitempty"`
}

func (m Measurement) MarshalJSON() ([]byte, error) {
	w := measurementWire{Name: m.Name, Type: m.Type, Unit: m.Unit}
	switch m.Type {
	case Int:
		w.Value = json.RawMessage(strconv.FormatInt(m.Int, 10))
	case Float:
		w.Value = json.RawMessage(strconv.FormatFloat(m.Float, 'f', -1, 64))
	case Bool:
		// true and false, never 1 and 0. Encoded as numbers a property invites
		// comparison and arithmetic that mean nothing, and nothing distinguishes
		// it from a count that happens to be small.
		w.Value = json.RawMessage(strconv.FormatBool(m.Bool))
	default:
		return nil, fmt.Errorf("metric %q has no type", m.Name)
	}
	return json.Marshal(w)
}

func (m *Measurement) UnmarshalJSON(b []byte) error {
	var w measurementWire
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	m.Name, m.Type, m.Unit = w.Name, w.Type, w.Unit
	switch w.Type {
	case Int:
		// A count arriving with a fractional part is not a count. Refusing it
		// is the point: absorbed, it would be a type change nothing recorded.
		if err := json.Unmarshal(w.Value, &m.Int); err != nil {
			return fmt.Errorf("metric %q is declared %s but its value is not: %w", w.Name, w.Type, err)
		}
	case Float:
		if err := json.Unmarshal(w.Value, &m.Float); err != nil {
			return fmt.Errorf("metric %q is declared %s but its value is not: %w", w.Name, w.Type, err)
		}
	case Bool:
		if err := json.Unmarshal(w.Value, &m.Bool); err != nil {
			return fmt.Errorf("metric %q is declared %s but its value is not: %w", w.Name, w.Type, err)
		}
	default:
		return fmt.Errorf("metric %q has an unknown type %q", w.Name, w.Type)
	}
	return nil
}

// Group is one unit's own measurements. It is what tells a reader where a
// number came from, and it is where a failing verdict's evidence comes from
// (docs/project-tools.md, Run).
type Group struct {
	Name    string        `json:"name"`
	Metrics []Measurement `json:"metrics"`
}

// UnitResult is what a toolchain measured over one unit, before the units are
// combined.
type UnitResult struct {
	// Counts are whole-number measurements. They sum across units.
	Counts []Tally
	// Ratios are proportions. Their two counts sum across units, and the
	// division happens once, afterwards.
	Ratios []Proportion
	// Incomplete is why this unit measured less than a full one.
	Incomplete string
}

// Tally is one count over one unit.
type Tally struct {
	Name string
	N    int64
	Unit string
}

// Proportion is one ratio over one unit, carried as its two counts so that
// combining sums rather than averages.
type Proportion struct {
	Name        string
	Part, Whole int64
	Unit        string
	// Scale multiplies the quotient — 100 for a percentage.
	Scale float64
}

// combine folds one result per unit into the gate's own measurements and the
// groups that say where each came from.
func combine(units []Unit, results []UnitResult) Measured {
	var order []string
	counts := map[string]*Tally{}
	ratios := map[string]*Proportion{}
	var ratioOrder []string
	var reasons []string
	out := Measured{}

	for i, res := range results {
		var group []Measurement
		for _, t := range res.Counts {
			if _, seen := counts[t.Name]; !seen {
				counts[t.Name] = &Tally{Name: t.Name, Unit: t.Unit}
				order = append(order, t.Name)
			}
			counts[t.Name].N += t.N
			group = append(group, Counted(t.Name, t.N, t.Unit))
		}
		for _, p := range res.Ratios {
			if _, seen := ratios[p.Name]; !seen {
				ratios[p.Name] = &Proportion{Name: p.Name, Unit: p.Unit, Scale: p.Scale}
				ratioOrder = append(ratioOrder, p.Name)
			}
			ratios[p.Name].Part += p.Part
			ratios[p.Name].Whole += p.Whole
			group = append(group, quotient(p))
		}
		if res.Incomplete != "" {
			reasons = append(reasons, units[i].Label()+": "+res.Incomplete)
		}
		if len(group) > 0 {
			out.Groups = append(out.Groups, Group{Name: units[i].Label(), Metrics: group})
		}
	}

	for _, name := range order {
		t := counts[name]
		out.Metrics = append(out.Metrics, Counted(t.Name, t.N, t.Unit))
	}
	for _, name := range ratioOrder {
		out.Metrics = append(out.Metrics, quotient(*ratios[name]))
	}
	out.Incomplete = joinReasons(reasons)
	return out
}

// quotient divides a proportion, answering zero over an empty whole rather than
// a NaN no term can be compared against.
func quotient(p Proportion) Measurement {
	if p.Whole == 0 {
		return Quantity(p.Name, 0, p.Unit)
	}
	scale := p.Scale
	if scale == 0 {
		scale = 1
	}
	return Quantity(p.Name, scale*float64(p.Part)/float64(p.Whole), p.Unit)
}
