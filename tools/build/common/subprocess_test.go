package common

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"testing"
)

// Two things here are only observable across a process boundary: RunOneGate
// runs the gate as a program, and CheckStale ends the process rather than
// returning. Both are re-executions of this test binary, which is the only way
// to see an exit status — and for the gate, the only way to produce the
// failures the boundary introduces (a gate that dies, one that prints something
// other than an envelope).
//
// The role is read from the environment, which a child inherits because neither
// caller clears it.
const subprocessRole = "FORGE_COMMON_SUBPROCESS"

func TestMain(m *testing.M) {
	if role := os.Getenv(subprocessRole); role != "" {
		runAsSubprocess(role)
	}
	os.Exit(m.Run())
}

// runAsSubprocess never returns: every role either exits or falls through to
// the refusal below, so an unknown role cannot be mistaken for a role that did
// nothing.
func runAsSubprocess(role string) {
	// The gate roles are invoked as `<bin> <name> --envelope`, the command
	// RunOneGate builds.
	name := ""
	if len(os.Args) > 1 {
		name = os.Args[1]
	}
	switch role {
	case "gate-clean":
		writeEnvelope(Envelope{Gate: name, Metrics: []Metric{Count("failed_tests", 0)}})
	case "gate-over-cap":
		writeEnvelope(Envelope{Gate: name, Metrics: []Metric{Count("failed_tests", 3)}})
	case "gate-not-an-envelope":
		// Exits 0 with something on stdout that is not an envelope — the shape
		// a gate takes when it prints progress where the measurement goes.
		fmt.Println("measuring...")
		fmt.Println("still measuring...")
		os.Exit(0)
	case "gate-dies":
		fmt.Fprintln(os.Stderr, "the gate fell over")
		os.Exit(1)
	case "check-stale":
		CheckStale(os.Getenv("REPO"), os.Getenv("HASH"))
		// Reached only when the binary is current. A distinct status, so
		// "was allowed to run" is never confused with a clean exit after an
		// abort.
		os.Exit(9)
	}
	fmt.Fprintf(os.Stderr, "unknown subprocess role %q\n", role)
	os.Exit(2)
}

func writeEnvelope(env Envelope) {
	data, err := json.Marshal(env)
	if err != nil {
		panic(err)
	}
	os.Stdout.Write(data)
	os.Exit(0)
}

// asSubprocess returns the path to this test binary and arranges for a child of
// it to play the named role.
func asSubprocess(t *testing.T, role string) string {
	t.Helper()
	t.Setenv(subprocessRole, role)
	return os.Args[0]
}

// runSubprocess runs the binary asSubprocess named, returning its combined
// output and exit status. RunOneGate spawns its own; this is for the roles
// nothing else spawns.
func runSubprocess(t *testing.T, bin string) (string, int) {
	t.Helper()
	out, err := exec.Command(bin).CombinedOutput()
	if err == nil {
		return string(out), 0
	}
	exit, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("re-running this binary: %v\n%s", err, out)
	}
	return string(out), exit.ExitCode()
}
