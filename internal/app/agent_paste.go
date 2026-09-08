package app

import (
	"fmt"
	"io"
	"path/filepath"

	"github.com/charmbracelet/x/ansi"
)

type agentPasteEvent struct{ err error }

func agentCommand(command string) string {
	switch name := filepath.Base(command); name {
	case "codex", "claude":
		return name
	}
	return ""
}

// A handoff is one literal paste, never an Enter key or an instruction to clear
// existing input. The CLI keeps ownership of editing and submitting the draft.
func (s *screenState) pasteToAgent(events chan<- any, stop <-chan struct{}) {
	defer func() {
		if !s.pastePending {
			s.pasteInput = nil
		}
	}()
	if s.exited || s.agentInput == nil {
		s.review.Notice = "No running agent · y copies code for another chat"
		return
	}
	if s.agentName == "" {
		s.review.Notice = "Direct paste supports stvena codex / claude · y copies code"
		return
	}
	if !s.bracketedPaste {
		s.review.Notice = "Agent has not enabled literal paste · return to its prompt, then retry"
		return
	}
	if s.pastePending {
		s.review.Notice = "Pasting selection…"
		return
	}
	message, err := s.review.SelectionMessage()
	if s.review.Panel == "Context" || s.review.Panel == "Context preview" {
		message, err = s.review.ContextMessage()
	}
	if err != nil {
		s.review.Notice = err.Error()
		return
	}
	s.pastePending = true
	s.review.Notice = "Pasting selection to " + s.agentName + "…"
	writer := s.agentInput
	s.workers.Add(1)
	go func() {
		defer s.workers.Done()
		payload := ansi.BracketedPasteStart + message + ansi.BracketedPasteEnd
		n, err := io.WriteString(writer, payload)
		if err == nil && n != len(payload) {
			err = io.ErrShortWrite
		}
		select {
		case events <- agentPasteEvent{err: err}:
		case <-stop:
		}
	}()
}
func (s *screenState) finishAgentPaste(err error) {
	s.pastePending = false
	if err != nil {
		s.pasteInput = nil
		s.review.Notice = fmt.Sprintf("Paste may be incomplete: %v · inspect the agent before retrying", err)
		return
	}
	if s.exited {
		s.pasteInput = nil
		s.review.Notice = "Agent exited during handoff; delivery was not confirmed"
		return
	}
	s.review.AgentDraft = true
	s.diffFocused = false
	s.fullscreen = false
	s.relayout(s.layout.Width, s.layout.Height)
	s.review.Notice = "Code pasted · add your request in the agent, then press Enter"
}
