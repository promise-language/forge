package tooling

// Writes and processes (docs/project-tools.md).
//
// A tool writes nothing outside its repository root. Scratch goes under
// .home/tmp/, in a directory unique to the run and removed when the run ends;
// what a toolchain computes from this checkout goes under .home/cache/. Every
// child is told so through its own environment, runs in its own process group,
// is bounded by the run's context, and dies with the tool.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
)

// HomeDir is the per-checkout directory a project's tools write inside, and
// ScratchDir and CacheDir are the two lifetimes within it (workspace's
// tool-contract.md, Layout).
const (
	HomeDir    = ".home"
	ScratchDir = ".home/tmp"
	CacheDir   = ".home/cache"
	// VerifyLock is where one verify per checkout is held.
	VerifyLock = ".home/verify.lock"
)

// MaxChildOutput bounds what a run reads from one child's stdout. The
// requirement that a bound exist is flow's; the size is
// docs/project-tools.md's.
const MaxChildOutput = 10 << 20 // 10 MiB

// Run is one run of one tool: the repository it acts on, the context every
// child is bounded by, the scratch it writes in, and where its narration goes.
type Run struct {
	// Root is the absolute repository root.
	Root string
	// Narrate is where progress goes. It is the tool's stderr, so that
	// `tool > out.json` and `tool -json 2>/dev/null` both behave.
	Narrate io.Writer

	ctx     context.Context
	cancel  context.CancelFunc
	project Project
	scratch string

	units      []Unit
	unitsKnown bool
	// prepared records each gate's preparation once per process, and why it
	// failed where it did: a preparation runs once, and a second part that
	// needs it is told the same thing rather than paying for it again.
	prepared map[string]string
	// measured is what this run's measuring steps found, so the ratchet stage
	// moves a baseline from the run that earned it rather than measuring again.
	measured []Envelope
	// tree is the id of the tree the record stage blessed.
	tree string

	mu          sync.Mutex
	children    map[int]*exec.Cmd
	interrupted bool
	stop        func()
}

// Begin starts one run: it checks that what it is about to write is ignored,
// makes the run's scratch directory, and takes ownership of the interrupt
// signals. The returned function ends the run — it removes the scratch, stops
// listening, and makes sure no child outlives the tool.
func Begin(p Project, root string, narrate io.Writer) (*Run, func(), error) {
	ctx, cancel := context.WithCancel(context.Background())
	r := &Run{
		Root:     root,
		Narrate:  narrate,
		ctx:      ctx,
		cancel:   cancel,
		project:  p,
		children: map[int]*exec.Cmd{},
	}
	if err := r.ignored(HomeDir); err != nil {
		cancel()
		return nil, func() {}, err
	}
	scratch, err := os.MkdirTemp(filepath.Join(root, filepath.FromSlash(ScratchDir)), "run-")
	if err != nil {
		if mkErr := os.MkdirAll(filepath.Join(root, filepath.FromSlash(ScratchDir)), 0o755); mkErr != nil {
			cancel()
			return nil, func() {}, fmt.Errorf("making this run's scratch directory: %w", mkErr)
		}
		if scratch, err = os.MkdirTemp(filepath.Join(root, filepath.FromSlash(ScratchDir)), "run-"); err != nil {
			cancel()
			return nil, func() {}, fmt.Errorf("making this run's scratch directory: %w", err)
		}
	}
	r.scratch = scratch
	r.listen()
	return r, func() {
		r.stopListening()
		r.killChildren()
		cancel()
		os.RemoveAll(scratch)
	}, nil
}

// Context is the context every child is bounded by.
func (r *Run) Context() context.Context { return r.ctx }

// Interrupted reports whether an interrupt has arrived. A pipeline asks between
// steps: the first interrupt signals the children and ends the run after the
// current step, rather than tearing it down where it stands.
func (r *Run) Interrupted() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.interrupted
}

// Scratch is a path unique to this run, under .home/tmp/. A coverage profile, a
// build output and a batched file list are all scratch.
func (r *Run) Scratch(name string) string { return filepath.Join(r.scratch, name) }

// ScratchRoot is this run's own scratch directory.
func (r *Run) ScratchRoot() string { return r.scratch }

