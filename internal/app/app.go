package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/nccapo/stvena/internal/checks"
	"github.com/nccapo/stvena/internal/session"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
	"github.com/nccapo/stvena/internal/diffview"
	"github.com/nccapo/stvena/internal/review"
	"github.com/nccapo/stvena/internal/ui"
	"golang.org/x/term"
)

const diffRefresh = 700 * time.Millisecond

// Version is replaced with the release version at build time.
var Version = "dev"

type outputEvent struct{ data []byte }
type diffEvent struct{ snapshot, session, project diffview.Snapshot }
type operationEvent struct {
	message string
	err     error
	git     bool
}
type checkEvent struct{ result checks.Result }
type resizeEvent struct{}
type exitEvent struct{ err error }
type shutdownEvent struct{}
type inputEvent struct{ data []byte }
type contentRequest struct {
	id   int
	root string
	file diffview.File
}
type contentEvent struct {
	id      int
	key     string
	content diffview.Content
}

// Run starts the requested agent command. With no arguments it runs Codex.
func Run(args []string) error {
	if len(args) == 1 && args[0] == "--version" {
		fmt.Fprintln(os.Stdout, "stvena "+Version)
		return nil
	}
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Fprintln(os.Stdout, "Usage: stvena [--] [command [args...]]\n       stvena review [--session ID]\n       stvena sessions\n\nDefault command: codex. Ctrl-G switches panes (agent/workspace), preserving the open file; a opens Actions.\nCtrl-Q closes stvena and stops the running command. Ctrl-C interrupts the command.\nReview stays open after the command exits. q closes review.\nRun inside the repository you want to review.")
		return nil
	}
	if len(args) == 1 && args[0] == "sessions" {
		cwd, e := os.Getwd()
		if e != nil {
			return e
		}
		root, e := diffview.GitRoot(cwd)
		if e != nil {
			return e
		}
		sessions, e := session.List(root)
		if e != nil {
			return e
		}
		if len(sessions) == 0 {
			fmt.Fprintln(os.Stdout, "No saved sessions in this repository.")
		}
		for i := range sessions {
			s := sessions[i]
			fmt.Fprintf(os.Stdout, "%s  %s\n", s.ID, s.CreatedAt.Format(time.RFC3339))
		}
		return nil
	}
	standalone := len(args) > 0 && args[0] == "review"
	sessionID := ""
	if standalone {
		if len(args) == 3 && args[1] == "--session" {
			sessionID = args[2]
		} else if len(args) != 1 {
			return errors.New("usage: stvena review [--session ID]")
		}
	}
	if len(args) == 0 {
		args = []string{"codex"}
	}
	if args[0] == "--" {
		args = args[1:]
		if len(args) == 0 {
			return errors.New("missing command after --")
		}
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		return errors.New("stdin and stdout must be attached to a terminal")
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	root, rootErr := diffview.GitRoot(cwd)
	if rootErr != nil {
		root = cwd
	}

	width, height, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		return fmt.Errorf("read terminal size: %w", err)
	}
	layout := ui.NewLayout(width, height)
	var savedSession *session.Session
	var sessionErr error
	if rootErr == nil {
		savedSession, sessionErr = session.Open(root, standalone, sessionID)
	}
	if standalone && sessionID != "" && sessionErr != nil {
		return sessionErr
	}
	if savedSession != nil {
		defer savedSession.Close()
	}
	var command *exec.Cmd
	var commandDone chan struct{}
	var commandErr error
	stopCommand := func() {}
	var ptmx *os.File
	var child io.Writer = io.Discard
	if !standalone {
		command = exec.Command(args[0], args[1:]...)
		command.Env = withTerminalEnv(os.Environ())
		ptmx, err = pty.StartWithSize(command, &pty.Winsize{Cols: uint16(layout.LeftWidth), Rows: uint16(layout.LeftHeight)})
		if err != nil {
			return fmt.Errorf("start %s: %w", args[0], err)
		}
		defer ptmx.Close()
		commandDone = make(chan struct{})
		go func() {
			commandErr = command.Wait()
			close(commandDone)
		}()
		stopCommand = sync.OnceFunc(func() {
			// Claude can remain in process teardown on macOS until the PTY
			// master is closed. Release it before waiting for the CLI.
			_ = ptmx.Close()
			stopAgent(command, commandDone)
		})
		defer stopCommand()
		child = ptmx
	}

	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		if command != nil {
			_ = command.Process.Kill()
		}
		return fmt.Errorf("enable raw terminal: %w", err)
	}
	defer func() {
		_, _ = io.WriteString(os.Stdout, "\x1b[?1000l\x1b[?1002l\x1b[?1006l\x1b[0m\x1b[?25h\x1b[?1049l")
		_ = term.Restore(int(os.Stdin.Fd()), oldState)
	}()
	_, _ = io.WriteString(os.Stdout, "\x1b[?1049h\x1b[2J\x1b[H")

	state := screenState{layout: layout, session: savedSession, root: root, exited: standalone, ratio: 58}
	state.agentInput = child
	if !standalone {
		state.agentName = agentCommand(args[0])
	}
	virtual := vt.NewEmulator(layout.LeftWidth, layout.LeftHeight)
	cursorVisible := true
	virtual.SetCallbacks(vt.Callbacks{
		CursorVisibility: func(visible bool) { cursorVisible = visible },
		EnableMode: func(mode ansi.Mode) {
			if mode == ansi.ModeBracketedPaste {
				state.bracketedPaste = true
			}
		},
		DisableMode: func(mode ansi.Mode) {
			if mode == ansi.ModeBracketedPaste {
				state.bracketedPaste = false
			}
		},
	})
	// Feed device-status/color-query replies back to the child.
	stopReplies := forwardTerminalReplies(virtual, child)
	defer stopReplies()
	events := make(chan any, 32)
	stop := make(chan struct{})

	if ptmx != nil {
		go readPTY(ptmx, events, stop)
	}
	go readInput(events)
	watchDone := make(chan struct{})
	go func() { defer close(watchDone); watchSnapshots(root, rootErr, savedSession, events, stop) }()

	contentRequests := make(chan contentRequest, 1)
	go watchContent(contentRequests, events, stop)
	if command != nil {
		go func() {
			<-commandDone
			select {
			case events <- exitEvent{err: commandErr}:
			case <-stop:
			}
		}()
	}

	resizeSignals := make(chan os.Signal, 1)
	signal.Notify(resizeSignals, syscall.SIGWINCH)
	defer signal.Stop(resizeSignals)
	go func() {
		for {
			select {
			case <-resizeSignals:
				select {
				case events <- resizeEvent{}:
				case <-stop:
					return
				}
			case <-stop:
				return
			}
		}
	}()

	terminate := make(chan os.Signal, 1)
	signal.Notify(terminate, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	defer signal.Stop(terminate)
	go func() {
		select {
		case sig := <-terminate:
			defer func() {
				select {
				case events <- shutdownEvent{}:
				case <-stop:
				}
			}()
			if command != nil {
				_ = command.Process.Signal(sig)
			}
		case <-stop:
		}
	}()

	state.loadPreferences()
	defer state.savePreferences()
	state.review.Source = "workspace"
	state.review.AgentStatus = "Running"
	if savedSession != nil {
		state.review.Source = "session"
	}
	if err := state.review.Load(root); err != nil {
		state.review.Notice = err.Error()
	}
	if sessionErr != nil {
		state.review.Notice = "Session capture unavailable: " + sessionErr.Error()
	}
	if standalone {
		state.diffFocused = true
		state.fullscreen = true
		state.review.Browser = true
		state.review.AgentStatus = "Review"
		state.relayout(width, height)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		close(stop)
		stopCommand()
		<-watchDone
		state.workers.Wait()
	}()
	defer state.review.Save()
	if dir, e := session.RepoDir(root); e == nil {
		if data, e := os.ReadFile(filepath.Join(dir, "check.json")); e == nil {
			var result checks.Result
			if json.Unmarshal(data, &result) == nil {
				state.setCheck(result)
			}
		}
	}

	renderTicker := time.NewTicker(33 * time.Millisecond)
	defer renderTicker.Stop()
	dirty := true
	mouseEnabled := false
	lastWelcomeFrame := time.Now()
	for {
		select {
		case event := <-events:
			switch value := event.(type) {
			case shutdownEvent:
				return nil
			case outputEvent:
				_, _ = virtual.Write(value.data)
				dirty = true
			case diffEvent:
				state.workspace = value.snapshot
				state.workspace.Label = "Workspace"
				state.sessionView = value.session
				state.projectView = value.project
				state.review.LiveTree = value.project.Tree
				if state.review.LiveTree == "" {
					state.review.LiveTree = value.snapshot.Tree
				}
				state.updateSource()
				state.clampScroll()
				state.queueContent(contentRequests)
				dirty = true
			case contentEvent:
				if value.id == state.contentID {
					state.review.Content, state.review.ContentKey = value.content, value.key
					state.review.ContentLoading = false
					if n := state.review.TargetLine; n > 0 {
						state.review.TargetLine = 0
						state.review.GoToLine(n)
					}
					state.clampScroll()
					dirty = true
				}
			case inputEvent:
				state.handleInput(value.data, child)
				if state.dispatch(ctx, events, stop) {
					return nil
				}
				state.resizePTY(virtual, ptmx)
				state.queueContent(contentRequests)
				dirty = true
			case resizeEvent:
				state.mouseDragging = false
				newWidth, newHeight, sizeErr := term.GetSize(int(os.Stdout.Fd()))
				if sizeErr == nil {
					state.relayout(newWidth, newHeight)
					state.resizePTY(virtual, ptmx)
					state.clampScroll()
					dirty = true
				}
			case agentPasteEvent:
				state.finishAgentPaste(value.err)
				state.resizePTY(virtual, ptmx)
				if value.err == nil && !state.exited {
					queued := state.pasteInput
					state.pasteInput = nil
					state.handleInput(queued, child)
					if state.dispatch(ctx, events, stop) {
						return nil
					}
					state.resizePTY(virtual, ptmx)
					state.queueContent(contentRequests)
				}
				dirty = true
			case operationEvent:
				state.review.Notice = value.message
				if value.git {
					state.busy = false
				}
				if value.err != nil {
					state.review.Notice = value.err.Error()
				}
				dirty = true
			case problemEvent:
				state.applyProblem(value)
				state.queueContent(contentRequests)
				dirty = true
			case checkEvent:
				state.review.CheckRunning = false
				state.setCheck(value.result)
				if dir, e := session.RepoDir(root); e == nil {
					if e = session.AtomicJSON(filepath.Join(dir, "check.json"), value.result); e != nil {
						state.review.Notice = e.Error()
					}
				}
				dirty = true
			case exitEvent:
				state.exited = true
				state.diffFocused = true
				state.review.Browser = true
				state.review.AgentStatus = "Finished"
				if value.err != nil {
					state.review.AgentStatus = "Exited: " + value.err.Error()
				}
				state.review.Notice = "Agent finished · review your changes · q exits"
				dirty = true

			}
		case <-renderTicker.C:
			if ui.WelcomeVisible(&state.review) && time.Since(lastWelcomeFrame) >= 250*time.Millisecond {
				state.review.WelcomeFrame = (state.review.WelcomeFrame + 1) % 6
				lastWelcomeFrame = time.Now()
				dirty = true
			}
			if len(state.pending) > 0 && time.Since(state.lastInput) > 50*time.Millisecond {
				state.decodeInput(true)
				if state.dispatch(ctx, events, stop) {
					return nil
				}
				state.resizePTY(virtual, ptmx)
				state.queueContent(contentRequests)
				dirty = true
			}
			if dirty {
				if mouseEnabled != state.diffFocused {
					mouseEnabled = state.diffFocused
					if mouseEnabled {
						fmt.Fprint(os.Stdout, "\x1b[?1002h\x1b[?1006h")
					} else {
						fmt.Fprint(os.Stdout, "\x1b[?1000l\x1b[?1002l\x1b[?1006l")
					}
				}
				ui.Render(os.Stdout, virtual, &state.review, state.layout, state.diffFocused, cursorVisible)
				dirty = false
			}
		}
	}
}

