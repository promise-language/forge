package primitives

import (
	"fmt"
	"os"
)

// StaleReason returns a human-readable reason this binary is out of sync with
// its tools source, or "" if it is current. repoRoot and compiledHash are
// injected via -ldflags; empty values mean the binary was built some other way
// (go install, manual go build). It never exits — callers decide whether
// staleness is fatal (pipeline tools that would otherwise produce misleading
// results) or merely a warning (the git hook, which must never block a commit).
//
// dirs names the tool source, and naming none means tools/build — see
// SourceHash. A caller that names them must name the same set the meta-builder
// hashed, or every binary reports itself stale forever.
func StaleReason(repoRoot, compiledHash string, dirs ...string) string {
	if repoRoot == "" || compiledHash == "" {
		return "this binary was not built via ./make"
	}
	currentHash, err := SourceHash(repoRoot, dirs...)
	if err != nil {
		return fmt.Sprintf("binary's repo (%s) is unreachable: %v", repoRoot, err)
	}
	if compiledHash != currentHash {
		return "tools source has changed since this binary was built"
	}
	return ""
}

// MakeCmd is the bootstrap command to print in recovery hints.
func MakeCmd() string {
	if IsWindows() {
		return ".\\make.cmd"
	}
	return "./make"
}

// CheckStale aborts a tool whose stale logic would otherwise run: pipeline
// tools (verify, build, test, …) would produce misleading results, and the
// commit gate (precommit) must never validate a commit with out-of-date logic.
// It points the caller at ./make.
//
// It is deliberately NOT a one-way door: the recovery, ./make, runs via 'go
// run' and has no staleness gate of its own, so it always works no matter how
// stale — or how broken — the compiled binaries are. Editing the tool source to
// fix a broken build is likewise permitted by the guard. So the way out is
// always fix-and-rebuild, never committing the broken state. Stale tools are a
// speed bump (re-run ./make), never a lockout.
func CheckStale(repoRoot, compiledHash string, dirs ...string) {
	reason := StaleReason(repoRoot, compiledHash, dirs...)
	if reason == "" {
		return
	}
	fmt.Fprintf(os.Stderr, "%s — run %s", reason, MakeCmd())
	if repoRoot != "" {
		fmt.Fprintf(os.Stderr, " (in %s)", repoRoot)
	}
	fmt.Fprintln(os.Stderr)
	os.Exit(1)
}
