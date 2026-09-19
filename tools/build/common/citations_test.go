package common

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// inlineLink matches the one link form this repository's documents use. The
// target stops at the first space, so a link carrying a title — [t](p "why") —
// yields the path and not the title.
var inlineLink = regexp.MustCompile(`\[[^\]]*\]\(([^)\s]+)\)`)

// atxHeading matches the heading form the documents use, which is the only one
// GitHub derives an anchor from here.
var atxHeading = regexp.MustCompile(`(?m)^#{1,6} +(.*)$`)

// notRelative matches the targets that are not a path in this tree: a URL, and
// the mail address form.
var notRelative = regexp.MustCompile(`^[a-z][a-z0-9+.-]*:`)

// anchorFor is the anchor GitHub derives from a heading: lower case, every
// character that is not a letter, a digit, a space or a hyphen dropped, then
// spaces to hyphens. It is what a reader's browser resolves the fragment
// against, so it is what the check has to compute rather than something tidier.
func anchorFor(headingText string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(headingText)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune('-')
		}
	}
	return b.String()
}

// anchorsIn reports the anchors a document offers.
func anchorsIn(path string) (map[string]bool, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	anchors := map[string]bool{}
	for _, m := range atxHeading.FindAllStringSubmatch(string(body), -1) {
		anchors[anchorFor(m[1])] = true
	}
	return anchors, nil
}

// unresolvedFragments walks root for the documents this repository owns and
// reports every link whose fragment names no heading in the document it points
// at. scanned is how many documents were read, so a walk that reached none
// cannot pass as a walk that found none.
//
// A link with no fragment is left alone: whether its target is a tracked file
// is the half the workspace guard already reports, and checking it here would
// be a second spelling of one rule rather than the rest of it.
//
// Code fences are not skipped, because the guard does not skip them either: a
// link is a link wherever it is written, and the two halves of one rule may not
// disagree about what they are looking at.
//
// The vendored corpus is not read as a source. It is byte-identical to its home
// and is not this repository's to repair (org/normative.md, Location), and a
// fragment it gets wrong is an item filed against org. Links pointing into it
// are followed like any other, because the document making the claim is one
// this repository owns.
func unresolvedFragments(root string) (found []string, scanned int, err error) {
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel != "." && (strings.HasPrefix(d.Name(), ".") || d.Name() == "bin" || rel == "docs/org") {
				return fs.SkipDir
			}
			return nil
		}
		if filepath.Ext(p) != ".md" {
			return nil
		}
		body, readErr := os.ReadFile(p)
		if readErr != nil {
			return readErr
		}
		scanned++
		for _, m := range inlineLink.FindAllStringSubmatch(string(body), -1) {
			target := m[1]
			if notRelative.MatchString(target) {
				continue
			}
			path, fragment, carries := strings.Cut(target, "#")
			if !carries {
				continue
			}
			// An empty path is the document citing a section of itself.
			cited := p
			if path != "" {
				cited = filepath.Join(filepath.Dir(p), filepath.FromSlash(path))
			}
			anchors, anchorErr := anchorsIn(cited)
			if anchorErr != nil {
				found = append(found, fmt.Sprintf("%s cites %q and %s cannot be read", rel, target, path))
				continue
			}
			if !anchors[fragment] {
				found = append(found, fmt.Sprintf("%s cites %q and no heading there carries that anchor", rel, target))
			}
		}
		return nil
	})
	return found, scanned, err
}

