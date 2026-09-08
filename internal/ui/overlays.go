package ui

import (
	"fmt"
	"github.com/charmbracelet/x/ansi"
	"github.com/nccapo/stvena/internal/review"
	"strings"
)

func renderOverlay(s *review.State, width, height int) []string {
	var content []string
	title, footer := "", " Esc: back"
	switch {
	case s.ConfirmAction != "":
		title = "Confirm Git action"
		content = []string{"", safeText(s.ConfirmDetail), "", "Enter confirms · Esc cancels", "", "Only the staging index will change."}
		if s.ConfirmAction == "finish-checkpoint" {
			title = "Finish checkpoint · review incomplete"
			content = []string{"", safeText(s.ConfirmDetail), "", "Esc: return to review", "Enter: preview draft anyway", "", "Previewing does not submit anything to the agent."}
		}
	case s.Prompt != "":
		title = s.Prompt
		content = []string{"", cyan + horizontalText(safeText(s.Input), max(0, ansiWidth(safeText(s.Input))-max(1, width-4))) + "▏", "", "Enter: submit · Esc: cancel"}
		if s.Prompt == "Run checks" {
			content = append(content, "", "Runs in a temporary copy of captured code.", "Ignored dependencies are not copied.", "Example: npm ci && npm test", "Commands have your normal user permissions.")
		}
		if s.Prompt == "Context request" {
			content = append(content, "", fmt.Sprintf("%d attachments · Enter saves the request", len(s.Attachments)), "Review the assembled draft before pasting.")
		}
		if s.Prompt == "Add comment" {
			if f := s.Current(); f != nil {
				content = append(content, "", safeText(f.Path), safeText(s.SelectedText()))
			}
		}
	case s.Menu:
		title = "Actions · choose with ↑/↓, then Enter"
		footer = " Type a shortcut · Esc: back"
		count := max(1, height-4)
		start := min(max(0, s.MenuIndex-count/2), max(0, len(review.Actions)-count))
		for i := start; i < min(len(review.Actions), start+count); i++ {
			a := review.Actions[i]
			key := a.Key
			if key == " " {
				key = "Space"
			}
			style := ""
			marker := "  "
			if i == s.MenuIndex {
				style = focusedBG + bold
				marker = "› "
			}
			content = append(content, style+fmt.Sprintf("%s%-5s %s", marker, key, a.Name)+reset)
		}
		content = append(content, "", muted+review.Actions[s.MenuIndex].Hint)
	case s.Panel == "Comments":
		title = fmt.Sprintf("Review comments · %d", len(s.Comments))
		footer = " ↑/↓: scroll · E: copy feedback · Esc: back"
		if len(s.Comments) == 0 {
			content = []string{"No comments yet.", "Open a file, select a source line and press c.", "V starts a line range selection."}
		}
		for _, c := range s.Comments {
			status := ""
			if s.CommentStale(c) {
				status = " · OUTDATED"
			}
			side := "new"
			if c.Old {
				side = "old"
			}
			content = append(content, cyan+fmt.Sprintf("%s:%d-%d (%s)%s", safeText(c.Path), c.Start, c.End, side, status), safeText(c.Text))
			for _, line := range strings.Split(c.Code, "\n") {
				content = append(content, muted+"  "+safeText(line))
			}
			content = append(content, "")
		}
	case s.Panel == "Context":
		title = fmt.Sprintf("Context · %d attachments", len(s.Attachments))
		footer = " Enter: preview · i: request · d: remove · b: paste · Esc: back"
		start, count := PanelListWindow(s.TrayIndex, len(s.Attachments), height)
		for i := start; i < start+count; i++ {
			a := s.Attachments[i]
			style, marker := "", "  "
			if i == s.TrayIndex {
				style, marker = focusedBG+bold, "› "
			}
			freshness := ""
			if s.LiveTree != "" && a.Tree != "" && s.LiveTree != a.Tree {
				freshness = " · earlier snapshot"
			}
			content = append(content, style+marker+safeText(a.Label)+muted+freshness+reset)
		}
		if len(s.Attachments) == 0 {
			content = append(content, "Select code and press x to collect it.", "3: browse all project files · Esc: back")
		}
		content = append(content, "", cyan+"Your request", safeText(s.ContextQuestion))
		if s.ContextQuestion == "" {
			content = append(content, muted+"i: add a question for the agent")
		}
		content = append(content, "", muted+"Attachments stay saved after pasting.")
	case s.Panel == "Context preview":
		title = "Context · draft preview"
		footer = " ↑/↓: scroll · b: paste to agent · y: copy · Esc: tray"
		message, err := s.ContextMessage()
		if err != nil {
			content = []string{err.Error()}
		} else {
			for _, line := range strings.Split(message, "\n") {
				content = append(content, strings.Split(ansi.Hardwrap(safeText(line), max(1, width-2), true), "\n")...)
			}
		}
	case s.Panel == "Checkpoint draft":
		title = "Review checkpoint · draft preview"
		if s.CheckpointNewer() {
			title += " · NEW LIVE"
		}
		footer = " b: paste / export · y: copy · Esc: return to review"
		if s.Checkpoint != nil {
			content = []string{"b prepares agent input without submitting.", "Standalone review copies the draft instead.", "P resumes live after returning to review.", ""}
			for _, line := range strings.Split(s.Checkpoint.Draft, "\n") {
				content = append(content, strings.Split(ansi.Hardwrap(safeText(line), max(1, width-2), true), "\n")...)
			}
		}
	case s.Panel == "Problems":
		title = fmt.Sprintf("Problems · %d locations · %s", len(s.Problems), s.CheckStatus)
		if s.LiveTree != "" && s.CheckTree != s.LiveTree {
			title += " · earlier snapshot"
		}
		footer = " Enter: source · x: collect failure · t: rerun · Esc: logs"
		start, count := PanelListWindow(s.ProblemIndex, len(s.Problems), height)
		for i := start; i < start+count; i++ {
			p := s.Problems[i]
			style, marker := "", "  "
			if i == s.ProblemIndex {
				style, marker = focusedBG+bold, "› "
			}
			content = append(content, style+marker+fmt.Sprintf("%s:%d", safeText(p.Path), p.Line)+reset+muted+"  "+safeText(p.Message))
		}
		if len(s.Problems) == 0 {
			content = []string{"No recognized source locations.", "Esc: read the complete check output."}
		}
	case s.Panel == "Checks":
		footer = " o: Problems · t: run / rerun · Esc: back"
		freshness := "captured version"
		if s.CheckTree != s.LiveTree && s.LiveTree != "" {
			freshness = "OUTDATED · code changed"
		}
		title = "Checks · " + s.CheckStatus + " · " + freshness
		if s.CheckStatus == "" {
			title = "Checks"
			content = []string{"No checks run yet.", "Press Esc, then t to run a command."}
		} else {
			for _, line := range s.CheckLines {
				content = append(content, safeText(line))
			}
		}
	}
	var rows []string
	add := func(t string) { rows = append(rows, reviewRow(t, width)) }
	add(headerBG + bold + " " + title)
	start := 0
	if s.Panel != "" && s.Panel != "Context" && s.Panel != "Problems" {
		start = min(s.PanelScroll, max(0, len(content)-max(1, height-2)))
		s.PanelScroll = start
	}
	for _, line := range content[start:] {
		if len(rows) >= height-1 {
			break
		}
		add(" " + line)
	}
	for len(rows) < height-1 {
		add("")
	}
	if height > 1 {
		if s.Prompt == "" && s.ConfirmAction == "" && !s.Menu && len(panelControls(s)) > 0 {
			footer = panelControlBar(s)
		}
		if s.Notice != "" {
			footer = " " + safeText(s.Notice)
		}
		add(muted + footer)
	}
	return rows[:min(height, len(rows))]
}

// Shared by list rendering and mouse hit testing; leaves space for detail text.
func PanelListWindow(index, total, height int) (int, int) {
	count := min(total, max(0, height-7))
	start := min(max(0, index-count/2), max(0, total-count))
	return start, count
}
