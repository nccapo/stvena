package ui

import "fmt"

// AgentPicker is an app-level overlay so opening a terminal preserves review.
type AgentPicker struct {
	Options []string
	Index   int
	Error   string
}

func renderAgentPicker(rows []string, p *AgentPicker, layout Layout) {
	content := []string{bold + " New agent · choose a command"}
	for i, option := range p.Options {
		style, marker := "", "  "
		if i == p.Index {
			style, marker = focusedBG+bold, "› "
		}
		content = append(content, style+" "+marker+safeText(option)+reset)
	}
	content = append(content, "", " ↑/↓: choose · Enter: start · Esc: cancel",
		" c: Codex · l: Claude Code", "", " Launch command keeps its original arguments.",
		" Codex and Claude Code use their CLI defaults.",
		" All terminals share this repository and review.")
	if p.Error != "" {
		content = append(content, "", red+" "+safeText(p.Error), " Choose another command or Esc to return.")
	}
	for y := 1; y < len(rows); y++ {
		text := ""
		if y-1 < len(content) {
			text = content[y-1]
		}
		rows[y] = reviewRow(text, layout.Width)
	}
	if len(rows) > 1 && len(rows) < len(content)+1 {
		footer := fmt.Sprintf(" %d/%d · ↑/↓ Enter · c: Codex · l: Claude · Esc", p.Index+1, len(p.Options))
		if p.Error != "" {
			footer = " " + safeText(p.Error)
		}
		rows[len(rows)-1] = reviewRow(footer, layout.Width)
	}
}

func AgentPickerChoiceAt(p *AgentPicker, layout Layout, x, y int) int {
	index := y - 2
	if x >= 0 && x < layout.Width && y < layout.Height-1 && index >= 0 && index < len(p.Options) {
		return index
	}
	return -1
}
