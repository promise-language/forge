package primitives

import (
	"io"
	"os"
	"os/exec"
	"strings"
)

// RunIn runs name+args in dir with stdout/stderr/stdin attached to the parent.
func RunIn(dir, name string, args ...string) error {
	return RunInStreams(dir, os.Stdout, os.Stderr, name, args...)
}

// RunInStreams runs name+args in dir, writing the child's output where the
// caller says rather than to the parent's own streams.
//
// A tool built on primitives/command has one thing on stdout — its result — so
// a child whose output is narration is given the narration writer. `go test`
// reports on stdout, and a tool that let that through would be writing test
// output into the stream a caller is parsing.
func RunInStreams(dir string, out, errs io.Writer, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = out
	cmd.Stderr = errs
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

// RunOutputIn runs name+args in dir and returns trimmed stdout.
func RunOutputIn(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// OutputBytesIn runs name+args in dir and returns raw, untrimmed stdout bytes.
// Use this instead of RunOutputIn when the output may be binary (e.g. reading a
// blob with 'git cat-file'), where trimming whitespace would corrupt content.
func OutputBytesIn(dir, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	return cmd.Output()
}

// RunSilent runs name+args with output discarded.
func RunSilent(name string, args ...string) error {
	return exec.Command(name, args...).Run()
}
