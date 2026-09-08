package common

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Gates: measurements a decision may rest on.
//
// A gate MEASURES and modifies nothing it measures — including afterwards.
// Measuring faithfully and then tidying up is not a gate, because the answer
// then describes a tree that no longer exists. That is what separates these
// from `verify`, which is a COMMAND: verify repairs what has one right answer
// on its way to an answer, which is exactly what a producing step wants and
// exactly why a decision may not rest on it.
//
// The names are the flow SDK's closed vocabulary — a concept, optionally with
// an instance after a colon (`tested:tools`). Gates are addressed by name
// rather than configured as command strings, which is what lets a step ask for
// the one suite it broke instead of paying for the whole set, and what keeps
// this from becoming an arbitrary command executor.
//
// A project need not provide all of them. The flow requires two: the `verify`
// command, and the `integration` gate a landing decision rests on.
//
// This repository is two Go modules — the root and tools/build — so every gate
// here runs against both. A gate that silently covered only one would report a
// tree sound while half of it was unmeasured.

// GateConcept is the part of a gate name before the colon.
type GateConcept string

const (
	// GateFormatted asks whether the tree is formatted. It does NOT format it:
	// `gofmt -l` lists what would change, where verify's `gofmt -w` changes it.
	// Same subject, same tool, opposite obligation — and running the mutating
	// form here is the single easiest way to turn this gate into a lie.
	//
	// It takes no instance. gofmt walks DIRECTORIES, not modules, and every
	// module here lives under the repository root — so one run already covers
	// the whole tree, and offering `formatted:root` would advertise a narrowing
	// that does not exist.
	GateFormatted GateConcept = "formatted"

	// GateBuilds asks whether the tree compiles.
	GateBuilds GateConcept = "builds"

	// GateChecked asks whether the static checks pass.
	GateChecked GateConcept = "checked"

	// GateTested asks whether the tests pass.
	GateTested GateConcept = "tested"

	// GateIntegration is the composition a landing decision rests on: every
	// other gate this project provides, in the cheapest-first order that fails
	// fastest.
	GateIntegration GateConcept = "integration"

	// GateFit asks whether this MACHINE may be given work at all — the one
	// gate here whose subject is not the code.
	//
	// Required, alongside integration: flow's gates-and-commands.md lists
	// `verify`, `integration`, `fit` and the judge as the four things a flow
	// cannot run without. A project that does not answer it is not merely
	// missing a measurement — `issue resolve` refuses the project outright and
	// waits for a condition that no amount of waiting clears.
	//
	// It is deliberately NOT part of integration. A machine that cannot build
	// is not a change that may not land, and folding the two would fail an
	// honest change for a fact about the host it happened to run on.
	GateFit GateConcept = "fit"
)

// modules are the Go modules in this repository, as directories relative to the
// repository root. Every gate runs across all of them.
var modules = []string{".", filepath.Join("tools", "build")}

// RunGate runs the named gate and returns nil when it passes.
//
// name is a flow gate name: a concept, optionally `concept:instance`. An
// instance selects one module here; omitting it runs every module.
func RunGate(repoRoot, name string) error {
	concept, instance := splitGateName(name)

	if concept == GateIntegration {
		if instance != "" {
			return fmt.Errorf("gate %q: integration takes no instance — it is the composition of the others", name)
		}
		return runIntegration(repoRoot)
	}

	if concept == GateFormatted {
		if instance != "" {
			return fmt.Errorf("gate %q: formatted takes no instance — gofmt walks directories, "+
				"not modules, so one run already covers the whole tree", name)
		}
		return runFormatted(repoRoot)
	}

	if concept == GateFit {
		if instance != "" {
			return fmt.Errorf("gate %q: fit takes no instance here — its instances name conditions "+
				"on the machine (`fit:disk`, `fit:toolchain`), not modules of this project", name)
		}
		return runFit(repoRoot)
	}

	run, ok := gateRunners()[concept]
	if !ok {
		return fmt.Errorf("gate %q: unknown concept %q (this project provides: %s)",
			name, concept, strings.Join(providedConcepts(), ", "))
	}
	mods, err := modulesFor(instance)
	if err != nil {
		return fmt.Errorf("gate %q: %w", name, err)
	}
	for _, m := range mods {
		if err := run(repoRoot, m); err != nil {
			return fmt.Errorf("gate %q failed in %s: %w", name, moduleLabel(m), err)
		}
	}
	return nil
}

// gateRunner measures one concept in one module.
type gateRunner func(repoRoot, module string) error

func gateRunners() map[GateConcept]gateRunner {
	return map[GateConcept]gateRunner{
		GateBuilds:  func(root, mod string) error { return RunIn(filepath.Join(root, mod), "go", "build", "./...") },
		GateChecked: func(root, mod string) error { return RunIn(filepath.Join(root, mod), "go", "vet", "./...") },
		GateTested:  func(root, mod string) error { return RunIn(filepath.Join(root, mod), "go", "test", "./...") },
	}
}

// runFormatted checks the whole tree once.
//
// -l LISTS what is unformatted and changes nothing. Any output is a failure:
// gofmt exits 0 whether or not it found anything, so the output is the verdict
// and an exit-status check alone would pass every time.
func runFormatted(repoRoot string) error {
	out, err := RunOutputIn(repoRoot, "gofmt", "-l", ".")
	if err != nil {
		return err
	}
	if files := strings.TrimSpace(out); files != "" {
		return fmt.Errorf("gate \"formatted\" failed: not formatted:\n%s", files)
	}
	return nil
}

