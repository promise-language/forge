package primitives

import (
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ToolsBuildDir is the directory every project's tool source lives in, and the
// set SourceHash covers when a caller names none.
const ToolsBuildDir = "tools/build"

// SourceHash computes an FNV-128a hash over every .go/go.mod/go.sum file under
// each of dirs, which are slash-separated paths relative to repoRoot. It is
// stable across runs and platforms, and is what the meta-builder bakes into
// each binary to drive the staleness check.
//
// Naming no directory means <repoRoot>/tools/build — the whole of a project's
// tool source when the dependency on this library is a pinned version, because
// the version is recorded in go.mod and go.sum and both are hashed. A project
// that REPLACES the dependency with a local path names the replaced tree too:
// its tool source is then outside anything go.sum describes, and hashing only
// tools/build there would let an edit to the replaced tree leave every binary
// claiming to be current (docs/primitives.md §4).
//
// Each file is named by its path relative to repoRoot rather than to the
// directory it was found under, so a file at the same offset inside two
// directories cannot collide. The per-file size delimiter prevents file-boundary
// collisions, and the sort is over the whole set, so the answer does not depend
// on the order the directories were given in.
func SourceHash(repoRoot string, dirs ...string) (string, error) {
	if len(dirs) == 0 {
		dirs = []string{ToolsBuildDir}
	}

	// Keyed by repo-relative path, so overlapping directories contribute a
	// file once rather than twice.
	files := map[string]string{}
	for _, dir := range dirs {
		base := filepath.Join(repoRoot, filepath.FromSlash(dir))
		err := filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}
			name := info.Name()
			if !strings.HasSuffix(name, ".go") && name != "go.mod" && name != "go.sum" {
				return nil
			}
			rel, err := filepath.Rel(repoRoot, path)
			if err != nil {
				return err
			}
			files[filepath.ToSlash(rel)] = path
			return nil
		})
		if err != nil {
			return "", err
		}
	}

	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	h := fnv.New128a()
	for _, name := range names {
		data, err := os.ReadFile(files[name])
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s\n%d\n", name, len(data))
		h.Write(data)
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// ToolsSourceHash is SourceHash over the one directory a project that pins this
// library has. It is the call every project already writes, and it keeps its
// meaning (docs/primitives.md §5).
func ToolsSourceHash(repoRoot string) (string, error) {
	return SourceHash(repoRoot)
}
