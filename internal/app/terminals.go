package app

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
	"github.com/nccapo/stvena/internal/ui"
)

// Each command owns its screen and PTY. Only the event loop touches screen state.
type agentTerminal struct {
	name                          string
	virtual                       *vt.Emulator
	ptmx                          *os.File
	input                         io.Writer
	cursorVisible, bracketedPaste bool
	exited, draft, closed         bool
	status                        string
	close                         func()
}

func newAgentTerminal(name string, width, height int) *agentTerminal {
	a := &agentTerminal{name: name, virtual: vt.NewEmulator(width, height), cursorVisible: true, status: "Running"}
	// PTY output queued during resize can still specify the old scroll margins.
	// Clamp before vt's default handlers store them and scroll outside the buffer.
	// Remove when our vt version includes https://github.com/charmbracelet/x/pull/908.
	a.virtual.RegisterCsiHandler('r', func(params ansi.Params) bool {
		clampScrollMargin(params, a.virtual.Height())
		return false // Let vt apply the margins and update the cursor normally.
	})
	a.virtual.RegisterCsiHandler('s', func(params ansi.Params) bool {
		clampScrollMargin(params, a.virtual.Width())
		return false // Also preserves CSI s cursor saving when margin mode is off.
	})
	a.virtual.SetCallbacks(vt.Callbacks{
		CursorVisibility: func(visible bool) { a.cursorVisible = visible },
		EnableMode: func(mode ansi.Mode) {
			if mode == ansi.ModeBracketedPaste {
				a.bracketedPaste = true
			}
		},
		DisableMode: func(mode ansi.Mode) {
			if mode == ansi.ModeBracketedPaste {
				a.bracketedPaste = false
			}
		},
	})
	return a
}

func clampScrollMargin(params ansi.Params, limit int) {
	// Params shares the parser's storage, including the bottom margin that vt
	// reads directly from its parser. Leave missing/zero defaults untouched.
	if value, more, ok := params.Param(1, limit); ok && value > limit {
		params[1] = ansi.Param(ansi.Parameter(limit, more))
	}
}

func startAgentTerminal(args []string, cwd string, layout ui.Layout, events chan<- any, stop <-chan struct{}, extraEnv ...string) (*agentTerminal, error) {
	command := exec.Command(args[0], args[1:]...)
	command.Dir = cwd
	command.Env = append(withTerminalEnv(os.Environ()), extraEnv...)
	ptmx, err := pty.StartWithSize(command, &pty.Winsize{Cols: uint16(layout.LeftWidth), Rows: uint16(layout.LeftHeight)})
	if err != nil {
		return nil, fmt.Errorf("start %s: %w", args[0], err)
	}
	a := newAgentTerminal(filepath.Base(args[0]), layout.LeftWidth, layout.LeftHeight)
	a.ptmx, a.input = ptmx, ptmx
	stopReplies := forwardTerminalReplies(a.virtual, ptmx)
	done := make(chan struct{})
	go func() {
		err := command.Wait()
		close(done)
		select {
		case events <- exitEvent{agent: a, err: err}:
		case <-stop:
		}
	}()
	go readPTY(a, events, stop)
	a.close = sync.OnceFunc(func() {
		// Release a blocked PTY write before waiting for process teardown.
		_ = ptmx.Close()
		stopAgent(command, done)
		stopReplies()
	})
	return a, nil
}

func (s *screenState) activeAgent() *agentTerminal {
	if len(s.agents) == 0 {
		return nil
	}
	return s.agents[s.activeAgentIndex]
}

func (s *screenState) syncAgent() {
	if a := s.activeAgent(); a != nil {
		s.agentInput, s.agentName = a.input, agentCommand(a.name)
		s.exited, s.bracketedPaste = a.exited, a.bracketedPaste
		s.review.AgentStatus = a.status
		s.review.AgentLabel = fmt.Sprintf("%s %d/%d", a.name, s.activeAgentIndex+1, len(s.agents))
	} else {
		s.agentInput, s.agentName = io.Discard, ""
		s.exited, s.bracketedPaste = true, false
		s.review.AgentStatus, s.review.AgentLabel = "Review", ""
		s.review.AgentDraft = false
	}
}

