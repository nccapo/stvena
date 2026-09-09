package app

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/nccapo/stvena/internal/diffview"
	"github.com/nccapo/stvena/internal/review"
	"github.com/nccapo/stvena/internal/ui"
)

func TestFooterRecoveryConfiguresGlobalShortcutAcrossProjects(t *testing.T) {
	isolateHotkeys(t)
	s := terminalState(t)
	s.root = t.TempDir()
	// No Ctrl-G or mouse: F6, arrows to Configuration, Enter, Enter, Ctrl-O.
	input := "\x1b[17~"
	for _, control := range ui.FooterControls {
		if control.Action == "?" {
			break
		}
		input += "\x1b[C"
	}
	input += "\r\r\x0f"
	for _, b := range []byte(input) {
		s.handleInput([]byte{b}, s.agentInput)
	}
	if !s.review.Help || s.review.EditingHotkey || s.review.Binding("Ctrl-G") != "Ctrl-O" || s.review.FooterFocused {
		t.Fatalf("keyboard recovery did not configure pane shortcut: %s", s.review.Notice)
	}
	s.dispatch(context.Background(), make(chan any, 1), make(chan struct{}))
	reopened := screenState{root: t.TempDir()}
	reopened.loadPreferences()
	if reopened.review.Binding("Ctrl-G") != "Ctrl-O" {
		t.Fatal("global shortcut did not reach another project")
	}
	s.handleInput([]byte("\x0f\x07agent"), s.agentInput)
	if s.diffFocused || s.agentInput.(*bytes.Buffer).String() != "\x07agent" {
		t.Fatal("new binding failed or old Ctrl-G was not released to the agent")
	}
}

func TestFooterPreservesAgentArrowsAndRestoresFocus(t *testing.T) {
	s := terminalState(t)
	s.handleInput([]byte("\x1b[A\x1b[B\x1b[C\x1b[D"), s.agentInput)
	if s.review.FooterFocused || s.agentInput.(*bytes.Buffer).String() != "\x1b[A\x1b[B\x1b[C\x1b[D" {
		t.Fatal("agent arrows were intercepted")
	}
	before := s.agentInput.(*bytes.Buffer).String()
	s.handleInput([]byte("\x1b[17~\x1b[C\x1b[B\x1b[A\x1b"), s.agentInput)
	s.decodeInput(true)
	if s.review.FooterFocused || s.diffFocused || s.agentInput.(*bytes.Buffer).String() != before {
		t.Fatal("footer navigation leaked or changed pane")
	}
	s.diffFocused = true
	s.review.Browser = true
	s.review.Update(diffview.Snapshot{Files: []diffview.File{{Path: "one"}}})
	s.handleInput([]byte("\x1b[B"), s.agentInput)
	if !s.review.FooterFocused {
		t.Fatal("Down at end of file list did not reach footer")
	}
	s.handleInput([]byte("\x1b[A"), s.agentInput)
	if s.review.FooterFocused || !s.diffFocused {
		t.Fatal("Up from first footer row did not return to review")
	}
}

func TestFooterConfigurationOverSearchPreservesQuery(t *testing.T) {
	s := terminalState(t)
	s.review.Searching, s.review.Query = true, "unfinished"
	s.activateFooter("?")
	s.handleInput([]byte("\r\x0f\x1b"), s.agentInput)
	s.decodeInput(true)
	if s.review.Help || !s.review.Searching || s.review.Query != "unfinished" || s.review.Binding("Ctrl-G") != "Ctrl-O" {
		t.Fatal("configuration changed search text or could not capture Ctrl key")
	}
}

