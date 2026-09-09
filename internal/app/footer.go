package app

import (
	"github.com/nccapo/stvena/internal/review"
	"github.com/nccapo/stvena/internal/ui"
)

func (s *screenState) toggleFooter() {
	s.review.FooterFocused = !s.review.FooterFocused
	s.review.EditingHotkey = false
	s.agentPicker = nil
	s.pending = nil
	s.mouseDragging = false
}

func (s *screenState) footerKey(key string) {
	switch key {
	case "esc":
		s.review.FooterFocused = false
	case "enter":
		index := min(max(0, s.review.FooterIndex), len(ui.FooterControls)-1)
		s.activateFooter(ui.FooterControls[index].Action)
	default:
		index := ui.FooterMove(s.layout.Width, s.review.FooterIndex, key, &s.review)
		if index < 0 {
			s.review.FooterFocused = false
		} else {
			s.review.FooterIndex = index
		}
	}
}

// Mouse and keyboard activation share canonical actions, never remapped input.
func (s *screenState) activateFooter(key string) {
	if key == "" {
		return
	}
	if key == "footer" {
		s.toggleFooter()
		return
	}
	s.review.FooterFocused = false
	switch key {
	case "focus":
		s.switchPane()
	case "new-agent", "next-agent", "previous-agent", "close-agent":
		s.agentShortcut(map[string]byte{"new-agent": 0x1d, "next-agent": 0x0e, "previous-agent": 0x10, "close-agent": 0x17}[key])
	case "quit-app":
		s.review.Request = key
	default:
		s.diffFocused = true
		if key == "?" {
			s.review.Help = true
			s.review.EditingHotkey = false
			s.review.Notice = ""
			// Start with pane switching: this is the recovery route for conflicts.
			for i, action := range review.HotkeyActions {
				if action.Key == "Ctrl-G" {
					s.review.HelpIndex = i
					break
				}
			}
			return
		}
		if s.review.Prompt != "" || s.review.ConfirmAction != "" || s.review.Searching {
			s.review.Notice = "Finish or cancel text entry before choosing this action"
			return
		}
		// A toolbar action operates on the underlying review even with Help open.
		s.review.Help = false
		s.review.Key(key, s.visibleLines())
	}
}

func (s *screenState) switchPane() {
	s.review.FooterFocused = false
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
}

func (s *screenState) atFileListEnd() bool {
	r := &s.review
	if !r.Browser || r.Help || r.Menu || r.Searching || r.Prompt != "" || r.ConfirmAction != "" || r.Panel != "" {
		return false
	}
	index := r.Selected
	if r.Source == "project" {
		index = r.TreeIndex
	}
	return index >= r.FileListCount()-1
}