// runFit answers whether this machine may be given work.
//
// A placeholder that passes, and deliberately so. The gate has to EXIST before
// it can be useful: with no `fit` here, `issue resolve` refuses this project
// before its first step and waits out a bound on a condition that will never
// clear, so every arena of this repo is unresolvable. Wiring the gate up across
// the projects comes first; the terms come after, once each project has said
// what its own work needs.
//
// What belongs here is exactly what only this project can know — how much disk
// a build of it wants, which services its suites expect, what toolchain must be
// present. A floor compiled into the SDK would be a threshold held by the one
// party that cannot know it. Those conditions arrive as instances (`fit:disk`,
// `fit:toolchain`) so a wait on one re-measures that one rather than the set.
//
// Reporting no impediment is the honest reading of an empty set of conditions:
// nothing this project requires of a host is missing, because it has not yet
// said it requires anything.
func runFit(repoRoot string) error {
	return nil
}

// measurementOrder is what the non-formatting measurement consists of, cheapest
// first. Named rather than inlined so the composition is a value a test can
// read: "fit is not part of integration" is a rule about this list, and a rule
// nothing can check is one that quietly stops holding.
var measurementOrder = []GateConcept{GateChecked, GateBuilds, GateTested}

// RunMeasurement runs the non-formatting measurement across all modules:
// checked → builds → tested, cheapest first. Non-mutating by construction:
// every runner here reads and reports, never writes.
//
// This is the single callable function the issue requires. Two entry points
// use it: bin/verify (format, then measure) and the integration gate
// (formatted check, then measure). Neither shells out to the other.
func RunMeasurement(repoRoot string) error {
	for _, c := range measurementOrder {
		if err := RunGate(repoRoot, string(c)); err != nil {
			return err
		}
	}
	return nil
}

// runIntegration runs every provided gate, cheapest first.
//
// The order is not cosmetic: formatting and vet are seconds and the test suite
// is not, so a tree that is merely unformatted says so immediately rather than
// after the slowest measurement in the set.
func runIntegration(repoRoot string) error {
	if err := runFormatted(repoRoot); err != nil {
		return err
	}
	return RunMeasurement(repoRoot)
}

// splitGateName divides `concept:instance`. A name with no colon is all concept.
func splitGateName(name string) (GateConcept, string) {
	if i := strings.Index(name, ":"); i >= 0 {
		return GateConcept(name[:i]), name[i+1:]
	}
	return GateConcept(name), ""
}

// modulesFor resolves an instance to the modules it names. An empty instance
// means every module — the safe reading, since a gate that quietly measured a
// subset would report a tree sound while part of it was unmeasured.
func modulesFor(instance string) ([]string, error) {
	if instance == "" {
		return modules, nil
	}
	for _, m := range modules {
		if moduleLabel(m) == instance {
			return []string{m}, nil
		}
	}
	var known []string
	for _, m := range modules {
		known = append(known, moduleLabel(m))
	}
	return nil, fmt.Errorf("unknown instance %q (this project has: %s)", instance, strings.Join(known, ", "))
}

// moduleLabel is the instance name for a module directory: "root" for the
// repository root, else the path with separators flattened.
func moduleLabel(module string) string {
	if module == "." {
		return "root"
	}
	return strings.ReplaceAll(filepath.ToSlash(module), "/", "-")
}

// providedConcepts lists what this project answers, for an error message that
// tells the caller what it could have asked for.
func providedConcepts() []string {
	out := []string{string(GateIntegration), string(GateFormatted), string(GateFit)}
	for c := range gateRunners() {
		out = append(out, string(c))
	}
	sort.Strings(out)
	return out
}

// GateNames returns every gate name this project answers, concepts and
// instances, for `bin/gate` with no arguments.
// MeasureGate runs one gate and returns the envelope describing what it found,
// alongside the gate's own error.
//
// The envelope IS the seam between measuring and judging, so it is built here
// once rather than at each caller. `bin/gate <name> --envelope` prints it for a
// runner that will carry it elsewhere; `bin/run <gate>` hands it straight to
// the judge in the same process. Two constructions of the same document could
// disagree about what was measured, and the disagreement would be invisible —
// both callers would still emit something well-formed, and only the verdicts
// would differ.
//
// Stdout is redirected to stderr for the duration. The caller's stdout carries
// its product and nothing else — an envelope a runner parses, or a report a
// person reads — while the gate's progress still streams where someone watching
// a long gate can see it. RunIn reads os.Stdout when it spawns, so swapping it
// here redirects the children too.
func MeasureGate(repoRoot, name string) (map[string]any, error) {
	realStdout := os.Stdout
	os.Stdout = os.Stderr
	started := time.Now()
	gerr := RunGate(repoRoot, name)
	os.Stdout = realStdout

	env := map[string]any{
		"gate":            name,
		"measured":        gerr == nil,
		"elapsed_seconds": time.Since(started).Round(time.Millisecond).Seconds(),
	}
	if gerr != nil {
		env["detail"] = gerr.Error()
	}
	return env, gerr
}

func GateNames() []string {
	var out []string
	for _, c := range providedConcepts() {
		out = append(out, c)
		// None of these narrows by module: integration is the composition,
		// formatting is directory-scoped, and fit measures the machine — a
		// module is not a property of the host.
		if GateConcept(c) == GateIntegration || GateConcept(c) == GateFormatted || GateConcept(c) == GateFit {
			continue
		}
		for _, m := range modules {
			out = append(out, c+":"+moduleLabel(m))
		}
	}
	return out
}
