package containment

import (
	"path/filepath"
	"strings"
	"testing"
)

const (
	root    = "/Users/u/prog/forge"
	sibling = "/Users/u/prog/workspace"
	scratch = "/tmp/scratch"
)

func ask(command string) error {
	return Allowed(Request{Command: command, CWD: root, Root: root, AlsoWritable: []string{scratch}})
}

func mustRefuse(t *testing.T, what, command string) {
	t.Helper()
	if err := ask(command); err == nil {
		t.Errorf("%s: allowed %q", what, command)
	}
}

func mustAllow(t *testing.T, what, command string) {
	t.Helper()
	if err := ask(command); err != nil {
		t.Errorf("%s: refused %q — %v", what, command, err)
	}
}

// These are the commands that actually escaped, reconstructed. Each one ran, and
// nothing observed that it had left the repository it was invoked for.
func TestTheEscapesThatActuallyHappened(t *testing.T) {
	mustRefuse(t, "cd to a sibling then commit there", "cd "+sibling+" && git add -A && git commit -q -F -")
	mustRefuse(t, "cd to a sibling then write a file", "cd "+sibling+"/projects && cat > forge/project.toml")
	mustRefuse(t, "a heredoc into a sibling path", "cat > "+sibling+"/projects/forge/issue/config.go")
	mustRefuse(t, "sed -i on a sibling", "sed -i '' 's/a/b/' "+sibling+"/projects/registry.go")
	mustRefuse(t, "python writing a sibling file", "python3 - "+sibling+"/projects/registry.go")
	mustRefuse(t, "git -C a sibling, mutating", "git -C "+sibling+" reset HEAD~1")
}

// Leaving the tree is refused on its own, before anything writes. Tracking a cd
// and judging what follows is defeated by every way a shell can reach a
// directory without spelling it; refusing the departure is not.
func TestLeavingTheTreeIsRefusedByItself(t *testing.T) {
	for _, c := range []string{
		"cd " + sibling,
		"cd ../workspace",
		"cd ../..",
		"pushd " + sibling,
		"cd /",
	} {
		mustRefuse(t, "leaving the tree", c)
	}
}

func TestMovingInsideTheTreeIsFine(t *testing.T) {
	for _, c := range []string{
		"cd tools/build && go build ./...",
		"cd " + root + "/primitives",
		"cd .",
		"cd " + scratch + " && cat > notes.txt",
	} {
		mustAllow(t, "moving inside the tree or into a writable path", c)
	}
}

// Reading another checkout must stay allowed. It is how a project learns the
// contract it has to satisfy and adopts an implementation that already passes,
// instead of inventing a worse one locally.
func TestReadingASiblingIsAllowed(t *testing.T) {
	for _, c := range []string{
		"cat " + sibling + "/docs/tool-contract.md",
		"sed -n 1,80p " + sibling + "/guard/guard.go",
		"grep -rn KnownGate " + sibling + "/guard",
		"diff " + root + "/tools/build/common/gate.go " + sibling + "/tools/build/common/gate.go",
		"git -C " + sibling + " log --oneline -3",
		"git -C " + sibling + " status --short",
		"git -C " + sibling + " show --stat HEAD",
		"wc -l " + sibling + "/docs/conformance.md",
		"md5 " + sibling + "/make",
	} {
		mustAllow(t, "reading a sibling", c)
	}
}

func TestRedirectionOutsideIsRefused(t *testing.T) {
	for _, c := range []string{
		"echo hi > " + sibling + "/x",
		"echo hi >> " + sibling + "/x",
		"go build ./... 2> " + sibling + "/errors.log",
		"printf x > /etc/hosts",
		"echo hi | tee " + sibling + "/x",
	} {
		mustRefuse(t, "a redirection leaving the tree", c)
	}
}

func TestRedirectionInsideIsFine(t *testing.T) {
	for _, c := range []string{
		"echo hi > notes.txt",
		"go test ./... > " + scratch + "/out.txt",
		"gofmt -l . > " + root + "/tmp.txt",
		"cat < " + sibling + "/docs/tooling.md",
	} {
		mustAllow(t, "a redirection inside the tree, or a read from outside", c)
	}
}

