// Package common holds this repository's tooling definition and nothing the
// library carries.
//
// The machinery — the invocation surface, the envelope and the listings, the
// judge's comparison, the ratchet and the verified-tree record, the staleness
// check, the confinement of what a tool writes — is
// github.com/promise-language/forge/primitives/tooling
// (docs/project-tools.md, One implementation). What is here is the part with
// more than one right answer: which gates this repository answers beyond the
// standard set, and what they are judged by.
package common

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/promise-language/forge/primitives/tooling"
)

// StampFile is the org corpus's version stamp: the release the vendored copies
// came from, with a digest per file.
const StampFile = "docs/org/stamp.json"

// Define is this repository's tooling: the standard set, and one gate of its
// own for the vendored organization-wide corpus.
//
// docs/org/ is refused at the edit by the workspace guard and verified against
// its stamp by the integration gate (docs/org/normative.md, Mechanical checks).
// The guard is the workspace's and stops an edit; this is the half that notices
// one that arrived some other way.
func Define() tooling.Project {
	p := tooling.Standard()
	p.Gates.Add(tooling.Gate{
		Name:        "stamped",
		Summary:     "vendored corpus files that differ from the stamp they were vendored at",
		Metrics:     tooling.Declared(tooling.Count("unstamped_files")),
		Measure:     measureStamped,
		Remediation: "re-vendor docs/org/ from the release stamp.json names, or raise the stamp in the change that raised the corpus",
	})
	p.Integration(tooling.Formatted, tooling.Builds, tooling.Checked, tooling.Tested, "stamped")
	// Coverage comes from a gate outside integration, so verify measures it in
	// the stage docs/project-tools.md, Verify names for exactly that: "A
	// project whose ratcheted metric comes from a gate outside integration adds
	// that gate here."
	//
	// It is capped rather than ratcheted today, and the cap is in
	// tools/gates/thresholds.json. The library reads and moves baselines, and
	// this repository would declare statement_coverage as one, but the
	// workspace's commit guard reads tools/gates/baselines.json with a shape
	// that is not the one The terms fixes — platform → metric → {direction,
	// value} against metric → {direction, value | targets} — so an entry
	// written as this project's own specification says is refused at the
	// commit. Making it a ratchet waits on
	// promise-language/workspace#490; nothing else here does.
	p.Verify.Into(tooling.StageMeasure, tooling.JudgedStep(tooling.Covered))
	return p
}

// stamp is what docs/org/stamp.json records.
type stamp struct {
	Home   string            `json:"home"`
	Tag    string            `json:"tag"`
	Commit string            `json:"commit"`
	Files  map[string]string `json:"files"`
}

// measureStamped counts the vendored files that are not what the stamp says
// they are: a digest that differs, a file the stamp names and the tree does not
// hold, and a file under docs/org/ the stamp does not name.
//
// All three are one count because all three are the same fact — this copy is
// not the release it claims to be — and a caller deciding whether to trust the
// corpus needs that answered, not itemized into three terms to keep in step.
func measureStamped(r *tooling.Run, _ []tooling.Unit) (tooling.Measured, error) {
	dir := filepath.Dir(StampFile)
	data, err := os.ReadFile(filepath.Join(r.Root, filepath.FromSlash(StampFile)))
	if err != nil {
		return tooling.Measured{}, fmt.Errorf("reading %s: %w", StampFile, err)
	}
	var recorded stamp
	if err := json.Unmarshal(data, &recorded); err != nil {
		return tooling.Measured{}, fmt.Errorf("reading %s: %w", StampFile, err)
	}
	if len(recorded.Files) == 0 {
		return tooling.Measured{}, fmt.Errorf("%s names no files, so nothing about the corpus was checked", StampFile)
	}

	var wrong []string
	for name, want := range recorded.Files {
		got, err := digest(filepath.Join(r.Root, filepath.FromSlash(dir), filepath.FromSlash(name)))
		switch {
		case err != nil:
			wrong = append(wrong, fmt.Sprintf("%s/%s is named by the stamp and is not here", dir, name))
		case got != want:
			wrong = append(wrong, fmt.Sprintf("%s/%s differs from the %s stamp", dir, name, recorded.Tag))
		}
	}

	entries, err := os.ReadDir(filepath.Join(r.Root, filepath.FromSlash(dir)))
	if err != nil {
		return tooling.Measured{}, fmt.Errorf("reading %s: %w", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() || e.Name() == filepath.Base(StampFile) {
			continue
		}
		if _, named := recorded.Files[e.Name()]; !named {
			wrong = append(wrong, fmt.Sprintf("%s/%s is here and the stamp does not name it", dir, e.Name()))
		}
	}

	sort.Strings(wrong)
	for _, line := range wrong {
		fmt.Fprintf(r.Narrate, "    %s\n", line)
	}
	return tooling.Measured{Metrics: []tooling.Measurement{
		tooling.Counted("unstamped_files", int64(len(wrong)), ""),
	}}, nil
}

// digest is the SHA-256 of one file, hex-encoded, which is the form
// stamp.json records.
func digest(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
