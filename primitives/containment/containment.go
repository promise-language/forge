// Package containment decides whether a shell command may run, by asking a
// narrower question than "is it dangerous": does it write anywhere but inside
// the repository it was invoked for.
//
// It is the checked form of a rule the engineering guide already states as prose:
//
//	A change is made inside a materialized worktree of one repository and
//	nothing else.
//
// Prose is satisfied by whoever remembers it. An agent with a shell can `cd` to a
// sibling checkout and commit there, and nothing observes that it happened — the
// tool-use guard judges what a command DOES, not which tree it does it to, so a
// cross-repository write looked exactly like an ordinary one.
//
// # Reads stay allowed, deliberately
//
// Only writes are confined. Reading a sibling repository is how a project learns
// the contract it has to satisfy and adopts an implementation that already
// passes, instead of inventing a worse one locally. A rule that blocked reads
// would push every project toward writing its own version of everything, which
// is the drift the shared library exists to end. So `cat ../other/gate.go` is
// fine and `cat > ../other/gate.go` is not.
//
// # It fails closed on what it cannot establish
//
// This is the disclosure layer's stance rather than the danger layer's. Danger
// matches a blocklist of things known to be destructive; a blocklist is fine
// there because anything it misses is merely not-blocked. Here a miss is a write
// to another repository, so the question is inverted: a command is allowed when
// it can be SHOWN to write only inside the root, and refused when that cannot be
// shown. An interpreter given a program on stdin can write anywhere and says
// nothing about it in its argv, so it is refused when an outside path is anywhere
// in sight rather than guessed at.
//
// # What it does not catch, stated plainly
//
// An interpreter that computes an outside path rather than spelling it — building
// it from an environment variable, or reading it from a file — passes. So does a
// command that writes through a symlink inside the root that points outside. Both
// are real, both are recorded as known misses with tests, and neither is a reason
// to skip the cases that ARE decidable: the escape that actually happened was a
// plain `cd` and a plain `git commit`, spelled in the clear.
package containment

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Request is one command, and everything needed to judge where it would write.
type Request struct {
	// Command is the shell command as written.
	Command string
	// CWD is the working directory it will run in.
	CWD string
	// Root is the repository it may write inside.
	Root string
	// AlsoWritable are absolute paths outside Root that may still be written —
	// a session scratchpad, a temporary directory. They are passed in rather
	// than compiled in: what is legitimately writable is the caller's policy,
	// and a list held here would be a policy nobody could see.
	AlsoWritable []string
}

// Allowed reports whether the command writes only where it may. A nil return
// allows it; a non-nil error refuses and names both the path and the reason, so
// whoever is stopped can tell a real containment breach from a false positive.
func Allowed(r Request) error {
	if r.Root == "" {
		return fmt.Errorf("containment refused: no repository root was given, so nothing can be established about where this writes")
	}
	root, err := filepath.Abs(r.Root)
	if err != nil {
		return fmt.Errorf("containment refused: the repository root %q cannot be resolved: %v", r.Root, err)
	}
	cwd := r.CWD
	if cwd == "" {
		cwd = root
	}

	// Paths outside the root named anywhere in the current pipeline. A stage
	// that writes to targets it reads from stdin cannot be shown to stay inside,
	// so it is judged against everything its pipeline mentioned.
	var pipelineOutside []string

	for _, s := range segments(r.Command) {
		seg := s.words
		if s.startsPipeline {
			pipelineOutside = nil
		}
		pipelineOutside = append(pipelineOutside, outsidePaths(seg, cwd, root, r.AlsoWritable)...)
		if name, ok := stdinFedWriter(seg); ok && len(pipelineOutside) > 0 {
			return fmt.Errorf("containment refused: %s takes what it writes from stdin, and this pipeline names %s "+
				"outside %s — a change is made inside one repository and nothing else",
				name, pipelineOutside[0], root)
		}

		// `cd` is both tracked AND judged, and the judging is the load-bearing
		// half.
		//
		// Tracked, because its whole significance is what the next segment
		// inherits: `cd ../sibling && git commit` names no path that the commit
		// itself could be judged on. That is the escape that actually happened.
		//
		// Judged, because tracking alone is defeated by everything a shell can
		// do to reach a directory without spelling it here — a subshell, a
		// pushd, a path built from a variable, a symlink. A working directory
		// outside the tree is one unqualified command away from writing there,
		// so leaving the tree is refused rather than followed. Reading another
		// checkout stays available the way it should have been done in the first
		// place: an absolute path, or `git -C` for a git read.
		if target, ok := cdTarget(seg); ok {
			dest := resolve(cwd, target)
			if err := mustBeInside("a working directory there is one unqualified command away from writing there; "+
				"read another checkout by absolute path, or with `git -C`", dest, "", root, r.AlsoWritable); err != nil {
				return err
			}
			cwd = dest
			continue
		}
		if err := judgeSegment(seg, cwd, root, r.AlsoWritable); err != nil {
			return err
		}
	}
	return nil
}

