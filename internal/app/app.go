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
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
	"github.com/nccapo/stvena/internal/diffview"
	"github.com/nccapo/stvena/internal/editor"
	"github.com/nccapo/stvena/internal/review"
	"github.com/nccapo/stvena/internal/ui"
	"golang.org/x/term"
)

const diffRefresh = 700 * time.Millisecond

// Version is replaced with the release version at build time.
var Version = "dev"

type outputEvent struct {
	agent *agentTerminal
	data  []byte
}
type diffEvent struct{ snapshot, session, project diffview.Snapshot }
type operationEvent struct {
	message string
	err     error
	git     bool
}
type checkEvent struct{ result checks.Result }
type resizeEvent struct{}
type exitEvent struct {
	agent *agentTerminal
	err   error
}
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
	if len(args) == 1 && args[0] == "editor-hook" {
		// Observers must never block an agent operation on a display failure.
		_ = editor.RecordHook(os.Stdin)
		return nil
	}
	if len(args) == 1 && args[0] == "--version" {
		fmt.Fprintln(os.Stdout, "stvena "+Version)
		return nil
	}
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Fprintln(os.Stdout, "Usage: stvena [--] [command [args...]]\n       stvena review [--session ID]\n       stvena sessions\n\nDefault command: codex. The bottom panel starts focused on Configuration.\nArrows select, Enter activates, Esc returns to the agent or review. F6 can refocus the panel.\nSelect Configuration to change global or review shortcuts for all projects.\nCtrl-G switches panes (agent/workspace), preserving the open file; a opens Actions.\nCtrl-] chooses Codex, Claude Code, or the launch command for a new terminal. Ctrl-N / Ctrl-P switch agent terminals.\nCtrl-W closes the current agent terminal and stops its command.\nCtrl-Q closes stvena and stops all running commands. Ctrl-C interrupts the command.\nReview stays open after commands exit. q closes review once all agents finish.\nRun inside the repository you want to review.")
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
	events := make(chan any, 32)
	stop := make(chan struct{})
	stopEvents := sync.OnceFunc(func() { close(stop) })
	defer stopEvents()
	state := screenState{layout: layout, session: savedSession, root: root, exited: standalone, ratio: 58, agentInput: io.Discard}
	defer state.closeAgents()
	if !standalone {
		state.launchCommand = append([]string(nil), args...)
		state.startAgent = func(args []string, layout ui.Layout) (*agentTerminal, error) {
			var env []string
			if savedSession != nil {
				if executable, err := os.Executable(); err == nil {
					env = []string{"STVENA_ACTIVITY_PATH=" + editor.ActivityPath(savedSession), "STVENA_ROOT=" + root,
						"STVENA_SESSION=" + savedSession.ID, "STVENA_AGENT=" + filepath.Base(args[0])}
					args = editor.HookArgs(args, executable)
				}
			}
			return startAgentTerminal(args, cwd, layout, events, stop, env...)
		}
		a, err := state.startAgent(args, layout)
		if err != nil {
			return err
		}
		state.agents = append(state.agents, a)
		state.syncAgent()
	}

	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return fmt.Errorf("enable raw terminal: %w", err)
	}
	defer func() {
		_, _ = io.WriteString(os.Stdout, ansi.ResetModeBracketedPaste+"\x1b[?1000l\x1b[?1002l\x1b[?1006l\x1b[0m\x1b[?25h\x1b[?1049l")
		_ = term.Restore(int(os.Stdin.Fd()), oldState)
	}()
	_, _ = io.WriteString(os.Stdout, "\x1b[?1049h\x1b[2J\x1b[H")

	// Standalone review uses an empty terminal behind its fullscreen workspace.
	blank := vt.NewEmulator(layout.LeftWidth, layout.LeftHeight)
	defer blank.Close()
	go readInput(events)
	watchDone := make(chan struct{})
	go func() { defer close(watchDone); watchSnapshots(root, rootErr, savedSession, events, stop) }()

	contentRequests := make(chan contentRequest, 1)
	go watchContent(contentRequests, events, stop)

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
		case <-terminate:
			defer func() {
				select {
				case events <- shutdownEvent{}:
				case <-stop:
				}
			}()
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
	// Start on a usable control before any potentially conflicting shortcut is
	// needed. Esc gives the agent (or standalone review) normal keyboard input.
	state.review.FooterFocused = true
	for i, control := range ui.FooterControls {
		if control.Action == "?" {
			state.review.FooterIndex = i
			break
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		stopEvents()
		// Closing PTYs releases pending handoffs before waiting for workers.
		state.closeAgents()
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
	pasteEnabled := false
	lastWelcomeFrame := time.Now()
	for {
		select {
		case event := <-events:
			switch value := event.(type) {
			case shutdownEvent:
				return nil
			case outputEvent:
				if value.agent.closed {
					continue
				}
				_, _ = value.agent.virtual.Write(value.data)
				state.syncAgent()
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
				state.handleInput(value.data, state.agentInput)
				if state.dispatch(ctx, events, stop) {
					return nil
				}
				state.resizeAgents()
				state.queueContent(contentRequests)
				dirty = true
			case resizeEvent:
				state.mouseDragging = false
				newWidth, newHeight, sizeErr := term.GetSize(int(os.Stdout.Fd()))
				if sizeErr == nil {
					state.relayout(newWidth, newHeight)
					state.resizeAgents()
					state.clampScroll()
					dirty = true
				}
			case agentPasteEvent:
				state.finishAgentPaste(value.err)
				state.resizeAgents()
				if value.err == nil && !state.exited {
					queued := state.pasteInput
					state.pasteInput = nil
					state.handleInput(queued, state.agentInput)
					if state.dispatch(ctx, events, stop) {
						return nil
					}
					state.resizeAgents()
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
				state.agentExited(value.agent, value.err)
				dirty = true

			}
		case <-renderTicker.C:
			if ui.WelcomeVisible(&state.review) && time.Since(lastWelcomeFrame) >= 250*time.Millisecond {
				state.review.WelcomeFrame = (state.review.WelcomeFrame + 1) % 6
				lastWelcomeFrame = time.Now()
				dirty = true
			}
			if (len(state.pending) > 0 || len(state.keyboardPending) > 0) && time.Since(state.lastInput) > 50*time.Millisecond {
				state.decodeInput(true)
				if state.dispatch(ctx, events, stop) {
					return nil
				}
				state.resizeAgents()
				state.queueContent(contentRequests)
				dirty = true
			}
			if dirty {
				if wantMouse := state.diffFocused || state.review.FooterFocused || state.agentPicker != nil; mouseEnabled != wantMouse {
					mouseEnabled = wantMouse
					if mouseEnabled {
						fmt.Fprint(os.Stdout, "\x1b[?1002h\x1b[?1006h")
					} else {
						fmt.Fprint(os.Stdout, "\x1b[?1000l\x1b[?1002l\x1b[?1006l")
					}
				}
				if wantPaste := state.bracketedPaste || state.agentPicker != nil; pasteEnabled != wantPaste {
					pasteEnabled = wantPaste
					mode := ansi.ResetModeBracketedPaste
					if pasteEnabled {
						mode = ansi.SetModeBracketedPaste
					}
					_, _ = io.WriteString(os.Stdout, mode)
				}
				virtual, cursorVisible := blank, false
				if a := state.activeAgent(); a != nil {
					virtual, cursorVisible = a.virtual, a.cursorVisible && !a.exited
				}
				ui.Render(os.Stdout, virtual, &state.review, state.layout, state.diffFocused, cursorVisible, state.agentPicker)
				dirty = false
			}
		}
	}
}

type screenState struct {
	agents                              []*agentTerminal
	activeAgentIndex                    int
	startAgent                          func([]string, ui.Layout) (*agentTerminal, error)
	launchCommand                       []string
	agentPicker                         *ui.AgentPicker
	layout                              ui.Layout
	review                              review.State
	diffFocused                         bool
	keyboardPending                     []byte
	keyboardPasting                     bool
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
	terminalPasting                     bool
	terminalPasteToAgent                bool
	terminalPasteMarker                 int
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
func (s *screenState) handleLegacyInput(data []byte, child io.Writer) {
	if s.pastePending {
		// Closing the app must also work while a handoff waits for the CLI.
		for _, b := range data {
			if s.review.GlobalAction(review.ControlKey(b)) == "Ctrl-Q" {
				s.review.Request = "quit-app"
				s.pending, s.pasteInput = nil, nil
				return
			}
		}
		s.pasteInput = append(s.pasteInput, data...)
		return
	}
	// Preserve input chunks: byte-sized PTY writes make large pastes look like
	// prolonged typing and force the child to redraw for individual characters.
	forward := make([]byte, 0, len(data))
	defer func() {
		if len(forward) > 0 {
			_, _ = child.Write(forward)
		}
	}()
	for i, b := range data {
		// Markers can straddle any stdin read. Once a paste starts, its bytes
		// belong to the original pane, including newlines and app shortcuts.
		wasPasting := s.terminalPasting
		marker := ansi.BracketedPasteStart
		if wasPasting {
			marker = ansi.BracketedPasteEnd
		}
		if b == marker[s.terminalPasteMarker] {
			s.terminalPasteMarker++
		} else {
			s.terminalPasteMarker = 0
			if b == marker[0] {
				s.terminalPasteMarker = 1
			}
		}
		if s.terminalPasteMarker == len(marker) {
			s.terminalPasteMarker = 0
			s.terminalPasting = !wasPasting
			if !wasPasting {
				s.terminalPasteToAgent = !s.diffFocused && s.agentPicker == nil
				s.pending = nil
			}
		}
		if wasPasting || s.terminalPasting {
			if s.terminalPasteToAgent {
				forward = append(forward, b)
			}
			continue
		}
		// A plain Escape leaves the footer before the following text is routed.
		// Keep that text in this input batch so rapid Esc + typing loses no key.
		if s.review.FooterFocused && len(s.pending) == 1 && s.pending[0] == 0x1b && b != '[' && b != 'O' {
			s.pending = nil
			s.footerKey("esc")
		}
		inputKey := review.ControlKey(b)
		if s.review.EditingGlobalHotkey() && inputKey != "" && b != '\r' && b != '\n' && b != '\t' && b != 0x08 {
			s.review.InputKey(inputKey, s.visibleLines())
			continue
		}
		action := s.review.GlobalAction(inputKey)
		if action == "Ctrl-Q" {
			s.review.Request = "quit-app"
			s.pending, s.pasteInput = nil, nil
			return
		}
		if s.agentPicker != nil {
			s.pending = append(s.pending, b)
			s.lastInput = time.Now()
			s.decodeInput(false)
			child = s.agentInput
			continue
		}
		if action == "Ctrl-]" || action == "Ctrl-N" || action == "Ctrl-P" || action == "Ctrl-W" {
			// Flush preceding text to its original terminal before switching.
			if len(forward) > 0 {
				_, _ = child.Write(forward)
				forward = forward[:0]
			}
			s.agentShortcut(review.ControlByte(action))
			if s.agentInput != nil {
				child = s.agentInput
			}
			continue
		}
		if action == "Ctrl-G" {
			s.switchPane()
			continue
		}
		if !s.diffFocused && !s.review.FooterFocused {
			if b == '\r' || b == '\n' {
				if s.review.AgentDraft {
					s.review.ClearSelection()
				}
				s.review.AgentDraft = false
			}
			forward = append(forward, b)
			continue
		}
		if len(forward) > 0 {
			_, _ = child.Write(forward)
			forward = forward[:0]
		}
		s.pending = append(s.pending, b)
		s.lastInput = time.Now()
		s.decodeInput(false)
		if s.agentInput != nil {
			child = s.agentInput
		}
		if s.review.Request == "paste-agent" || s.review.Request == "paste-context" || s.review.Request == "paste-checkpoint" {
			s.pasteInput = append(s.pasteInput, data[i+1:]...)
			return
		}
	}
}

func (s *screenState) decodeInput(flush bool) {
	if flush {
		s.flushKeyboardInput()
	}
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
					s.pending = s.pending[consumed:]
					s.mouse(sequence)
					continue
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
			if s.review.FooterFocused {
				s.footerKey(key)
			} else if s.agentPicker != nil {
				s.agentPickerKey(key)
			} else if key == "down" && s.atFileListEnd() {
				s.review.FooterFocused, s.review.FooterIndex = true, 0
			} else {
				s.review.InputKey(key, s.visibleLines())
			}
		}
	}
	s.clampScroll()
}

func (s *screenState) clampScroll() { s.review.Clamp(s.visibleLines()) }

// The event loop owns the emulator: parsing, resizing, and rendering cannot race.
func readPTY(agent *agentTerminal, events chan<- any, stop <-chan struct{}) {
	buffer := make([]byte, 32<<10)
	for {
		n, err := agent.ptmx.Read(buffer)
		if n > 0 {
			select {
			case events <- outputEvent{agent: agent, data: append([]byte(nil), buffer[:n]...)}:
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
	buffer := make([]byte, 4096)
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
