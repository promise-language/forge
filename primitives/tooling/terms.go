package tooling

// The terms (docs/project-tools.md).
//
// A term a run may move is a baseline. A term only a person moves is a cap.
// Caps live in tools/gates/thresholds.json and baselines in
// tools/gates/baselines.json. A metric may have both, and when it does, they
// agree on direction.
//
// The terms are kept apart from the code under measurement so the party under
// judgement cannot move them in the same change, which is why the paths are
// fixed rather than configurable.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Where the terms live, relative to a repository root.
const (
	ThresholdsFile = "tools/gates/thresholds.json"
	BaselinesFile  = "tools/gates/baselines.json"
)

// Direction is the sense in which a measurement is compared to its term. It is
// base's vocabulary, with the meaning that document gives it. The set is
// closed: an unknown value is refused at load.
type Direction string

const (
	// Down means a smaller number is better, so a term is a ceiling.
	Down Direction = "down"
	// Up means a larger number is better, so a term is a floor.
	Up Direction = "up"
)

// Cap is one entry of the thresholds file: a bound no history relaxes.
type Cap struct {
	Direction Direction `json:"direction"`
	Cap       *float64  `json:"cap"`
}

// Baseline is one entry of the baselines file: the best value a complete, green
// run has recorded.
type Baseline struct {
	Direction Direction `json:"direction"`
	// Value holds on every target.
	Value *float64 `json:"value,omitempty"`
	// Targets records a metric that genuinely differs by platform.
	Targets map[string]float64 `json:"targets,omitempty"`
}

// Terms are both files, read together.
type Terms struct {
	Caps      map[string]Cap
	Baselines map[string]Baseline
}

// LoadTerms reads both files strictly. The judge refuses to answer, naming the
// file and the entry, when an entry has an unknown key, a missing field, an
// unknown direction, or both a value and targets.
func LoadTerms(root string) (Terms, error) {
	caps, err := loadCaps(root)
	if err != nil {
		return Terms{}, err
	}
	baselines, err := loadBaselines(root)
	if err != nil {
		return Terms{}, err
	}
	for name, c := range caps {
		b, both := baselines[name]
		if both && b.Direction != c.Direction {
			return Terms{}, fmt.Errorf("%s and %s: %q is capped %q and based %q, and a metric's two terms agree on direction",
				ThresholdsFile, BaselinesFile, name, c.Direction, b.Direction)
		}
	}
	return Terms{Caps: caps, Baselines: baselines}, nil
}

func loadCaps(root string) (map[string]Cap, error) {
	var caps map[string]Cap
	if err := readTerms(root, ThresholdsFile, &caps); err != nil {
		return nil, err
	}
	for name, c := range caps {
		if err := knownDirection(ThresholdsFile, name, c.Direction); err != nil {
			return nil, err
		}
		if c.Cap == nil {
			return nil, fmt.Errorf("%s: %q states no cap", ThresholdsFile, name)
		}
	}
	return caps, nil
}

func loadBaselines(root string) (map[string]Baseline, error) {
	var baselines map[string]Baseline
	if err := readTerms(root, BaselinesFile, &baselines); err != nil {
		if os.IsNotExist(err) {
			// A project with no ratchet has no file, and that is not a defect.
			return map[string]Baseline{}, nil
		}
		return nil, err
	}
	for name, b := range baselines {
		if err := knownDirection(BaselinesFile, name, b.Direction); err != nil {
			return nil, err
		}
		switch {
		case b.Value != nil && b.Targets != nil:
			return nil, fmt.Errorf("%s: %q states both a value and targets, and a baseline is one or the other", BaselinesFile, name)
		case b.Value == nil && b.Targets == nil:
			return nil, fmt.Errorf("%s: %q states neither a value nor targets", BaselinesFile, name)
		}
	}
	return baselines, nil
}

// readTerms decodes one term file, refusing a key the shape does not carry. A
// key nobody reads is a term nobody applies, and silence there is the one
// failure a ratchet cannot recover from.
func readTerms(root, file string, into any) error {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		return fmt.Errorf("%s: %w", file, err)
	}
	return nil
}

func knownDirection(file, name string, d Direction) error {
	switch d {
	case Down, Up:
		return nil
	case "":
		return fmt.Errorf("%s: %q states no direction (must be %q or %q)", file, name, Down, Up)
	}
	return fmt.Errorf("%s: %q has unknown direction %q (must be %q or %q)", file, name, d, Down, Up)
}

