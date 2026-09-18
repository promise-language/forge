// Command make is the meta-builder. It compiles every other tool under cmd/
// into <repoRoot>/bin, stamping each binary with the tools-source hash and the
// absolute repo root via -ldflags. It is the one tool that runs via 'go run'
// (from the ./make trampoline), so it is never compiled into bin/ and never
// stale — which is what breaks the bootstrap cycle.
//
// It is also the recovery every refusal names, which is why it declares no Fit:
// a tool that refused because the binaries are stale points at this one, and a
// builder that could refuse on the same ground would close the way out
// (docs/project-tools.md, Staleness).
package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/promise-language/forge/primitives"
	"github.com/promise-language/forge/primitives/command"
	"github.com/promise-language/forge/tools/build/common"
)

// result is what make answers: whether it had anything to do, and what it did.
type result struct {
	UpToDate bool     `json:"up_to_date"`
	Built    []string `json:"built"`
	Removed  []string `json:"removed"`
}

// Human is the line, or the lines, a person reads.
func (r result) Human(w io.Writer) error {
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

// define is make's whole surface. It carries no version: it is the one main
// without a stamp, because it runs from the source it builds
// (docs/project-tools.md, One implementation).
func define() command.Tool {
	return command.Tool{
		Project: "make",
		Root: command.Command{
			Name:    "make",
			Summary: "compile every tool under tools/build/cmd into bin/",
			Flags: []command.Flag{{
				Name:        "force",
				Type:        command.Boolean,
				Description: "compile every tool even where bin/ is already up to date",
			}},
			Action: build,
		},
	}
}

// build is the meta-builder's whole run.
func build(c *command.Call) (command.Result, error) {
	// 1. Resolve the repo root. The ./make trampoline cd'd go run into
	//    <root>/tools/build, so our cwd is exactly that. Two levels up is root.
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	repoRoot := filepath.Dir(filepath.Dir(cwd))
	if !filepath.IsAbs(repoRoot) {
		return nil, fmt.Errorf("resolved repo root is not absolute: %s", repoRoot)
	}

	// 2. Hash the tools source — baked into every binary below.
	hash, err := common.SourceHash(repoRoot)
	if err != nil {
		return nil, err
	}

	// 3. Enable git hooks unconditionally (idempotent, fast).
	if err := primitives.RunSetup(repoRoot); err != nil {
		fmt.Fprintf(c.Narrate, "warning: could not configure git hooks: %v\n", err)
	}

	// What this project builds, from the one function that answers that —
	// the same one `bin/run --list` reports from, so the binaries this writes
	// into bin/ and the commands the project claims cannot drift apart.
	tools, err := common.CommandNames(repoRoot)
	if err != nil {
		return nil, err
	}

	binDir := filepath.Join(repoRoot, "bin")
	hashFile := filepath.Join(binDir, ".tools.hash")

	// 4. Up-to-date short circuit.
	if !c.Bool("force") && upToDate(hashFile, hash, binDir, tools) {
		return result{UpToDate: true, Built: []string{}, Removed: []string{}}, nil
	}

	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return nil, err
	}

	// 5. Build each tool, injecting repoRoot and sourceHash via ldflags.
	ldflags := fmt.Sprintf("-s -w -X main.sourceHash=%s -X main.repoRoot=%s", hash, repoRoot)
	toolsModDir := filepath.Join(repoRoot, "tools", "build")
	for _, name := range tools {
		out := filepath.Join(binDir, primitives.BinaryName(name))
		fmt.Fprintf(c.Narrate, "building %s\n", name)
		if err := primitives.RunInStreams(toolsModDir, c.Narrate, c.Narrate, "go", "build",
			"-trimpath",
			"-ldflags", ldflags,
			"-o", out,
			"./cmd/"+name,
		); err != nil {
			return nil, fmt.Errorf("building %s: %w", name, err)
		}
	}

	// 6. Write the hash sidecar — the staleness contract.
	//    Line 1: source hash. Lines 2+: name:sha256 per binary.
	var sb strings.Builder
	sb.WriteString(hash)
	sb.WriteByte('\n')
	for _, name := range tools {
		h, err := fileHash(filepath.Join(binDir, primitives.BinaryName(name)))
		if err != nil {
			return nil, fmt.Errorf("hashing %s: %w", name, err)
		}
		sb.WriteString(name)
		sb.WriteByte(':')
		sb.WriteString(h)
		sb.WriteByte('\n')
	}
	if err := os.WriteFile(hashFile, []byte(sb.String()), 0o644); err != nil {
		return nil, err
	}
	return result{Built: tools, Removed: []string{}}, nil
}

func main() {
	os.Exit(command.Run(define(), os.Args[1:], command.Stdio()))
}

func upToDate(hashFile, hash, binDir string, tools []string) bool {
	f, err := os.Open(hashFile)
	if err != nil {
		return false
	}
	defer f.Close()

	sc := bufio.NewScanner(f)

	// Line 1: source hash.
	if !sc.Scan() || strings.TrimSpace(sc.Text()) != hash {
		return false
	}

	// Lines 2+: name:sha256 per binary. Build a lookup.
	recorded := make(map[string]string)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			return false // malformed entry
		}
		recorded[parts[0]] = parts[1]
	}

	// Every expected tool must have a recorded hash that matches the binary on disk.
	for _, name := range tools {
		want, ok := recorded[name]
		if !ok {
			return false // tool not recorded in sidecar
		}
		got, err := fileHash(filepath.Join(binDir, primitives.BinaryName(name)))
		if err != nil {
			return false // binary missing or unreadable
		}
		if got != want {
			return false // binary replaced since last build
		}
	}
	return true
}

// fileHash returns the hex-encoded SHA-256 of the file at path.
func fileHash(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:]), nil
}
