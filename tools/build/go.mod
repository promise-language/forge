module github.com/promise-language/forge/tools/build

go 1.26

require github.com/promise-language/forge v0.0.0

// This repository is its own first consumer (docs/primitives.md §6): forge's
// tools are built against forge's own working tree, so a helper that does not
// work is a build that does not pass here. The replaced tree is hashed as tool
// source — see common/sourcedirs.go.
replace github.com/promise-language/forge => ../..
