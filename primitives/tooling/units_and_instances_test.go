package tooling

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Units are found by asking git for tracked manifests, never listed by hand: a
// module added to the repository is measured with no edit anywhere else, and a
// manifest git does not track is not part of the tree a gate measures.
func TestAUnitIsADirectoryWhoseManifestTheRepositoryTracks(t *testing.T) {
	root := fixture(t, "", "tools/build")

	// Untracked: written into the tree and never added.
	write(t, root, "scratchpad/go.mod", "module example.com/scratch\n")

	units, err := Units(quiet(Standard(), root))
	if err != nil {
		t.Fatal(err)
	}
	if got := labels(units); !slices.Equal(got, []string{"root", "tools-build"}) {
		t.Errorf("units = %v, want the two tracked manifests and not the untracked one", got)
	}
}

// A manifest under a testdata directory is a fixture, not a unit: a sample
// project a test stands up is not part of the repository that holds it.
func TestAManifestUnderTestdataIsAFixture(t *testing.T) {
	root := fixture(t, "", "primitives/testdata/sample")
	units, err := Units(quiet(Standard(), root))
	if err != nil {
		t.Fatal(err)
	}
	if got := labels(units); !slices.Equal(got, []string{"root"}) {
		t.Errorf("units = %v, want the fixture left out", got)
	}
}

// A repository whose root has no go.mod has no root Go unit.
func TestARootWithNoManifestIsNoUnit(t *testing.T) {
	root := fixture(t, "tools/build")
	units, err := Units(quiet(Standard(), root))
	if err != nil {
		t.Fatal(err)
	}
	if got := labels(units); !slices.Equal(got, []string{"tools-build"}) {
		t.Errorf("units = %v, want no root unit", got)
	}
}

// A level appears only where it distinguishes something, so a project with one
// toolchain and one unit lists only the concepts.
func TestAnInstanceAppearsOnlyWhereItDistinguishesSomething(t *testing.T) {
	for _, c := range []struct {
		name  string
		units []Unit
		want  []string
	}{
		{"one toolchain, one unit narrows to nothing",
			[]Unit{{Dir: "", Toolchain: "go"}}, nil},
		{"one toolchain, two units narrow by unit",
			[]Unit{{Dir: "", Toolchain: "go"}, {Dir: "tools/build", Toolchain: "go"}},
			[]string{"root", "tools-build"}},
		{"two toolchains, one unit each narrow by toolchain",
			[]Unit{{Dir: "", Toolchain: "go"}, {Dir: "lib", Toolchain: "promise"}},
			[]string{"go", "promise"}},
		{"two toolchains, one with two units, prefix the unit by its toolchain",
			[]Unit{{Dir: "", Toolchain: "go"}, {Dir: "tools/build", Toolchain: "go"}, {Dir: "lib", Toolchain: "promise"}},
			[]string{"go", "go-root", "go-tools-build", "promise"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			var got []string
			for _, i := range Instances(c.units) {
				got = append(got, i.Name)
			}
			if !slices.Equal(got, c.want) {
				t.Errorf("instances = %v, want %v", got, c.want)
			}
		})
	}
}

// An instance measures only the units it names.
func TestAnInstanceSelectsItsOwnUnits(t *testing.T) {
	units := []Unit{{Dir: "", Toolchain: "go"}, {Dir: "tools/build", Toolchain: "go"}}

	whole, err := UnitsFor(units, "")
	if err != nil || len(whole) != 2 {
		t.Errorf("the concept alone measured %v (%v), want every unit", labels(whole), err)
	}
	narrowed, err := UnitsFor(units, "tools-build")
	if err != nil || len(narrowed) != 1 || narrowed[0].Dir != "tools/build" {
		t.Errorf("tools-build measured %v (%v), want that unit alone", labels(narrowed), err)
	}
	if _, err := UnitsFor(units, "nowhere"); err == nil {
		t.Error("an instance this project does not have was accepted")
	}
}

// A unit's source files are the tracked files of its language under it,
// excluding nested units: a file under tools/build is tools/build's source and
// never the root's, or the two would count it twice.
func TestSourceFilesExcludeNestedUnitsAndFixtures(t *testing.T) {
	root := fixture(t, "", "tools/build")
	write(t, root, "a.go", "package a\n")
	write(t, root, "notes.md", "not source\n")
	write(t, root, "testdata/sample.go", "package sample\n")
	write(t, root, "tools/build/b.go", "package b\n")
	git(t, root, "add", "-A")

	r := quiet(Standard(), root)
	units, err := Units(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		unit string
		want []string
	}{
		{"root", []string{"a.go"}},
		{"tools-build", []string{"tools/build/b.go"}},
	} {
		unit := unitNamed(t, units, c.unit)
		files, err := r.SourceFiles(unit)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(files, c.want) {
			t.Errorf("%s's source files = %v, want %v", c.unit, files, c.want)
		}
	}
}

// One function supplies the file set verify repairs and the set the formatted
// gate measures, so verify cannot repair one tree while the gate measures
// another.
func TestRepairAndTheFormattedGateReadOneFileSet(t *testing.T) {
	root := fixture(t, "")
	write(t, root, "bad.go", "package a\nfunc  F( ){}\n")
	git(t, root, "add", "-A")

	p := Standard()
	r, _ := run(t, p, root)
	if err := repair(r); err != nil {
		t.Fatalf("repairing: %v", err)
	}
	env, err := MeasureGate(r, Formatted)
	if err != nil {
		t.Fatalf("measuring: %v", err)
	}
	if got := env.Metrics[0].Int; got != 0 {
		t.Errorf("after the repair stage the gate still counts %d unformatted file(s)", got)
	}
	body, err := os.ReadFile(filepath.Join(root, "bad.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "func F() {}") {
		t.Errorf("the repair stage did not rewrite the file: %q", body)
	}
}

func labels(units []Unit) []string {
	var out []string
	for _, u := range units {
		out = append(out, u.Label())
	}
	return out
}

func unitNamed(t *testing.T, units []Unit, label string) Unit {
	t.Helper()
	for _, u := range units {
		if u.Label() == label {
			return u
		}
	}
	t.Fatalf("no unit called %q in %v", label, labels(units))
	return Unit{}
}
