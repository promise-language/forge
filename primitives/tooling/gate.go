package tooling

// Gate (docs/project-tools.md).
//
// A gate measures and never modifies what it measures, and it never judges:
// whether a number is acceptable is the judge's question, and a gate holds no
// term to answer it with. A gate leaves the tracked tree as it found it —
// everything a measurement writes is scratch under .home/, which the repository
// ignores and the subject therefore excludes.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// Envelope is what a gate writes on stdout: one JSON object, written whole,
// after every measurement has returned.
//
// It is written whole deliberately. A run killed part-way leaves output that
// does not parse, which is how a reader tells "measured nothing" from "measured
// and reported" without asking the gate — a gate that died is not alive to say
// so.
type Envelope struct {
	// Gate names the gate that measured it, so a judge handed an envelope it
	// did not produce can refuse one another gate wrote.
	Gate string `json:"gate"`
	// Target is the host's os/arch, unless the gate declared one for the
	// instance it measured.
	Target string `json:"target"`
	// Metrics are the combined measurements.
	Metrics []Measurement `json:"metrics"`
	// Groups say where each number came from, which is what a failing verdict's
	// evidence is drawn from.
	Groups []Group `json:"groups,omitempty"`
	// Incomplete names the reason this run measured less than a full one, and
	// is empty when it did not. A run that skipped part of its work reports
	// honest numbers that understate what was checked, which is
	// indistinguishable from an improvement unless the run says so.
	Incomplete string `json:"incomplete,omitempty"`
}

// Human is not reached: --envelope's stdout belongs to the gate contract, so
// the command has one mode and the library never asks for a rendering.
func (Envelope) Human(io.Writer) error {
	return fmt.Errorf("an envelope's shape is gate-contract.md's, and it has no rendering for a person")
}

// HostTarget is the os/arch an envelope speaks for when its gate declares none.
func HostTarget() string { return runtime.GOOS + "/" + runtime.GOARCH }

// GateNames returns every name this project answers: each declared gate, and
// for the ones that narrow, the derived instances. It is what `gate --list`
// prints and what `run --list` reports as gates, because a name missing here is
// a name nothing will ever request.
func GateNames(r *Run) ([]string, error) {
	units, err := Units(r)
	if err != nil {
		return nil, err
	}
	instances := Instances(units)
	var out []string
	for _, g := range r.project.Gates.All() {
		out = append(out, g.Name)
		if !g.PerUnit {
			continue
		}
		for _, i := range instances {
			out = append(out, g.Name+InstanceSeparator+i.Name)
		}
	}
	sort.Strings(out)
	return out, nil
}

// ListedGate is one name, what it measures, and what it declares it will
// report. A program reads the JSON, where what a gate declares of itself can
// grow additively; the line form is for a person.
type ListedGate struct {
	Name    string   `json:"name"`
	Summary string   `json:"summary"`
	Metrics []Metric `json:"metrics"`
}

// Listing is what `gate --list` answers.
type Listing struct {
	Gates []ListedGate `json:"gates"`
}

// Human is one name per line, which is what a person scanning for the name they
// want reads fastest.
func (l Listing) Human(w io.Writer) error {
	for _, g := range l.Gates {
		if _, err := fmt.Fprintln(w, g.Name); err != nil {
			return err
		}
	}
	return nil
}

// GateListing names every concept and every instance, sorted, each with what it
// declares.
func GateListing(r *Run) (Listing, error) {
	names, err := GateNames(r)
	if err != nil {
		return Listing{}, err
	}
	units, err := Units(r)
	if err != nil {
		return Listing{}, err
	}
	listing := Listing{Gates: make([]ListedGate, 0, len(names))}
	for _, name := range names {
		g, narrowed, err := resolveGate(r, name)
		if err != nil {
			return Listing{}, err
		}
		listing.Gates = append(listing.Gates, ListedGate{
			Name:    name,
			Summary: g.Summary,
			Metrics: declaredMetrics(r, g, pick(units, narrowed)),
		})
	}
	return listing, nil
}

// pick is the units an instance narrowed to, or every unit.
func pick(units []Unit, narrowed []Unit) []Unit {
	if narrowed == nil {
		return units
	}
	return narrowed
}

