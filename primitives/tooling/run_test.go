package tooling

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/promise-language/forge/primitives/command"
)

// --list says what this project builds and what it answers. Both are
// discovered, so neither can go stale, and one object carries them together
// because a caller that must not confuse the two needs the whole vocabulary at
// once.
func TestTheListingSaysWhatTheProjectBuildsAndWhatItAnswers(t *testing.T) {
	root := fixture(t, "", "tools/build")
	for _, name := range []string{"make", "gate", "verify"} {
		write(t, root, filepath.FromSlash("tools/build/cmd/"+name+"/main.go"), "package main\n")
	}
	git(t, root, "add", "-A")

	listing, err := List(quiet(Standard(), root))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(listing.Commands, []string{"gate", "verify"}) {
		t.Errorf("commands = %v, want the build set without the builder", listing.Commands)
	}
	if !slices.Contains(listing.Gates, "tested:tools-build") {
		t.Errorf("gates = %v, want the derived instances", listing.Gates)
	}
}

// The listing says what the project builds, not what is built: a name it
// reports whose binary is absent is the diagnosable state the listing exists to
// expose, so only dispatching to that name refuses.
func TestDispatchingToAnUnbuiltCommandRefuses(t *testing.T) {
	root := fixture(t, "")
	refusal := func() *command.Refusal {
		_, r := dispatch(root, "gate")(nil)
		return r
	}

	got := refusal()
	if got == nil {
		t.Fatal("dispatching to a name with no binary was accepted")
	}
	if got.Refusal != command.Unbuilt || len(got.Recovery) == 0 {
		t.Errorf("refusal = %+v, want the condition and the recovery", got)
	}

	write(t, root, filepath.Join("bin", "gate"), "a binary\n")
	if got := refusal(); got != nil {
		t.Errorf("dispatching to a built name refused: %+v", got)
	}
}

// A command receives its arguments verbatim: nothing after the name is parsed,
// so a command's own -json means what that command says it means.
func TestACommandsArgumentsAreNotParsedByTheRunner(t *testing.T) {
	root := fixture(t, "")
	for _, name := range []string{"gate", "issue"} {
		write(t, root, filepath.FromSlash("tools/build/cmd/"+name+"/main.go"), "package main\n")
		write(t, root, filepath.Join("bin", name), "a binary\n")
	}
	git(t, root, "add", "-A")

	for _, child := range runChildren(Standard(), root) {
		if child.Name != "issue" {
			continue
		}
		if child.Delegate == nil {
			t.Error("a command this project builds does not delegate")
		}
		if len(child.Flags) != 0 {
			t.Errorf("a delegating command declares %v, and every word after its name belongs to the delegate", child.Flags)
		}
		return
	}
	t.Error("the command set does not carry the names this project builds")
}

// A tool whose command tree carries both delegating and ordinary children is
// not a definition defect: the flag the gates take is the parent's, not the
// delegating command's own declaration.
func TestTheRunTreePassesTheLibrarysCheck(t *testing.T) {
	root := fixture(t, "")
	write(t, root, filepath.FromSlash("tools/build/cmd/issue/main.go"), "package main\n")
	git(t, root, "add", "-A")

	stamp := Stamp{Root: root, Hash: "h", Dirs: []string{"tools/build"}}.Encode()
	for _, defect := range command.Check(RunTool(Standard(), stamp).Tool()) {
		t.Errorf("run's tree: %v", defect)
	}
}

