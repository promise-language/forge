package primitives

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// CheckStale and MaybeHelp end the process, so what they do cannot be observed
// from inside the test that calls them. Each runs in a re-execution of this test
// binary, which is the only way to see an exit status and the stream it was
// printed on — and both are the contract: MaybeHelp exits 0 because help
// succeeded, CheckStale exits 1 with the reason on stderr, and the flow that
// drives this repository matches the literal phrase it prints.
const subprocessRole = "FORGE_PRIMITIVES_SUBPROCESS"

func TestMain(m *testing.M) {
	switch os.Getenv(subprocessRole) {
	case "":
		os.Exit(m.Run())
	case "maybe-help":
		MaybeHelp(strings.Fields(os.Getenv("ARGS")), "the usage text")
		// Reached only when args did not request help. A distinct status, so
		// "returned" is never confused with "exited 0 after printing usage".
		os.Exit(9)
	case "check-stale":
		CheckStale(os.Getenv("REPO"), os.Getenv("HASH"), strings.Fields(os.Getenv("DIRS"))...)
		os.Exit(9)
	}
	os.Exit(2)
}

// reexec runs this test binary again in the named role, returning its combined
// output and exit status.
func reexec(t *testing.T, role string, env ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(append(os.Environ(), subprocessRole+"="+role), env...)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return string(out), 0
	}
	exit, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("re-running this binary as %q: %v\n%s", role, err, out)
	}
	return string(out), exit.ExitCode()
}

func TestMaybeHelpPrintsUsageAndSucceeds(t *testing.T) {
	out, code := reexec(t, "maybe-help", "ARGS=--help")
	if code != 0 {
		t.Errorf("help exited %d, want 0 — asking for usage is not a failure", code)
	}
	if !strings.Contains(out, "the usage text") {
		t.Errorf("help printed %q, want the usage", out)
	}
}

func TestMaybeHelpReturnsWhenNoHelpWasAsked(t *testing.T) {
	out, code := reexec(t, "maybe-help", "ARGS=-force")
	if code != 9 {
		t.Errorf("exited %d, want the caller to have been allowed to continue", code)
	}
	if strings.Contains(out, "the usage text") {
		t.Error("usage was printed for a command that did not ask for it")
	}
}

func TestCheckStaleAbortsAndNamesTheRecovery(t *testing.T) {
	repo := hashFixture(t)
	out, code := reexec(t, "check-stale", "REPO="+repo, "HASH=deadbeef", "DIRS="+ToolsBuildDir+" primitives")
	if code != 1 {
		t.Fatalf("a stale binary exited %d, want 1", code)
	}
	// The flow matches this phrase; the recovery and the repo are what a person
	// needs to act on it.
	for _, want := range []string{"tools source has changed", MakeCmd(), repo} {
		if !strings.Contains(out, want) {
			t.Errorf("the abort said %q, which does not name %q", out, want)
		}
	}
}

// An unstamped binary has no repo to name, so the hint must not offer one.
func TestCheckStaleOnAnUnstampedBinary(t *testing.T) {
	out, code := reexec(t, "check-stale", "REPO=", "HASH=")
	if code != 1 {
		t.Fatalf("an unstamped binary exited %d, want 1", code)
	}
	if !strings.Contains(out, "not built via") {
		t.Errorf("the abort said %q, want it to say the binary was not built via ./make", out)
	}
	if strings.Contains(out, "(in ") {
		t.Errorf("the abort named a repo it does not have: %q", out)
	}
}

func TestCheckStaleReturnsWhenCurrent(t *testing.T) {
	repo := hashFixture(t)
	current, err := SourceHash(repo, ToolsBuildDir, "primitives")
	if err != nil {
		t.Fatal(err)
	}
	out, code := reexec(t, "check-stale", "REPO="+repo, "HASH="+current, "DIRS="+ToolsBuildDir+" primitives")
	if code != 9 {
		t.Errorf("a current binary exited %d (%q), want it to have been allowed to run", code, out)
	}
}