// Cache is where a toolchain writes what it computes from this checkout. It is
// made on demand, because `fit:disk` measures the filesystem it will land on
// before any work is given to the machine.
func (r *Run) Cache(toolchain string) string {
	return filepath.Join(r.Root, filepath.FromSlash(CacheDir), toolchain)
}

// Toolchain returns the toolchain of that name, and whether this project
// carries it.
func (r *Run) Toolchain(name string) (Toolchain, bool) {
	for _, tc := range r.project.Toolchains {
		if tc.Name == name {
			return tc, true
		}
	}
	return Toolchain{}, false
}

// Abs resolves a unit's directory against the repository root.
func (r *Run) Abs(dir string) string {
	if dir == "" {
		return r.Root
	}
	return filepath.Join(r.Root, filepath.FromSlash(dir))
}

// ignored refuses to write under a directory git does not ignore. A scratch
// file in a tracked path is a change to the tree a gate measures, and a file
// `git add -A` would put into the verified tree.
func (r *Run) ignored(dir string) error {
	if _, err := r.gitOutput("rev-parse", "--git-dir"); err != nil {
		// Outside a git checkout there is no ignore rule to satisfy and no
		// commit to gate. Nothing is refused on a ground that cannot exist.
		return nil
	}
	if err := r.git("check-ignore", "-q", "--", dir+"/"); err != nil {
		return fmt.Errorf("this repository does not ignore %s/, and a tool writes scratch there; add %q to .gitignore", dir, "/"+dir+"/")
	}
	return nil
}

// Ignored reports whether git ignores a repository-relative directory.
func (r *Run) Ignored(dir string) bool {
	return r.git("check-ignore", "-q", "--", dir) == nil
}

// Output runs a child whose output is a stream of records to count, and returns
// what it wrote to stdout. The child's stderr is attached to the tool's own
// stderr rather than copied through a pipe, so a person watching a long
// measurement sees it as it happens.
func (r *Run) Output(u Unit, name string, args ...string) (string, bool, error) {
	captured := newSegmented(MaxChildOutput)
	err := r.spawn(u, captured, r.Narrate, nil, name, args...)
	return captured.String(), captured.Overflowed(), err
}

// Value runs a child whose output is a value, and keeps its two streams apart.
// Both are captured: stdout because the tool's own stdout carries the result,
// and stderr because the caller parses it. This is the one deliberate exception
// to attaching a child's stderr.
func (r *Run) Value(u Unit, name string, args ...string) (stdout, stderr string, err error) {
	out, errs := newSegmented(MaxChildOutput), newSegmented(MaxChildOutput)
	err = r.spawn(u, out, errs, nil, name, args...)
	return out.String(), errs.String(), err
}

// Attached runs a child with both its streams on the tool's narration, for work
// whose output nothing parses — a formatter rewriting the tree, a project's own
// build step.
func (r *Run) Attached(u Unit, name string, args ...string) error {
	return r.spawn(u, r.Narrate, r.Narrate, nil, name, args...)
}

// spawn runs one child under the run's context, in its own process group, with
// its scratch and its cache pointed inside the checkout.
func (r *Run) spawn(u Unit, out, errs io.Writer, extraEnv []string, name string, args ...string) error {
	fmt.Fprintf(r.Narrate, "==> %s %s %s\n", u.Label(), name, r.relative(strings.Join(args, " ")))
	cmd := exec.CommandContext(r.ctx, name, args...)
	cmd.Dir = r.Abs(u.Dir)
	cmd.Stdout = out
	cmd.Stderr = errs
	cmd.Env = append(r.childEnv(), extraEnv...)
	setProcessGroup(cmd)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	r.track(cmd)
	err := cmd.Wait()
	r.untrack(cmd)
	return err
}

// relative re-roots what a progress line says about a path. A child is handed
// absolute paths — a coverage profile in this run's scratch, a build output —
// because it runs in its unit's directory and an absolute path is the only
// spelling that means the same thing there. What a person reads is the other
// question: a progress line naming the reader's home directory says nothing
// about this repository, and the same line differs on every machine.
func (r *Run) relative(line string) string {
	return strings.ReplaceAll(line, r.Root+string(os.PathSeparator), "")
}

