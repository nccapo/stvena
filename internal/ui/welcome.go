package ui

import (
	"strings"

	"github.com/nccapo/stvena/internal/review"
)

// WelcomeVisible never replaces changes, errors, filters or review controls.
func WelcomeVisible(s *review.State) bool {
	return s.Source != "project" && s.AgentStatus == "Running" && s.Snapshot.Err == nil &&
		len(s.Snapshot.Files) == 0 && s.Snapshot.FileCount == 0 &&
		!s.Pinned && s.Scope == 0 && s.Query == "" && !s.Searching &&
		!s.Help && !s.Menu && s.Panel == "" && s.Prompt == "" && s.ConfirmAction == ""
}

func renderWelcome(s *review.State, width, height int, focused bool) []string {
	rows := make([]string, height)
	for i := range rows {
		rows[i] = reviewRow("", width)
	}
	center := func(text string) string {
		return reviewRow(strings.Repeat(" ", max(0, (width-ansiWidth(text))/2))+text, width)
	}
	bg := headerBG
	if focused {
		bg = focusedBG
	}
	rows[0] = reviewRow(bg+muted+" LIVE REVIEW"+reset, width)
	if height < 4 {
		return rows
	}
	// Only the color of a fixed dot changes: no flashing text or shifting rows.
	pulse := []string{"\x1b[38;5;24m", "\x1b[38;5;31m", "\x1b[38;5;38m", cyan, "\x1b[38;5;38m", "\x1b[38;5;31m"}[s.WelcomeFrame%6]
	status := "Watching for changes"
	if s.Snapshot.UpdatedAt.IsZero() {
		status = "Preparing your review"
	}
	body := []string{bold + "Stvena" + reset, pulse + "●" + reset + muted + " " + status}
	if width >= 34 && height >= 12 {
		body = []string{
			bold + "Stvena" + reset,
			muted + "Your code, in plain sight.",
			"",
			pulse + "●" + reset + muted + " " + status,
			"",
			cyan + s.Binding("Ctrl-G") + reset + "  " + fitANSI("Switch panes", 23),
			cyan + "     v" + reset + "  " + fitANSI("See the whole file", 23),
			cyan + "Drag+b" + reset + "  Add code to agent draft",
		}
		if height >= 21 {
			logoHeight := min(16, height-len(body)-3, (width-2)/2)
			body = append(append(renderLogo(logoHeight*2), ""), body...)
		}
	}
	start := max(1, (height-1-len(body))/2)
	for i, line := range body {
		if start+i < height-1 {
			rows[start+i] = center(line)
		}
	}
	footer := "Changes appear here automatically"
	if s.Source == "session" {
		footer = "This session · 2: existing workspace edits"
	}
	if s.CheckStatus != "" {
		footer = "Checks: " + safeText(s.CheckStatus) + " · T: results"
		if s.CheckTree != s.Latest.Tree {
			footer += " · OUTDATED"
		}
	}
	if s.Notice != "" {
		footer = safeText(s.Notice)
	}
	rows[height-1] = reviewRow(muted+" "+footer, width)
	return rows
}
