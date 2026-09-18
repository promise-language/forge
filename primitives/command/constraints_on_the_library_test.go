package command

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// Constraints on the library: no init(), and no package-level mutable state.
// Its streams, working directory and arguments are passed in, which is what
// lets every rule above be tested without a process — and a package-level
// variable would be one invocation's parse leaking into the next.
//
// The standard-library-only constraint is checked for this package too, by
// primitives' own import test, which walks the tree.
func TestConstraintsOnTheLibrary(t *testing.T) {
	set := token.NewFileSet()
	packages, err := parser.ParseDir(set, ".", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	scanned := 0
	for name, pkg := range packages {
		for path, file := range pkg.Files {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			scanned++
			for _, decl := range file.Decls {
				switch d := decl.(type) {
				case *ast.FuncDecl:
					if d.Recv == nil && d.Name.Name == "init" {
						t.Errorf("%s declares init(), and this package has none", path)
					}
				case *ast.GenDecl:
					if d.Tok == token.VAR {
						for _, spec := range d.Specs {
							for _, ident := range spec.(*ast.ValueSpec).Names {
								t.Errorf("%s declares the package-level var %s; one invocation's state would reach the next", path, ident.Name)
							}
						}
					}
				}
			}
		}
		_ = name
	}
	// A walk that found nothing would pass silently, which reads as coverage of
	// a constraint nothing was checked against.
	if scanned == 0 {
		t.Fatal("no source files were scanned — the constraint was not checked")
	}
}
