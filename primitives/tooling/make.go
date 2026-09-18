package tooling

// Make (docs/project-tools.md).
//
// make runs from source through the committed trampoline and needs nothing
// pre-built. It is the one tool without a stamp: it is compiled from the source
// it builds on every run, which is why it is the recovery every refusal names.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/promise-language/forge/primitives"
	"github.com/promise-language/forge/primitives/command"
)

// SidecarFile is where make records what it built, relative to a repository
// root. It is what the up-to-date check reads and what the prune consults.
const SidecarFile = "bin/.tools.hash"

// WorkspaceMarker is what provisioning writes, and it is where the names the
// workspace is accountable for are recorded.
const WorkspaceMarker = ".workspace/project.json"

// MakeResult is what make answers.
type MakeResult struct {
	UpToDate bool     `json:"up_to_date"`
	Built    []string `json:"built"`
	Removed  []string `json:"removed"`
}

// Human is the line, or the lines, a person reads.
func (r MakeResult) Human(w io.Writer) error {
	if r.UpToDate {
		_, err := fmt.Fprintln(w, "Tools up to date")
		return err
	}
	for _, name := range r.Built {
		if _, err := fmt.Fprintf(w, "built    %s\n", name); err != nil {
			return err
		}
	}
	for _, name := range r.Removed {
		if _, err := fmt.Fprintf(w, "removed  %s\n", name); err != nil {
			return err
		}
	}
	return nil
}

// ResolveRoot pins the root from the working directory the trampoline left the
// builder in: <root>/tools/build. It refuses unless the builder's own source is
// there, because a builder that guessed at a root would stamp every binary with
// somewhere else.
func ResolveRoot(cwd string) (string, error) {
	root := filepath.Dir(filepath.Dir(cwd))
	if !filepath.IsAbs(root) {
		return "", fmt.Errorf("the resolved repository root is not absolute: %s", root)
	}
	own := filepath.Join(root, filepath.FromSlash(primitives.ToolsBuildDir), "cmd", "make", "main.go")
	if _, err := os.Stat(own); err != nil {
		return "", fmt.Errorf("this is not a project's tools directory: %s does not exist, so the trampoline did not run from %s",
			own, filepath.Join(root, filepath.FromSlash(primitives.ToolsBuildDir)))
	}
	return root, nil
}

// SourceSet derives what this project's tool source is, rather than reading a
// list someone maintained. It is all of tools/build, plus every package outside
// it that the tools import from a local replace target.
//
// The set is derived, never declared: a new import changes a file inside
// tools/build, which changes the hash, and the next build derives the set
// again.
func SourceSet(r *Run) ([]string, error) {
	set := []string{primitives.ToolsBuildDir}
	tools := Unit{Dir: primitives.ToolsBuildDir, Toolchain: "go"}
	out, _, err := r.Value(tools, "go", "list", "-deps", "-f",
		"{{if .Module}}{{if .Module.Replace}}{{if .Module.Replace.Dir}}{{.Dir}}{{end}}{{end}}{{end}}", "./...")
	if err != nil {
		return nil, fmt.Errorf("asking go list what the tools import from a local replace: %w", err)
	}
	seen := map[string]bool{}
	for line := range strings.SplitSeq(out, "\n") {
		dir := strings.TrimSpace(line)
		if dir == "" {
			continue
		}
		rel, err := filepath.Rel(r.Root, dir)
		if err != nil || strings.HasPrefix(rel, "..") {
			// A replace pointing outside the repository is not this
			// repository's source, and hashing it would make every binary's
			// staleness depend on a tree no clone has.
			continue
		}
		// A package directory, not a tree: its subdirectories are packages of
		// their own, and `go list` reports each one it imported.
		entry := filepath.ToSlash(rel) + "/*"
		if seen[entry] {
			continue
		}
		seen[entry] = true
		set = append(set, entry)
	}
	sort.Strings(set[1:])
	return set, nil
}

// BuildSet is every directory under tools/build/cmd except make. `run --list`
// reports the same set, computed by this same function, so the binaries make
// writes into bin/ and the commands the project claims cannot drift apart.
//
// make runs from source via the trampoline and is never compiled into bin/, so
// a caller asking what this project puts there must not be told about a binary
// that is never there.
func BuildSet(root string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(primitives.ToolsBuildDir), "cmd"))
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "make" {
			continue
		}
		if !command.ValidName(e.Name()) {
			return nil, fmt.Errorf("tools/build/cmd/%s is not a command name: lowercase letters, digits and - as the only separator", e.Name())
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names, nil
}

// checkCollisions refuses a name in the build set that the workspace marker
// records as a workspace tool (tool-contract.md, One name one builder). With no
// marker there is nothing to check: nothing was promised.
func checkCollisions(root string, built []string) error {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(WorkspaceMarker)))
	if err != nil {
		return nil
	}
	var marker struct {
		Tools []string `json:"tools"`
	}
	if err := json.Unmarshal(data, &marker); err != nil {
		return fmt.Errorf("reading %s: %w", WorkspaceMarker, err)
	}
	theirs := map[string]bool{}
	for _, name := range marker.Tools {
		theirs[name] = true
	}
	var clashing []string
	for _, name := range built {
		if theirs[name] {
			clashing = append(clashing, name)
		}
	}
	if len(clashing) == 0 {
		return nil
	}
	return fmt.Errorf("%s is the workspace's, and this project builds a tool of the same name: one name has one builder, so rename tools/build/cmd/%s",
		strings.Join(clashing, ", "), clashing[0])
}

