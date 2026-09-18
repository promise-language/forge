package tooling

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/promise-language/forge/primitives"
)

// make refuses unless the builder's own source is where the trampoline pins the
// working directory. A builder that guessed at a root would stamp every binary
// with somewhere else.
func TestTheBuilderRefusesARootItCannotConfirm(t *testing.T) {
	root := t.TempDir()
	if _, err := ResolveRoot(filepath.Join(root, "tools", "build")); err == nil {
		t.Error("a directory with no builder source resolved as a root")
	}
	write(t, root, filepath.FromSlash("tools/build/cmd/make/main.go"), "package main\n")
	got, err := ResolveRoot(filepath.Join(root, "tools", "build"))
	if err != nil || got != root {
		t.Errorf("ResolveRoot = (%q, %v), want %q", got, err, root)
	}
}

// The build set is every directory under tools/build/cmd except make, and a
// directory whose name the CLI guide's alphabet does not admit is refused.
func TestTheBuildSetIsEveryCommandButTheBuilder(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"make", "gate", "run"} {
		write(t, root, filepath.FromSlash("tools/build/cmd/"+name+"/main.go"), "package main\n")
	}
	got, err := BuildSet(root)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{"gate", "run"}) {
		t.Errorf("the build set is %v, want the commands without the builder", got)
	}

	write(t, root, filepath.FromSlash("tools/build/cmd/Release_2/main.go"), "package main\n")
	if _, err := BuildSet(root); err == nil {
		t.Error("a directory outside the alphabet was accepted as a command name")
	}
}

// A name in the build set that the workspace marker records is refused, with
// the name: one name has one builder (tool-contract.md, One name one builder).
// With no marker there is nothing to check, because nothing was promised.
func TestANameTheWorkspaceBuildsIsRefused(t *testing.T) {
	root := t.TempDir()
	if err := checkCollisions(root, []string{"gate", "workspace"}); err != nil {
		t.Errorf("a checkout with no marker was refused: %v", err)
	}

	write(t, root, filepath.FromSlash(WorkspaceMarker), `{"tools": ["workspace", "issue"]}`)
	err := checkCollisions(root, []string{"gate", "workspace"})
	if err == nil {
		t.Fatal("a collision with a workspace tool was accepted")
	}
	if !strings.Contains(err.Error(), "workspace") {
		t.Errorf("the refusal is %q, want it to name the colliding name", err)
	}
	if err := checkCollisions(root, []string{"gate", "run"}); err != nil {
		t.Errorf("a build set with no collision was refused: %v", err)
	}
}

// A name the previous sidecar recorded, and the build set no longer holds, is
// removed. A name make did not record is never touched, because workspace tools
// share bin/.
func TestThePruneTouchesOnlyWhatTheBuilderRecorded(t *testing.T) {
	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	for _, name := range []string{"gate", "retired", "workspace"} {
		write(t, root, filepath.Join("bin", primitives.BinaryName(name)), "a binary\n")
	}
	recorded := sidecar{binaries: map[string]string{"gate": "x", "retired": "y"}}

	removed := prune(binDir, recorded, []string{"gate"})
	if !slices.Equal(removed, []string{"retired"}) {
		t.Errorf("pruned %v, want the retired name alone", removed)
	}
	for _, kept := range []string{"gate", "workspace"} {
		if _, err := os.Stat(filepath.Join(binDir, primitives.BinaryName(kept))); err != nil {
			t.Errorf("%s was removed: %v", kept, err)
		}
	}
}

