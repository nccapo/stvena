package app

import (
	"fmt"
	"github.com/nccapo/stvena/internal/review"
	"github.com/nccapo/stvena/internal/ui"
)

func (s *screenState) mouse(sequence string) {
	if len(sequence) < 2 {
		return
	}
	final := sequence[len(sequence)-1]
	if final != 'M' && final != 'm' {
		return
	}
	var button, x, y int
	if _, err := fmt.Sscanf(sequence[:len(sequence)-1], "<%d;%d;%d", &button, &x, &y); err != nil || x < 1 || y < 1 {
		return
	}
	x--
	y--
	if s.agentPicker != nil {
		if final == 'M' && button == 0 {
			if index := ui.AgentPickerChoiceAt(s.agentPicker, s.layout, x, y); index >= 0 {
				s.agentPicker.Index = index
				s.agentPickerKey("enter")
			}
		}
		return
	}
	if final == 'm' {
		if s.mouseDragging {
			s.extendMouseSelection(x, y)
		}
		s.mouseDragging = false
		return
	}
	if button&32 != 0 {
		if s.mouseDragging && button&3 == 0 {
			s.extendMouseSelection(x, y)
		}
		return
	}
	// Only keyboard input can assign a shortcut while capture is active.
	if s.review.Help && s.review.EditingHotkey {
		return
	}
	if button == 64 || button == 65 {
		if x < s.layout.DiffX || x >= s.layout.DiffX+s.layout.DiffWidth || y < s.layout.DiffY || y >= s.layout.FooterY {
			return
		}
		key, delta := "up", -1
		if button == 65 {
			key, delta = "down", 1
		}
		if s.review.SelectionMouse && s.review.Panel == "" && s.review.Prompt == "" && !s.review.Menu && !s.review.Help && !s.review.Browser {
			s.review.Scroll += delta
			s.clampScroll()
			if s.mouseDragging {
				s.extendMouseSelection(x, y)
			}
		} else {
			s.review.Key(key, s.visibleLines())
		}
		return
	}
	if button != 0 && button != 4 {
		return
	}
	s.mouseDragging = false
	if y >= s.layout.FooterY {
		key := ui.ControlKeyAt(s.layout.Width, x, y-s.layout.FooterY+ui.FooterStart(s.layout.Width, s.layout.FooterHeight, &s.review), true, &s.review)
		s.activateFooter(key)
		return
	}
	if s.review.FooterFocused {
		s.review.FooterFocused = false
		if x < s.layout.DiffX || x >= s.layout.DiffX+s.layout.DiffWidth || y < s.layout.DiffY {
			return
		}
		s.diffFocused = true
	}
	if x < s.layout.DiffX || x >= s.layout.DiffX+s.layout.DiffWidth {
		return
	}
	row := y - s.layout.DiffY
	if s.review.Help {
		if !s.review.EditingHotkey {
			if index := ui.HelpActionAt(&s.review, s.layout.DiffHeight, row); index >= 0 {
				s.review.HelpIndex = index
				s.review.Key("enter", s.visibleLines())
			}
		}
		return
	}
	if s.review.Menu {
		count := max(1, s.layout.DiffHeight-4)
		start := min(max(0, s.review.MenuIndex-count/2), max(0, len(review.Actions)-count))
		index := start + row - 1
		if row >= 1 && index < len(review.Actions) && row <= count {
			s.review.MenuIndex = index
			s.review.Key("enter", s.visibleLines())
		}
		return
	}
	if s.review.Prompt == "" && s.review.ConfirmAction == "" {
		if key := ui.PanelControlKeyAt(&s.review, s.layout.DiffWidth, s.layout.DiffHeight, x-s.layout.DiffX, row); key != "" {
			s.review.Key(key, s.visibleLines())
			return
		}
		if s.review.Panel == "Context" || s.review.Panel == "Problems" {
			index, total := s.review.TrayIndex, len(s.review.Attachments)
			if s.review.Panel == "Problems" {
				index, total = s.review.ProblemIndex, len(s.review.Problems)
			}
			start, count := ui.PanelListWindow(index, total, s.layout.DiffHeight)
			if row >= 1 && row <= count {
				if s.review.Panel == "Context" {
					s.review.TrayIndex = start + row - 1
				} else {
					s.review.ProblemIndex = start + row - 1
					s.review.Key("enter", s.visibleLines())
				}
			}
			return
		}
	}
	if s.review.Panel != "" || s.review.Prompt != "" || s.review.ConfirmAction != "" || s.review.Help {
		return
	}
	if key := ui.ReviewControlKeyAt(&s.review, s.layout.DiffWidth, s.layout.DiffHeight, x-s.layout.DiffX, row); key != "" {
		s.review.Key(key, s.visibleLines())
		return
	}
	if s.review.Source == "project" {
		start, count := ui.ProjectTreeWindow(&s.review, s.layout.DiffHeight)
		if row >= 2 && row < 2+count {
			s.review.ActivateProjectRow(start + row - 2)
			return
		}
		if s.review.Browser {
			return
		}
	}
	fileRows, _ := ui.ReviewSize(s.layout.DiffHeight, len(s.review.Indices))
	if s.review.Browser {
		fileRows = min(len(s.review.Indices), max(0, s.layout.DiffHeight-3))
	}
	start := min(max(0, s.review.Selected-fileRows/2), max(0, len(s.review.Indices)-fileRows))
	if s.review.Source != "project" && row >= 2 && row < 2+fileRows {
		s.review.Selected = start + row - 2
		s.review.Scroll = 0
		s.review.TargetLine = 0
		s.review.ClearSelection()
		s.review.Key("enter", s.visibleLines())
		return
	}
	side := byte(0)
	extend := button == 4 && s.review.Selecting
	if extend {
		side = s.review.SelectionSide
	}
	if index, side, ok := ui.CodeHitAt(&s.review, s.layout.DiffWidth, s.layout.DiffHeight, x-s.layout.DiffX, row, side); ok {
		if s.review.Snapshot.Tree == "" {
			s.review.Notice = "Selection needs a captured version"
			return
		}
		s.review.SelectWithMouse(index, side, extend)
		s.mouseDragging = true
	}
}
func (s *screenState) extendMouseSelection(x, y int) {
	if index, _, ok := ui.CodeHitAt(&s.review, s.layout.DiffWidth, s.layout.DiffHeight, x-s.layout.DiffX, y-s.layout.DiffY, s.review.SelectionSide); ok {
		s.review.SelectionEnd = index
		s.review.Notice = "Selected lines · release, then b: paste to agent"
	}
}