// A citation into another document's section is how this repository's documents
// keep one fact in one home: they "link to the document that owns a fact"
// rather than restate it (org/normative.md, Links). org/normative.md, Mechanical
// checks makes that citation machine-checked — "a link carrying a fragment
// resolves to a heading in it" — and says how a failure is repaired: "by fixing
// the reference, never by removing the link".
//
// The workspace guard reports a link whose FILE does not resolve, on every
// tracked Markdown file. It says nothing about the fragment, so a renamed
// heading leaves a citation that still opens the right document, at the top,
// and reads to its author as though it worked. That is the drift the Links
// section describes arriving by a route nothing watches, and it is why the
// constraint needs a test rather than review.
//
// This is the shape the corpus stamp already has here: the guard is the
// workspace's and covers one half, and this repository covers the half the
// guard does not (see Define). A tool would be the twin that tool-contract §5
// forbids; a test over this repository's own tree is not one.
func TestEveryFragmentThisRepositoryCitesResolves(t *testing.T) {
	found, scanned, err := unresolvedFragments(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	// A walk that read nothing would pass silently, which reads as coverage of
	// a constraint nothing was checked against.
	if scanned == 0 {
		t.Fatal("no document was read — the constraint was not checked")
	}
	for _, f := range found {
		t.Errorf("%s — repair the reference rather than dropping the link", f)
	}
}

// The walk itself, over a tree that breaks the constraint: without this the
// test above passes whether or not it can see a broken citation at all.
func TestUnresolvedFragmentsReportsABrokenCitation(t *testing.T) {
	tree := t.TempDir()
	write(t, tree, "docs/target.md", "# Target\n\n## The Terms\n\nSaid once.\n")
	// Resolving: a section of another document, and a section of this one.
	write(t, tree, "docs/good.md", "# Good\n\n## Here\n\n[terms](target.md#the-terms) and [above](#here)\n")
	// The heading it names was renamed out from under it.
	write(t, tree, "docs/renamed.md", "# Renamed\n\n[terms](target.md#the-conditions)\n")
	// The document it names is not there at all.
	write(t, tree, "docs/absent.md", "# Absent\n\n[terms](gone.md#the-terms)\n")
	// Neither carries a fragment, so neither is this half's to report — the
	// second is exactly what the workspace guard reports.
	write(t, tree, "docs/plain.md", "# Plain\n\n[doc](target.md) and [gone](gone.md)\n")
	// Not a path in this tree.
	write(t, tree, "docs/remote.md", "# Remote\n\n[spec](https://example.invalid/x#frag)\n")
	// Not read: the vendored corpus is not this repository's to repair, and
	// nothing a repository publishes lives under a dot directory or in bin/.
	write(t, tree, "docs/org/corpus.md", "# Corpus\n\n[terms](../target.md#the-conditions)\n")
	write(t, tree, ".flow/notes.md", "[terms](../docs/target.md#the-conditions)\n")
	write(t, tree, "bin/notes.md", "[terms](../docs/target.md#the-conditions)\n")

	found, scanned, err := unresolvedFragments(tree)
	if err != nil {
		t.Fatal(err)
	}
	if scanned != 6 {
		t.Errorf("read %d documents, want the 6 outside the corpus, the dot directory and bin/", scanned)
	}
	if len(found) != 2 {
		t.Fatalf("reported %d broken citations, want the 2 in renamed.md and absent.md: %v", len(found), found)
	}
	// Each report names the document and the link as written, because a
	// document citing several sections of one target is repaired a link at a
	// time and the line has to say which one.
	for _, want := range []struct{ document, link string }{
		{"docs/renamed.md", "target.md#the-conditions"},
		{"docs/absent.md", "gone.md#the-terms"},
	} {
		named := false
		for _, f := range found {
			if strings.Contains(f, want.document) && strings.Contains(f, want.link) {
				named = true
			}
		}
		if !named {
			t.Errorf("no report names %s and %q: %v", want.document, want.link, found)
		}
	}
}

// The anchor is GitHub's, not a tidier spelling of it: a fragment is resolved
// by the browser against the anchor GitHub derived, so a check computing a
// different one would pass links that land nowhere and fail links that work.
// The headings here are forms this repository's documents actually use.
func TestTheAnchorIsTheOneGitHubDerives(t *testing.T) {
	for _, c := range []struct{ heading, want string }{
		{"Verify", "verify"},
		{"The bootstrap entry point", "the-bootstrap-entry-point"},
		{"What the standard toolchains measure", "what-the-standard-toolchains-measure"},
		// Punctuation is dropped rather than turned into a separator, so a
		// heading naming a path keeps its words joined the way GitHub joins
		// them.
		{"`cmd/init` and this repository", "cmdinit-and-this-repository"},
		{"One name, one builder", "one-name-one-builder"},
		{"Tools the project does not build", "tools-the-project-does-not-build"},
	} {
		if got := anchorFor(c.heading); got != c.want {
			t.Errorf("anchorFor(%q) = %q, want %q", c.heading, got, c.want)
		}
	}
}