// outsidePaths are the paths a segment names that resolve outside the root. They
// are not themselves a refusal — reading a sibling is allowed — but they are what
// a later stage of the same pipeline may end up writing to.
func outsidePaths(seg []string, cwd, root string, also []string) []string {
	var out []string
	for _, w := range seg {
		if isOperator(w) {
			continue
		}
		for _, cand := range append(pathCandidates(w), embeddedAbsPaths(w)...) {
			if mustBeInside("", cand, cwd, root, also) != nil {
				out = append(out, resolve(cwd, cand))
			}
		}
	}
	return out
}

// pathCandidates treats a whole word as a path when it could be one.
func pathCandidates(w string) []string {
	if w == "" || strings.HasPrefix(w, "-") || !strings.Contains(w, "/") {
		return nil
	}
	// A word carrying whitespace is a quoted program, not a path; its own paths
	// come from embeddedAbsPaths.
	if strings.ContainsAny(w, " \t\n") {
		return nil
	}
	return []string{w}
}

// embeddedAbsPaths pulls absolute paths out of a quoted program or a longer
// string. `bash -c 'cat > /other/x'` carries its target inside one token, and a
// reader that treated the token as a single path would resolve it relative to the
// cwd and conclude it was inside.
func embeddedAbsPaths(w string) []string {
	var out []string
	for i := 0; i < len(w); i++ {
		if w[i] != '/' {
			continue
		}
		if i > 0 && !isSeparatorByte(w[i-1]) {
			continue
		}
		j := i
		for j < len(w) && !isSeparatorByte(w[j]) {
			j++
		}
		if j-i > 1 {
			out = append(out, w[i:j])
		}
		i = j
	}
	return out
}

func isSeparatorByte(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r', '\'', '"', '`', '(', ')', ';', '|', '&', ',', '=', '>', '<':
		return true
	}
	return false
}

// stdinFedWriter reports whether a segment writes to targets it reads from
// stdin, so nothing in its own words says where it writes.
func stdinFedWriter(seg []string) (string, bool) {
	words := stripAssignments(seg)
	if len(words) == 0 {
		return "", false
	}
	if commandName(words[0]) != "xargs" {
		return "", false
	}
	// `xargs grep` reads; `xargs sed -i` and `xargs rm` write. An xargs whose
	// command is not recognised is the unresolvable case, which fails closed.
	for _, w := range words[1:] {
		if strings.HasPrefix(w, "-") {
			continue
		}
		name := commandName(w)
		if _, writes := writeTargets(name, words[1:]); writes {
			return "xargs " + name, true
		}
		if knownReaders[name] {
			return "", false
		}
		return "xargs " + name, true
	}
	return "xargs", true
}

// knownReaders are commands that only read, so an xargs feeding one writes
// nothing. The list is small on purpose: anything absent from it is treated as a
// writer, which is the direction that fails closed.
var knownReaders = map[string]bool{
	"cat": true, "grep": true, "egrep": true, "fgrep": true, "head": true,
	"tail": true, "wc": true, "md5": true, "shasum": true, "sha256sum": true,
	"file": true, "stat": true, "echo": true, "printf": true, "ls": true,
	"diff": true, "cmp": true, "sort": true, "uniq": true, "awk": false,
}

