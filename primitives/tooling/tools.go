package tooling

// The five tools, each one call from a project's main.
//
// Every tool validates the definition before it acts: a tool whose definition
// has a defect refuses to run and names every defect, with status 1. -help and
// -version still answer, because they describe the binary rather than act on
// the tree (docs/project-tools.md, The definition).

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/promise-language/forge/primitives"
	"github.com/promise-language/forge/primitives/command"
)

// Standard is the whole definition for a project whose work needs nothing of
// its machine but a toolchain and disk. It carries both toolchains, the
// concepts flow names, integration composed of formatted, builds, checked and
// tested, fit divided into fit:disk and fit:toolchain, the standard verify, and
// setup's checks.
func Standard() Project {
	p := Project{Toolchains: []Toolchain{Go(), Promise()}}
	for _, g := range standardGates() {
		p.Gates.Add(g)
	}
	p.Verify = standardPipeline()
	// Composing integration is what fills verify's measure stage, so it comes
	// after the pipeline exists.
	p.Integration(Formatted, Builds, Checked, Tested)
	return p
}

// GateTool is a project's measuring entry point.
func GateTool(p Project, stamp string) Entry {
	root := StampedRoot(stamp)
	return Entry{tool: command.Tool{
		Project: "gate",
		Version: versionOf(stamp),
		Fit:     MayAct("gate", stamp),
		Root: command.Command{
			Name:    "gate",
			Summary: "measure one property of this tree",
			Flags: []command.Flag{{
				Name:        "list",
				Type:        command.Boolean,
				Description: "name every gate this project answers",
			}},
			SelectedBy: "list",
			Action: func(c *command.Call) (command.Result, error) {
				return acting(p, root, c, func(r *Run) (command.Result, error) { return GateListing(r) })
			},

			// The gates are the project's vocabulary rather than this tool's,
			// so they are computed once per invocation and then closed like any
			// declared set — and help describes them instead of listing them,
			// because `gate --list` is that enumeration's one home.
			Children:     func() []command.Command { return gateChildren(p, root) },
			ChildClass:   "<gate>",
			ChildSummary: "a gate this project answers",
			EnumeratedBy: "gate --list",
			ChildFlags: []command.Flag{{
				Name:        "envelope",
				Type:        command.Boolean,
				Description: "write the measurement as one envelope on stdout",
				Protocol:    "gate-contract.md",
			}},
			ChildValidate: requireEnvelope(p, root),
			ChildAction: func(c *command.Call) (command.Result, error) {
				return acting(p, root, c, func(r *Run) (command.Result, error) {
					env, err := MeasureGate(r, c.Name())
					if err != nil {
						// Nothing was measured. No envelope, because a partial
						// one is not a measurement and must not parse as one.
						return nil, err
					}
					return env, nil
				})
			},
		},
	}}
}

// requireEnvelope refuses a measurement nobody can read as one.
//
// A bare run that printed measurements and exited 0 would be read as a pass by
// the first script that wrapped it, and a gate has no verdict to give.
func requireEnvelope(p Project, root string) func(*command.Call) []error {
	return func(c *command.Call) []error {
		if c.Bool("envelope") {
			return nil
		}
		summary := GateSummary(inspect(p, root), c.Name())
		return []error{fmt.Errorf(
			"%s measures %s, and writes it only with --envelope; run `run %s` for a result meant for a person",
			c.Name(), summary, c.Name())}
	}
}

// RunTool is a project's judging entry point, and the one way to reach the
// commands it builds.
func RunTool(p Project, stamp string) Entry {
	root := StampedRoot(stamp)
	return Entry{tool: command.Tool{
		Project: "run",
		Version: versionOf(stamp),
		Fit:     MayAct("run", stamp),
		Root: command.Command{
			Name:    "run",
			Summary: "measure one gate and judge what it measured, or run one of this project's commands",
			Flags: []command.Flag{{
				Name:        "list",
				Type:        command.Boolean,
				Description: "name what this project builds and what it answers",
			}},
			SelectedBy: "list",
			Action: func(c *command.Call) (command.Result, error) {
				return acting(p, root, c, func(r *Run) (command.Result, error) { return List(r) })
			},

			Children:     func() []command.Command { return runChildren(p, root) },
			ChildClass:   "<gate> | <command>",
			ChildSummary: "a gate this project answers, or a command it builds",
			EnumeratedBy: "run --list",
			ChildFlags: []command.Flag{{
				Name:        "verdict",
				Type:        command.Boolean,
				Description: "judge the envelope on stdin, and run no gate",
				Protocol:    "gate-contract.md",
			}},
			ChildAction: func(c *command.Call) (command.Result, error) {
				return acting(p, root, c, func(r *Run) (command.Result, error) {
					if c.Bool("verdict") {
						return JudgeStdin(r, c.Name(), c.In)
					}
					judged, refusal, err := MeasureAndJudge(r, c.Name())
					if err != nil {
						return nil, err
					}
					if refusal != nil {
						// A refusal from a child is relayed, not
						// reinterpreted.
						return nil, refusedBy(refusal)
					}
					return judged, nil
				})
			},
		},
	}}
}

// VerifyTool is a project's commit gate.
func VerifyTool(p Project, stamp string) Entry {
	root := StampedRoot(stamp)
	return Entry{tool: command.Tool{
		Project: "verify",
		Version: versionOf(stamp),
		Fit:     MayAct("verify", stamp),
		Root: command.Command{
			Name:    "verify",
			Summary: "the commit gate: repair what has one right answer, then measure what remains",
			Action: func(c *command.Call) (command.Result, error) {
				return acting(p, root, c, func(r *Run) (command.Result, error) { return RunVerify(r) })
			},
		},
	}}
}

