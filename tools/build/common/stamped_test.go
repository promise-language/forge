package common

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/promise-language/forge/primitives/tooling"
)

// The stamp names the release the vendored copies came from, and the gate
// notices a copy that is not what it claims to be. The guard stops an edit;
// this is the half that notices one that arrived some other way
// (docs/org/normative.md, Mechanical checks).
func TestTheCorpusGateCountsWhatTheStampDoesNotAccountFor(t *testing.T) {
	for _, c := range []struct {
		name  string
		build func(t *testing.T, root string)
		want  int64
		says  string
	}{
		{
			name:  "a corpus that matches its stamp",
			build: func(*testing.T, string) {},
			want:  0,
		},
		{
			name: "a file whose content moved under the stamp",
			build: func(t *testing.T, root string) {
				write(t, root, "docs/org/normative.md", "edited since it was vendored\n")
			},
			want: 1,
			says: "differs from the",
		},
		{
			name: "a file the stamp names and the tree does not hold",
			build: func(t *testing.T, root string) {
				if err := os.Remove(filepath.Join(root, "docs", "org", "normative.md")); err != nil {
					t.Fatal(err)
				}
			},
			want: 1,
			says: "is not here",
		},
		{
			name: "a file the stamp does not name",
			build: func(t *testing.T, root string) {
				write(t, root, "docs/org/smuggled.md", "arrived some other way\n")
			},
			want: 1,
			says: "the stamp does not name it",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := corpus(t)
			c.build(t, root)

			r, narrated := runIn(t, root)
			got, err := measureStamped(r, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Metrics) != 1 || got.Metrics[0].Int != c.want {
				t.Errorf("unstamped_files = %v, want %d", got.Metrics, c.want)
			}
			if c.says != "" && !strings.Contains(narrated.String(), c.says) {
				t.Errorf("the narration is %q, want it to say %q", narrated.String(), c.says)
			}
		})
	}
}

// A re-vendor that goes wrong rarely goes wrong once: a release adds members,
// drops them and rewrites others in one pass, so the count is every problem the
// corpus has and not the first one found. A gate that stopped at one would
// report a tree as one file wrong however many were, and each remaining problem
// would surface only after the one before it was fixed.
func TestTheCorpusGateCountsEveryProblemAtOnce(t *testing.T) {
	root := corpus(t)
	write(t, root, "docs/org/normative.md", "edited since it was vendored\n")
	if err := os.Remove(filepath.Join(root, "docs", "org", "cli-guide.md")); err != nil {
		t.Fatal(err)
	}
	write(t, root, "docs/org/smuggled.md", "arrived some other way\n")

	r, narrated := runIn(t, root)
	got, err := measureStamped(r, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Metrics) != 1 || got.Metrics[0].Int != 3 {
		t.Errorf("unstamped_files = %v, want 3", got.Metrics)
	}
	// Each of the three is named, so the one count resolves into the three
	// facts behind it. The path is matched from its base name: the narration
	// joins the corpus directory, which is spelled with the platform's
	// separator, to a name that is always spelled with a slash.
	for _, says := range []string{
		"normative.md differs from the",
		"cli-guide.md is named by the stamp and is not here",
		"smuggled.md is here and the stamp does not name it",
	} {
		if !strings.Contains(narrated.String(), says) {
			t.Errorf("the narration is %q, want it to say %q", narrated.String(), says)
		}
	}
}

// The line naming a drifted file names the release the stamp declares. The
// gate's remediation is to re-vendor "from the release stamp.json names", and
// this is where a person reads which release that is — the fact the stamp moves
// at every upgrade, and the only part of it a measurement ever shows.
func TestTheCorpusGateNamesTheReleaseTheStampDeclares(t *testing.T) {
	root := corpusAt(t, "v9.9.9-fixture")
	write(t, root, "docs/org/normative.md", "edited since it was vendored\n")

	r, narrated := runIn(t, root)
	if _, err := measureStamped(r, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(narrated.String(), "v9.9.9-fixture") {
		t.Errorf("the narration is %q, and it does not name the release the stamp declares",
			narrated.String())
	}
}

// A stamp that cannot be read, or that names nothing, is a measurement that did
// not happen rather than a corpus that is sound: "zero files wrong" is not a
// reading of a check that was never made.
func TestAnUnreadableStampIsNotACleanCorpus(t *testing.T) {
	for _, c := range []struct {
		name string
		body string
	}{
		{"a stamp that is not JSON", "not a stamp\n"},
		{"a stamp naming no files", `{"home": "promise-language/org", "tag": "v1.0.0", "files": {}}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := corpus(t)
			write(t, root, StampFile, c.body)

			r, _ := runIn(t, root)
			if _, err := measureStamped(r, nil); err == nil {
				t.Error("an unreadable stamp measured a clean corpus")
			}
		})
	}
}

// A repository with no stamp at all cannot be measured either.
func TestAnAbsentStampIsRefused(t *testing.T) {
	root := checkout(t)
	r, _ := runIn(t, root)
	if _, err := measureStamped(r, nil); err == nil {
		t.Error("a repository with no stamp measured a clean corpus")
	}
}

// corpus is a checkout holding a small vendored corpus and the stamp that
// accounts for it, so the digests are real ones rather than a fixture's idea of
// them.
func corpus(t *testing.T) string {
	t.Helper()
	return corpusAt(t, "v1.0.0")
}

// corpusAt is corpus with the release the stamp declares chosen by the caller,
// for the test that reads what the narration says about it.
func corpusAt(t *testing.T, tag string) string {
	t.Helper()
	root := checkout(t)
	files := map[string]string{
		"normative.md": "what makes a document binding\n",
		"cli-guide.md": "how every tool behaves at its invocation surface\n",
	}
	var entries []string
	for name, body := range files {
		write(t, root, filepath.Join("docs", "org", name), body)
		sum, err := digest(filepath.Join(root, "docs", "org", name))
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, `"`+name+`": "`+sum+`"`)
	}
	write(t, root, StampFile, `{"home": "promise-language/org", "tag": "`+tag+`", "files": {`+
		strings.Join(entries, ", ")+`}}`)
	return root
}

// checkout is a git repository the tools may act in: the ignore entries a run
// requires, and nothing else.
func checkout(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	root := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(root))
	write(t, root, ".gitignore", "/bin/\n/.workspace/\n/.home/\n")
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	return root
}

// runIn begins a run against a checkout, with the narration captured so a test
// can read what a person would have been told.
func runIn(t *testing.T, root string) (*tooling.Run, *strings.Builder) {
	t.Helper()
	var narrated strings.Builder
	r, end, err := tooling.Begin(Define(), root, &narrated)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(end)
	return r, &narrated
}

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
