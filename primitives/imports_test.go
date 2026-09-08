package primitives

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// selfModule is this library's own import path. A file here may import a
// sibling package of the same library; that is still one implementation, not a
// dependency a consumer acquires.
const selfModule = "github.com/promise-language/forge/primitives"

// This package imports nothing outside the standard library, and that is a
// constraint rather than an accident: every project's tools module depends on
// primitives, so anything primitives depends on is something every island tools
// module drags in — including forge's own, which would then build against a
// published version of the repository it is sitting inside (docs/primitives.md
// §4, and doc.go). Prose at both ends is not an agreement; this is the only
// thing that fails when the constraint stops holding.
//
// Test files are exempt: their imports reach no consumer.
func TestPrimitivesImportsOnlyTheStandardLibrary(t *testing.T) {
	found, scanned, err := externalImports(".")
	if err != nil {
		t.Fatal(err)
	}
	// A walk that found nothing would pass silently, which reads as coverage of
	// a constraint nothing was checked against.
	if scanned == 0 {
		t.Fatal("no source files were scanned — the constraint was not checked")
	}
	for _, f := range found {
		t.Errorf("%s — every island tools module in the fleet would drag it in", f)
	}
}

// The check itself, over a tree that breaks the constraint: without this the
// test above passes whether or not the walk can see a dependency at all.
func TestExternalImportsReportsADependency(t *testing.T) {
	tree := t.TempDir()
	write(t, tree, "outer.go", "package p\n\nimport \"github.com/example/dep\"\n\nvar _ = dep.X\n")
	write(t, tree, "nested/inner.go", "package q\n\nimport (\n\t\"fmt\"\n\t\"golang.org/x/tools/imports\"\n)\n\nvar _ = fmt.Sprint\n")
	// Exempt by design: the library's own packages, and test files, whose
	// imports reach no consumer.
	write(t, tree, "sibling.go", "package p\n\nimport \"github.com/promise-language/forge/primitives/containment\"\n")
	write(t, tree, "helper_test.go", "package p\n\nimport \"github.com/example/testonly\"\n")

	found, scanned, err := externalImports(tree)
	if err != nil {
		t.Fatal(err)
	}
	if scanned != 3 {
		t.Errorf("scanned %d files, want the 3 non-test ones", scanned)
	}
	joined := strings.Join(found, "\n")
	for _, want := range []string{"github.com/example/dep", "golang.org/x/tools/imports"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the report %q does not name %q", joined, want)
		}
	}
	for _, unwanted := range []string{"fmt", "containment", "testonly"} {
		if strings.Contains(joined, unwanted) {
			t.Errorf("the report %q names %q, which is not an outside dependency", joined, unwanted)
		}
	}
}

// externalImports returns one "<file> imports <path>" line per import under root
// that is neither the standard library nor this library itself, along with the
// number of non-test files it read.
func externalImports(root string) (found []string, scanned int, err error) {
	fset := token.NewFileSet()
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		scanned++
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		for _, spec := range f.Imports {
			imported, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			if isStandardLibrary(imported) || imported == selfModule || strings.HasPrefix(imported, selfModule+"/") {
				continue
			}
			found = append(found, fmt.Sprintf("%s imports %q", filepath.ToSlash(rel), imported))
		}
		return nil
	})
	return found, scanned, err
}

// isStandardLibrary reports whether an import path names a standard-library
// package: only paths whose first segment carries a dot name a module domain.
func isStandardLibrary(path string) bool {
	first, _, _ := strings.Cut(path, "/")
	return !strings.Contains(first, ".")
}
