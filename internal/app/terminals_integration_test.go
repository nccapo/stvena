package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"encoding/json"
	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
	"github.com/nccapo/stvena/internal/attention"
	"golang.org/x/term"
)

// Exercise the actual event loop, PTYs, input routing, rendering and shutdown
// without requiring an installed agent or sending a real prompt.
func TestAgentTerminalsEndToEnd(t *testing.T) {
	const helper = "STVENA_TERMINALS_TEST"
	if os.Getenv(helper) == "1" {
		kind := os.Args[len(os.Args)-1]
		if kind == "agent" || kind == "codex" || kind == "claude" {
			if _, err := term.MakeRaw(int(os.Stdin.Fd())); err != nil {
				os.Exit(2)
			}
			fmt.Printf("\x1b[?2004hkind:%s\r\nready:%d\r\n", kind, os.Getpid())
			buffer := make([]byte, 1024)
			for {
				n, err := os.Stdin.Read(buffer)
				if err != nil || strings.ContainsRune(string(buffer[:n]), '\x03') {
					os.Exit(0)
				}
				if string(buffer[:n]) == "attention-edit" {
					path := fmt.Sprintf("%s-%d.txt", kind, os.Getpid())
					record := func(event string) {
						data, _ := json.Marshal(map[string]any{"hook_event_name": event, "tool_name": "Write", "tool_use_id": "edit-1", "tool_input": map[string]string{"file_path": path}})
						if err := attention.RecordHook(strings.NewReader(string(data))); err != nil {
							fmt.Println(err)
							os.Exit(2)
						}
					}
					record("PreToolUse")
					if err := os.WriteFile(path, []byte("agent-created change\n"), 0600); err != nil {
						os.Exit(2)
					}
					record("PostToolUse")
					record("Stop")
				}
				fmt.Printf("input:%d:%s\r\n", os.Getpid(), buffer[:n])
			}
		}
		preferencesConfigDir = func() (string, error) { return os.Getenv("STVENA_TEST_CONFIG"), nil }
		err := Run([]string{os.Args[0], "-test.run=^TestAgentTerminalsEndToEnd$", "--", "agent"})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		os.Exit(0)
	}
	for _, width := range []int{80, 140} {
		t.Run(fmt.Sprintf("width-%d", width), func(t *testing.T) {
			exerciseAgentTerminals(t, width, helper)
		})
	}
}