// The up-to-date check reads the sidecar's source hash and each binary's own
// digest, so a binary replaced since the last build is not reported current.
func TestUpToDateReadsTheSourceHashAndEachBinary(t *testing.T) {
	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	write(t, root, filepath.Join("bin", primitives.BinaryName("gate")), "a binary\n")
	digest, err := fileDigest(filepath.Join(binDir, primitives.BinaryName("gate")))
	if err != nil {
		t.Fatal(err)
	}
	current := sidecar{hash: "h", binaries: map[string]string{"gate": digest}}

	if !upToDate(current, "h", binDir, []string{"gate"}) {
		t.Error("a current tree was reported stale")
	}
	if upToDate(current, "other-hash", binDir, []string{"gate"}) {
		t.Error("a moved source hash was reported up to date")
	}
	if upToDate(current, "h", binDir, []string{"gate", "absent"}) {
		t.Error("a tool the sidecar never recorded was reported up to date")
	}
	write(t, root, filepath.Join("bin", primitives.BinaryName("gate")), "a different binary\n")
	if upToDate(current, "h", binDir, []string{"gate"}) {
		t.Error("a replaced binary was reported up to date")
	}
}

// The stamp is one encoded value, so a root containing a space, a quote or an
// equals sign reaches the binary as it was written.
func TestTheStampSurvivesARootTheLinkerWouldMangle(t *testing.T) {
	want := Stamp{Root: `/Users/a b/"x"=y`, Hash: "deadbeef", Dirs: []string{"tools/build", "primitives/*"}}
	encoded := want.Encode()
	if strings.ContainsAny(encoded, " \"=") {
		t.Errorf("the encoded stamp is %q, which the linker's own flag parsing would not survive", encoded)
	}
	got, ok := DecodeStamp(encoded)
	if !ok || got.Root != want.Root || got.Hash != want.Hash || !slices.Equal(got.Dirs, want.Dirs) {
		t.Errorf("DecodeStamp = (%+v, %v), want %+v", got, ok, want)
	}
	if _, ok := DecodeStamp(""); ok {
		t.Error("an unstamped binary decoded a stamp")
	}
	if _, ok := DecodeStamp("not-base64-json"); ok {
		t.Error("an unreadable stamp decoded")
	}
}

// setup refuses when the committed .gitignore does not ignore what a tool
// writes, naming each missing line. It never edits .gitignore: that file is
// tracked, and the entries belong to the project rather than to a clone.
func TestSetupNamesEachMissingIgnoreAndEditsNothing(t *testing.T) {
	root := fixture(t, "")
	write(t, root, ".gitignore", "/bin/\n")
	git(t, root, "add", "-A")
	before, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}

	err = CheckIgnores(quiet(Standard(), root))
	if err == nil {
		t.Fatal("a checkout missing two entries was accepted")
	}
	for _, want := range []string{"/.workspace/", "/.home/"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal is %q, want it to name %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "/bin/,") {
		t.Errorf("the refusal is %q, want it to name only what is missing", err)
	}

	after, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Errorf(".gitignore was rewritten: %q", after)
	}
}

// A second run changes nothing and says so, which is what lets the builder call
// setup's work on every run.
func TestSetupsSecondRunChangesNothing(t *testing.T) {
	root := fixture(t, "")
	p := Standard()

	first, err := RunSetup(quiet(p, root))
	if err != nil {
		t.Fatal(err)
	}
	if first.HooksPath != primitives.HooksPath {
		t.Errorf("hooks_path = %q, want %q", first.HooksPath, primitives.HooksPath)
	}
	if !first.Steps[0].Changed {
		t.Error("the first run reported it changed nothing")
	}

	second, err := RunSetup(quiet(p, root))
	if err != nil {
		t.Fatal(err)
	}
	if second.Steps[0].Changed {
		t.Error("a second run reported a change")
	}
}

// A project's own setup steps run after the hooks and the ignores, and each
// reports whether it changed anything.
func TestAProjectsSetupStepsAreReported(t *testing.T) {
	root := fixture(t, "")
	p := Standard()
	p.Setup = append(p.Setup, SetupStep{
		Name:    "fixture",
		Summary: "a step a test stands in for",
		Run:     func(*Run) (bool, error) { return true, nil },
	})

	got, err := RunSetup(quiet(p, root))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Steps) != 2 || got.Steps[1].Name != "fixture" || !got.Steps[1].Changed {
		t.Errorf("steps = %+v, want the project's own step reported", got.Steps)
	}
}
