package app

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestCommandShortcutsRouteAcrossAgentsAndPanes(t *testing.T) {
	for _, fragmented := range []bool{false, true} {
		t.Run(fmt.Sprint(fragmented), func(t *testing.T) {
			s := terminalState(t)
			first := s.activeAgent()
			input := "first\x1b[93;9u\rsecond\x1b[112;9uback\x1b[110;9unext\x1b[103;9u"
			if fragmented {
				for _, b := range []byte(input) {
					s.handleInput([]byte{b}, s.agentInput)
				}
			} else {
				s.handleInput([]byte(input), s.agentInput)
			}
			if len(s.agents) != 2 || s.activeAgentIndex != 1 || !s.diffFocused {
				t.Fatal("Command new/previous/next/focus shortcuts failed")
			}
			if first.input.(*bytes.Buffer).String() != "firstback" || s.agentInput.(*bytes.Buffer).String() != "secondnext" {
				t.Fatal("Command sequences leaked or input reached the wrong terminal")
			}
			s.handleInput([]byte("\x1b[119;9uafter"), s.agentInput)
			if len(s.agents) != 1 || s.activeAgent() != first || s.diffFocused || first.input.(*bytes.Buffer).String() != "firstbackafter" {
				t.Fatal("Command-W failed from review")
			}
		})
	}
}

func TestCommandQuitWorksDuringEditingAndHandoffAndSavesGlobally(t *testing.T) {
	for _, mode := range []string{"agent", "review", "paste", "editing", "picker"} {
		t.Run(mode, func(t *testing.T) {
			isolateHotkeys(t)
			s := terminalState(t)
			s.root = t.TempDir()
			switch mode {
			case "review":
				s.diffFocused = true
			case "paste":
				s.pastePending = true
			case "editing":
				s.diffFocused, s.review.Help, s.review.EditingHotkey = true, true, true
			case "picker":
				s.agentShortcut(0x1d)
			}
			if err := s.review.SetHotkey("a", "r"); err != nil {
				t.Fatal(err)
			}
			for _, b := range []byte("\x1b[113;9u") {
				s.handleInput([]byte{b}, s.agentInput)
			}
			if !s.dispatch(context.Background(), make(chan any, 1), make(chan struct{})) {
				t.Fatal("Command-Q did not quit")
			}
			if s.agentInput.(*bytes.Buffer).Len() != 0 || len(s.pasteInput) != 0 {
				t.Fatal("quit leaked input")
			}
			other := screenState{root: t.TempDir()}
			other.loadPreferences()
			if other.review.Binding("a") != "r" {
				t.Fatal("quitting lost unsaved global binding")
			}
		})
	}
}

func TestCommandSequencesInPasteStayLiteral(t *testing.T) {
	for _, pane := range []string{"agent", "review", "picker"} {
		t.Run(pane, func(t *testing.T) {
			s := terminalState(t)
			if pane == "review" {
				s.diffFocused = true
			}
			if pane == "picker" {
				s.agentShortcut(0x1d)
			}
			payload := ansi.BracketedPasteStart + "text\x1b[103;9u\x1b[93;9u\x1b[119;9u\x1b[113;9u" + ansi.BracketedPasteEnd
			for _, b := range []byte(payload) {
				s.handleInput([]byte{b}, s.agentInput)
			}
			want := ""
			if pane == "agent" {
				want = payload
			}
			if len(s.agents) != 1 || s.review.Request != "" || s.agentInput.(*bytes.Buffer).String() != want || s.diffFocused != (pane == "review") {
				t.Fatal("pasted Command shortcut executed or changed text")
			}
		})
	}
}

func TestUnrelatedEscapeSequencesReachAgentUnchanged(t *testing.T) {
	s := terminalState(t)
	payload := "\x1b[A\x1bOAx\x1b[99;9u\x1b[103;17u\x1b[103;13u\x1b[103;5u\x1b[103;10u\x1b[?1u\x1b[103;9:4u\x1b[103;9;103u\x03\t"
	for _, b := range []byte(payload) {
		s.handleInput([]byte{b}, s.agentInput)
	}
	if got := s.agentInput.(*bytes.Buffer).String(); got != payload || s.diffFocused {
		t.Fatalf("unrelated input changed: %q", got)
	}
	s.handleInput([]byte{27}, s.agentInput)
	s.decodeInput(true)
	if got := s.agentInput.(*bytes.Buffer).String(); got != payload+"\x1b" {
		t.Fatalf("Escape timeout lost input: %q", got)
	}
}

func TestCommandModifiersAndKeyEvents(t *testing.T) {
	s := terminalState(t)
	for _, seq := range []string{"\x1b[103;9:2u", "\x1b[103;9:3u"} {
		s.handleInput([]byte(seq), s.agentInput)
		if s.diffFocused || s.agentInput.(*bytes.Buffer).Len() != 0 {
			t.Fatal("repeat or release executed or leaked")
		}
	}
	s.handleInput([]byte("\x1b[103;201:1u"), s.agentInput) // Both lock modifiers.
	if !s.diffFocused {
		t.Fatal("lock modifiers blocked Command-G")
	}
	s.handleInput([]byte("\x1b[103:71:103;9u"), s.agentInput)
	if s.diffFocused {
		t.Fatal("alternate key codes blocked Command-G")
	}
}
