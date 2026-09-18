//go:build !windows

package tooling

// A spawned subprocess is tracked and dies with its parent — process groups,
// not orphans (docs/org/engineering-guide-go.md, Concurrency and lifecycle).
//
// A toolchain spawns its own children: `go test` runs one binary per package,
// `go build` runs the compiler and the linker. Signalling the child alone
// leaves those running, so the child leads a group of its own and the group is
// what the run signals and kills.

import (
	"os"
	"os/exec"
	"syscall"
)

// setProcessGroup puts the child at the head of a new process group.
func setProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// interruptGroup asks a child's whole group to stop. A negative pid addresses
// the group rather than the leader.
func interruptGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	syscall.Kill(-cmd.Process.Pid, syscall.SIGINT)
}

// killGroup ends a child's whole group.
func killGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}

// processAlive reports whether a process is still running. Signal 0 asks the
// kernel that question without delivering anything, which is how a lock left by
// a run that was killed is told from one a live run is holding.
func processAlive(pid int) bool {
	// A pid that is not positive is not a process id: 0 and negatives address a
	// process group, and signalling one of those would answer a question nobody
	// asked — and, for a lock, would answer "still held" forever.
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}