// judgeSegment decides one command between control operators.
func judgeSegment(seg []string, cwd, root string, also []string) error {
	words := stripAssignments(seg)
	if len(words) == 0 {
		return nil
	}
	name := commandName(words[0])
	args := words[1:]

	// A redirection writes wherever it points, whatever the command is.
	for _, path := range redirectionTargets(seg) {
		if err := mustBeInside("a redirection writes to it", path, cwd, root, also); err != nil {
			return err
		}
	}

	// An interpreter handed a program can write anywhere without naming it in
	// argv, so an outside path anywhere in the segment cannot be shown to be a
	// read. Refused rather than guessed at.
	if opaqueInterpreters[name] {
		// A shell handed `-c <program>` carries SHELL, so the program is read
		// with the same reader rather than scanned as text. Anything else — a
		// python or perl program — is not shell and only its embedded absolute
		// paths can be seen.
		if shells[name] {
			if prog, ok := dashCArgument(args); ok {
				for _, sub := range segments(prog) {
					if err := judgeSegment(sub.words, cwd, root, also); err != nil {
						return err
					}
				}
			}
		}
		for _, a := range args {
			for _, p := range embeddedAbsPaths(a) {
				if err := mustBeInside("an interpreter could write to it and its program is not visible here", p, cwd, root, also); err != nil {
					return err
				}
			}
			if !looksLikePath(a) {
				continue
			}
			if err := mustBeInside("an interpreter could write to it and its program is not visible here", a, cwd, root, also); err != nil {
				return err
			}
		}
		// The interpreter's own cwd is what an unqualified write lands in.
		return mustBeInside("an interpreter runs there and could write without naming a path", ".", cwd, root, also)
	}

	// `git -C <dir>` moves the whole command, so a mutating subcommand there
	// writes there — the -C form of the `cd` case above.
	if name == "git" {
		if err := judgeGit(args, cwd, root, also); err != nil {
			return err
		}
	}

	targets, ok := writeTargets(name, args)
	if !ok {
		// Not a command known to write. Its cwd still matters only for commands
		// that write without naming a path, which is what the git arm above and
		// the interpreter arm cover; a read here is allowed to name any path.
		return nil
	}
	for _, t := range targets {
		if err := mustBeInside(fmt.Sprintf("%s writes to it", name), t, cwd, root, also); err != nil {
			return err
		}
	}
	return nil
}

// judgeGit handles the two ways git writes outside the tree it was invoked in.
func judgeGit(args []string, cwd, root string, also []string) error {
	dir := cwd
	var sub string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "-C" && i+1 < len(args):
			dir = resolve(cwd, args[i+1])
			i++
		case strings.HasPrefix(args[i], "--git-dir=") || strings.HasPrefix(args[i], "--work-tree="):
			_, v, _ := strings.Cut(args[i], "=")
			dir = resolve(cwd, v)
		case strings.HasPrefix(args[i], "-"):
		case sub == "":
			sub = args[i]
		}
	}
	if sub == "" || !gitWrites[sub] {
		return nil
	}
	return mustBeInside("git "+sub+" writes to that repository", dir, "", root, also)
}

// gitWrites are the subcommands that change a repository. A read — log, show,
// diff, status, rev-parse — may name any repository: that is how one project
// learns what another actually does.
var gitWrites = map[string]bool{
	"commit": true, "add": true, "rm": true, "mv": true, "apply": true,
	"reset": true, "checkout": true, "switch": true, "restore": true,
	"merge": true, "rebase": true, "cherry-pick": true, "revert": true,
	"stash": true, "clean": true, "gc": true, "prune": true,
	"push": true, "fetch": true, "pull": true, "remote": true,
	"tag": true, "branch": true, "config": true, "init": true, "clone": true,
	"am": true, "update-ref": true, "symbolic-ref": true, "notes": true,
	"worktree": true, "submodule": true, "filter-branch": true, "replace": true,
}

// opaqueInterpreters run a program that argv does not show. Their writes cannot
// be enumerated, so an outside path in sight is a refusal.
var opaqueInterpreters = map[string]bool{
	"python": true, "python3": true, "perl": true, "ruby": true, "node": true,
	"deno": true, "bun": true, "php": true, "lua": true,
	"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true,
	"awk": true, "gawk": true, "xargs": true, "env": true, "nohup": true,
	"eval": true, "source": true, "go": false, // `go` is handled by writeTargets
}

var shells = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true}

// dashCArgument returns the program given after -c.
func dashCArgument(args []string) (string, bool) {
	for i, a := range args {
		if a == "-c" && i+1 < len(args) {
			return args[i+1], true
		}
	}
	return "", false
}

// writeTargets names the paths a known write command would write, and whether
// the command is one that writes at all.
func writeTargets(name string, args []string) ([]string, bool) {
	switch name {
	case "rm", "rmdir", "mkdir", "touch", "truncate", "shred", "unlink":
		return pathArgs(args), true
	case "tee":
		return pathArgs(args), true
	case "cp", "mv", "ln", "install", "rsync":
		// The destination is the last path argument.
		p := pathArgs(args)
		if len(p) == 0 {
			return nil, true
		}
		return p[len(p)-1:], true
	case "sed", "perl-i":
		if hasInPlace(args) {
			return pathArgs(args), true
		}
		return nil, false
	case "dd":
		for _, a := range args {
			if v, ok := strings.CutPrefix(a, "of="); ok {
				return []string{v}, true
			}
		}
		return nil, true
	case "chmod", "chown", "chgrp", "xattr":
		return pathArgs(args), true
	case "go":
		// Only `-o` writes outside the module's own cache.
		for i, a := range args {
			if a == "-o" && i+1 < len(args) {
				return []string{args[i+1]}, true
			}
		}
		return nil, false
	case "gofmt":
		if hasFlag(args, "-w") {
			return pathArgs(args), true
		}
		return nil, false
	}
	return nil, false
}

