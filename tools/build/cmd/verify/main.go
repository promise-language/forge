// Command verify is the commit gate: it repairs what has one right answer, then
// measures what remains.
//
// It is one call into the library and the definition beside it: what it parses,
// how it reports, what it runs and what it exits with are not this file's
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
	os.Exit(tooling.VerifyTool(common.Define(), stamp).Run(os.Args[1:]))
}
