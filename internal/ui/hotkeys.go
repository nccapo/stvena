package ui

import (
	"fmt"

	"github.com/nccapo/stvena/internal/review"
)

// HelpListWindow is shared by rendering and mouse hit testing.
func HelpListWindow(s *review.State, height int) (int, int) {
	count := min(len(review.HotkeyActions), max(0, height-4))
	start := min(max(0, s.HelpIndex-count/2), len(review.HotkeyActions)-count)
	return start, count
}

func HelpActionAt(s *review.State, height, row int) int {
	start, count := HelpListWindow(s, height)
	if row >= 3 && row < 3+count {
		return start + row - 3
	}
	return -1
}

func renderHelp(s *review.State, width, height int) []string {
	rows := make([]string, height)
	for i := range rows {
		rows[i] = reviewRow("", width)
	}
	put := func(row int, text string) {
		if row >= 0 && row < height {
			rows[row] = reviewRow(" "+text, width)
		}
	}
	put(0, headerBG+bold+"CONFIGURATION · Review and global shortcuts")
	put(1, "↑/↓: select · Enter / click: change · Esc: back")
	put(2, muted+"Backspace: reset key · Delete: reset all · * custom")
	start, count := HelpListWindow(s, height)
	for i := start; i < start+count; i++ {
		action := review.HotkeyActions[i]
		marker, style := "  ", ""
		if i == s.HelpIndex {
			marker, style = "› ", focusedBG+bold
		}
		custom := " "
		if s.Binding(action.Key) != action.Key {
			custom = "*"
		}
		put(3+i-start, style+fmt.Sprintf("%s%-7s %s %s", marker, review.KeyLabel(s.Binding(action.Key)), custom, action.Name)+reset)
	}
	footer := "Changes save automatically for all projects"
	if s.EditingHotkey {
		action := review.HotkeyActions[s.HelpIndex]
		footer = "Press a printable key for " + action.Name + " · Esc: cancel"
		if review.IsGlobalHotkey(action.Key) {
			footer = "Press a Ctrl key for " + action.Name + " · Esc: cancel"
		}
	}
	if s.Notice != "" {
		footer = s.Notice
	}
	put(height-1, cyan+footer)
	return rows
}