type screenState struct {
	layout                              ui.Layout
	review                              review.State
	diffFocused                         bool
	pending                             []byte
	lastInput                           time.Time
	contentID                           int
	problemID                           int
	contentStamp                        string
	root                                string
	session                             *session.Session
	workspace, sessionView, projectView diffview.Snapshot
	fullscreen, exited, busy            bool
	ratio                               int
	pendingStage                        *diffview.File
	pendingHunk                         int
	workers                             sync.WaitGroup
	agentName                           string
	agentInput                          io.Writer
	bracketedPaste, pastePending        bool
	pasteInput                          []byte
	mouseDragging                       bool
}

func (s *screenState) visibleLines() int {
	s.review.ViewWidth = s.layout.DiffWidth
	if s.review.Browser {
		return max(1, s.layout.DiffHeight-3)
	}
	_, lines := ui.ReviewSize(s.layout.DiffHeight, s.review.FileListCount())
	return lines
}
func (s *screenState) handleInput(data []byte, child io.Writer) {
	// Closing the app must also work while a paste is waiting for the CLI.
	for _, b := range data {
		if b == 0x11 {
			s.review.Request = "quit-app"
			s.pending, s.pasteInput = nil, nil
			return
		}
	}
	if s.pastePending {
		s.pasteInput = append(s.pasteInput, data...)
		return
	}
	for i, b := range data {
		if b == 0x07 { // Switch focus without discarding the open file or selection.
			s.pending = nil
			s.mouseDragging = false
			s.diffFocused = !s.diffFocused
			if s.exited {
				s.diffFocused = true
			}
			if !s.diffFocused && s.fullscreen {
				s.fullscreen = false
				s.relayout(s.layout.Width, s.layout.Height)
			}
			if s.diffFocused && !s.review.PatchFocused {
				s.review.Browser = true
			}
			continue
		}
		if !s.diffFocused {
			if b == '\r' || b == '\n' {
				if s.review.AgentDraft {
					s.review.ClearSelection()
				}
				s.review.AgentDraft = false
			}
			_, _ = child.Write([]byte{b})
			continue
		}
		s.pending = append(s.pending, b)
		s.lastInput = time.Now()
		s.decodeInput(false)
		if s.review.Request == "paste-agent" || s.review.Request == "paste-context" {
			s.pasteInput = append(s.pasteInput, data[i+1:]...)
			return
		}
	}
}