func TestGlobalQuitRemappingAndSwaps(t *testing.T) {
	for _, pending := range []bool{false, true} {
		s := terminalState(t)
		s.pastePending = pending
		if err := s.review.SetHotkey("Ctrl-Q", "Ctrl-X"); err != nil {
			t.Fatal(err)
		}
		s.handleInput([]byte{0x11}, s.agentInput)
		if s.review.Request != "" {
			t.Fatal("old quit key remained active")
		}
		s.handleInput([]byte{0x18}, s.agentInput)
		if s.review.Request != "quit-app" || len(s.pasteInput) != 0 {
			t.Fatal("new quit key failed during handoff")
		}
	}
	s := terminalState(t)
	s.review.Hotkeys = map[string]string{"Ctrl-G": "Ctrl-N", "Ctrl-N": "Ctrl-G"}
	if err := review.ValidateHotkeys(s.review.Hotkeys); err != nil {
		t.Fatal(err)
	}
	s.handleInput([]byte{0x0e}, s.agentInput)
	if !s.diffFocused {
		t.Fatal("swapped global binding was translated twice")
	}
}

func TestFooterAndCustomGlobalKeysInsidePasteStayLiteral(t *testing.T) {
	s := terminalState(t)
	if err := s.review.SetHotkey("Ctrl-G", "Ctrl-O"); err != nil {
		t.Fatal(err)
	}
	payload := ansi.BracketedPasteStart + "text\x0f\x1b[17~\x1b[C\r" + ansi.BracketedPasteEnd
	for _, b := range []byte(payload) {
		s.handleInput([]byte{b}, s.agentInput)
	}
	if s.review.FooterFocused || s.diffFocused || s.agentInput.(*bytes.Buffer).String() != payload {
		t.Fatal("paste executed footer or custom global shortcut")
	}
}

func TestFooterConfigurationMouseAndActionsFromAgent(t *testing.T) {
	s := terminalState(t)
	s.activateFooter("?")
	if !s.diffFocused || !s.review.Help {
		t.Fatal("Configuration did not focus review")
	}
	s.activateFooter("T")
	if s.review.Help || s.review.Panel != "Checks" {
		t.Fatal("toolbar action remained trapped in Configuration")
	}
	s.activateFooter("focus")
	if s.diffFocused {
		t.Fatal("pane control did not return to agent")
	}
	s.activateFooter("1")
	if !s.diffFocused || s.review.Request != "1" {
		t.Fatal("Changes action did not focus review")
	}
	// A released Cmd-G must not reclaim the reassigned Ctrl-G action either.
	if err := s.review.SetHotkey("Ctrl-G", "Ctrl-O"); err != nil {
		t.Fatal(err)
	}
	s.diffFocused = false
	s.handleInput([]byte("\x1b[103;9u"), s.agentInput)
	if s.diffFocused || !strings.Contains(s.agentInput.(*bytes.Buffer).String(), "\x1b[103;9u") {
		t.Fatal("old Command alias reactivated pane switching")
	}
}

func TestClickingPaneLeavesFooterNavigation(t *testing.T) {
	s := terminalState(t)
	s.toggleFooter()
	s.mouse(fmt.Sprintf("<0;%d;%dM", s.layout.DiffX+2, s.layout.DiffY+1))
	if s.review.FooterFocused || !s.diffFocused {
		t.Fatal("clicking review left keyboard focus on footer")
	}
}

func TestLeavingFooterWithEscapePreservesImmediateAgentInput(t *testing.T) {
	for _, text := range []string{"startup-input", "目录"} {
		for _, fragmented := range []bool{false, true} {
			s := terminalState(t)
			s.review.FooterFocused = true
			input := []byte("\x1b" + text)
			if fragmented {
				for _, b := range input {
					s.handleInput([]byte{b}, s.agentInput)
				}
			} else {
				s.handleInput(input, s.agentInput)
			}
			if s.review.FooterFocused || s.diffFocused || s.agentInput.(*bytes.Buffer).String() != text {
				t.Fatalf("text=%q fragmented=%t: lost or rerouted input %q", text, fragmented, s.agentInput.(*bytes.Buffer).String())
			}
		}
	}
}
