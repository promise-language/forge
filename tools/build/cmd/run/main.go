// Command run asks one gate for a measurement and reaches a verdict on it, and
// dispatches to the commands this project builds.
//
// This is the by-hand path, and it takes the same route a runner takes rather
// than a parallel one: it executes bin/gate as a process and reads what came
// back. Running a single gate is not a lesser case — it is faster than
// everything that blocks a change from landing, and it is what someone
// iterating on one failure actually wants.
//
// It is one call into the library and the definition beside it: what it parses,
// how it reports, what it judges and what it exits with are not this file's
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
	os.Exit(tooling.RunTool(common.Define(), stamp).Run(os.Args[1:]))
}
