package tooling

import (
	"slices"
	"strings"
	"testing"
)

// fixtureGate is a gate a test adds, with everything Check requires of one.
func fixtureGate(name string) Gate {
	return Gate{
		Name:        name,
		Summary:     "a gate a test stands in for",
		Metrics:     Declared(Count("n")),
		Measure:     func(*Run, []Unit) (Measured, error) { return Measured{}, nil },
		Remediation: "there is nothing to do about a fixture",
	}
}

// The definition admits four moves, and each one is a move a project makes
// rather than a variant of the library it gets (docs/project-tools.md, The
// definition).
func TestTheDefinitionAdmitsAddReplaceComposeAndRemove(t *testing.T) {
	t.Run("add a gate", func(t *testing.T) {
		p := Standard()
		p.Gates.Add(fixtureGate("size"))
		if _, ok := p.Gates.Get("size"); !ok {
			t.Error("a gate the project added is not in the set")
		}
	})

	t.Run("replace the measurement behind a concept", func(t *testing.T) {
		p := Standard()
		replaced := fixtureGate(Tested)
		replaced.Summary = "this project's own way of measuring its suite"
		p.Gates.Add(replaced)

		got, ok := p.Gates.Get(Tested)
		if !ok || got.Summary != replaced.Summary {
			t.Errorf("tested is %q, want the project's replacement", got.Summary)
		}
		// One gate of that name, not two: adding over a name replaces it, so a
		// project cannot end up with two answers to one concept.
		seen := 0
		for _, g := range p.Gates.All() {
			if g.Name == Tested {
				seen++
			}
		}
		if seen != 1 {
			t.Errorf("%d gates named %q", seen, Tested)
		}
	})

	t.Run("compose what integration is made of", func(t *testing.T) {
		p := Standard()
		p.Gates.Add(fixtureGate("size"))
		p.Integration(Formatted, "size")

		g, _ := p.Gates.Get(Integration)
		if !slices.Equal(g.Parts, []string{Formatted, "size"}) {
			t.Errorf("integration is %v, want what the project composed", g.Parts)
		}
	})

	t.Run("remove a concept the project does not have", func(t *testing.T) {
		p := Standard()
		p.Gates.Remove(Covered)
		p.Integration(Formatted, Builds, Checked, Tested)

		if _, ok := p.Gates.Get(Covered); ok {
			t.Error("a removed gate is still in the set")
		}
		// A project need not have every concept, and the set it kept is still a
		// definition with no defects.
		for _, defect := range Check(p, nil) {
			t.Errorf("after removing %s: %v", Covered, defect)
		}
	})
}

// A project adds a verify step before or after a stage, and the standard stages
// keep their order around it.
func TestAProjectPutsItsOwnStepsAroundTheStandardStages(t *testing.T) {
	p := Standard()
	p.Verify.Before(StageBuilds, Step{
		Name: "frontend", Summary: "build web/dist, which the server embeds",
		Run: func(*Run) error { return nil },
	})
	p.Verify.After(StageRecord, Step{
		Name: "publish", Summary: "a step that runs once the tree is blessed",
		Run: func(*Run) error { return nil },
	})

	var order []string
	for _, stage := range p.Verify.Stages() {
		order = append(order, stage.Name)
	}
	want := []string{StageRepair, "frontend", StageBuilds, StageMeasure, StageRatchet, StageRecord, "publish"}
	if !slices.Equal(order, want) {
		t.Errorf("stages = %v, want %v", order, want)
	}
}

// A step added into a stage runs beside the standard ones rather than ending
// the run on its own, and the composition's own steps come first.
func TestAProjectAddsAStepIntoAStage(t *testing.T) {
	p := Standard()
	p.Verify.Into(StageMeasure, Step{Name: "extra", Summary: "beside the parts", Run: func(*Run) error { return nil }})

	for _, stage := range p.Verify.Stages() {
		if stage.Name != StageMeasure {
			continue
		}
		var steps []string
		for _, s := range stage.Steps {
			steps = append(steps, s.Name)
		}
		if steps[len(steps)-1] != "extra" {
			t.Errorf("the measure stage runs %v, want the project's step beside the parts", steps)
		}
		return
	}
	t.Error("there is no measure stage")
}

// A tool whose definition has a defect refuses to run and names every defect.
// The project's own tests call the same check, so a defect fails `tested`
// before any invocation reaches it.
func TestEveryDefectInADefinitionIsNamed(t *testing.T) {
	for _, c := range []struct {
		name     string
		build    func(*Project)
		commands []string
		says     string
	}{
		{"a gate name that is also a command's name",
			func(p *Project) { p.Gates.Add(fixtureGate("issue")) },
			[]string{"issue"}, "both a gate and a command"},
		{"integration absent",
			func(p *Project) { p.Gates.Remove(Integration) }, nil, `"integration" is absent`},
		{"fit absent",
			func(p *Project) { p.Gates.Remove(Fit) }, nil, `"fit" is absent`},
		{"a composition part that names no gate",
			func(p *Project) { p.Integration(Formatted, "nowhere") }, nil, "names no gate"},
		{"a gate without a remediation",
			func(p *Project) {
				g := fixtureGate("size")
				g.Remediation = ""
				p.Gates.Add(g)
			}, nil, "states no remediation"},
		{"a name outside the alphabet",
			func(p *Project) { p.Gates.Add(fixtureGate("Size_2")) }, nil, "is not a gate name"},
		{"a gate that measures and declares nothing",
			func(p *Project) {
				g := fixtureGate("size")
				g.Metrics = nil
				p.Gates.Add(g)
			}, nil, "declares nothing"},
		{"a gate that neither measures nor composes",
			func(p *Project) {
				g := fixtureGate("size")
				g.Measure = nil
				p.Gates.Add(g)
			}, nil, "neither measures nor composes"},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := Standard()
			c.build(&p)

			defects := Check(p, c.commands)
			if len(defects) == 0 {
				t.Fatal("the definition was accepted")
			}
			var said []string
			for _, d := range defects {
				said = append(said, d.Error())
			}
			if !strings.Contains(strings.Join(said, "\n"), c.says) {
				t.Errorf("the defects are %v, want one saying %q", said, c.says)
			}
		})
	}
}

// The standard definition has no defects of its own: a project that adds
// nothing starts from a set that already passes the check every tool runs.
func TestTheStandardDefinitionHasNoDefects(t *testing.T) {
	for _, defect := range Check(Standard(), nil) {
		t.Errorf("tooling.Standard(): %v", defect)
	}
}

// An instance name may carry the separator the CLI guide's alphabet does not
// admit, and nothing else outside it.
func TestAGateNameIsTheAlphabetPlusTheInstanceSeparator(t *testing.T) {
	for name, want := range map[string]bool{
		"tested":              true,
		"tested:tools-build":  true,
		"fit:disk":            true,
		"Tested":              false,
		"tested::root":        false,
		"tested:":             false,
		"tested_root":         false,
		"":                    false,
		"tested:go-root:more": true,
	} {
		if got := validGateName(name); got != want {
			t.Errorf("validGateName(%q) = %v, want %v", name, got, want)
		}
	}
}
