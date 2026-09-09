package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/nccapo/stvena/internal/ui"
)

func terminalState(t *testing.T) *screenState {
	t.Helper()
	s := &screenState{layout: ui.NewLayout(140, 40), ratio: 58, launchCommand: []string{"codex"}}
	s.startAgent = func(args []string, layout ui.Layout) (*agentTerminal, error) {
		a := newAgentTerminal(args[0], layout.LeftWidth, layout.LeftHeight)
		a.input = &bytes.Buffer{}
		t.Cleanup(func() { _ = a.virtual.Close() })
		return a, nil
	}
	s.agentShortcut(0x1d)
	s.agentPickerKey("enter")
	return s
}

func TestAgentTerminalsRouteInputAcrossShortcutsInOneRead(t *testing.T) {
	s := terminalState(t)
	first := s.activeAgent()
	// Ctrl-O belongs to the agent and must pass through on either terminal.
	s.handleInput([]byte("first\x0f\x1d\rsecond\x0f\x10back\x0enext"), s.agentInput)
	if len(s.agents) != 2 || s.activeAgentIndex != 1 || s.diffFocused {
		t.Fatalf("new/switch failed: count=%d index=%d focus=%v", len(s.agents), s.activeAgentIndex, s.diffFocused)
	}
	if got := first.input.(*bytes.Buffer).String(); got != "first\x0fback" {
		t.Fatalf("first terminal received %q", got)
	}
	if got := s.agentInput.(*bytes.Buffer).String(); got != "second\x0fnext" {
		t.Fatalf("second terminal received %q", got)
	}
	s.handleInput([]byte{0x0e}, s.agentInput)
	if s.activeAgentIndex != 0 {
		t.Fatal("next did not wrap")
	}
	s.handleInput([]byte{0x10}, s.agentInput)
	if s.activeAgentIndex != 1 {
		t.Fatal("previous did not wrap")
	}
}

func TestAgentTerminalsKeepScreensModesAndDraftsSeparate(t *testing.T) {
	s := terminalState(t)
	first := s.activeAgent()
	_, _ = first.virtual.Write([]byte("first\x1b[?2004h\x1b[?25l"))
	s.syncAgent()
	s.review.AgentDraft = true
	s.review.Scroll = 7
	s.agentShortcut(0x1d)
	s.agentPickerKey("enter")
	second := s.activeAgent()
	_, _ = second.virtual.Write([]byte("second"))
	// Background output still advances its own screen and modes.
	_, _ = first.virtual.Write([]byte(" background"))
	s.syncAgent()
	if s.bracketedPaste || s.review.AgentDraft || !second.cursorVisible || s.review.Scroll != 7 {
		t.Fatal("new terminal inherited another terminal's modes/draft or changed review")
	}
	s.agentShortcut(0x10)
	if !s.bracketedPaste || !s.review.AgentDraft || first.cursorVisible {
		t.Fatal("switch did not restore terminal modes and draft")
	}
	for i, want := range []string{"first background", "second"} {
		for x, ch := range want {
			if got := s.agents[i].virtual.CellAt(x, 0).Content; got != string(ch) {
				t.Fatalf("terminal %d cell %d = %q, want %q", i, x, got, string(ch))
			}
		}
	}
}

func TestAgentShortcutsInsideFragmentedPasteStayLiteral(t *testing.T) {
	s := terminalState(t)
	payload := ansi.BracketedPasteStart + "text\x1d\x0e\x10\x17" + ansi.BracketedPasteEnd
	for _, b := range []byte(payload) {
		s.handleInput([]byte{b}, s.agentInput)
	}
	if len(s.agents) != 1 || s.agentInput.(*bytes.Buffer).String() != payload {
		t.Fatal("pasted shortcuts opened/switched terminals or altered text")
	}
	s.diffFocused = true
	s.handleInput([]byte(payload), s.agentInput)
	if len(s.agents) != 1 || !s.diffFocused || s.agentInput.(*bytes.Buffer).String() != payload {
		t.Fatal("review paste switched terminals or leaked to agent")
	}
}

func TestAgentSwitchWaitsForCodeHandoff(t *testing.T) {
	s := terminalState(t)
	s.pastePending = true
	s.handleInput([]byte("\x1d\rnew input"), s.agentInput)
	if len(s.agents) != 1 {
		t.Fatal("switched away during a code handoff")
	}
	s.finishAgentPaste(nil)
	queued := s.pasteInput
	s.pasteInput = nil
	s.handleInput(queued, s.agentInput)
	if len(s.agents) != 2 || !s.agents[0].draft || s.review.AgentDraft || s.agentInput.(*bytes.Buffer).String() != "new input" {
		t.Fatal("queued switch/input or original draft routed incorrectly")
	}
}