// SetupTool makes a fresh clone ready to gate its own commits.
func SetupTool(p Project, stamp string) Entry {
	root := StampedRoot(stamp)
	return Entry{tool: command.Tool{
		Project: "setup",
		Version: versionOf(stamp),
		Fit:     MayAct("setup", stamp),
		Root: command.Command{
			Name:    "setup",
			Summary: "wire this checkout's git hooks and check what it must ignore",
			Action: func(c *command.Call) (command.Result, error) {
				return acting(p, root, c, func(r *Run) (command.Result, error) { return RunSetup(r) })
			},
		},
	}}
}

// MakeTool is the meta-builder.
//
// It carries no version and declares no Fit: it is compiled from the source it
// builds on every run, so it is never stale — and a builder that could refuse
// on the ground every other refusal names would close the way out.
func MakeTool(p Project) Entry {
	return Entry{tool: command.Tool{
		Project: "make",
		Root: command.Command{
			Name:    "make",
			Summary: "compile every tool under tools/build/cmd into bin/",
			Flags: []command.Flag{{
				Name:        "rebuild",
				Type:        command.Boolean,
				Description: "compile every tool even where bin/ is already up to date",
			}},
			Action: func(c *command.Call) (command.Result, error) {
				cwd, err := os.Getwd()
				if err != nil {
					return nil, err
				}
				root, err := ResolveRoot(cwd)
				if err != nil {
					return nil, err
				}
				return acting(p, root, c, func(r *Run) (command.Result, error) {
					return RunMake(r, c.Bool("rebuild"))
				})
			},
		},
	}}
}

// versionOf is what -version reports: the tool's stamped source hash. The hash
// is the tool's identity — it is what the staleness check compares — so the
// version a person reads and the version the tool holds itself to are one
// string. A source hash is not a semantic version, so the payload carries no
// major, minor or patch.
func versionOf(stamp string) string {
	s, ok := DecodeStamp(stamp)
	if !ok {
		return ""
	}
	return s.Hash
}

// acting is the body every tool's action shares: it validates the definition,
// begins the run, and ends it however the work turns out.
func acting(p Project, root string, c *command.Call, do func(*Run) (command.Result, error)) (command.Result, error) {
	if root == "" {
		return nil, fmt.Errorf("this binary carries no repository root, so it cannot act on one")
	}
	commands, err := BuildSet(root)
	if err != nil {
		// A project with no cmd/ directory builds nothing, which is a project
		// with no command names for a gate name to collide with.
		commands = nil
	}
	if defects := Check(p, commands); len(defects) > 0 {
		return nil, defectsIn(defects)
	}
	r, end, err := Begin(p, root, c.Narrate)
	if err != nil {
		return nil, err
	}
	defer end()
	return do(r)
}

// defectsIn names every defect in one error, because a tool whose definition is
// wrong in three ways should not be run three times to learn that.
func defectsIn(defects []error) error {
	lines := make([]string, 0, len(defects))
	for _, d := range defects {
		lines = append(lines, d.Error())
	}
	return fmt.Errorf("this project's tooling definition has %d defect(s):\n  %s",
		len(defects), strings.Join(lines, "\n  "))
}

// refusedBy turns a child's refusal into this tool's own. The condition and the
// recovery are the child's; what carries them is the library's.
func refusedBy(r *command.Refusal) error {
	return fmt.Errorf("%s: %s — run %s", r.Tool, r.Detail, strings.Join(r.Recovery, " "))
}

// inspect is a run that only reads. It makes no scratch and takes no signals,
// because listing what a repository holds is not work that can be interrupted
// half-done — and a tool answering -help must not create a directory to do it.
func inspect(p Project, root string) *Run {
	return &Run{
		Root:    root,
		Narrate: io.Discard,
		ctx:     context.Background(),
		project: p,
	}
}

// gateChildren is the gate set this project answers.
func gateChildren(p Project, root string) []command.Command {
	if root == "" {
		return nil
	}
	r := inspect(p, root)
	names, err := GateNames(r)
	if err != nil {
		// A repository that cannot be asked what it holds answers no names. The
		// invocation then fails on an unknown command, which is the honest
		// report: nothing was hidden, because nothing could be found.
		return nil
	}
	set := make([]command.Command, 0, len(names))
	for _, name := range names {
		set = append(set, command.Command{Name: name, Summary: GateSummary(r, name)})
	}
	return set
}

// runChildren is every name `run` answers: the gates, judged, and the commands
// this project builds, dispatched.
//
// A command receives its arguments verbatim, which is what Delegate is for:
// nothing after the name is parsed, so a command's own -json means what that
// command says it means.
func runChildren(p Project, root string) []command.Command {
	set := gateChildren(p, root)
	commands, err := BuildSet(root)
	if err != nil {
		return set
	}
	for _, name := range commands {
		set = append(set, command.Command{
			Name:     name,
			Summary:  "run this project's " + name,
			Delegate: dispatch(root, name),
		})
	}
	return set
}

// dispatch hands one command its arguments, or refuses when this project builds
// that name and the binary is not there.
//
// The listing says what the project builds, not what is built, so a name it
// reports whose binary is absent is the diagnosable state the listing exists to
// expose. Only dispatching to that name refuses.
func dispatch(root, name string) func(*command.Call) ([]string, *command.Refusal) {
	return func(*command.Call) ([]string, *command.Refusal) {
		binary := filepath.Join(root, "bin", primitives.BinaryName(name))
		if _, err := os.Stat(binary); err != nil {
			return nil, &command.Refusal{
				Refusal:  command.Unbuilt,
				Tool:     "run",
				Detail:   fmt.Sprintf("this project builds %s and %s is not there", name, binary),
				Recovery: primitives.Recovery(),
			}
		}
		return []string{binary}, nil
	}
}