// declaredMetrics is what a gate says it will report over these units. A
// composition declares its parts' metrics, because its metrics are its parts'.
func declaredMetrics(r *Run, g Gate, units []Unit) []Metric {
	if len(g.Parts) > 0 {
		seen := map[string]bool{}
		var out []Metric
		for _, part := range g.Parts {
			sub, ok := r.project.Gates.Get(part)
			if !ok {
				continue
			}
			for _, m := range declaredMetrics(r, sub, units) {
				if seen[m.Name] {
					continue
				}
				seen[m.Name] = true
				out = append(out, m)
			}
		}
		return out
	}
	if g.Metrics == nil {
		return nil
	}
	return g.Metrics(r, units)
}

// resolveGate finds the gate a name asks for, and the units an instance
// narrowed it to. A declared name wins over a derived one: `fit:disk` is a gate
// of its own, not the `fit` concept narrowed to a unit called disk.
func resolveGate(r *Run, name string) (Gate, []Unit, error) {
	if g, ok := r.project.Gates.Get(name); ok {
		return g, nil, nil
	}
	concept, instance, found := strings.Cut(name, InstanceSeparator)
	if !found {
		return Gate{}, nil, unknownGate(r, name)
	}
	g, ok := r.project.Gates.Get(concept)
	if !ok {
		return Gate{}, nil, unknownGate(r, name)
	}
	if !g.PerUnit {
		return Gate{}, nil, fmt.Errorf("gate %q: %s takes no instance — its subject is not a unit", name, concept)
	}
	units, err := Units(r)
	if err != nil {
		return Gate{}, nil, err
	}
	narrowed, err := UnitsFor(units, instance)
	if err != nil {
		return Gate{}, nil, fmt.Errorf("gate %q: %w", name, err)
	}
	return g, narrowed, nil
}

// KnownGate reports whether this project answers that name.
func KnownGate(r *Run, name string) bool {
	_, _, err := resolveGate(r, name)
	return err == nil
}

// GateSummary is the one-line description of a gate, or "" if unknown. An
// instance carries its concept's summary: `tested:root` measures what `tested`
// measures, in one unit.
func GateSummary(r *Run, name string) string {
	g, _, err := resolveGate(r, name)
	if err != nil {
		return ""
	}
	return g.Summary
}

// unknownGate is the refusal every entry point gives for a name this project
// does not have. One wording, because a caller that mistyped a gate name is
// told the same thing whichever program it typed it at.
func unknownGate(r *Run, name string) error {
	names, err := GateNames(r)
	if err != nil {
		return fmt.Errorf("no gate named %q in this project", name)
	}
	return fmt.Errorf("no gate named %q in this project; known gates: %s", name, strings.Join(names, ", "))
}

// MeasureGate runs one gate and returns what it measured.
//
// The error return means the measurement could not be obtained — a tool is
// missing, or the environment refused. It never means "the numbers are bad":
// three failing tests is a successful run of the `tested` gate, and the
// envelope says so.
func MeasureGate(r *Run, name string) (Envelope, error) {
	g, narrowed, err := resolveGate(r, name)
	if err != nil {
		return Envelope{}, err
	}
	units, err := Units(r)
	if err != nil {
		return Envelope{}, err
	}
	subject := pick(units, narrowed)

	env := Envelope{Gate: name, Target: HostTarget(), Metrics: []Measurement{}}
	if g.Target != "" {
		env.Target = g.Target
	}

	if len(g.Parts) > 0 {
		return composition(r, env, g)
	}

	if reason := prepared(r, g); reason != "" {
		// A preparation's failure makes every dependent part incomplete, with
		// the reason. It never produces a measurement of zero.
		env.Incomplete = reason
		return env, nil
	}
	if g.Measure == nil {
		return Envelope{}, fmt.Errorf("gate %q neither measures nor composes", name)
	}
	measured, err := g.Measure(r, subject)
	if err != nil {
		return Envelope{}, err
	}
	env.Metrics = measured.Metrics
	env.Groups = measured.Groups
	env.Incomplete = measured.Incomplete
	if err := declares(env, declaredMetrics(r, g, subject)); err != nil {
		return Envelope{}, fmt.Errorf("gate %q: %w", name, err)
	}
	return env, nil
}

