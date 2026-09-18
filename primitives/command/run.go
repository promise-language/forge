package command

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// The four statuses an invocation ends with (docs/command-line.md, Exit status
// and refusal). They answer two questions: was the subject examined — 0 and 1
// say yes, 2 and 3 say no — and whose repair is it: 1 the subject's, 2 the
// invocation's, 3 the installation's.
const (
	// StatusDone is what was asked, including help, version and an empty
	// result.
	StatusDone = 0
	// StatusFailed could not complete, or stopped on a condition a person must
	// clear.
	StatusFailed = 1
	// StatusMalformed is an invocation that was malformed, and nothing was
	// done.
	StatusMalformed = 2
	// StatusRefused is a binary that declined to run, and nothing was done.
	StatusRefused = 3
)

// Run is the whole of an invocation, and the only entry point a main has:
//
//	func main() { os.Exit(command.Run(define(), os.Args[1:], command.Stdio())) }
//
// Nothing below it calls os.Exit, which is what keeps every outcome testable
// without starting a process.
func Run(t Tool, args []string, s Streams) int {
	// A binary that is not fit to act refuses every invocation, -help and
	// -version included, before it reads the command line. What a stale binary
	// would print is the surface it was built with, in the one place an
	// operator goes to learn what a tool is.
	if t.Fit != nil {
		if refusal := t.Fit(); refusal != nil {
			return writeRefusal(t, refusal, args, s)
		}
	}

	root := resolve(t.Root, nil, nil)
	// A tool whose definition fails the check refuses to run, reporting every
	// defect. Help is generated from that definition, so there is nothing
	// trustworthy left to answer with.
	if defects := check(t, root); len(defects) > 0 {
		for _, defect := range defects {
			fmt.Fprintf(s.Err, "%s: %v\n", t.Project, defect)
		}
		return StatusFailed
	}

	p := parse(root, args, s)

	if p.cmd.cmd.Delegate != nil {
		return delegate(t, p, args, s)
	}
	if len(p.problems) > 0 {
		// Every problem with the invocation is reported, and nothing is done.
		for _, problem := range p.problems {
			fmt.Fprintf(s.Err, "%s: %s\n", t.Project, problem)
		}
		return StatusMalformed
	}
	if p.help {
		return answer(t, helpFor(t, p.cmd), p.mode, s)
	}
	if p.version {
		return answer(t, versionOf(t), p.mode, s)
	}
	if p.brief {
		fmt.Fprint(s.Err, briefForm(t, p.cmd))
		return StatusMalformed
	}

	result, err := p.cmd.action(p.call)
	if err != nil {
		fmt.Fprintf(s.Err, "%s: %v\n", t.Project, err)
		return StatusFailed
	}
	if result == nil {
		return StatusDone
	}
	return answer(t, result, p.mode, s)
}

// answer writes one result and returns the status it carries.
func answer(t Tool, result Result, mode Mode, s Streams) int {
	if err := writeResult(result, mode, s.Out); err != nil {
		fmt.Fprintf(s.Err, "%s: %v\n", t.Project, err)
		return StatusFailed
	}
	if reported, ok := result.(StatusResult); ok {
		return reported.ExitStatus()
	}
	return StatusDone
}

// delegate hands everything after a delegating command's name, verbatim, to
// another program, and answers with that program's own status.
func delegate(t Tool, p *parsed, args []string, s Streams) int {
	argv, refusal := p.cmd.cmd.Delegate(p.call)
	if refusal != nil {
		return writeRefusal(t, refusal, args, s)
	}
	if len(argv) == 0 {
		fmt.Fprintf(s.Err, "%s: %s names no program to run\n", t.Project, strings.Join(p.cmd.path, " "))
		return StatusFailed
	}
	child := exec.Command(argv[0], append(argv[1:], p.delegate...)...)
	child.Stdin, child.Stdout, child.Stderr = s.In, s.Out, s.Err
	if err := child.Run(); err != nil {
		var exited *exec.ExitError
		if errors.As(err, &exited) {
			return exited.ExitCode()
		}
		fmt.Fprintf(s.Err, "%s: %v\n", t.Project, err)
		return StatusFailed
	}
	return StatusDone
}

// Stdio is the streams of a process: its own, with stdout asked whether it is a
// character device. A terminal is; a pipe or a redirect is not. Asking stdout
// rather than a flag is what makes `tool > out.json` produce JSON without
// anyone having to remember to say so.
func Stdio() Streams {
	terminal := false
	if info, err := os.Stdout.Stat(); err == nil {
		terminal = info.Mode()&os.ModeCharDevice != 0
	}
	// A working directory that cannot be read leaves a relative path resolving
	// against nothing, which convert reports as the path it could not make
	// absolute rather than guessing at one.
	dir, _ := os.Getwd()
	return Streams{
		In:            os.Stdin,
		Out:           os.Stdout,
		Err:           os.Stderr,
		OutIsTerminal: terminal,
		Dir:           dir,
	}
}