// RunMake is the whole of the meta-builder's run.
func RunMake(r *Run, rebuild bool) (MakeResult, error) {
	// Wire the hooks and check the ignores, exactly as setup does. A checkout
	// that can build can therefore always gate its commits.
	if _, err := wireHooks(r); err != nil {
		return MakeResult{}, err
	}
	if err := CheckIgnores(r); err != nil {
		return MakeResult{}, err
	}

	set, err := SourceSet(r)
	if err != nil {
		return MakeResult{}, err
	}
	hash, err := primitives.SourceHash(r.Root, set...)
	if err != nil {
		return MakeResult{}, err
	}

	tools, err := BuildSet(r.Root)
	if err != nil {
		return MakeResult{}, err
	}
	if err := checkCollisions(r.Root, tools); err != nil {
		return MakeResult{}, err
	}

	binDir := filepath.Join(r.Root, "bin")
	sidecar := filepath.Join(r.Root, filepath.FromSlash(SidecarFile))
	recorded, _ := readSidecar(sidecar)

	if !rebuild && upToDate(recorded, hash, binDir, tools) {
		return MakeResult{UpToDate: true, Built: []string{}, Removed: []string{}}, nil
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return MakeResult{}, err
	}

	if err := runBuildSteps(r, r.project.Make.Before, "before"); err != nil {
		return MakeResult{}, err
	}

	// Every tool is attempted, and every failure is reported: a person who ran
	// the builder learns about all of them in one round rather than one per
	// round.
	stamp := Stamp{Root: r.Root, Hash: hash, Dirs: set}.Encode()
	toolsUnit := Unit{Dir: primitives.ToolsBuildDir, Toolchain: "go"}
	var failures []string
	for _, name := range tools {
		out := filepath.Join(binDir, primitives.BinaryName(name))
		if err := r.Attached(toolsUnit, "go", "build",
			"-trimpath",
			"-ldflags", "-s -w -X main.stamp="+stamp,
			"-o", out,
			"./cmd/"+name,
		); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", name, err))
		}
	}
	if len(failures) > 0 {
		// No sidecar is written: a sidecar recording a build that did not
		// happen is what would make the next run report the tools up to date.
		return MakeResult{}, fmt.Errorf("%d of %d tools did not compile:\n  %s",
			len(failures), len(tools), strings.Join(failures, "\n  "))
	}

	removed := prune(binDir, recorded, tools)

	if err := runBuildSteps(r, r.project.Make.After, "after"); err != nil {
		return MakeResult{}, err
	}

	if err := writeSidecar(sidecar, hash, binDir, tools); err != nil {
		return MakeResult{}, err
	}
	return MakeResult{Built: tools, Removed: removed}, nil
}

// runBuildSteps runs the project's own work around the compile.
func runBuildSteps(r *Run, steps []Step, when string) error {
	for _, step := range steps {
		fmt.Fprintf(r.Narrate, "==> %s (%s build)\n", step.Name, when)
		if err := step.Run(r); err != nil {
			return fmt.Errorf("%s: %w", step.Name, err)
		}
	}
	return nil
}

// prune removes a name the previous sidecar recorded that the build set no
// longer holds. A name make did not record is never touched, because workspace
// tools share bin/.
func prune(binDir string, recorded sidecar, tools []string) []string {
	current := map[string]bool{}
	for _, name := range tools {
		current[name] = true
	}
	removed := []string{}
	for name := range recorded.binaries {
		if current[name] {
			continue
		}
		if err := os.Remove(filepath.Join(binDir, primitives.BinaryName(name))); err == nil || os.IsNotExist(err) {
			removed = append(removed, name)
		}
	}
	sort.Strings(removed)
	return removed
}

// sidecar is what the previous build recorded: the source hash, and each built
// name with its binary's digest.
type sidecar struct {
	hash     string
	binaries map[string]string
}

func readSidecar(path string) (sidecar, error) {
	read := sidecar{binaries: map[string]string{}}
	data, err := os.ReadFile(path)
	if err != nil {
		return read, err
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 0 {
		return read, fmt.Errorf("%s is empty", path)
	}
	read.hash = strings.TrimSpace(lines[0])
	for _, line := range lines[1:] {
		name, digest, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			return sidecar{binaries: map[string]string{}}, fmt.Errorf("%s has a malformed entry: %q", path, line)
		}
		read.binaries[name] = digest
	}
	return read, nil
}

// upToDate reports whether every tool the project builds is present and is the
// binary the sidecar recorded, built from this source.
func upToDate(recorded sidecar, hash, binDir string, tools []string) bool {
	if recorded.hash != hash {
		return false
	}
	for _, name := range tools {
		want, ok := recorded.binaries[name]
		if !ok {
			return false
		}
		got, err := fileDigest(filepath.Join(binDir, primitives.BinaryName(name)))
		if err != nil || got != want {
			return false
		}
	}
	return true
}

// writeSidecar records the source hash, then each built name with its binary's
// SHA-256. It is written last, so a run that died part-way leaves nothing
// claiming the tools are current.
func writeSidecar(path, hash, binDir string, tools []string) error {
	var b strings.Builder
	b.WriteString(hash)
	b.WriteByte('\n')
	for _, name := range tools {
		digest, err := fileDigest(filepath.Join(binDir, primitives.BinaryName(name)))
		if err != nil {
			return fmt.Errorf("hashing %s: %w", name, err)
		}
		fmt.Fprintf(&b, "%s:%s\n", name, digest)
	}
	return writeAtomically(path, []byte(b.String()))
}

// fileDigest returns the hex-encoded SHA-256 of the file at path.
func fileDigest(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
