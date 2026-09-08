package primitives

import (
	"os"
	"os/exec"
	"strings"
)

// RunIn runs name+args in dir with stdout/stderr/stdin attached to the parent.
func RunIn(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
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