// composition measures each part by the path a caller asking for that part
// alone would take, so the whole cannot disagree with its parts about how
// anything is measured.
func composition(r *Run, env Envelope, g Gate) (Envelope, error) {
	var reasons []string
	for _, part := range g.Parts {
		sub, err := MeasureGate(r, part)
		if err != nil {
			return Envelope{}, fmt.Errorf("%s: %w", part, err)
		}
		env.Metrics = append(env.Metrics, sub.Metrics...)
		env.Groups = append(env.Groups, sub.Groups...)
		if sub.Incomplete != "" {
			reasons = append(reasons, part+": "+sub.Incomplete)
		}
	}
	env.Incomplete = joinReasons(reasons)
	return env, nil
}

// prepared runs a gate's preparation once and reports why every dependent part
// is incomplete, or "" when it succeeded.
func prepared(r *Run, g Gate) string {
	if g.Prepare == nil {
		return ""
	}
	if done, ran := r.prepared[g.Name]; ran {
		return done
	}
	if r.prepared == nil {
		r.prepared = map[string]string{}
	}
	reason := ""
	if err := g.Prepare(r); err != nil {
		reason = fmt.Sprintf("the preparation for %s failed, so nothing it enables was measured: %v", g.Name, err)
	}
	r.prepared[g.Name] = reason
	return reason
}

// declares checks every measurement against the declaration that named it. A
// measurement that disagrees with its declaration is an error, never a value to
// absorb: a metric whose type changed measured something else, and absorbed
// silently it would move a ratchet that by construction never moves back.
func declares(env Envelope, declared []Metric) error {
	by := map[string]Metric{}
	for _, d := range declared {
		by[d.Name] = d
	}
	seen := map[string]bool{}
	for _, m := range env.Metrics {
		if seen[m.Name] {
			return fmt.Errorf("%s is reported twice in one envelope", m.Name)
		}
		seen[m.Name] = true
		d, ok := by[m.Name]
		if !ok {
			return fmt.Errorf("%s was measured and never declared", m.Name)
		}
		if err := m.Declares(d); err != nil {
			return err
		}
	}
	return nil
}

// standardGates are the concepts flow names, plus the two conditions `fit`
// divides into. A toolchain with no units in the repository contributes
// nothing, so a gate whose every toolchain is absent measures nothing and
// declares nothing.
func standardGates() []Gate {
	return []Gate{
		{
			Name:        Formatted,
			Summary:     "source files the formatter would rewrite",
			PerUnit:     true,
			Metrics:     unitMetrics(Formatted),
			Measure:     perUnit(Formatted),
			Remediation: "run bin/verify, whose repair stage rewrites them",
		},
		{
			Name:        Builds,
			Summary:     "packages that fail to compile",
			PerUnit:     true,
			Metrics:     unitMetrics(Builds),
			Measure:     perUnit(Builds),
			Remediation: "fix the compile error the build reported",
		},
		{
			Name:        Checked,
			Summary:     "static-analysis findings",
			PerUnit:     true,
			Metrics:     unitMetrics(Checked),
			Measure:     perUnit(Checked),
			Remediation: "fix what the checker reported, or justify it where the checker takes a directive",
		},
		{
			Name:        Tested,
			Summary:     "failing tests and failing packages",
			PerUnit:     true,
			Metrics:     unitMetrics(Tested),
			Measure:     perUnit(Tested),
			Remediation: "fix the failing tests",
		},
		{
			Name:        Covered,
			Summary:     "how much of the code the suite exercises",
			PerUnit:     true,
			Metrics:     unitMetrics(Covered),
			Measure:     perUnit(Covered),
			Remediation: "cover what the change added, or lower the baseline in a reviewed change",
		},
		{
			Name:        FitDisk,
			Summary:     "space where this project's work writes",
			Metrics:     Declared(Bytes("worktree_free_bytes"), Bytes("cache_free_bytes")),
			Measure:     measureDisk,
			Remediation: "free space on the filesystems the worktree and the cache sit on",
		},
		{
			Name:        FitToolchain,
			Summary:     "toolchains this repository's units need and this machine does not have",
			Metrics:     Declared(Count("missing_toolchains")),
			Measure:     measureToolchains,
			Remediation: "install the toolchain the measurement named, and put its program on PATH",
		},
		{
			Name:        Fit,
			Summary:     "whether this machine can do this project's work",
			Parts:       []string{FitDisk, FitToolchain},
			Remediation: "clear the condition the failing part named",
		},
	}
}

