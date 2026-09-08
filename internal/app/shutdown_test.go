package app

import (
	"bufio"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
)

func TestStopAgentReapsUncooperativeCLI(t *testing.T) {
	if os.Getenv("STVENA_SHUTDOWN_HELPER") == "1" {
		signal.Ignore(syscall.SIGTERM, syscall.SIGHUP)
		_, _ = os.Stdout.WriteString("ready\n")
		for {
			time.Sleep(time.Hour)
		}
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(executable, "-test.run=^TestStopAgentReapsUncooperativeCLI$")
	cmd.Env = append(os.Environ(), "STVENA_SHUTDOWN_HELPER=1")
	terminal, err := pty.Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer terminal.Close()
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	defer func() { _ = cmd.Process.Kill(); <-done }()
	ready := make(chan error, 1)
	go func() { _, err := bufio.NewReader(terminal).ReadString('\n'); ready <- err }()
	select {
	case err := <-ready:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("CLI did not start")
	}
	stopped := make(chan struct{})
	go func() { stopAgent(cmd, done); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not escalate after SIGTERM was ignored")
	}
	select {
	case <-done:
	default:
		t.Fatal("shutdown left the CLI running")
	}
	if err := syscall.Kill(cmd.Process.Pid, 0); err != syscall.ESRCH {
		t.Fatalf("CLI was not reaped: %v", err)
	}
	stopAgent(cmd, done) // Deferred cleanup must be safe after explicit shutdown.
}

func TestStopAgentDoesNotWaitForeverForExitNotification(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	reaped := make(chan struct{})
	go func() { _ = cmd.Wait(); close(reaped) }()
	defer func() { _ = cmd.Process.Kill(); <-reaped }()
	// Reproduce a process whose Wait notification never arrives, even after
	// SIGKILL (as observed during Claude's macOS terminal teardown).
	done := make(chan struct{})
	defer close(done)
	stopped := make(chan struct{})
	go func() { stopAgent(cmd, done); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("missing process exit notification froze shutdown")
	}
}
