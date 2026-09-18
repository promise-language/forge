package primitives

import (
	"fmt"

	"github.com/promise-language/forge/primitives/command"
)

// StaleRefusal reports why this binary may not act, or nil when it may.
//
// repoRoot and compiledHash are injected via -ldflags; empty values mean the
// binary was built some other way (go install, manual go build). It never
// exits and never writes: the condition is decided here, and what a refusal
// does with it — its status, its object, and which stream each goes to — is
// the command library's (docs/command-line.md, Exit status and refusal).
//
// The condition is typed rather than prose, because a caller must be able to
// tell a stale toolchain from a failing tree without matching a sentence: a
// tool that exits over a stale build has measured nothing, and a caller that
// reports that as the tree's failure has named a repair that is not the repair
// (docs/org/cli-guide.md, Exit codes).
//
// dirs names the tool source, and naming none means tools/build — see
// SourceHash. A caller that names them must name the same set the meta-builder
// hashed, or every binary reports itself stale forever.
func StaleRefusal(tool, repoRoot, compiledHash string, dirs ...string) *command.Refusal {
	if repoRoot == "" || compiledHash == "" {
		return &command.Refusal{
			Refusal:  command.Unstamped,
			Tool:     tool,
			Detail:   fmt.Sprintf("this binary carries no stamp, so it was not built by %s", MakeCmd()),
			Recovery: Recovery(),
		}
	}
	currentHash, err := SourceHash(repoRoot, dirs...)
	if err != nil {
		return &command.Refusal{
			Refusal:  command.RepositoryUnreachable,
			Tool:     tool,
			Detail:   fmt.Sprintf("the repository this binary was built in (%s) is unreachable: %v", repoRoot, err),
			Recovery: Recovery(),
		}
	}
	if compiledHash != currentHash {
		return &command.Refusal{
			Refusal:  command.Stale,
			Tool:     tool,
			Detail:   "the tools source has changed since this binary was built",
			Recovery: Recovery(),
		}
	}
	return nil
}

// MakeCmd is the bootstrap command every refusal names.
func MakeCmd() string {
	if IsWindows() {
		return ".\\make.cmd"
	}
	return "./make"
}

// Recovery is the program and arguments that clear a refusal, run from the
// repository root and exec'd, never interpreted.
//
// It is deliberately NOT a one-way door: the recovery runs via `go run` and has
// no staleness check of its own, so it always works no matter how stale — or
// how broken — the compiled binaries are. The way out is always
// fix-and-rebuild, never committing the broken state.
func Recovery() []string {
	return []string{MakeCmd()}
}
