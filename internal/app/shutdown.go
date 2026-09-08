package app

import (
	"os/exec"
	"syscall"
	"time"
)

// The PTY starts the CLI in its own session/process group. Stop that group,
// leaving the user's shell alone. Give reaping a bounded wait so a process
// stuck in kernel teardown cannot prevent the outer terminal being restored.
func stopAgent(command *exec.Cmd, done <-chan struct{}) {
	select {
	case <-done:
		return
	default:
	}
	_ = syscall.Kill(-command.Process.Pid, syscall.SIGTERM)
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	}
	// Also stop group members that ignored SIGTERM or outlived the CLI.
	_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	// Even SIGKILL cannot immediately release a process stuck in kernel I/O.
	// Keep reaping in the Wait goroutine, but never strand the user's terminal.
	timer.Reset(time.Second)
	select {
	case <-done:
	case <-timer.C:
	}
}
