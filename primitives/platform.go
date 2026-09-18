package primitives

import (
	"os"
	"os/exec"
	"runtime"
)

// IsWindows reports whether the host OS is Windows.
func IsWindows() bool { return runtime.GOOS == "windows" }

// ExeSuffix is ".exe" on Windows, "" elsewhere.
func ExeSuffix() string {
	if IsWindows() {
		return ".exe"
	}
	return ""
}

// BinaryName appends the platform executable suffix to a tool name.
func BinaryName(name string) string { return name + ExeSuffix() }

// Which resolves a program in PATH, reporting whether it was found. Absence is
// the second result rather than an empty path, so a caller cannot read "not on
// PATH" as a path it then tries to run.
func Which(program string) (string, bool) {
	path, err := exec.LookPath(program)
	if err != nil {
		return "", false
	}
	return path, true
}

// Exists reports whether a path exists.
func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
