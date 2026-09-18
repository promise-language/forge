//go:build windows

package tooling

// The Windows half of "a spawned subprocess is tracked and dies with its
// parent" (docs/org/engineering-guide-go.md, Concurrency and lifecycle).
//
// Windows has no process group to signal the way a unix one is signalled:
// CREATE_NEW_PROCESS_GROUP makes the child a group leader so a console
// interrupt does not reach it through the parent's console, and `taskkill /T`
// is what ends the child together with everything it started.

import (
	"os/exec"
	"strconv"
	"syscall"
)

const createNewProcessGroup = 0x00000200

// setProcessGroup puts the child at the head of a new process group.
func setProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= createNewProcessGroup
}

// interruptGroup asks a child's whole tree to stop. Windows offers no gentler
// signal a console child is obliged to honour, so the ask and the kill are the
// same call and the second interrupt is what makes it immediate.
func interruptGroup(cmd *exec.Cmd) { killGroup(cmd) }

// killGroup ends a child and every process it started.
func killGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
}

// processAlive reports whether a process is still running. Windows refuses to
// open a handle to a pid that has exited, so opening one is the question.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	handle, err := syscall.OpenProcess(syscall.PROCESS_QUERY_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(handle)
	var code uint32
	if err := syscall.GetExitCodeProcess(handle, &code); err != nil {
		return false
	}
	const stillActive = 259
	return code == stillActive
}