// For resolves a baseline to the value in force on one target. A baseline with
// a single value holds on every target; one recorded per target holds only
// where it was recorded.
func (b Baseline) For(target string) (float64, bool) {
	if b.Value != nil {
		return *b.Value, true
	}
	v, ok := b.Targets[target]
	return v, ok
}

// AppliedTerm is one term as the verdict reports it. A verdict travels with the
// terms it was reached from, so a reader who was not there can re-check it.
type AppliedTerm struct {
	Direction Direction `json:"direction"`
	Cap       *float64  `json:"cap,omitempty"`
	Baseline  *float64  `json:"baseline,omitempty"`
}

// Verdict is what the judging layer answers: one JSON object, whole.
//
// Thresholds is never nil. A verdict handed over with the terms it was reached
// from discarded cannot be re-checked by anyone who was not there, which is
// exactly the property that lets a judge live in the tree it judges.
type Verdict struct {
	Acceptable bool                   `json:"acceptable"`
	Thresholds map[string]AppliedTerm `json:"thresholds"`
	Detail     string                 `json:"detail,omitempty"`
}

// Human is not reached: --verdict's stdout belongs to the gate contract, so the
// command has one mode and the library never asks for a rendering.
func (Verdict) Human(io.Writer) error {
	return fmt.Errorf("a verdict's shape is gate-contract.md's, and it has no rendering for a person")
}

// Judge compares one envelope against the terms and reaches the verdict.
//
// This is the only comparison. The human path and the wire path both come
// through here, because two paths reaching a verdict separately would
// eventually disagree — and a project that answers "acceptable" to a runner and
// prints a failure to a person has two terms wearing one name.
//
// It returns the terms it applied, not the whole table: the terms a measurement
// never mentioned had no part in it.
func Judge(env Envelope, terms Terms, remediation string) (Verdict, error) {
	applied := map[string]AppliedTerm{}
	var failures []string

	for _, m := range env.Metrics {
		term := AppliedTerm{}
		judged := false

		if c, ok := terms.Caps[m.Name]; ok {
			if err := whole(m, *c.Cap, "cap"); err != nil {
				return Verdict{}, err
			}
			term.Direction, term.Cap, judged = c.Direction, c.Cap, true
			if beyond(m.Number(), *c.Cap, c.Direction) {
				failures = append(failures, fmt.Sprintf("%s is %s, cap %s%s",
					m.Name, m, number(*c.Cap), where(env, m.Name, *c.Cap, c.Direction)))
			}
		}
		if b, ok := terms.Baselines[m.Name]; ok {
			if floor, recorded := b.For(env.Target); recorded {
				if err := whole(m, floor, "baseline"); err != nil {
					return Verdict{}, err
				}
				value := floor
				term.Direction, term.Baseline, judged = b.Direction, &value, true
				if beyond(m.Number(), floor, b.Direction) {
					failures = append(failures, fmt.Sprintf("%s is %s, baseline %s%s",
						m.Name, m, number(floor), where(env, m.Name, floor, b.Direction)))
				}
			}
		}
		// A metric with no term is reported as not judged, and cannot fail.
		if judged {
			applied[m.Name] = term
		}
	}

	switch {
	case env.Incomplete != "":
		// Refused even when every number is within its term. The numbers are
		// honest and describe less than a full run, which is indistinguishable
		// from an improvement unless the run says so.
		return Verdict{
			Acceptable: false,
			Thresholds: applied,
			Detail:     "the run measured less than a full one, and an incomplete run is never a pass: " + env.Incomplete,
		}, nil
	case len(failures) > 0:
		detail := strings.Join(failures, "; ") + "."
		if remediation != "" {
			detail += " " + remediation + "."
		}
		return Verdict{Acceptable: false, Thresholds: applied, Detail: detail}, nil
	case len(applied) == 0:
		// acceptable: true over nothing would be a pass that no term granted.
		return Verdict{}, ErrNothingJudged
	default:
		return Verdict{Acceptable: true, Thresholds: applied, Detail: "every judged measurement is within its term"}, nil
	}
}

// ErrNothingJudged is an envelope none of whose metrics has a term. `run
// --verdict` writes nothing and exits 1: a verdict the judge could not reach is
// not a refusal of the tree.
var ErrNothingJudged = fmt.Errorf("no metric this gate reported has a term, so there is no verdict to reach")