func (s *screenState) selectAgent(index int) {
	if a := s.activeAgent(); a != nil {
		a.draft = s.review.AgentDraft
	}
	s.activeAgentIndex = index
	s.syncAgent()
	if a := s.activeAgent(); a != nil {
		s.review.AgentDraft = a.draft
	}
	s.pending = nil
	s.mouseDragging = false
	s.diffFocused = s.exited
	s.fullscreen = false
	s.relayout(s.layout.Width, s.layout.Height)
}

func (s *screenState) agentShortcut(key byte) {
	if s.startAgent == nil {
		s.review.Notice = "Agent terminals are available when launching stvena with a command"
		return
	}
	if key == 0x17 {
		s.closeActiveAgent()
	} else if key == 0x1d {
		s.pending = nil
		s.mouseDragging = false
		s.agentPicker = &ui.AgentPicker{Options: []string{
			"Launch command: " + strings.Join(s.launchCommand, " "),
			"Codex", "Claude Code",
		}}
	} else if len(s.agents) > 0 {
		delta := 1
		if key == 0x10 {
			delta = -1
		}
		s.selectAgent((s.activeAgentIndex + delta + len(s.agents)) % len(s.agents))
	}
}

func (s *screenState) agentPickerKey(key string) {
	p := s.agentPicker
	switch key {
	case "esc", "\x03", "\x1d":
		s.agentPicker, s.pending = nil, nil
	case "up", "k":
		p.Index = (p.Index + len(p.Options) - 1) % len(p.Options)
	case "down", "j", "tab":
		p.Index = (p.Index + 1) % len(p.Options)
	case "c", "l", "enter":
		if key == "c" {
			p.Index = 1
		} else if key == "l" {
			p.Index = 2
		}
		args := s.launchCommand
		if p.Index == 1 {
			args = []string{"codex"}
		} else if p.Index == 2 {
			args = []string{"claude"}
		}
		// A fullscreen review has no PTY area; start at the regular pane size.
		layout := ui.NewLayoutOptions(s.layout.Width, s.layout.Height, s.ratio, false)
		a, err := s.startAgent(append([]string(nil), args...), layout)
		if err != nil {
			p.Error = err.Error()
			return
		}
		s.agentPicker = nil
		s.agents = append(s.agents, a)
		s.selectAgent(len(s.agents) - 1)
		s.review.Notice = ""
	}
}

func (s *screenState) closeActiveAgent() {
	a := s.activeAgent()
	if a == nil {
		return
	}
	a.closed = true
	if a.close != nil {
		a.close()
	}
	index := s.activeAgentIndex
	// Select before removing so the surviving terminal keeps its own draft.
	s.selectAgent((index - 1 + len(s.agents)) % len(s.agents))
	s.agents = append(s.agents[:index], s.agents[index+1:]...)
	if s.activeAgentIndex >= index {
		s.activeAgentIndex = max(0, s.activeAgentIndex-1)
	}
	s.syncAgent()
	if len(s.agents) == 0 {
		s.diffFocused, s.fullscreen = true, true
		s.relayout(s.layout.Width, s.layout.Height)
	}
	s.review.Notice = "Agent closed"
	if len(s.agents) == 0 {
		s.review.Notice += " · " + s.review.Binding("Ctrl-]") + ": new agent · q exits"
	}
}

func (s *screenState) agentExited(a *agentTerminal, err error) {
	if a.closed {
		return
	}
	a.exited, a.status = true, "Finished"
	if err != nil {
		a.status = "Exited: " + err.Error()
	}
	if a != s.activeAgent() {
		return
	}
	s.syncAgent()
	s.diffFocused = true
	s.review.Browser = true
	s.review.Notice = "Agent finished · " + s.review.Binding("Ctrl-]") + ": new agent · " + s.review.Binding("Ctrl-N") + "/" + s.review.Binding("Ctrl-P") + ": switch agents"
	if !s.agentsRunning() {
		s.review.Notice += " · q exits"
	}
}

func (s *screenState) agentsRunning() bool {
	for _, a := range s.agents {
		if !a.exited {
			return true
		}
	}
	return len(s.agents) == 0 && !s.exited
}

func (s *screenState) resizeAgents() {
	for _, a := range s.agents {
		s.resizePTY(a.virtual, a.ptmx)
	}
}

func (s *screenState) closeAgents() {
	// Stop all commands together so shutdown time does not grow per terminal.
	var done sync.WaitGroup
	for _, a := range s.agents {
		done.Add(1)
		go func() { defer done.Done(); a.close() }()
	}
	done.Wait()
}
