package common

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/promise-language/forge/primitives/command"
	"github.com/promise-language/forge/primitives/tooling"
)

// This project's own tests call the same validation every tool runs before it
// acts, so a defect in the definition fails `tested` rather than the first
// invocation that reaches it (docs/project-tools.md, The definition).
func TestTheDefinitionHasNoDefects(t *testing.T) {
	p := Define()
	for _, defect := range tooling.Check(p, commandsThisProjectBuilds(t)) {
		t.Errorf("this project's definition: %v", defect)
	}
}

// Every tool's command tree is checked too: the library refuses to run a tool
// whose tree is malformed, and finding that here is finding it before anyone
// runs one.
func TestEveryToolsTreePassesTheLibrarysCheck(t *testing.T) {
	p := Define()
	for name, entry := range map[string]tooling.Entry{
		"gate":   tooling.GateTool(p, ""),
		"run":    tooling.RunTool(p, ""),
		"verify": tooling.VerifyTool(p, ""),
		"setup":  tooling.SetupTool(p, ""),
		"make":   tooling.MakeTool(p),
	} {
		for _, defect := range command.Check(entry.Tool()) {
			t.Errorf("%s's tree: %v", name, defect)
		}
	}
}

// integration is what a decision rests on, and the corpus check is part of it:
// docs/org/ is verified against its stamp by the integration gate
// (docs/org/normative.md, Mechanical checks).
func TestIntegrationCarriesTheCorpusCheck(t *testing.T) {
	p := Define()
	g, ok := p.Gates.Get(tooling.Integration)
	if !ok {
		t.Fatal("this project has no integration gate")
	}
	if !strings.Contains(strings.Join(g.Parts, " "), "stamped") {
		t.Errorf("integration is %v, and nothing in it verifies docs/org/ against its stamp", g.Parts)
	}
}

// Coverage comes from a gate outside integration, so verify measures it in the
// stage the document names for exactly that. What judges it — a cap today, a
// baseline once promise-language/workspace#490 settles the file's shape — does
// not change which stage measures it.
func TestVerifyMeasuresTheGateOutsideIntegration(t *testing.T) {
	p := Define()
	for _, stage := range p.Verify.Stages() {
		if stage.Name != tooling.StageMeasure {
			continue
		}
		for _, step := range stage.Steps {
			if step.Name == tooling.Covered {
				return
			}
		}
	}
	t.Errorf("verify's %s stage does not measure %s, so nothing judges it before a change lands",
		tooling.StageMeasure, tooling.Covered)
}

// commandsThisProjectBuilds is the build set of the checkout the tests run in,
// so the gate-name-collides-with-a-command check is made against the real set.
func commandsThisProjectBuilds(t *testing.T) []string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "tools", "build", "cmd")); err != nil {
		t.Skipf("not running in a checkout: %v", err)
	}
	names, err := tooling.BuildSet(root)
	if err != nil {
		t.Fatal(err)
	}
	return names
}