// The envelope must be for the gate being judged: judging one gate's numbers
// against another's terms answers a question nobody asked.
func TestJudgingRefusesAnEnvelopeAnotherGateWrote(t *testing.T) {
	root := fixture(t, "")
	terms(t, root, `{"n": {"direction": "down", "cap": 0}}`, `{}`)
	p := counting("x", []Metric{Count("n")}, Measured{}, nil)
	p.Gates.Add(Gate{
		Name: "y", Summary: "another gate",
		Metrics:     Declared(Count("n")),
		Measure:     func(*Run, []Unit) (Measured, error) { return Measured{}, nil },
		Remediation: "there is nothing to do about a fixture",
	})

	body, err := json.Marshal(Envelope{Gate: "y", Target: HostTarget(), Metrics: []Measurement{Counted("n", 0, "")}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = JudgeStdin(quiet(p, root), "x", strings.NewReader(string(body)))
	if err == nil {
		t.Fatal("one gate's numbers were judged against another's terms")
	}
	if !strings.Contains(err.Error(), "its own gate's terms") {
		t.Errorf("the refusal is %q", err)
	}
}

// What arrives on stdin and is not an envelope is refused, and nothing reaches
// stdout on any error path: a caller reads one object or none.
func TestJudgingRefusesWhatIsNotAnEnvelope(t *testing.T) {
	root := fixture(t, "")
	terms(t, root, `{"n": {"direction": "down", "cap": 0}}`, `{}`)
	p := counting("x", []Metric{Count("n")}, Measured{}, nil)

	if _, err := JudgeStdin(quiet(p, root), "x", strings.NewReader("not an envelope")); err == nil {
		t.Error("prose was judged as an envelope")
	}
	if _, err := JudgeStdin(quiet(p, root), "nowhere", strings.NewReader("{}")); err == nil {
		t.Error("a gate this project does not have was judged")
	}
}

// Both modes reach a verdict through the same comparison, so they cannot
// disagree about what this project allows.
func TestBothModesReachTheVerdictThroughOneComparison(t *testing.T) {
	root := fixture(t, "")
	terms(t, root, `{"n": {"direction": "down", "cap": 1}}`, `{}`)
	p := counting("x", []Metric{Count("n")}, Measured{Metrics: []Measurement{Counted("n", 4, "")}}, nil)
	r := quiet(p, root)

	env, err := MeasureGate(r, "x")
	if err != nil {
		t.Fatal(err)
	}
	inProcess, err := judgeEnvelope(r, "x", env)
	if err != nil {
		t.Fatal(err)
	}

	body, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	onStdin, err := JudgeStdin(r, "x", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	if inProcess.Verdict.Acceptable != onStdin.Acceptable || inProcess.Verdict.Detail != onStdin.Detail {
		t.Errorf("the two modes answered %+v and %+v", inProcess.Verdict, onStdin)
	}
	if inProcess.ExitStatus() != 1 {
		t.Errorf("status = %d, want 1 when the verdict is not acceptable", inProcess.ExitStatus())
	}
}

// A count arriving with a fractional part is not a count, and the judge refuses
// it rather than absorbing it. Absorbed, it would be a type change nothing
// recorded — and it would move a ratchet that by construction never moves back.
func TestACountArrivingWithAFractionalPartIsRefused(t *testing.T) {
	root := fixture(t, "")
	terms(t, root, `{"n": {"direction": "down", "cap": 0}}`, `{}`)
	p := counting("x", []Metric{Count("n")}, Measured{}, nil)
	r, _ := run(t, p, root)

	for _, value := range []string{"1.5", `"three"`} {
		body := `{"gate":"x","target":"` + HostTarget() + `","metrics":[{"name":"n","type":"int","value":` + value + `}]}`
		if verdict, err := JudgeStdin(r, "x", strings.NewReader(body)); err == nil {
			t.Errorf("a value of %s was absorbed as a count and judged %+v", value, verdict)
		}
	}
}

// A binary that is not fit to act refuses every invocation, and the source set
// it checks against comes out of its own stamp.
func TestAStaleBinaryRefusesWithTheRecoveryNamed(t *testing.T) {
	root := fixture(t, "")
	write(t, root, filepath.FromSlash("tools/build/x.go"), "package x\n")

	for _, c := range []struct {
		name  string
		stamp string
		want  string
	}{
		{"no stamp at all", "", command.Unstamped},
		{"a hash that no longer matches",
			Stamp{Root: root, Hash: "not-the-hash-it-would-have", Dirs: []string{"tools/build"}}.Encode(),
			command.Stale},
		{"a root that no longer exists",
			Stamp{Root: filepath.Join(root, "gone"), Hash: "h", Dirs: []string{"tools/build"}}.Encode(),
			command.RepositoryUnreachable},
	} {
		t.Run(c.name, func(t *testing.T) {
			refusal := MayAct("gate", c.stamp)()
			if refusal == nil {
				t.Fatal("the binary reported itself fit to act")
			}
			if refusal.Refusal != c.want {
				t.Errorf("refusal = %q, want %q", refusal.Refusal, c.want)
			}
			if refusal.Tool != "gate" || len(refusal.Recovery) == 0 {
				t.Errorf("refusal = %+v, want the tool and the recovery named", refusal)
			}
		})
	}
}