// beyond reports whether a measurement is on the wrong side of a term.
func beyond(value, term float64, d Direction) bool {
	if d == Down {
		return value > term
	}
	return value < term
}

// whole refuses a term that is not a whole number for an integer metric. The
// judge cannot answer, rather than rounding: a rounded term is a second term
// nobody wrote down.
func whole(m Measurement, term float64, kind string) error {
	if m.Type != Int || term == float64(int64(term)) {
		return nil
	}
	return fmt.Errorf("%s counts, and its %s %s is not a whole number: the term is the defect, and the judge will not round it",
		m.Name, kind, strconv.FormatFloat(term, 'f', -1, 64))
}

// where names the units a metric's number came from, out of the envelope's own
// groups. A judge handed no evidence says it was handed none, rather than
// supplying a plausible cause for a measurement it did not take.
func where(env Envelope, metric string, term float64, d Direction) string {
	var named []string
	for _, g := range env.Groups {
		for _, m := range g.Metrics {
			if m.Name != metric || !contributed(m.Number(), term, d) {
				continue
			}
			named = append(named, fmt.Sprintf("%s (%s)", g.Name, m))
		}
	}
	if len(named) == 0 {
		return ""
	}
	return ", in " + strings.Join(named, " and ")
}

// contributed reports whether one unit's number is part of why the whole is
// beyond its term, which is what makes it evidence rather than noise.
//
// A ceiling is a bound on things counted, and the counts sum: a unit that
// counted none contributed none, and naming it points at a unit with nothing
// wrong. A floor is not a sum — statement coverage is one ratio over every
// unit — so what explains a shortfall is the unit that is itself short, and a
// unit at zero is the strongest evidence there is rather than the weakest.
func contributed(value, term float64, d Direction) bool {
	if d == Down {
		return value != 0
	}
	return beyond(value, term, d)
}

func number(v float64) string {
	if v == float64(int64(v)) {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'f', 1, 64)
}

// Ratchet moves each baseline whose metric this run measured completely, in the
// baseline's direction, when the run improved on it. Only verify calls it, and
// only after every measuring stage has passed.
//
// An entry is never added or removed by a run: making a metric a ratchet is a
// person's decision, and tracking the file is the declaration. An incomplete
// run moves nothing, and neither does a red one.
func Ratchet(root string, envelopes []Envelope) ([]string, error) {
	baselines, err := loadBaselines(root)
	if err != nil {
		return nil, err
	}
	if len(baselines) == 0 {
		return nil, nil
	}

	var moved []string
	for _, env := range envelopes {
		if env.Incomplete != "" {
			continue
		}
		for _, m := range env.Metrics {
			b, ok := baselines[m.Name]
			if !ok {
				continue
			}
			was, recorded := b.For(env.Target)
			switch {
			case !recorded:
				// A target with no recorded value is recorded.
				moved = append(moved, fmt.Sprintf("%s on %s recorded at %s", m.Name, env.Target, m))
			case beyond(was, m.Number(), b.Direction):
				moved = append(moved, fmt.Sprintf("%s on %s moved from %s to %s", m.Name, env.Target, number(was), m))
			default:
				continue
			}
			baselines[m.Name] = record(b, env.Target, m.Number())
		}
	}
	if len(moved) == 0 {
		return nil, nil
	}
	if err := writeBaselines(root, baselines); err != nil {
		return nil, err
	}
	sort.Strings(moved)
	return moved, nil
}

// record puts a value into a baseline, keeping the shape the person who wrote
// the entry chose. A baseline recorded per target stays per target.
func record(b Baseline, target string, value float64) Baseline {
	if b.Targets != nil {
		targets := map[string]float64{}
		for k, v := range b.Targets {
			targets[k] = v
		}
		targets[target] = value
		b.Targets = targets
		return b
	}
	v := value
	b.Value = &v
	return b
}

// writeBaselines writes the file back, atomically and with the stable key order
// a tracked file needs to produce a reviewable diff.
func writeBaselines(root string, baselines map[string]Baseline) error {
	body, err := json.MarshalIndent(baselines, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(root, filepath.FromSlash(BaselinesFile))
	return writeAtomically(path, append(body, '\n'))
}

// writeAtomically replaces a file through a temporary beside it and a rename,
// so no reader ever sees a half-written one. The temporary is made in the
// target's own directory, since a rename is only atomic within one filesystem.
func writeAtomically(path string, body []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}
