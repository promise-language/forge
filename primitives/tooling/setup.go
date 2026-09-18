package tooling

// Setup (docs/project-tools.md).
//
// `make` performs the hooks and the ignores on every run. A checkout that can
// build can therefore always gate its commits, and a tool never writes scratch
// into a directory `git add -A` would stage.

import (
	"fmt"
	"io"
	"strings"

	"github.com/promise-language/forge/primitives"
)

// IgnoredDirs are the entries a project's committed .gitignore must carry, as
// `git check-ignore` reports them. They are the three a tool writes into and
// the tree must not carry: what make builds, what the workspace provisions, and
// what a run computes.
func IgnoredDirs() []string { return []string{"/bin/", "/.workspace/", "/.home/"} }

// SetupStep is one of a project's own setup steps. It reports whether it
// changed anything, because a second run changes nothing and says so.
type SetupStep struct {
	Name    string
	Summary string
	Run     func(*Run) (changed bool, err error)
}

// SetupResult is what `setup` answers.
type SetupResult struct {
	HooksPath string       `json:"hooks_path"`
	Steps     []StepChange `json:"steps"`
}

// StepChange is one step, and whether it changed anything.
type StepChange struct {
	Name    string `json:"name"`
	Changed bool   `json:"changed"`
}

// Human is the lines a person reads.
func (r SetupResult) Human(w io.Writer) error {
	if _, err := fmt.Fprintf(w, "git hooks configured (core.hooksPath = %s)\n", r.HooksPath); err != nil {
		return err
	}
	for _, s := range r.Steps {
		changed := "unchanged"
		if s.Changed {
			changed = "changed"
		}
		if _, err := fmt.Fprintf(w, "  %-16s %s\n", s.Name, changed); err != nil {
			return err
		}
	}
	return nil
}

// RunSetup makes a checkout ready to gate its own commits: it wires the hooks,
// checks the ignores, and runs the project's own steps.
func RunSetup(r *Run) (SetupResult, error) {
	changed, err := wireHooks(r)
	if err != nil {
		return SetupResult{}, err
	}
	if err := CheckIgnores(r); err != nil {
		return SetupResult{}, err
	}
	result := SetupResult{
		HooksPath: primitives.HooksPath,
		Steps:     []StepChange{{Name: "hooks", Changed: changed}},
	}
	for _, step := range r.project.Setup {
		fmt.Fprintf(r.Narrate, "==> %s\n", step.Name)
		did, err := step.Run(r)
		if err != nil {
			return SetupResult{}, fmt.Errorf("%s: %w", step.Name, err)
		}
		result.Steps = append(result.Steps, StepChange{Name: step.Name, Changed: did})
	}
	return result, nil
}

// wireHooks points git at this repository's hooks, and reports whether it had
// to. Reading first is what lets a second run say it changed nothing.
func wireHooks(r *Run) (bool, error) {
	current, _ := r.gitOutput("config", "--get", "core.hooksPath")
	if strings.TrimSpace(current) == primitives.HooksPath {
		return false, nil
	}
	if _, err := r.gitOutput("config", "core.hooksPath", primitives.HooksPath); err != nil {
		return false, fmt.Errorf("wiring the git hooks: %w", err)
	}
	return true, nil
}

// CheckIgnores refuses when the committed .gitignore does not ignore what a
// tool writes, naming each missing line.
//
// It never edits .gitignore: that file is tracked, and the entries belong to
// the project, not to a clone.
func CheckIgnores(r *Run) error {
	if _, err := r.gitOutput("rev-parse", "--git-dir"); err != nil {
		// Outside a git checkout there is no ignore rule to satisfy, and
		// nothing is refused on a ground that cannot exist.
		return nil
	}
	var missing []string
	for _, entry := range IgnoredDirs() {
		// The entries are spelled as .gitignore carries them, anchored at the
		// repository root. git check-ignore is asked about a path, and a path
		// beginning with "/" is an absolute one — which is never inside the
		// repository, so every entry would read as un-ignored.
		if !r.Ignored(strings.TrimPrefix(entry, "/")) {
			missing = append(missing, entry)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("this repository's .gitignore does not ignore %s — add %s to it, because a tool writes there and a scratch file in a tracked path is a change to the tree a gate measures",
		strings.Join(missing, ", "), strings.Join(missing, " and "))
}