func TestAgentExitDoesNotDisruptAnotherTerminalOrAllowQuit(t *testing.T) {
	s := terminalState(t)
	first := s.activeAgent()
	s.agentShortcut(0x1d)
	s.agentPickerKey("enter")
	s.agentExited(first, errors.New("exit status 1"))
	if s.exited || s.diffFocused || s.review.AgentStatus != "Running" {
		t.Fatal("background exit interrupted active agent")
	}
	s.agentShortcut(0x10)
	if !s.exited || !s.diffFocused || !strings.Contains(s.review.AgentStatus, "exit status 1") {
		t.Fatal("finished terminal did not retain exit status")
	}
	s.review.Request = "quit"
	if s.dispatch(context.Background(), make(chan any, 1), make(chan struct{})) {
		t.Fatal("q quit while another agent was running")
	}
	s.agentExited(s.agents[1], nil)
	s.review.Request = "quit"
	if !s.dispatch(context.Background(), make(chan any, 1), make(chan struct{})) {
		t.Fatal("q did not quit after every agent finished")
	}
	s.agentShortcut(0x1d)
	s.agentPickerKey("enter")
	if s.exited || s.diffFocused || len(s.agents) != 3 {
		t.Fatal("could not open an agent after all previous agents exited")
	}
}

func TestAgentStartFailurePreservesCurrentTerminal(t *testing.T) {
	s := terminalState(t)
	first := s.activeAgent()
	s.startAgent = func([]string, ui.Layout) (*agentTerminal, error) { return nil, errors.New("cannot start") }
	s.handleInput([]byte("before\x1d\rafter"), s.agentInput)
	if s.activeAgent() != first || len(s.agents) != 1 || s.agentPicker == nil || s.agentPicker.Error != "cannot start" || first.input.(*bytes.Buffer).String() != "before" {
		t.Fatal("failed launch lost current terminal or input")
	}
}

func TestAgentControlsFromFullscreenAndMouse(t *testing.T) {
	s := terminalState(t)
	s.diffFocused, s.fullscreen = true, true
	s.relayout(80, 24)
	s.handleInput([]byte{0x1d, '\r'}, s.agentInput)
	if s.fullscreen || s.diffFocused || s.activeAgent().virtual.Width() != 80 || s.activeAgent().virtual.Height() < 1 {
		t.Fatal("new terminal from fullscreen review has no usable size/focus")
	}
	s.relayout(140, 40)
	s.resizeAgents()
	for _, a := range s.agents {
		if a.virtual.Width() != s.layout.LeftWidth || a.virtual.Height() != s.layout.LeftHeight {
			t.Fatal("background terminal was not resized")
		}
	}
	s.diffFocused = true
	for y := 0; y < s.layout.FooterHeight; y++ {
		for x := 0; x < s.layout.Width; x++ {
			if ui.ControlKeyAt(s.layout.Width, x, y, true) == "new-agent" {
				s.handleInput([]byte(fmt.Sprintf("\x1b[<0;%d;%dM\rtyped", x+1, s.layout.FooterY+y+1)), s.agentInput)
				if len(s.agents) != 3 || s.diffFocused || s.agentInput.(*bytes.Buffer).String() != "typed" {
					t.Fatal("new agent footer control failed")
				}
				return
			}
		}
	}
	t.Fatal("new agent footer control not found")
}

func TestStandaloneReviewDoesNotStartAgent(t *testing.T) {
	s := screenState{layout: ui.NewLayout(140, 40), diffFocused: true, exited: true, agentInput: io.Discard}
	s.handleInput([]byte{0x1d, 0x0e, 0x10, 0x17}, io.Discard)
	if len(s.agents) != 0 || !s.diffFocused || !s.exited {
		t.Fatal("standalone review launched an agent")
	}
}

func TestCloseAgentRestoresPreviousTerminalAndRoutesInput(t *testing.T) {
	s := terminalState(t)
	first := s.activeAgent()
	s.review.AgentDraft = true
	s.agentShortcut(0x1d)
	s.agentPickerKey("enter")
	second := s.activeAgent()
	closed := 0
	second.close = func() { closed++ }
	s.handleInput([]byte("before\x17after"), s.agentInput)
	if closed != 1 || !second.closed || len(s.agents) != 1 || s.activeAgent() != first || s.diffFocused || !s.review.AgentDraft {
		t.Fatal("close did not stop the active agent and restore the previous draft/focus")
	}
	if second.input.(*bytes.Buffer).String() != "before" || first.input.(*bytes.Buffer).String() != "after" {
		t.Fatal("close routed input to the wrong terminal")
	}
	s.agentExited(second, errors.New("terminated"))
	if s.exited || s.diffFocused || s.review.AgentLabel != "codex 1/1" || s.review.Notice != "Agent closed" {
		t.Fatal("late exit disrupted the surviving agent")
	}
}

