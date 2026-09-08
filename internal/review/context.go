package review

import (
	"crypto/sha256"
	"fmt"
	"strings"
)

type Attachment struct {
	ID, Label, Tree, Message string
}

// Attachments own their text; later navigation, edits and selection changes
// cannot replace what the user collected. Nothing is submitted automatically.
func (s *State) AddAttachment(a Attachment) error {
	a.Label, a.Message = pasteText(a.Label), pasteText(a.Message)
	if a.Message == "" {
		return fmt.Errorf("no context to collect")
	}
	a.ID = fmt.Sprintf("%x", sha256.Sum256([]byte(a.Message)))
	for _, existing := range s.Attachments {
		if existing.ID == a.ID {
			return fmt.Errorf("already in context · B opens the tray")
		}
	}
	if len(s.Attachments) >= 20 {
		return fmt.Errorf("context is limited to 20 attachments; remove an item first")
	}
	s.Attachments = append(s.Attachments, a)
	if _, err := s.ContextMessage(); err != nil {
		s.Attachments = s.Attachments[:len(s.Attachments)-1]
		return err
	}
	s.TrayIndex = len(s.Attachments) - 1
	s.Request = "save"
	s.Notice = fmt.Sprintf("Collected · %d attachments · B: context tray", len(s.Attachments))
	return nil
}

func (s *State) CollectSelection() error {
	if s.Snapshot.Tree == "" {
		return fmt.Errorf("wait for a captured version before collecting context")
	}
	message, err := s.SelectionMessage()
	if err != nil {
		return err
	}
	first, last := 0, 0
	lines := s.DisplayLines()
	a, b := s.SelectedRange()
	for i := a; i <= b && i < len(lines); i++ {
		line := lines[i]
		if !s.SelectionIncludes(i, line) {
			continue
		}
		n := line.New
		if n == 0 || s.SelectionSide == 'o' {
			n = line.Old
		}
		if n == 0 {
			continue
		}
		if first == 0 {
			first = n
		}
		last = n
	}
	return s.AddAttachment(Attachment{Label: fmt.Sprintf("%s:%d–%d", s.Current().Path, first, last), Tree: s.Snapshot.Tree, Message: message})
}

func (s *State) ContextMessage() (string, error) {
	if len(s.Attachments) == 0 {
		return "", fmt.Errorf("context tray is empty · select code and press x")
	}
	var out strings.Builder
	out.WriteString("\nAttached context for this request. Verify current files before editing.\n")
	for i, a := range s.Attachments {
		fmt.Fprintf(&out, "\nAttachment %d: %s\n", i+1, a.Label)
		if s.LiveTree != "" && a.Tree != "" && a.Tree != s.LiveTree {
			out.WriteString("From an earlier workspace snapshot; current code may differ.\n")
		}
		out.WriteString(a.Message)
	}
	if strings.TrimSpace(s.ContextQuestion) != "" {
		fmt.Fprintf(&out, "\nMy request:\n%s\n", s.ContextQuestion)
	}
	message := pasteText(out.String())
	if len(message) > 32<<10 {
		return "", fmt.Errorf("combined context exceeds 32 KiB; remove an attachment or shorten your request")
	}
	return message, nil
}

func (s *State) contextKey(key string, visible int) bool {
	if s.Panel != "Context" && s.Panel != "Context preview" {
		return false
	}
	switch key {
	case "esc":
		if s.Panel == "Context preview" {
			s.Panel = "Context"
		} else {
			s.Panel = ""
		}
		s.PanelScroll = 0
	case "B":
		s.Panel = ""
		s.PanelScroll = 0
	case "up", "k":
		if s.Panel == "Context" {
			s.TrayIndex = max(0, s.TrayIndex-1)
		} else {
			s.PanelScroll = max(0, s.PanelScroll-1)
		}
	case "down", "j":
		if s.Panel == "Context" {
			s.TrayIndex = min(max(0, len(s.Attachments)-1), s.TrayIndex+1)
		} else {
			s.PanelScroll++
		}
	case "pagedown":
		s.PanelScroll += max(1, visible/2)
	case "pageup":
		s.PanelScroll = max(0, s.PanelScroll-max(1, visible/2))
	case "enter":
		s.Panel = "Context preview"
		s.PanelScroll = 0
	case "i":
		s.Prompt = "Context request"
		s.Input = s.ContextQuestion
	case "delete", "backspace", "d":
		if s.Panel == "Context" && len(s.Attachments) > 0 {
			i := min(s.TrayIndex, len(s.Attachments)-1)
			s.Attachments = append(s.Attachments[:i], s.Attachments[i+1:]...)
			s.TrayIndex = min(i, max(0, len(s.Attachments)-1))
			s.Request = "save"
		}
	case "b":
		s.Request = "paste-context"
	case "y":
		s.Request = "copy-context"
	}
	return true
}