// Declared is a fixed declaration, for a gate whose metrics do not depend on
// which toolchains have units.
func Declared(metrics ...Metric) func(*Run, []Unit) []Metric {
	return func(*Run, []Unit) []Metric { return metrics }
}

// unitMetrics is the declaration of a concept measured per unit: the union of
// what each toolchain with units reports for it. `go vet` reports vet findings
// and `promise check` reports check findings, so the names follow the toolchain
// that will answer rather than being fixed here.
func unitMetrics(concept string) func(*Run, []Unit) []Metric {
	return func(r *Run, units []Unit) []Metric {
		seen := map[string]bool{}
		var out []Metric
		for _, u := range units {
			tc, ok := r.Toolchain(u.Toolchain)
			if !ok {
				continue
			}
			for _, m := range tc.Metrics[concept] {
				if seen[m.Name] {
					continue
				}
				seen[m.Name] = true
				out = append(out, m)
			}
		}
		return out
	}
}

// perUnit measures one concept over every unit it was narrowed to, and combines
// the results by meaning. An instance measures only the units it names.
func perUnit(concept string) MeasureFunc {
	return func(r *Run, units []Unit) (Measured, error) {
		var results []UnitResult
		var measured []Unit
		for _, u := range units {
			tc, ok := r.Toolchain(u.Toolchain)
			if !ok {
				continue
			}
			take := tc.Measure[concept]
			if take == nil {
				// The language does not have this concept. A unit that cannot
				// answer contributes nothing rather than a zero, which would be
				// a reading of a measurement that never happened.
				continue
			}
			res, err := take(r, u)
			if err != nil {
				return Measured{}, err
			}
			results = append(results, res)
			measured = append(measured, u)
		}
		return combine(measured, results), nil
	}
}

// measureDisk reports the space available where this project's work writes.
//
// It reports bytes and stops. How much is enough is a property of this
// project's build, held by the judging layer: "is there enough disk" reads like
// a yes/no question and is not one, and a gate that answered it would be the
// threshold sitting inside the party under measurement.
//
// Two filesystems, because they are two requirements and are often not one
// device: the worktree is where the change is written, and the cache is where
// the toolchains write what they reuse. Both are reported every run even when
// they resolve to the same filesystem — an envelope whose shape varied by host
// is one no term can be written against.
func measureDisk(r *Run, _ []Unit) (Measured, error) {
	worktree, err := freeBytesNear(r.Root)
	if err != nil {
		return Measured{}, fmt.Errorf("free space at the worktree: %w", err)
	}
	cache, err := freeBytesNear(filepath.Join(r.Root, filepath.FromSlash(CacheDir)))
	if err != nil {
		return Measured{}, fmt.Errorf("free space at %s: %w", CacheDir, err)
	}
	return Measured{Metrics: []Measurement{
		Counted("worktree_free_bytes", worktree, "bytes"),
		Counted("cache_free_bytes", cache, "bytes"),
	}}, nil
}

// measureToolchains counts the toolchains with units in this repository whose
// program does not run and state its version. Any other gate that needs such a
// program cannot measure, which is what makes this the one worth reporting
// before work is given to the machine.
func measureToolchains(r *Run, units []Unit) (Measured, error) {
	needed := map[string]bool{}
	for _, u := range units {
		needed[u.Toolchain] = true
	}
	var missing []string
	for _, tc := range r.project.Toolchains {
		if !needed[tc.Name] {
			continue
		}
		if why := tc.present(r); why != "" {
			missing = append(missing, why)
		}
	}
	sort.Strings(missing)
	for _, why := range missing {
		fmt.Fprintf(r.Narrate, "    %s\n", why)
	}
	return Measured{Metrics: []Measurement{
		Counted("missing_toolchains", int64(len(missing)), ""),
	}}, nil
}

// freeBytesNear reports free space for path, or for the nearest ancestor that
// exists. A cache that has never been written has no directory yet, and `fit`
// runs on exactly that machine — the fresh one, before work is given — so a
// path that is not there yet is the ordinary case rather than an error.
func freeBytesNear(path string) (int64, error) {
	for p := filepath.Clean(path); ; {
		if _, err := os.Stat(p); err == nil {
			return freeBytes(p)
		}
		parent := filepath.Dir(p)
		if parent == p {
			return 0, fmt.Errorf("nothing on the path %s exists", path)
		}
		p = parent
	}
}
