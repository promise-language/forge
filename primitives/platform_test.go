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
	if Which("definitely-not-a-real-command-xyz") != "" {
		t.Error("Which found a command that does not exist")
	}
	if Which("go") == "" {
		t.Error("Which could not find the go toolchain")
	}
	if !Exists(t.TempDir()) || Exists(filepath.Join(t.TempDir(), "nope")) {
		t.Error("Exists is wrong about a directory or a missing path")
	}
}
