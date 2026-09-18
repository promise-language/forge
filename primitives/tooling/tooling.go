// Package tooling is the one implementation of `make`, `setup`, `verify`,
// `gate` and `run` that every managed project builds its tools from
// (docs/project-tools.md). A project shapes it with a definition written in Go,
// and with nothing else.
//
// What the definition reaches is the gate set, the steps and the terms. What it
// does not reach — the surface of the five tools, the envelope, the listings,
// the judge's comparison, the ratchet, the verified-tree record, the staleness
// refusal and the confinement of writes — is the same in every project, because
// none of those is a place a project has standing to differ
// (docs/primitives.md, What belongs here).
//
// # The entry points carry a Tool suffix
//
// docs/project-tools.md writes a tool's main as `tooling.Gate(common.Define(),
// stamp).Run(...)` and a gate declaration as `tooling.Gate{...}` in the same
// document. Go has one identifier for both, so the declarations keep the
// document's nouns — Gate, Step, Metric — and the five entry points are
// GateTool, RunTool, VerifyTool, SetupTool and MakeTool. The document says the
// API's home is this source, and this is the one place the sample could not be
// transcribed literally.
package tooling

import (
	"fmt"
	"sort"
	"strings"

	"github.com/promise-language/forge/primitives/command"
)

// The gate concepts flow names, as the definition spells them.
const (
	Formatted    = "formatted"
	Builds       = "builds"
	Checked      = "checked"
	Tested       = "tested"
	Covered      = "covered"
	Integration  = "integration"
	Fit          = "fit"
	FitDisk      = "fit:disk"
	FitToolchain = "fit:toolchain"
)

// The standard verify stages, in order (docs/project-tools.md, Verify). The
// second is spelled Builds above, because it is the gate concept and the stage
// under one name — which is what the document's own `p.Verify.Before(
// tooling.Builds, …)` writes.
const (
	StageRepair  = "repair"
	StageBuilds  = Builds
	StageMeasure = "measure"
	StageRatchet = "ratchet"
	StageRecord  = "record"
)

// InstanceSeparator joins a concept to the instance that narrows it. It is the
// one character the CLI guide's alphabet does not admit that a derived name may
// carry (docs/project-tools.md, The definition).
const InstanceSeparator = ":"

// MetricType is what kind of value a measurement is. The set is base's
// (gate-contract.md, What a metric declares), and it is closed.
type MetricType string

const (
	// Int counts things. A count is a whole number of them.
	Int MetricType = "int"
	// Float measures a quantity that is not a count.
	Float MetricType = "float"
	// Bool measures a property — builds for a target, carries the licence
	// header. It is for a subject that genuinely has two states, and never for
	// summarizing one that has more: a count collapsed to a bool has thrown
	// away the magnitude that lets a ratchet move by degrees and a regression
	// be located.
	Bool MetricType = "bool"
)

// Metric is a metric's declaration: the name a gate reports, the kind of value
// it is, and the unit it is in. It carries no value — the declaration and the
// measurement are a claim and its check (docs/project-tools.md, Gate).
type Metric struct {
	Name string     `json:"name"`
	Type MetricType `json:"type"`
	Unit string     `json:"unit,omitempty"`
}

// Count declares a measurement of how many.
func Count(name string) Metric { return Metric{Name: name, Type: Int} }

// Bytes declares a measurement of how much, in whole bytes.
func Bytes(name string) Metric { return Metric{Name: name, Type: Int, Unit: "bytes"} }

// Percent declares a measurement of a proportion.
func Percent(name string) Metric { return Metric{Name: name, Type: Float, Unit: "percent"} }

// Property declares a measurement of whether something holds. It carries no
// unit: a property is not measured in anything.
func Property(name string) Metric { return Metric{Name: name, Type: Bool} }

// Gate is one gate the project answers: a leaf that measures, or a composition
// of other gates.
type Gate struct {
	// Name is the gate's one name, a concept or a declared instance of one.
	Name string
	// Summary is the one-line description of what it measures.
	Summary string
	// Metrics is what this gate declares it will report. A measurement whose
	// type or unit disagrees with its declaration is an error rather than a
	// value to absorb.
	//
	// It is a function of the units because the set depends on which toolchains
	// have units: `checked` reports vet_findings for Go and check_findings for
	// Promise, and a toolchain with no units contributes nothing. A gate whose
	// declaration is fixed writes Declared(...).
	Metrics func(*Run, []Unit) []Metric
	// Measure takes the measurement. Nil on a composition.
	Measure MeasureFunc
	// Parts names the gates a composition is made of, in order.
	Parts []string
	// Remediation is what a person does about a measurement over its term.
	Remediation string
	// PerUnit says the gate accepts an instance naming one unit, so a step that
	// broke one unit's suite can re-run that suite alone. A gate whose subject
	// is not a unit does not narrow.
	PerUnit bool
	// Prepare runs once per process before any part that needs it — building
	// the compiler the measurements use, for example. Its failure makes every
	// dependent part incomplete, with the reason.
	Prepare func(*Run) error
	// Target overrides the host's os/arch for the instance this gate measured.
	Target string
}