func (s *screenState) decodeInput(flush bool) {
	for len(s.pending) > 0 {
		key, consumed := "", 1
		switch s.pending[0] {
		case 0x1b:
			if len(s.pending) == 1 && !flush {
				return
			}
			key = "esc"
			if len(s.pending) > 1 && (s.pending[1] == '[' || s.pending[1] == 'O') {
				end := 2
				for end < len(s.pending) && !(s.pending[end] >= 0x40 && s.pending[end] <= 0x7e) {
					end++
				}
				if end == len(s.pending) {
					if !flush {
						return
					}
					s.pending = nil
					return
				}
				consumed = end + 1
				sequence := string(s.pending[2:consumed])
				if strings.HasPrefix(sequence, "<") {
					s.mouse(sequence)
					key = ""
				}
				key = map[string]string{"A": "up", "B": "down", "C": "right", "D": "left", "H": "home", "F": "end", "3~": "delete", "5~": "pageup", "6~": "pagedown", "1~": "home", "4~": "end"}[sequence]
			}
		case '\r', '\n':
			key = "enter"
		case '\t':
			key = "tab"
		case 0x7f, 0x08:
			key = "backspace"
		case 0x04:
			key = "pagedown"
		case 0x15:
			key = "pageup"
		default:
			if !utf8.FullRune(s.pending) {
				if !flush {
					return
				}
				s.pending = nil
				return
			}
			r, n := utf8.DecodeRune(s.pending)
			consumed = n
			key = string(r)
		}
		s.pending = s.pending[consumed:]
		if key != "" {
			s.mouseDragging = false
			s.review.Key(key, s.visibleLines())
		}
	}
	s.clampScroll()
}