func TestCloseAgentAtEachPosition(t *testing.T) {
	for index := range 3 {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			s := terminalState(t)
			s.agentShortcut(0x1d)
			s.agentPickerKey("enter")
			s.agentShortcut(0x1d)
			s.agentPickerKey("enter")
			s.selectAgent(index)
			previous := s.agents[(index+2)%3]
			s.diffFocused, s.fullscreen = true, true
			s.handleInput([]byte{0x17}, s.agentInput)
			if len(s.agents) != 2 || s.activeAgent() != previous || s.diffFocused || s.fullscreen {
				t.Fatal("close from review did not select the previous terminal")
			}
		})
	}
}

func TestCloseLastAgentKeepsReviewAndAllowsNewAgent(t *testing.T) {
	s := terminalState(t)
	s.review.AgentDraft = true
	s.handleInput([]byte{0x17}, s.agentInput)
	if len(s.agents) != 0 || !s.exited || !s.diffFocused || !s.fullscreen || s.agentsRunning() || s.review.AgentDraft || s.bracketedPaste || s.agentInput != io.Discard || s.review.AgentLabel != "" {
		t.Fatal("last close did not leave an empty review workspace")
	}
	s.handleInput([]byte{0x17, 0x0e, 0x10}, s.agentInput)
	s.review.Request = "quit"
	if !s.dispatch(context.Background(), make(chan any, 1), make(chan struct{})) {
		t.Fatal("q did not allow exit after closing the last terminal")
	}
	s.handleInput([]byte("\x1d\rnew"), s.agentInput)
	if len(s.agents) != 1 || s.exited || s.diffFocused || s.fullscreen || s.agentInput.(*bytes.Buffer).String() != "new" {
		t.Fatal("could not launch and type after closing all terminals")
	}
}

func TestCloseAgentFromFooter(t *testing.T) {
	s := terminalState(t)
	first := s.activeAgent()
	s.agentShortcut(0x1d)
	s.agentPickerKey("enter")
	s.diffFocused = true
	for y := 0; y < s.layout.FooterHeight; y++ {
		for x := 0; x < s.layout.Width; x++ {
			if ui.ControlKeyAt(s.layout.Width, x, y, true) == "close-agent" {
				s.handleInput([]byte(fmt.Sprintf("\x1b[<0;%d;%dM", x+1, s.layout.FooterY+y+1)), s.agentInput)
				if len(s.agents) != 1 || s.activeAgent() != first || s.diffFocused {
					t.Fatal("close agent footer control failed")
				}
				return
			}
		}
	}
	t.Fatal("close agent footer control not found")
}

func TestAgentPickerCommandsAndArgumentsAreIndependent(t *testing.T) {
	s := terminalState(t)
	s.launchCommand = []string{"/custom/codex", "--model", "custom model", "literal; argument"}
	start := s.startAgent
	var launches [][]string
	s.startAgent = func(args []string, layout ui.Layout) (*agentTerminal, error) {
		launches = append(launches, append([]string(nil), args...))
		return start(args, layout)
	}
	// Select Claude with fragmented arrow sequences, then Codex by shortcut,
	// and finally repeat the original command including exact argument bounds.
	for _, input := range []string{"\x1d\x1b[", "B\x1b[B\rclaude input", "\x1dccodex input", "\x1d\r"} {
		s.handleInput([]byte(input), s.agentInput)
	}
	if fmt.Sprint(launches) != fmt.Sprint([][]string{{"claude"}, {"codex"}, s.launchCommand}) {
		t.Fatalf("commands or arguments crossed between CLIs: %q", launches)
	}
	if s.agents[1].input.(*bytes.Buffer).String() != "claude input" || s.agents[2].input.(*bytes.Buffer).String() != "codex input" {
		t.Fatal("mixed CLI input routed to the wrong process")
	}
	s.selectAgent(1)
	if s.agentName != "claude" || s.review.AgentLabel != "claude 2/4" {
		t.Fatal("Claude handoff identity was not restored")
	}
	s.selectAgent(2)
	if s.agentName != "codex" || s.review.AgentLabel != "codex 3/4" {
		t.Fatal("Codex handoff identity was not restored")
	}
}