// MeasureFunc takes one gate's measurement over the units it was narrowed to.
type MeasureFunc func(*Run, []Unit) (Measured, error)

// Measured is what one gate's measurement returned: the combined metrics, the
// per-unit groups that say where each number came from, and the reason the run
// measured less than a full one, when it did.
type Measured struct {
	Metrics []Measurement
	Groups  []Group
	// Incomplete is never set to an empty string by a measurement that is
	// incomplete: a run that is incomplete with nothing to say is the one state
	// an envelope cannot mean.
	Incomplete string
}

// GateSet is the gates a project answers. The order gates were added in is not
// the order they are listed in: every listing sorts.
type GateSet struct {
	gates []Gate
}

// Add appends a gate. A second gate of the same name replaces the first, which
// is what makes Replace and Add one operation rather than two spellings.
func (s *GateSet) Add(g Gate) {
	for i := range s.gates {
		if s.gates[i].Name == g.Name {
			s.gates[i] = g
			return
		}
	}
	s.gates = append(s.gates, g)
}

// Remove drops a gate by name. Removing one a composition still names is a
// definition defect, reported by Check rather than here.
func (s *GateSet) Remove(name string) {
	kept := s.gates[:0]
	for _, g := range s.gates {
		if g.Name != name {
			kept = append(kept, g)
		}
	}
	s.gates = kept
}

// Get returns the gate of that name, and whether there is one.
func (s *GateSet) Get(name string) (Gate, bool) {
	for _, g := range s.gates {
		if g.Name == name {
			return g, true
		}
	}
	return Gate{}, false
}

