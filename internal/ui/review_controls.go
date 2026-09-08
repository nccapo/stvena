package ui

import (
	"fmt"
	"strings"

	"github.com/nccapo/stvena/internal/review"
)

type reviewControl struct{ key, label string }

func reviewControls(s *review.State) []reviewControl {
	controls := baseReviewControls(s)
	if s.Pinned && !s.Selecting {
		controls = append([]reviewControl{{"P", "P: Resume live"}}, controls...)
	}
	return controls
}

func baseReviewControls(s *review.State) []reviewControl {
	if s.Source == "project" && s.Browser {
		label := "Enter: Open"
		if s.ProjectFolderSelected() {
			label = "Enter: Toggle folder"
		}
		return []reviewControl{{"enter", label}, {"left", "←: Parent"}, {"/", "/: Find file"}}
	}
	if s.Browser {
		return []reviewControl{{"enter", "Enter: open"}, {"/", "/: find file"}, {"N", "N: next unreviewed"}}
	}
	if s.Selecting {
		return []reviewControl{{"x", "x: Collect"}, {"b", "b: Add to agent"}, {"y", "y: Copy"}, {"V", "V: Clear"}, {"P", "P: Resume live"}}
	}
	if s.Source == "project" {
		return []reviewControl{{"f", "f: Files"}, {"x", "x: Collect"}, {"B", "B: Context"}, {"e", "e: Editor"}}
	}
	mark := "Space: Mark reviewed"
	if f := s.Current(); f != nil && s.Reviewed(*f) {
		mark = "Space: Unmark"
	}
	return []reviewControl{{" ", mark}, {"N", "N: Next unreviewed"}, {"e", "e: Open editor"}}
}

func reviewControlBar(s *review.State) string {
	var labels []string
	for _, c := range reviewControls(s) {
		labels = append(labels, cyan+c.label+reset)
	}
	return " " + strings.Join(labels, "  ")
}

func reviewFooter(s *review.State) string {
	if s.Snapshot.Err != nil {
		return red + " " + safeText(s.Snapshot.Err.Error())
	}
	if s.Searching {
		return cyan + " Type path · Enter: apply · Esc: cancel"
	}
	selectionHint := s.Selecting && (strings.HasPrefix(s.Notice, "Selected lines ·") || strings.HasPrefix(s.Notice, "Pinned · arrows:"))
	if s.Notice != "" && !selectionHint {
		return cyan + " " + safeText(s.Notice)
	}
	if s.Help {
		return muted + " Ctrl-G: Switch panes  ?: help"
	}
	return reviewControlBar(s)
}

// Rendering and hit testing share labels, so clipped controls never get an
// invisible click target. Coordinates are relative to the review pane.
func ReviewControlKeyAt(s *review.State, width, height, x, y int) string {
	if x < 0 || x >= width || s.Help || s.Menu || s.Panel != "" || s.Prompt != "" || s.ConfirmAction != "" || WelcomeVisible(s) {
		return ""
	}
	files, _ := ReviewSize(height, s.FileListCount())
	if s.Source != "project" && !s.Browser && s.Current() != nil && y == files+2 && y < height-1 {
		if x >= 1 && x < 12 && width >= 12 {
			return "view-diff"
		}
		if x >= 13 && x < 26 && width >= 26 {
			return "view-file"
		}
	}
	if y != height-1 || reviewFooter(s) != reviewControlBar(s) {
		return ""
	}
	column := 1
	for _, c := range reviewControls(s) {
		end := column + ansiWidth(c.label)
		if x >= column && x < end && end <= width {
			return c.key
		}
		column = end + 2
	}
	return ""
}

func viewTabs(s *review.State) string {
	if s.Source == "project" {
		return cyan + bold + " [ Full file ]" + reset
	}
	diff, file := muted, muted
	if s.FullFile {
		file = cyan + bold
	} else {
		diff = cyan + bold
	}
	return " " + diff + "[ Changes ]" + reset + " " + file + "[ Full file ]" + reset
}

func selectionSummary(s *review.State) string {
	count, first, last := 0, 0, 0
	lines := s.DisplayLines()
	a, b := s.SelectedRange()
	for i := a; i <= b && i < len(lines); i++ {
		line := lines[i]
		if !s.SelectionIncludes(i, line) || line.Old == 0 && line.New == 0 {
			continue
		}
		n := line.New
		if n == 0 || s.SelectionSide == 'o' {
			n = line.Old
		}
		if count == 0 {
			first = n
		}
		last = n
		count++
	}
	if !s.FullFile && s.SelectionSide == 0 {
		return fmt.Sprintf(" %d source lines selected · version pinned", count)
	}
	return fmt.Sprintf(" Lines %d–%d selected · version pinned", first, last)
}

func panelControls(s *review.State) []reviewControl {
	switch s.Panel {
	case "Context":
		return []reviewControl{{"enter", "Enter: Preview"}, {"i", "i: Request"}, {"d", "d: Remove"}, {"b", "b: Paste"}, {"esc", "Esc: Back"}}
	case "Context preview":
		return []reviewControl{{"b", "b: Paste to agent"}, {"y", "y: Copy"}, {"esc", "Esc: Tray"}}
	case "Problems":
		return []reviewControl{{"enter", "Enter: Source"}, {"x", "x: Collect failure"}, {"t", "t: Rerun"}, {"esc", "Esc: Logs"}}
	case "Checks":
		return []reviewControl{{"o", "o: Problems"}, {"t", "t: Run / rerun"}, {"esc", "Esc: Back"}}
	}
	return nil
}
func panelControlBar(s *review.State) string {
	var labels []string
	for _, c := range panelControls(s) {
		labels = append(labels, c.label)
	}
	return " " + strings.Join(labels, "  ")
}
func PanelControlKeyAt(s *review.State, width, height, x, y int) string {
	if s.Notice != "" || s.Prompt != "" || s.ConfirmAction != "" || s.Menu || s.Help || y != height-1 || x < 0 || x >= width {
		return ""
	}
	column := 1
	for _, c := range panelControls(s) {
		end := column + ansiWidth(c.label)
		if x >= column && x < end && end <= width {
			return c.key
		}
		column = end + 2
	}
	return ""
}
