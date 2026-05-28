// Command init scaffolds the Forge dev-tooling layout into a target repository.
//
// Usage:
//
//	go run github.com/promise-language/forge/cmd/init@latest [target-dir]
//
// When run, it lays down:
//
//   - ./make, ./make.cmd                  bootstrap trampolines
//   - tools/build/go.mod                  Go module for the dev tools
//   - tools/build/common/                 stub helper package (hash, stale, exec, …)
//   - tools/build/cmd/<tool>/main.go      one stub per binary (make, build, verify, …)
//   - .githooks/pre-commit                git hook trampoline
//   - .claude/settings.json               wires bin/guard as a PreToolUse hook
//   - .claude/edit_gates.json             starter (empty) edit-gate list
//   - project.toml                        stub gate registry
//   - .gitignore additions                bin/, .baselines.json runtime sidecar, etc.
//
// After init exits, the target repo owns every file. Forge is not a runtime
// dependency unless the project explicitly imports primitives/.
//
// TODO(forge): implement. See docs/blueprint.md for the layout this tool produces.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "forge/init: not yet implemented")
	fmt.Fprintln(os.Stderr, "see docs/blueprint.md for the layout this will produce")
	os.Exit(1)
}