func TestAgentPickerCancelPreservesPaneAndReview(t *testing.T) {
	for _, focused := range []bool{false, true} {
		s := terminalState(t)
		s.diffFocused, s.fullscreen = focused, focused
		s.review.Prompt, s.review.Input, s.review.AgentDraft = "Add comment", "unfinished", true
		s.handleInput([]byte("\x1d\x1b"), s.agentInput)
		s.decodeInput(true)
		if s.agentPicker != nil || len(s.agents) != 1 || s.diffFocused != focused || s.fullscreen != focused || !s.review.AgentDraft || s.review.Prompt != "Add comment" || s.review.Input != "unfinished" {
			t.Fatal("cancel changed the underlying pane, draft, or review prompt")
		}
		if s.agentInput.(*bytes.Buffer).Len() != 0 {
			t.Fatal("picker input leaked to agent")
		}
	}
}

func TestAgentPickerIgnoresPastedChoicesAndCanQuit(t *testing.T) {
	s := terminalState(t)
	s.handleInput([]byte{0x1d}, s.agentInput)
	for _, b := range []byte(ansi.BracketedPasteStart + "cl\r\x11\x17" + ansi.BracketedPasteEnd) {
		s.handleInput([]byte{b}, s.agentInput)
	}
	if s.agentPicker == nil || len(s.agents) != 1 || s.review.Request != "" || s.agentInput.(*bytes.Buffer).Len() != 0 {
		t.Fatal("pasted chooser shortcuts executed or leaked to agent")
	}
	s.handleInput([]byte{0x11}, s.agentInput)
	if s.review.Request != "quit-app" {
		t.Fatal("chooser blocked quitting")
	}
}

func TestAgentPickerFailureAllowsDifferentCLI(t *testing.T) {
	s := terminalState(t)
	start := s.startAgent
	s.startAgent = func(args []string, layout ui.Layout) (*agentTerminal, error) {
		if args[0] == "claude" {
			return nil, errors.New("claude not found")
		}
		return start(args, layout)
	}
	s.handleInput([]byte("\x1dl"), s.agentInput)
	if s.agentPicker == nil || s.agentPicker.Error != "claude not found" || len(s.agents) != 1 {
		t.Fatal("missing CLI did not leave a usable chooser")
	}
	s.handleInput([]byte("cworks"), s.agentInput)
	if s.agentPicker != nil || len(s.agents) != 2 || s.agentName != "codex" || s.agentInput.(*bytes.Buffer).String() != "works" {
		t.Fatal("could not recover by choosing another CLI")
	}
}

func TestAgentPickerMouseChoosesClaude(t *testing.T) {
	for _, width := range []int{80, 140} {
		s := terminalState(t)
		s.relayout(width, 40)
		s.handleInput([]byte("\x1d\x1b[<0;5;5Mhello"), s.agentInput)
		if s.agentPicker != nil || len(s.agents) != 2 || s.agentName != "claude" || s.agentInput.(*bytes.Buffer).String() != "hello" {
			t.Fatal("mouse selection did not launch and focus Claude")
		}
	}
}

func TestMixedAgentCodeHandoffTargetsSelectedCLI(t *testing.T) {
	s := terminalState(t)
	s.handleInput([]byte("\x1dl"), s.agentInput)
	s.review = pasteState(io.Discard).review
	s.review.Snapshot.Root = t.TempDir()
	for _, a := range s.agents {
		_, _ = a.virtual.Write([]byte(ansi.SetModeBracketedPaste))
	}
	for _, index := range []int{1, 0} {
		s.selectAgent(index)
		s.diffFocused = true
		events := make(chan any, 1)
		s.pasteToAgent(events, make(chan struct{}))
		result := awaitPaste(t, events)
		s.workers.Wait()
		s.finishAgentPaste(result.err)
		if result.err != nil || !s.review.AgentDraft || s.diffFocused {
			t.Fatalf("handoff failed for %s: %v", s.agentName, result.err)
		}
		if got := s.agentInput.(*bytes.Buffer).String(); !strings.HasPrefix(got, ansi.BracketedPasteStart) || !strings.HasSuffix(got, ansi.BracketedPasteEnd) || !strings.Contains(got, "+new()") {
			t.Fatalf("bad handoff to %s: %q", s.agentName, got)
		}
		if index == 1 && s.agents[0].input.(*bytes.Buffer).Len() != 0 {
			t.Fatal("Claude handoff reached Codex")
		}
	}
}
