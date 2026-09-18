// Command make is the meta-builder. It compiles every other tool under cmd/
// into <root>/bin, stamping each binary with the root, the source hash and the
// source set. It is the one tool that runs via `go run` — from the ./make
// trampoline — so it is never compiled into bin/ and never stale, which is what
// breaks the bootstrap cycle.
//
// It is also the recovery every refusal names, which is why it is the one main
// without a stamp: a builder that could refuse on the ground every other
// refusal names would close the way out (docs/project-tools.md, Staleness).
package main

import (
	"os"

	"github.com/promise-language/forge/primitives/tooling"
	"github.com/promise-language/forge/tools/build/common"
)

func main() {
	os.Exit(tooling.MakeTool(common.Define()).Run(os.Args[1:]))
}