func TestKnownWriteCommandsAreJudgedOnTheirTargets(t *testing.T) {
	mustRefuse(t, "cp destination outside", "cp make "+sibling+"/make")
	mustAllow(t, "cp source outside, destination inside", "cp "+sibling+"/make ./make")
	mustRefuse(t, "mv destination outside", "mv notes.txt "+sibling+"/notes.txt")
	mustRefuse(t, "rm outside", "rm "+sibling+"/make")
	mustRefuse(t, "mkdir outside", "mkdir -p "+sibling+"/projects/forge")
	mustRefuse(t, "touch outside", "touch "+sibling+"/x")
	mustRefuse(t, "chmod outside", "chmod +x "+sibling+"/make")
	mustRefuse(t, "gofmt -w outside", "gofmt -w "+sibling)
	mustAllow(t, "gofmt -l outside only reads", "gofmt -l "+sibling)
	mustRefuse(t, "go build -o outside", "go build -o "+sibling+"/bin/x ./cmd/init")
	mustAllow(t, "go build with no -o", "go build ./...")
	mustRefuse(t, "dd of= outside", "dd if=/dev/zero of="+sibling+"/x")
	mustAllow(t, "sed without -i only reads", "sed -n 1,5p "+sibling+"/make")
}

// An interpreter handed its program cannot be read from argv, so an outside path
// in sight is a refusal rather than a guess.
func TestOpaqueInterpretersAreRefusedNearOutsidePaths(t *testing.T) {
	mustRefuse(t, "python naming an outside path", "python3 -c 'x' "+sibling+"/registry.go")
	mustRefuse(t, "bash -c naming an outside path", "bash -c 'cat > "+sibling+"/x'")
	mustRefuse(t, "xargs naming an outside path", "grep -rl x "+sibling+" | xargs sed -i '' s/a/b/")
	mustAllow(t, "python inside the tree", "python3 - tools/build/common/gate.go")
	mustAllow(t, "python with no path at all", "python3 -c 'print(1)'")
}

// A heredoc body is data. The bodies in this repository's own commands contain
// prose about destructive commands, paths in other checkouts, and whole
// programs; parsed as commands they would refuse text that runs nothing.
func TestHeredocBodiesAreData(t *testing.T) {
	mustAllow(t, "a heredoc body mentioning a sibling path", "cat > notes.md <<'EOF'\nsee "+sibling+"/docs\nrm -rf everything\ncd "+sibling+"\nEOF")
	mustAllow(t, "a quoted delimiter", "cat > x.go <<'GOEOF'\ncd "+sibling+"\nGOEOF")
	mustAllow(t, "an indented delimiter", "cat > x <<-END\n\tcd "+sibling+"\n\tEND")
	// The redirection itself is still judged, body or no body.
	mustRefuse(t, "a heredoc redirected outside", "cat > "+sibling+"/notes.md <<'EOF'\nhello\nEOF")
}

// A comment is not a command.
func TestCommentsAreNotCommands(t *testing.T) {
	mustAllow(t, "a comment naming a sibling", "# cd "+sibling+" && rm -rf x\ngo build ./...")
}

// A sibling whose name merely begins with the root's is still outside it.
func TestAPrefixNeighbourIsOutside(t *testing.T) {
	mustRefuse(t, "a name sharing the root's prefix", "cat > "+root+"-backup/x")
	mustRefuse(t, "a sibling under a longer name", "cd "+root+"2")
}

func TestQuotingDoesNotHideAPath(t *testing.T) {
	for _, c := range []string{
		`cat > "` + sibling + `/x"`,
		"cat > '" + sibling + "/x'",
		"cd \"" + sibling + "\"",
	} {
		mustRefuse(t, "a quoted outside path", c)
	}
}

// Without a root nothing can be established, so nothing is allowed.
func TestNoRootIsARefusal(t *testing.T) {
	if err := Allowed(Request{Command: "echo hi", CWD: root}); err == nil {
		t.Error("a request with no root was allowed")
	}
}

