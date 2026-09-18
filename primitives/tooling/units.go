package tooling

// Units and instances (docs/project-tools.md).
//
// A unit is a directory whose manifest the repository tracks. Units are found
// by asking git, never listed by hand: an untracked manifest is not part of the
// tree a gate measures, and a list in code is measured by nothing until someone
// remembers to add to it.

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// RootLabel is what the repository root is called where a unit is named by its
// directory.
const RootLabel = "root"

// Unit is one directory whose manifest the repository tracks.
type Unit struct {
	// Dir is the unit's directory, repository-relative and slash-separated.
	// The root is "".
	Dir string
	// Toolchain is the name of the toolchain whose manifest was found there.
	Toolchain string
}

// Label is the unit named by its repository-relative directory, with "/"
// written as "-", and "root" standing for the repository root.
func (u Unit) Label() string {
	if u.Dir == "" {
		return RootLabel
	}
	return strings.ReplaceAll(u.Dir, "/", "-")
}

// Instance is one derived narrowing of a concept: the name that follows the
// separator, and the units it selects.
type Instance struct {
	Name  string
	Units []Unit
}

// Units finds every unit by asking git for the manifests it tracks. One
// subprocess answers from the tree's own index.
//
// A manifest under a testdata directory is a fixture rather than a unit, which
// is what keeps a test's sample project from being measured as part of the
// repository that holds it.
func Units(r *Run) ([]Unit, error) {
	if len(r.units) > 0 || r.unitsKnown {
		return r.units, nil
	}
	manifests := map[string]string{}
	for _, tc := range r.project.Toolchains {
		manifests[tc.Manifest] = tc.Name
	}
	names := make([]string, 0, len(manifests))
	for m := range manifests {
		names = append(names, m)
	}
	sort.Strings(names)

	args := []string{"ls-files", "-z", "--"}
	for _, m := range names {
		args = append(args, m, "*/"+m)
	}
	out, err := r.gitOutput(args...)
	if err != nil {
		return nil, fmt.Errorf("asking git for this repository's tracked manifests: %w", err)
	}

	var units []Unit
	for _, tracked := range strings.Split(out, "\x00") {
		if tracked == "" {
			continue
		}
		if isFixture(tracked) {
			continue
		}
		toolchain, ok := manifests[path.Base(tracked)]
		if !ok {
			continue
		}
		units = append(units, Unit{Dir: path.Dir(tracked), Toolchain: toolchain})
	}
	for i := range units {
		if units[i].Dir == "." {
			units[i].Dir = ""
		}
	}
	sort.Slice(units, func(i, j int) bool {
		if units[i].Toolchain != units[j].Toolchain {
			return units[i].Toolchain < units[j].Toolchain
		}
		return units[i].Dir < units[j].Dir
	})
	r.units, r.unitsKnown = units, true
	return units, nil
}

// isFixture reports whether a tracked path sits under a testdata directory.
func isFixture(tracked string) bool {
	for _, segment := range strings.Split(path.Dir(tracked), "/") {
		if segment == "testdata" {
			return true
		}
	}
	return false
}

// Instances derives the narrowings of a concept over a set of units. A level
// appears only where it distinguishes something: a project with one toolchain
// and one unit narrows to nothing, and a name that would select exactly what
// another name already selects is not minted.
func Instances(units []Unit) []Instance {
	byToolchain := map[string][]Unit{}
	var order []string
	for _, u := range units {
		if _, seen := byToolchain[u.Toolchain]; !seen {
			order = append(order, u.Toolchain)
		}
		byToolchain[u.Toolchain] = append(byToolchain[u.Toolchain], u)
	}
	sort.Strings(order)

	var out []Instance
	several := len(order) > 1
	for _, tc := range order {
		held := byToolchain[tc]
		// An instance per toolchain, where the project has more than one.
		if several {
			out = append(out, Instance{Name: tc, Units: held})
		}
		// An instance per unit, where a toolchain has more than one. With a
		// single toolchain and a single unit there is nothing left to narrow:
		// the concept alone already names it.
		if len(held) < 2 {
			continue
		}
		for _, u := range held {
			name := u.Label()
			if several {
				name = tc + "-" + name
			}
			out = append(out, Instance{Name: name, Units: []Unit{u}})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// UnitsFor resolves an instance name to the units it names. An empty name means
// every unit — the safe reading, since a gate that quietly measured a subset
// would report a tree sound while part of it was unmeasured.
func UnitsFor(units []Unit, instance string) ([]Unit, error) {
	if instance == "" {
		return units, nil
	}
	for _, i := range Instances(units) {
		if i.Name == instance {
			return i.Units, nil
		}
	}
	return nil, fmt.Errorf("no instance named %q", instance)
}

// SourceFiles are the tracked files of a unit's language under that unit,
// excluding nested units and testdata. One function answers it, so verify's
// repair stage cannot rewrite one tree while the formatted gate measures
// another (docs/project-tools.md, Verify).
func (r *Run) SourceFiles(u Unit) ([]string, error) {
	tc, ok := r.Toolchain(u.Toolchain)
	if !ok {
		return nil, fmt.Errorf("unit %s names no toolchain this project carries", u.Label())
	}
	scope := "."
	if u.Dir != "" {
		scope = u.Dir
	}
	out, err := r.gitOutput("ls-files", "-z", "--", scope)
	if err != nil {
		return nil, fmt.Errorf("asking git for %s's source files: %w", u.Label(), err)
	}
	units, err := Units(r)
	if err != nil {
		return nil, err
	}

	var files []string
	for _, tracked := range strings.Split(out, "\x00") {
		if tracked == "" || isFixture(tracked) {
			continue
		}
		if !tc.owns(tracked) {
			continue
		}
		if nested(tracked, u, units) {
			continue
		}
		files = append(files, tracked)
	}
	sort.Strings(files)
	return files, nil
}

// nested reports whether a tracked path belongs to a unit inside this one. A
// file under tools/build is tools/build's source, never the root's, or the two
// units would measure it twice and a count would be the sum of one thing.
func nested(tracked string, u Unit, units []Unit) bool {
	for _, other := range units {
		if other.Dir == u.Dir || other.Dir == "" {
			continue
		}
		if !strings.HasPrefix(other.Dir, prefix(u.Dir)) {
			continue
		}
		if strings.HasPrefix(tracked, other.Dir+"/") {
			return true
		}
	}
	return false
}

// prefix is the directory as a path prefix: "" for the root, "dir/" otherwise.
func prefix(dir string) string {
	if dir == "" {
		return ""
	}
	return dir + "/"
}
