package tooling

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A tool writes nothing outside its repository root: scratch goes under
// .home/tmp/, in a directory unique to the run and removed when the run ends.
func TestScratchIsUniqueToTheRunAndRemovedWithIt(t *testing.T) {
	root := fixture(t, "")
	r, end, err := Begin(Standard(), root, &strings.Builder{})
	if err != nil {
		t.Fatal(err)
	}

	scratch := r.ScratchRoot()
	under := filepath.Join(root, filepath.FromSlash(ScratchDir))
	if !strings.HasPrefix(scratch, under+string(filepath.Separator)) {
		t.Errorf("the run's scratch is %q, want it under %s", scratch, under)
	}
	if _, err := os.Stat(scratch); err != nil {
		t.Fatalf("the run's scratch does not exist: %v", err)
	}

	end()
	if _, err := os.Stat(scratch); !os.IsNotExist(err) {
		t.Errorf("the run's scratch survived the run: %v", err)
	}
}

// A tool refuses to write under .home/ when git does not ignore it. A scratch
// file in a tracked path is a change to the tree a gate measures, and a file
// `git add -A` would put into the verified tree.
func TestARunRefusesWhereItsScratchWouldBeTracked(t *testing.T) {
	root := fixture(t, "")
	write(t, root, ".gitignore", "/bin/\n/.workspace/\n")
	git(t, root, "add", "-A")

	if _, _, err := Begin(Standard(), root, &strings.Builder{}); err == nil {
		t.Error("a run began in a checkout that does not ignore .home/")
	} else if !strings.Contains(err.Error(), ".home") {
		t.Errorf("the refusal is %q, want it to name the directory", err)
	}
}

// Every child runs with its scratch and its cache pointed inside the checkout.
// Setting a child's environment is how the tool instructs that child; nothing
// about it is an input to the tool.
func TestEveryChildIsToldToWriteInsideTheCheckout(t *testing.T) {
	root := fixture(t, "")
	r, _ := run(t, Standard(), root)

	told := map[string]string{}
	for _, entry := range r.childEnv() {
		if name, value, ok := strings.Cut(entry, "="); ok {
			told[name] = value
		}
	}
	for _, name := range []string{"TMPDIR", "TMP", "TEMP", "GOTMPDIR"} {
		if told[name] != r.ScratchRoot() {
			t.Errorf("%s = %q, want this run's scratch %q", name, told[name], r.ScratchRoot())
		}
	}
	for _, name := range []string{"GOCACHE", "GOMODCACHE"} {
		want := filepath.Join(root, filepath.FromSlash(CacheDir), "go")
		if told[name] != want {
			t.Errorf("%s = %q, want %q", name, told[name], want)
		}
	}
}

// The parent's own value is replaced rather than appended to: on the platforms
// where the first assignment wins, appending would leave the inherited value in
// force and the child would write where the tool told it not to.
func TestTheParentsOwnValueIsReplaced(t *testing.T) {
	env := replaceEnv([]string{"TMPDIR=/somewhere/else", "PATH=/bin"}, "TMPDIR", "/inside")
	count := 0
	for _, entry := range env {
		if strings.HasPrefix(entry, "TMPDIR=") {
			count++
			if entry != "TMPDIR=/inside" {
				t.Errorf("TMPDIR is %q", entry)
			}
		}
	}
	if count != 1 {
		t.Errorf("TMPDIR appears %d times, want once", count)
	}
}

// Progress lines go to stderr as `==> <unit> <command>`, with every path
// repository-relative.
func TestProgressNamesTheUnitAndTheCommand(t *testing.T) {
	root := fixture(t, "", "tools/build")
	p := Standard()
	r, narrated := run(t, p, root)

	units, err := Units(r)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.Value(unitNamed(t, units, "tools-build"), "git", "rev-parse", "--git-dir"); err != nil {
		t.Fatal(err)
	}
	if want := "==> tools-build git rev-parse --git-dir"; !strings.Contains(narrated.String(), want) {
		t.Errorf("narration is %q, want a line reading %q", narrated.String(), want)
	}
	if strings.Contains(narrated.String(), root) {
		t.Errorf("narration carries an absolute path: %q", narrated.String())
	}

	// Including where the argument itself is a path this run made. A coverage
	// profile and a build output are scratch, handed to the child absolute
	// because it runs in its unit's directory — and a progress line that
	// repeated that spelling would name the reader's home directory rather than
	// anything about this repository.
	// What git makes of the argument is beside the point; the progress line is
	// written before the child runs.
	r.Value(units[0], "git", "rev-parse", "--git-dir", r.Scratch("cover.out"))
	if strings.Contains(narrated.String(), root) {
		t.Errorf("a scratch argument was narrated absolute: %q", narrated.String())
	}
	if !strings.Contains(narrated.String(), filepath.ToSlash(filepath.Join(ScratchDir))) {
		t.Errorf("narration lost the scratch path entirely: %q", narrated.String())
	}
}