// hasInPlace reports whether sed was asked to edit in place. BSD sed takes an
// argument after -i and GNU sed does not, which is why the flag is detected
// rather than parsed: either way the files that follow are written.
func hasInPlace(args []string) bool {
	for _, a := range args {
		if a == "-i" || strings.HasPrefix(a, "-i.") || a == "--in-place" || strings.HasPrefix(a, "--in-place=") {
			return true
		}
	}
	return false
}

func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

// pathArgs are the arguments that are paths rather than flags.
func pathArgs(args []string) []string {
	var out []string
	for _, a := range args {
		if a == "" || strings.HasPrefix(a, "-") {
			continue
		}
		out = append(out, a)
	}
	return out
}

// mustBeInside refuses a path that resolves outside the root and the extra
// writable prefixes.
func mustBeInside(why, path, cwd, root string, also []string) error {
	abs := path
	if cwd != "" {
		abs = resolve(cwd, path)
	} else {
		abs = filepath.Clean(path)
	}
	if inside(abs, root) {
		return nil
	}
	for _, w := range also {
		if a, err := filepath.Abs(w); err == nil && inside(abs, a) {
			return nil
		}
	}
	return fmt.Errorf("containment refused: %s is outside %s — %s; "+
		"a change is made inside one repository and nothing else", abs, root, why)
}

// inside reports whether abs is root or below it, comparing whole path
// components so a sibling whose name merely starts with the root's is outside.
func inside(abs, root string) bool {
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "..")
}

func resolve(cwd, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(cwd, path))
}

// looksLikePath keeps the interpreter arm from firing on every flag and literal.
func looksLikePath(a string) bool {
	if a == "" || strings.HasPrefix(a, "-") {
		return false
	}
	return strings.Contains(a, "/")
}

// commandName strips a leading path and an env prefix, so `/usr/bin/git` and
// `git` are the same command.
func commandName(word string) string {
	return filepath.Base(word)
}

// stripAssignments drops leading `NAME=value` words, which are environment for
// the command rather than the command.
func stripAssignments(seg []string) []string {
	for i, w := range seg {
		if isOperator(w) {
			continue
		}
		if strings.Contains(w, "=") && !strings.HasPrefix(w, "-") && !strings.Contains(w, "/") {
			if name, _, _ := strings.Cut(w, "="); name != "" && !strings.ContainsAny(name, " \t") {
				continue
			}
		}
		return withoutOperands(seg[i:])
	}
	return nil
}

// withoutOperands drops redirection operators and their targets, which are
// judged separately by redirectionTargets.
func withoutOperands(seg []string) []string {
	var out []string
	for i := 0; i < len(seg); i++ {
		if isRedirect(seg[i]) {
			i++ // skip its target
			continue
		}
		if isOperator(seg[i]) {
			continue
		}
		out = append(out, seg[i])
	}
	return out
}

// cdTarget reports the directory a `cd` segment moves to.
func cdTarget(seg []string) (string, bool) {
	words := stripAssignments(seg)
	if len(words) == 0 {
		return "", false
	}
	// pushd moves the shell exactly as cd does; a rule that knew only cd would
	// be one synonym away from irrelevant.
	switch commandName(words[0]) {
	case "cd", "pushd":
	default:
		return "", false
	}
	for _, a := range words[1:] {
		if !strings.HasPrefix(a, "-") {
			return a, true
		}
	}
	return "", false
}

// redirectionTargets are the paths a segment's redirections write to. `<` is a
// read and is not among them.
func redirectionTargets(seg []string) []string {
	var out []string
	for i := 0; i < len(seg); i++ {
		if isWriteRedirect(seg[i]) && i+1 < len(seg) {
			out = append(out, seg[i+1])
			i++
		}
	}
	return out
}

func isRedirect(w string) bool {
	return isWriteRedirect(w) || w == "<" || strings.HasSuffix(w, "<")
}

func isWriteRedirect(w string) bool {
	switch w {
	case ">", ">>", "&>", "&>>", "1>", "1>>", "2>", "2>>", ">|":
		return true
	}
	return false
}

func isOperator(w string) bool {
	switch w {
	case "&&", "||", ";", "|", "&", "(", ")", "{", "}":
		return true
	}
	return isRedirect(w)
}
