// Command gate measures one property of this tree and prints what it found.
//
// It is not meant to be run by hand — `bin/run <gate>` is that path. A gate
// answers a runner, and a runner is the only caller that can say what became of
// the run: a gate killed for memory is not alive to report it, and a gate that
// exited cleanly having printed nothing would be believed.
//
// It is one call into the library and the definition beside it: what it parses,
// how it reports, what it measures and what it exits with are not this file's
// (docs/project-tools.md, Layout).
package main

import (
	"os"

	"github.com/promise-language/forge/primitives/tooling"
	"github.com/promise-language/forge/tools/build/common"
)

// stamp is written at link time by ./make.
var stamp string

func main() {
	os.Exit(tooling.GateTool(common.Define(), stamp).Run(os.Args[1:]))
}