// childEnv is the environment every child is told to write inside the checkout
// with. Setting a child's environment is how the tool instructs that child;
// nothing about it is an input to the tool.
func (r *Run) childEnv() []string {
	env := os.Environ()
	for _, name := range []string{"TMPDIR", "TMP", "TEMP", "GOTMPDIR"} {
		env = replaceEnv(env, name, r.scratch)
	}
	for _, tc := range r.project.Toolchains {
		dir := r.Cache(tc.CacheDir)
		for _, name := range tc.CacheVars {
			env = replaceEnv(env, name, dir)
		}
	}
	return env
}

// replaceEnv sets one variable, dropping whatever the parent's environment said
// about it. Appending would leave the inherited value in place on the platforms
// where the first assignment wins.
func replaceEnv(env []string, name, value string) []string {
	kept := env[:0]
	for _, entry := range env {
		if key, _, ok := strings.Cut(entry, "="); !ok || key != name {
			kept = append(kept, entry)
		}
	}
	return append(kept, name+"="+value)
}

func (r *Run) track(cmd *exec.Cmd) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if cmd.Process != nil {
		r.children[cmd.Process.Pid] = cmd
	}
}

func (r *Run) untrack(cmd *exec.Cmd) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if cmd.Process != nil {
		delete(r.children, cmd.Process.Pid)
	}
}

// listen takes ownership of the interrupt signals for the life of the run.
func (r *Run) listen() {
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	done := make(chan struct{})
	r.stop = func() {
		signal.Stop(signals)
		close(done)
	}
	go func() {
		first := false
		for {
			select {
			case <-done:
				return
			case <-signals:
				if !first {
					first = true
					r.mu.Lock()
					r.interrupted = true
					r.mu.Unlock()
					fmt.Fprintln(r.Narrate, "interrupted — ending after the current step; interrupt again to stop now")
					r.signalChildren()
					continue
				}
				fmt.Fprintln(r.Narrate, "interrupted again — stopping now")
				r.killChildren()
				r.cancel()
				return
			}
		}
	}()
}

func (r *Run) stopListening() {
	if r.stop != nil {
		r.stop()
		r.stop = nil
	}
}

// signalChildren asks every live child's group to stop.
func (r *Run) signalChildren() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, cmd := range r.children {
		interruptGroup(cmd)
	}
}

// killChildren ends every live child's group. No child outlives the tool that
// started it.
func (r *Run) killChildren() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, cmd := range r.children {
		killGroup(cmd)
	}
}

// git runs git at the repository root with its output discarded, for the
// queries whose answer is the exit status.
func (r *Run) git(args ...string) error {
	cmd := exec.CommandContext(r.ctx, "git", args...)
	cmd.Dir = r.Root
	return cmd.Run()
}

// gitOutput runs git at the repository root and returns its stdout. Its stderr
// is captured into the error so it never reaches the terminal, and it is not
// narrated: asking git what it tracks is how the tool reads the tree, not work
// a person is watching.
func (r *Run) gitOutput(args ...string) (string, error) {
	var out, errs bytes.Buffer
	cmd := exec.CommandContext(r.ctx, "git", args...)
	cmd.Dir = r.Root
	cmd.Stdout = &out
	cmd.Stderr = &errs
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(errs.String()); msg != "" {
			return "", fmt.Errorf("git %s: %s", args[0], msg)
		}
		return "", fmt.Errorf("git %s: %w", args[0], err)
	}
	return out.String(), nil
}

// child runs one of this project's own binaries and reports what it wrote on
// stdout beside the status it exited with.
//
// The status is returned rather than folded into an error because a child's
// refusal is a status a caller relays, not a failure it reinterprets: a tool
// that declined to run measured nothing, and "exit 3" and "it went wrong" are
// different facts about the tree.
// The child's stderr goes where the caller says: onto the narration for the
// invocation a person is watching, and discarded for the second, silent
// invocation that asks a refusing child what its refusal was — which would
// otherwise print the same line twice.
func (r *Run) child(errs io.Writer, name string, args ...string) (string, int, error) {
	captured := newSegmented(MaxChildOutput)
	cmd := exec.CommandContext(r.ctx, name, args...)
	cmd.Dir = r.Root
	cmd.Stdout = captured
	cmd.Stderr = errs
	cmd.Env = r.childEnv()
	setProcessGroup(cmd)

	if err := cmd.Start(); err != nil {
		return "", 0, err
	}
	r.track(cmd)
	err := cmd.Wait()
	r.untrack(cmd)

	var exited *exec.ExitError
	switch {
	case err == nil:
		return captured.String(), 0, nil
	case errors.As(err, &exited):
		return captured.String(), exited.ExitCode(), nil
	default:
		return captured.String(), 0, err
	}
}