// All returns every declared gate, sorted by name.
func (s *GateSet) All() []Gate {
	out := append([]Gate{}, s.gates...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Step is one step of one verify or setup stage.
type Step struct {
	// Name is what the summary and the JSON call it.
	Name string
	// Summary is the one-line description of what it does.
	Summary string
	// Run does the work, and its error is the step's failure.
	Run func(*Run) error
}

// Stage is one stage of the verify pipeline. Within a stage every step runs and
// every failure is tallied; a stage with a failure ends the run.
type Stage struct {
	Name  string
	Steps []Step
}

// Pipeline is the verify pipeline: the stages, in order.
type Pipeline struct {
	stages []Stage
}

// Stages returns the pipeline, in order.
func (p *Pipeline) Stages() []Stage { return append([]Stage{}, p.stages...) }

// AddStage appends a stage, or replaces the one of that name.
func (p *Pipeline) AddStage(s Stage) {
	for i := range p.stages {
		if p.stages[i].Name == s.Name {
			p.stages[i] = s
			return
		}
	}
	p.stages = append(p.stages, s)
}

// Before inserts a stage holding one step immediately before the named stage. A
// name no stage carries appends, which is the reading that never silently drops
// a project's step.
func (p *Pipeline) Before(stage string, s Step) {
	p.insert(stage, Stage{Name: s.Name, Steps: []Step{s}}, 0)
}

// After inserts a stage holding one step immediately after the named stage.
func (p *Pipeline) After(stage string, s Step) {
	p.insert(stage, Stage{Name: s.Name, Steps: []Step{s}}, 1)
}

func (p *Pipeline) insert(stage string, s Stage, offset int) {
	for i := range p.stages {
		if p.stages[i].Name != stage {
			continue
		}
		at := i + offset
		p.stages = append(p.stages[:at], append([]Stage{s}, p.stages[at:]...)...)
		return
	}
	p.stages = append(p.stages, s)
}

// replaceGateSteps puts the steps a composition implies into one stage,
// keeping whatever else that stage holds. It is how one composition stays the
// single source of what verify measures without discarding a project's own
// steps in the same stage.
func (p *Pipeline) replaceGateSteps(stage string, steps []Step, isGate func(string) bool) {
	for i := range p.stages {
		if p.stages[i].Name != stage {
			continue
		}
		kept := []Step{}
		for _, s := range p.stages[i].Steps {
			if !isGate(s.Name) {
				kept = append(kept, s)
			}
		}
		p.stages[i].Steps = append(steps, kept...)
		return
	}
	p.stages = append(p.stages, Stage{Name: stage, Steps: steps})
}

// Into appends a step to the named stage, which is how a project adds work that
// runs beside the standard steps rather than stopping the run on its own.
func (p *Pipeline) Into(stage string, s Step) {
	for i := range p.stages {
		if p.stages[i].Name == stage {
			p.stages[i].Steps = append(p.stages[i].Steps, s)
			return
		}
	}
	p.stages = append(p.stages, Stage{Name: stage, Steps: []Step{s}})
}

// Build is the project's own work around the compile that `make` performs.
type Build struct {
	// Before runs after the hooks are wired and before any tool is compiled.
	Before []Step
	// After runs once every tool has compiled and before the sidecar is written.
	After []Step
}

// Project is a project's whole tooling definition.
type Project struct {
	// Gates are the gates this project answers.
	Gates GateSet
	// Verify is the commit gate's pipeline.
	Verify Pipeline
	// Setup are the project's own setup steps, run after the hooks and the
	// ignores. They report whether they changed anything, because a second run
	// changes nothing and says so.
	Setup []SetupStep
	// Make are the project's own steps around the compile.
	Make Build
	// Toolchains are the languages this project's units may be written in.
	Toolchains []Toolchain

	integration []string
}

// Integration composes what `integration` is made of, and in what order. A flow
// requires the gate, so this replaces the parts rather than the gate.
func (p *Project) Integration(parts ...string) {
	p.integration = append([]string{}, parts...)
	g, ok := p.Gates.Get(Integration)
	if !ok {
		g = Gate{
			Name:        Integration,
			Summary:     "everything that must hold before a change may land",
			Remediation: "clear the part that failed; `bin/run <part>` measures it alone",
		}
	}
	g.Parts = p.integration
	g.Measure = nil
	p.Gates.Add(g)

	// What verify measures follows what integration is made of, from this one
	// place: a project that recomposed integration and left verify measuring
	// the old set would have two answers to what must hold before a change may
	// land.
	p.Verify.replaceGateSteps(StageMeasure, measureSteps(p), func(name string) bool {
		_, ok := p.Gates.Get(name)
		return ok
	})
}

// IntegrationParts names what `integration` is composed of.
func (p *Project) IntegrationParts() []string { return append([]string{}, p.integration...) }

// Check reports every defect in a definition. A tool whose definition has one
// refuses to run and names every defect, with status 1; `-help` and `-version`
// still answer, because they describe the binary rather than act on the tree
// (docs/project-tools.md, The definition).
//
// The project's own tests call it, so a defect fails `tested` before any
// invocation reaches it.
func Check(p Project, commands []string) []error {
	var problems []error
	named := map[string]bool{}
	for _, c := range commands {
		named[c] = true
	}

	declared := map[string]bool{}
	for _, g := range p.Gates.All() {
		if named[g.Name] {
			problems = append(problems, fmt.Errorf("%q is both a gate and a command, and `run %s` would have two meanings", g.Name, g.Name))
		}
		if declared[g.Name] {
			problems = append(problems, fmt.Errorf("two gates named %q", g.Name))
		}
		declared[g.Name] = true
		if !validGateName(g.Name) {
			problems = append(problems, fmt.Errorf("%q is not a gate name: lowercase letters, digits, - as the only separator, and %q joining an instance", g.Name, InstanceSeparator))
		}
		if g.Remediation == "" {
			problems = append(problems, fmt.Errorf("gate %q states no remediation, so a failing verdict cannot say what to do about it", g.Name))
		}
		if g.Measure != nil && g.Metrics == nil {
			problems = append(problems, fmt.Errorf("gate %q measures and declares nothing, so nothing it reports can be checked against a declaration", g.Name))
		}
		if g.Measure == nil && len(g.Parts) == 0 {
			problems = append(problems, fmt.Errorf("gate %q neither measures nor composes", g.Name))
		}
	}

	for _, want := range []string{Integration, Fit} {
		if _, ok := p.Gates.Get(want); !ok {
			problems = append(problems, fmt.Errorf("%q is absent, and a flow requires it", want))
		}
	}
	for _, g := range p.Gates.All() {
		for _, part := range g.Parts {
			if _, ok := p.Gates.Get(part); !ok {
				problems = append(problems, fmt.Errorf("gate %q is composed of %q, which names no gate", g.Name, part))
			}
		}
	}
	return problems
}

// validGateName is the CLI guide's alphabet with the instance separator
// admitted, which is the one exception docs/project-tools.md, The definition
// makes for a derived name.
func validGateName(name string) bool {
	if name == "" {
		return false
	}
	for _, segment := range strings.Split(name, InstanceSeparator) {
		if !command.ValidName(segment) {
			return false
		}
	}
	return true
}

// Entry is one built tool: its command tree, ready to run.
type Entry struct {
	tool command.Tool
}

// Run is the whole of an invocation, and the only call a main makes.
func (e Entry) Run(args []string) int {
	return command.Run(e.tool, args, command.Stdio())
}

// RunWith is Run with the streams injected, which is what lets every rule be
// tested without starting a process.
func (e Entry) RunWith(args []string, s command.Streams) int {
	return command.Run(e.tool, args, s)
}

// Tool is the command tree this entry point built, for a project's own tests.
func (e Entry) Tool() command.Tool { return e.tool }