// An absent CWD means the root: a command with nowhere to run has not escaped it.
func TestAbsentCWDDefaultsToRoot(t *testing.T) {
	if err := Allowed(Request{Command: "cat > notes.txt", Root: root}); err != nil {
		t.Errorf("a relative write with no CWD was refused: %v", err)
	}
}

// The refusal has to name the path and the root, or whoever hit it cannot tell a
// breach from a false positive.
func TestRefusalNamesThePathAndTheRoot(t *testing.T) {
	err := ask("cat > " + sibling + "/x")
	if err == nil {
		t.Fatal("expected a refusal")
	}
	for _, want := range []string{filepath.Join(sibling, "x"), root, "one repository"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not mention %q: %v", want, err)
		}
	}
}

// Ordinary work inside the tree must pass untouched, including everything the
// build and gate loop runs.
func TestOrdinaryWorkAllowed(t *testing.T) {
	for _, c := range []string{
		"./make -force",
		"bin/verify",
		"bin/gate -list",
		"bin/gate formatted --envelope | bin/run formatted --verdict",
		"go test ./... 2>&1 | grep -E 'FAIL|^ok'",
		"git add -A",
		"git commit -q -F -",
		"git status -sb",
		"ls -la bin/",
		"for g in $(bin/gate -list); do bin/run $g; done",
	} {
		mustAllow(t, "ordinary work", c)
	}
}

// Two misses, recorded rather than fixed, because a check that pretends to be
// complete is worse than one whose edges are written down.
//
// A path an interpreter COMPUTES is invisible here: nothing in the argv names it,
// which is the whole reason the interpreter arm keys on paths in sight. Closing
// it needs the program's own text read, and the honest partial answer is the
// refusal above plus this note.
//
// A symlink inside the root that points outside resolves outside only if
// something resolves it; this compares lexical paths, so it does not.
func TestKnownMisses(t *testing.T) {
	if err := ask("python3 -c 'import os,pathlib;pathlib.Path(os.environ[\"D\"]).write_text(\"x\")'"); err == nil {
		return // the computed-path miss
	}
	t.Error("the computed-path miss has been closed — move this into the refusal tests and delete it here")
}

// Commands taken from real sessions in this repository, which must all pass.
// This is the test that fails when a rule is tightened past what ordinary work
// needs — the failure mode that matters most, because a check that refuses
// ordinary things is one people switch off.
func TestRealSessionCommandsAllowed(t *testing.T) {
	for _, c := range []string{
		// Adopting a reference implementation: reading a sibling, writing here.
		"cp -R " + sibling + "/tools/build/. tools/build/",
		"cp " + sibling + "/tools/build/common/gate.go tools/build/common/",
		"diff -q tools/build/common/exec.go " + sibling + "/tools/build/common/exec.go",
		// Editing only inside, with the pattern naming a sibling's module path.
		"grep -rl 'promise-language/other' tools/build | xargs sed -i '' 's|other/tools/build|forge/tools/build|g'",
		"sed -i '' 's/other/forge/g' tools/build/common/verifiedtree.go",
		// The gate and build loop.
		"./make -force",
		"bin/verify 2>&1 | tail -9",
		"go test ./... 2>&1 | grep -E 'FAIL|^ok'",
		"go build -o " + scratch + "/workspace ./cmd/init",
		"for g in $(bin/gate -list); do out=$(bin/run \"$g\" 2>&1); rc=$?; done",
		// Reading a sibling's docs and source to learn the contract.
		"sed -n 1,200p " + sibling + "/docs/tool-contract.md",
		"grep -rn 'func Allowed' -A 25 " + sibling + "/disclosure/*.go",
		"wc -l " + sibling + "/disclosure/*.go",
		// The scratchpad is writable, and moving a file back out of it is not an escape.
		"mv " + scratch + "/gate.parked bin/gate",
		"cat > " + scratch + "/payload.json",
		"python3 - <<'PY'\nimport json\nPY",
		// Publishing, which the disclosure layer judges rather than this one.
		"gh issue create --repo org/repo --title T --body-file " + scratch + "/body.md",
	} {
		mustAllow(t, "real session command", c)
	}
}
