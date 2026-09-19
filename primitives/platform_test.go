package primitives

import (
	"path/filepath"
	"testing"
)

func TestPlatformHelpers(t *testing.T) {
	if BinaryName("verify") != "verify"+ExeSuffix() {
		t.Error("BinaryName does not append the platform suffix")
	}
	if IsWindows() != (ExeSuffix() == ".exe") {
		t.Error("ExeSuffix and IsWindows disagree")
	}
	// Absence is the second result, not an empty path: a caller that checked
	// only the path would read "not on PATH" as something it could run.
	if path, found := Which("definitely-not-a-real-command-xyz"); found || path != "" {
		t.Errorf("Which found a command that does not exist: (%q, %v)", path, found)
	}
	if path, found := Which("go"); !found || path == "" {
		t.Errorf("Which could not find the go toolchain: (%q, %v)", path, found)
	}
	if !Exists(t.TempDir()) || Exists(filepath.Join(t.TempDir(), "nope")) {
		t.Error("Exists is wrong about a directory or a missing path")
	}
}