// The bound keeps a first and a last segment and says how much it dropped.
// Capping to the tail loses the line that dates a condition; capping to the
// head loses a failing test's summary, which is the last thing the run wrote
// (docs/org/engineering-guide.md, Everything a program writes has a ceiling).
func TestTheCaptureKeepsBothEndsAndSaysWhatItDropped(t *testing.T) {
	w := newSegmented(20)
	w.Write([]byte("FIRST-"))
	for range 100 {
		w.Write([]byte("xxxxxxxxxx"))
	}
	w.Write([]byte("-LAST"))

	kept := w.String()
	if !strings.HasPrefix(kept, "FIRST-") {
		t.Errorf("the first bytes are gone: %q", kept)
	}
	if !strings.HasSuffix(kept, "-LAST") {
		t.Errorf("the last bytes are gone: %q", kept)
	}
	if !strings.Contains(kept, "bytes dropped") {
		t.Errorf("the capture does not say how much it dropped: %q", kept)
	}
	if !w.Overflowed() || w.Dropped() == 0 {
		t.Errorf("overflowed=%v dropped=%d, want the overflow reported", w.Overflowed(), w.Dropped())
	}
}

// A stream that fit is kept whole, with nothing inserted into it: a measurement
// parsing the capture must not find a marker line among the records.
func TestACaptureThatFitIsUnchanged(t *testing.T) {
	w := newSegmented(1 << 10)
	w.Write([]byte("one\ntwo\nthree\n"))
	if got := w.String(); got != "one\ntwo\nthree\n" {
		t.Errorf("the capture is %q, want it unchanged", got)
	}
	if w.Overflowed() {
		t.Error("a stream inside the bound reported an overflow")
	}
}

// A stream longer than the head segment and no longer than the bound is still a
// stream that fit, and it comes back whole. This is the band a child actually
// lands in — `go test -json` over a large module writes megabytes — and a
// capture that returned the head alone here would drop records off a stream a
// measurement counts, while reporting no overflow for the envelope to call
// itself incomplete over.
func TestACaptureBetweenTheHeadAndTheBoundIsStillWhole(t *testing.T) {
	for _, n := range []int{51, 99, 100} {
		w := newSegmented(100)
		body := strings.Repeat("r", n-1) + "!"
		w.Write([]byte(body))
		if got := w.String(); got != body {
			t.Errorf("%d bytes into a bound of 100 came back as %d: %q", n, len(got), got)
		}
		if w.Overflowed() || w.Dropped() != 0 {
			t.Errorf("%d bytes into a bound of 100 reported an overflow", n)
		}
	}
}

// Write reports the whole slice consumed, so a child writing here is never
// blocked or errored: its lifetime is the run's context, not this reader's
// appetite. A short write ends a child with an error about the reader.
func TestTheCaptureNeverShortWrites(t *testing.T) {
	w := newSegmented(4)
	for _, body := range []string{"aaaa", "bbbbbbbb", "c"} {
		n, err := w.Write([]byte(body))
		if n != len(body) || err != nil {
			t.Errorf("writing %d bytes reported (%d, %v), want the whole slice consumed", len(body), n, err)
		}
	}
}

// A measurement whose child exceeded the bound is incomplete, and says so.
func TestAnOverflowingChildMakesTheMeasurementIncomplete(t *testing.T) {
	if reason := OverflowReason("go test -json"); !strings.Contains(reason, "go test -json") || reason == "" {
		t.Errorf("the reason is %q, want it to name what outran the bound", reason)
	}
}

// No child outlives the tool that started it: the run's context bounds every
// one, and ending the run kills what is still alive.
func TestAChildDiesWithTheRun(t *testing.T) {
	root := fixture(t, "")
	r, end, err := Begin(Standard(), root, &strings.Builder{})
	if err != nil {
		t.Fatal(err)
	}

	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(started)
		// A child that would outlive any reasonable test, so that what ends it
		// is the run and not its own exit.
		done <- r.Attached(Unit{Toolchain: "go"}, sleeper(), sleepArgs()...)
	}()
	<-started

	// The run ends while the child is alive. Every path out of Begin's closer
	// signals the group and cancels the context.
	end()
	if err := <-done; err == nil {
		t.Error("a child outlived the run that started it")
	}
}