func (s *screenState) clampScroll() { s.review.Clamp(s.visibleLines()) }

// The event loop owns the emulator: parsing, resizing, and rendering cannot race.
func readPTY(ptmx *os.File, events chan<- any, stop <-chan struct{}) {
	buffer := make([]byte, 32<<10)
	for {
		n, err := ptmx.Read(buffer)
		if n > 0 {
			select {
			case events <- outputEvent{data: append([]byte(nil), buffer[:n]...)}:
			case <-stop:
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func readInput(events chan<- any) {
	buffer := make([]byte, 256)
	for {
		n, err := os.Stdin.Read(buffer)
		if n > 0 {
			data := append([]byte(nil), buffer[:n]...)
			events <- inputEvent{data: data}
		}
		if err != nil {
			return
		}
	}
}

func watchDiff(root string, rootErr error, events chan<- any, stop <-chan struct{}) {
	refresh := func() {
		if rootErr != nil {
			if discovered, err := diffview.GitRoot(root); err == nil {
				root, rootErr = discovered, nil
			} else {
				rootErr = err
			}
		}
		if rootErr != nil {
			select {
			case events <- diffEvent{snapshot: diffview.Snapshot{Err: rootErr, UpdatedAt: time.Now()}}:
			case <-stop:
			}
			return
		}
		snapshot := diffview.Collect(root)
		select {
		case events <- diffEvent{snapshot: snapshot}:
		case <-stop:
		}
	}
	refresh()
	ticker := time.NewTicker(diffRefresh)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			refresh()
		case <-stop:
			return
		}
	}
}

func withTerminalEnv(env []string) []string {
	result := make([]string, 0, len(env)+2)
	for _, item := range env {
		if !strings.HasPrefix(item, "TERM=") && !strings.HasPrefix(item, "COLORTERM=") {
			result = append(result, item)
		}
	}
	return append(result, "TERM=xterm-256color", "COLORTERM=truecolor")
}

// Only the selected full file is loaded, off the input/render loop. A newer
// selection or refresh supersedes queued work and stale results are ignored.
func (s *screenState) queueContent(requests chan contentRequest) {
	f := s.review.Current()
	if f == nil || !s.review.FullFile || s.review.Source == "project" && s.review.Browser {
		return
	}

	stamp := s.review.Snapshot.Root + "\x00" + f.Key() + diffview.Revision(*f) + f.ContentRef
	if stamp == s.contentStamp {
		if s.review.TargetLine > 0 && !s.review.ContentLoading {
			n := s.review.TargetLine
			s.review.TargetLine = 0
			s.review.GoToLine(n)
			s.clampScroll()
		}
		return
	}
	s.contentStamp = stamp
	s.contentID++
	if s.review.ContentKey != f.Key() {
		s.review.Content = diffview.Content{}
	}
	s.review.ContentKey, s.review.ContentLoading = f.Key(), true
	request := contentRequest{id: s.contentID, root: s.review.Snapshot.Root, file: *f}
	select {
	case <-requests:
	default:
	}
	requests <- request
}

func watchContent(requests <-chan contentRequest, events chan<- any, stop <-chan struct{}) {
	for {
		select {
		case request := <-requests:
			content := diffview.LoadContent(request.root, request.file)
			select {
			case events <- contentEvent{id: request.id, key: request.file.Key(), content: content}:
			case <-stop:
				return
			}
		case <-stop:
			return
		}
	}
}

func forwardTerminalReplies(virtual *vt.Emulator, child io.Writer) func() {
	done := make(chan struct{})
	go func() { defer close(done); _, _ = io.Copy(child, virtual) }()
	return func() {
		// Closing the response pipe releases Read without racing the emulator's
		// unsynchronized closed flag. Close the emulator after the reader exits.
		_ = virtual.InputPipe().(io.Closer).Close()
		<-done
		_ = virtual.Close()
	}
}