// gitTrimmed is gitOutput with the trailing newline removed, for the queries
// whose answer is one value.
func (r *Run) gitTrimmed(args ...string) (string, error) {
	out, err := r.gitOutput(args...)
	return strings.TrimSpace(out), err
}

// gitWithIndex runs git against a temporary index, so the real one is
// untouched. It is the one git call that needs an environment, which is why it
// does not go through gitOutput.
func (r *Run) gitWithIndex(index string, args ...string) (string, error) {
	var out, errs bytes.Buffer
	cmd := exec.CommandContext(r.ctx, "git", args...)
	cmd.Dir = r.Root
	cmd.Env = append(os.Environ(), "GIT_INDEX_FILE="+index)
	cmd.Stdout = &out
	cmd.Stderr = &errs
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(errs.String()); msg != "" {
			return "", fmt.Errorf("git %s: %s", args[0], msg)
		}
		return "", fmt.Errorf("git %s: %w", args[0], err)
	}
	return strings.TrimSpace(out.String()), nil
}

// segmented is the bound every captured stream is written through. It keeps a
// first and a last segment and says how much it dropped between them.
//
// Capping by truncating to the tail loses the line that dates a condition;
// capping by truncating to the head loses a failing test's summary, which is
// the last thing the run wrote. Keeping both ends is what
// docs/org/engineering-guide.md, Everything a program writes has a ceiling
// asks for where the first record matters as much as the last.
type segmented struct {
	head []byte
	tail []byte
	max  int
	n    int64
}

func newSegmented(max int) *segmented {
	if max < 2 {
		max = 2
	}
	return &segmented{max: max}
}

// Write always reports the whole slice consumed, so a child writing here is
// never blocked or errored: its lifetime is the run's context, not this
// reader's appetite. The count is taken before the slice is walked, because
// what is reported is what the child offered and not what was kept.
func (s *segmented) Write(p []byte) (int, error) {
	offered := len(p)
	s.n += int64(offered)
	half := s.max / 2
	if room := half - len(s.head); room > 0 {
		take := min(room, len(p))
		s.head = append(s.head, p[:take]...)
		p = p[take:]
	}
	if len(p) == 0 {
		return offered, nil
	}
	// The tail keeps the most recent half, so what a failing child said last
	// survives however much it said before.
	if len(p) >= half {
		s.tail = append(s.tail[:0], p[len(p)-half:]...)
	} else {
		s.tail = append(s.tail, p...)
		if over := len(s.tail) - half; over > 0 {
			s.tail = append(s.tail[:0], s.tail[over:]...)
		}
	}
	return offered, nil
}

// String is what was kept: the whole of a stream that fit, or the first and the
// last segment with a line between them saying how much fell out.
//
// A stream that fit is head and tail joined, not head alone. The head stops at
// half the bound, so everything past that point is in the tail — and returning
// the head by itself would truncate a stream that was never over the bound, with
// nothing reporting that it had been.
func (s *segmented) String() string {
	if !s.Overflowed() {
		return string(s.head) + string(s.tail)
	}
	return fmt.Sprintf("%s\n... %d bytes dropped between the first and last %d bytes ...\n%s",
		s.head, s.Dropped(), s.max/2, s.tail)
}

// Overflowed reports whether more than the bound was written.
func (s *segmented) Overflowed() bool { return s.n > int64(s.max) }

// Dropped is how much was written and not kept.
func (s *segmented) Dropped() int64 {
	kept := int64(len(s.head) + len(s.tail))
	if s.n <= kept {
		return 0
	}
	return s.n - kept
}

// OverflowReason is what a measurement says when its child outran the bound. A
// measurement whose child exceeded it is incomplete, and says so.
func OverflowReason(what string) string {
	return fmt.Sprintf("%s wrote more than the %d MiB a run captures, so part of what it reported was not read",
		what, MaxChildOutput>>20)
}