func exerciseAgentTerminals(t *testing.T, width int, helper string) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(executable, "-test.run=^TestAgentTerminalsEndToEnd$")
	agentBin := t.TempDir()
	for _, name := range []string{"codex", "claude"} {
		script := "#!/bin/sh\nexec '" + strings.ReplaceAll(executable, "'", "'\"'\"'") + "' -test.run='^TestAgentTerminalsEndToEnd$' -- " + name + "\n"
		if err := os.WriteFile(filepath.Join(agentBin, name), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	command.Env = append(os.Environ(), helper+"=1", "STVENA_TEST_CONFIG="+t.TempDir(), "PATH="+agentBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	command.Dir = t.TempDir()
	if output, err := exec.Command("git", "init", "--quiet", command.Dir).CombinedOutput(); err != nil {
		t.Fatalf("init test repository: %v: %s", err, output)
	}
	ptmx, err := pty.StartWithSize(command, &pty.Winsize{Cols: uint16(width), Rows: 40})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var commandErr error
	go func() { commandErr = command.Wait(); close(done) }()
	stop := make(chan struct{})
	t.Cleanup(func() {
		close(stop)
		_ = ptmx.Close()
		stopAgent(command, done)
	})
	chunks := make(chan []byte, 32)
	go func() {
		defer close(chunks)
		buf := make([]byte, 32<<10)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				select {
				case chunks <- append([]byte(nil), buf[:n]...):
				case <-stop:
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	display := vt.NewEmulator(width, 40)
	defer display.Close()
	screen := func() string {
		var b strings.Builder
		for y := 0; y < display.Height(); y++ {
			for x := 0; x < display.Width(); x++ {
				if c := display.CellAt(x, y); c != nil {
					b.WriteString(c.Content)
				}
			}
			b.WriteByte('\n')
		}
		return b.String()
	}
	waitFor := func(want string) string {
		t.Helper()
		timer := time.NewTimer(10 * time.Second)
		defer timer.Stop()
		for {
			select {
			case data, ok := <-chunks:
				if !ok {
					t.Fatalf("app exited waiting for %q; screen:\n%s", want, screen())
				}
				_, _ = display.Write(data)
				if text := screen(); strings.Contains(text, want) {
					return text
				}
			case <-timer.C:
				t.Fatalf("timed out waiting for %q; screen:\n%s", want, screen())
			}
		}
	}
	send := func(text string) {
		t.Helper()
		if width == 140 {
			text = strings.NewReplacer("\x07", "\x1b[103;9u", "\x1d", "\x1b[93;9u", "\x0e", "\x1b[110;9u", "\x10", "\x1b[112;9u", "\x17", "\x1b[119;9u", "\x11", "\x1b[113;9u", "\x19", "\x1b[121;9u").Replace(text)
		}
		if _, err := ptmx.WriteString(text); err != nil {
			t.Fatal(err)
		}
	}
	ready := regexp.MustCompile(`ready:(\d+)`)
	first := ready.FindStringSubmatch(waitFor("ready:"))[1]
	// Startup offers Configuration without needing Ctrl-G or a function key.
	if !strings.Contains(screen(), "BOTTOM PANEL") {
		t.Fatal("startup did not focus the bottom panel")
	}
	// Escape lets users start typing immediately; the other width checks the
	// direct startup route to Configuration.
	if width == 80 {
		send("\x1b")
		send("startup-input")
		waitFor("input:" + first + ":startup-input")
		send("\x1b[17~")
		waitFor("BOTTOM PANEL")
	}
	send("\x1b[D\x1b[C\r")
	waitFor("CONFIGURATION")
	send("\r\x0f")
	waitFor("Hotkeys saved for all projects")
	send("?\x0f")
	send("\x07released")
	waitFor("input:" + first + ":released")
	send("first")
	waitFor("input:" + first + ":first")
	send("\x1d")
	waitFor("New agent")
	send("l")
	waitFor("[claude 1 ")
	text := screen()
	if !strings.Contains(text, "ready:") {
		text = waitFor("ready:")
	}
	second := ready.FindStringSubmatch(text)[1]
	if !strings.Contains(text, "kind:claude") {
		t.Fatal("chooser did not launch Claude")
	}
	if first == second {
		t.Fatal("Ctrl-] did not start an independent process")
	}
	send("second")
	waitFor("input:" + second + ":second")
	send("\x10")
	text = waitFor("[" + filepath.Base(executable) + " 1 ")
	if !strings.Contains(text, "input:"+first+":first") || strings.Contains(text, "input:"+second) {
		t.Fatal("switch did not restore the first terminal's screen")
	}
	// Interrupting one command must leave the other usable, and q must not
	// accidentally stop it while viewing the finished command.
	send("\x03")
	waitFor("✓ DONE")
	send("\x0f")
	send("q")
	waitFor("An agent is still running")
	send("\x0e")
	waitFor("[claude 1 ")
	send("alive")
	waitFor("input:" + second + ":alive")
	send("\x1dc")
	waitFor("[codex 1 ")
	text = screen()
	if !strings.Contains(text, "ready:") {
		text = waitFor("ready:")
	}
	third := ready.FindStringSubmatch(text)[1]
	if !strings.Contains(text, "kind:codex") {
		t.Fatal("chooser did not launch Codex")
	}
	// Codex creates changes while Claude is selected. No manual inspection of
	// the Codex pane should be needed to discover or acknowledge its review.
	send("attention-edit\x10")
	waitFor("! REVIEW 1f")
	if !strings.Contains(screen(), "[claude 1 ") {
		t.Fatal("background edit stole focus")
	}
	send("\x19")
	waitFor("agent-created change")
	if !strings.Contains(screen(), "codex 1 ! REVIEW 1f") {
		t.Fatal("next attention missed Codex")
	}
	send(" ")
	waitFor("Review:0")
	send("\x17")
	waitFor("[claude 1 ")
	var closedPID int
	_, _ = fmt.Sscan(third, &closedPID)
	if err := syscall.Kill(closedPID, 0); err != syscall.ESRCH {
		t.Fatalf("closed agent %d left running: %v", closedPID, err)
	}
	send("survived")
	waitFor("input:" + second + ":survived")
	// Remove the finished terminal, then the last running terminal.
	send("\x10\x17")
	waitFor("[claude 1 ")
	send("\x17")
	waitFor("q exits")
	send("\x1d\r")
	waitFor("[" + filepath.Base(executable) + " 2 ")
	text = screen()
	if !strings.Contains(text, "ready:") {
		text = waitFor("ready:")
	}
	fourth := ready.FindStringSubmatch(text)[1]
	send("\x11")
	select {
	case <-done:
		if commandErr != nil {
			t.Fatal(commandErr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Ctrl-Q did not stop stvena")
	}
	for _, value := range []string{first, second, third, fourth} {
		var pid int
		_, _ = fmt.Sscan(value, &pid)
		if err := syscall.Kill(pid, 0); err != syscall.ESRCH {
			t.Errorf("agent %d left running after quit: %v", pid, err)
		}
	}
}
