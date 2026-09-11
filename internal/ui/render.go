package ui

import (
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
	"github.com/nccapo/stvena/internal/review"
)

const (
	reset     = "\x1b[0m"
	dim       = "\x1b[2m"
	bold      = "\x1b[1m"
	red       = "\x1b[38;5;203m"
	green     = "\x1b[38;5;114m"
	cyan      = "\x1b[38;5;81m"
	yellow    = "\x1b[38;5;220m"
	muted     = "\x1b[38;5;244m"
	headerBG  = "\x1b[48;5;236m"
	focusedBG = "\x1b[48;5;238m"
)

// Layout keeps the child PTY and diff usable at both wide and narrow sizes.
type Layout struct {
	Width, Height         int
	LeftX, LeftY          int
	LeftWidth, LeftHeight int
	DiffX, DiffY          int
	DiffWidth, DiffHeight int
	Vertical              bool
	FooterY, FooterHeight int
	AgentRows             int
}

func NewLayout(width, height int) Layout { return NewLayoutOptions(width, height, 58, false) }

func NewLayoutOptions(width, height, ratio int, fullscreen bool, state ...*review.State) Layout {
	width, height = max(1, width), max(1, height)
	l := Layout{Width: width, Height: height, LeftY: 1}
	l.FooterHeight = min(len(controlRows(width, false, state...)), max(0, height-2))
	l.FooterY = height - l.FooterHeight
	if len(state) > 0 && state[0] != nil && len(state[0].Agents) > 0 {
		l.AgentRows = min(2, len(state[0].Agents), max(0, l.FooterY-3))
	}
	top := 1 + l.AgentRows
	l.LeftY = top
	contentHeight := max(1, l.FooterY-top)
	if fullscreen {
		l.DiffY = top
		l.DiffWidth = width
		l.DiffHeight = contentHeight
		return l
	}
	if width >= 100 {
		l.LeftWidth = max(40, width*min(75, max(25, ratio))/100)
		l.LeftHeight = contentHeight
		l.DiffX = l.LeftWidth + 1
		l.DiffY = top
		l.DiffWidth = max(1, width-l.DiffX)
		l.DiffHeight = contentHeight
		return l
	}
	l.Vertical = true
	l.LeftWidth = width
	l.LeftHeight = max(1, contentHeight*2/3)
	l.DiffY = l.LeftY + l.LeftHeight + 1
	l.DiffWidth = width
	l.DiffHeight = max(1, l.FooterY-l.DiffY)
	return l
}

// Render redraws one frame and returns the physical cursor position.
func Render(w io.Writer, terminal *vt.Emulator, state *review.State, layout Layout, diffFocused, cursorVisible bool, picker *AgentPicker) (int, int) {
	rows := make([]string, max(1, layout.Height))
	for i := range rows {
		rows[i] = strings.Repeat(" ", max(0, layout.Width))
	}
	focus := "AGENT"
	if diffFocused {
		focus = "REVIEW"
	}
	if state.AgentLabel != "" {
		focus += " · " + safeText(state.AgentLabel)
	}
	header := headerBG + bold + " Stvena " + reset + headerBG + "  " + focus + "  · " + safeText(state.AgentStatus) + reset
	if len(state.Agents) > 0 {
		pane := "AGENT"
		if diffFocused {
			pane = "REVIEW"
		}
		header = headerBG + bold + " Stvena " + reset + " · " + pane + " · " + agentOverview(state)
	}
	if len(state.Attachments) > 0 {
		header += cyan + fmt.Sprintf(" · Context: %d (B)", len(state.Attachments)) + reset
	}
	if state.CheckStatus != "" {
		header += muted + " · Checks: " + safeText(state.CheckStatus)
		if state.CheckTree != state.Latest.Tree {
			header += yellow + " (outdated)"
		}
		header += reset
	}
	rows[0] = reviewRow(header, layout.Width)
	for y, row := range agentRows(state, layout.Width) {
		if y >= layout.AgentRows {
			break
		}
		rows[y+1] = reviewRow(row, layout.Width)
	}

	for y := 0; y < layout.LeftHeight && layout.LeftY+y < layout.FooterY; y++ {
		rows[layout.LeftY+y] = renderTerminalRow(terminal, y, layout.LeftWidth)
	}
	cursor := terminal.CursorPosition()

	if layout.Vertical {
		separatorY := layout.DiffY - 1
		if separatorY >= 0 && separatorY < layout.FooterY {
			rows[separatorY] = muted + strings.Repeat("─", layout.Width) + reset
		}
	} else if layout.LeftWidth > 0 {
		for y := layout.LeftY; y < layout.FooterY; y++ {
			rows[y] = overlay(rows[y], layout.LeftWidth, muted+"│"+reset, layout.Width)
		}
	}

	diffRows := renderReview(state, layout.DiffWidth, layout.DiffHeight, diffFocused)
	for y, row := range diffRows {
		targetY := layout.DiffY + y
		if targetY >= layout.FooterY {
			break
		}
		rows[targetY] = overlay(rows[targetY], layout.DiffX, row, layout.Width)
	}

	controls := controlRows(layout.Width, diffFocused, state)
	if state.FooterFocused {
		target := "agent"
		if diffFocused {
			target = "review"
		}
		text := " BOTTOM PANEL"
		if len(state.Agents) > 0 {
			text += " · " + agentOverview(state)
		}
		rows[0] = reviewRow(headerBG+bold+text+" · Arrows: move · Enter: select · Esc: "+target, layout.Width)
		cursorVisible = false
	}
	footerStart := FooterStart(layout.Width, layout.FooterHeight, state)
	for y := 0; y < layout.FooterHeight; y++ {
		text := ""
		if footerStart+y < len(controls) {
			text = controls[footerStart+y]
		}
		rows[layout.FooterY+y] = reviewRow(headerBG+text, layout.Width)
	}

	if picker != nil {
		renderAgentPicker(rows, picker, layout)
		cursorVisible = false
	}

	var out strings.Builder
	out.WriteString("\x1b[?25l")
	for y, row := range rows {
		fmt.Fprintf(&out, "\x1b[%d;1H%s\x1b[0m\x1b[K", y+1, row)
	}
	physicalX, physicalY := 0, 0
	if !diffFocused && cursorVisible {
		physicalX = layout.LeftX + cursor.X
		physicalY = layout.LeftY + cursor.Y
		if physicalX < layout.Width && physicalY < layout.FooterY {
			fmt.Fprintf(&out, "\x1b[%d;%dH\x1b[?25h", physicalY+1, physicalX+1)
		}
	}
	_, _ = io.WriteString(w, out.String())
	return physicalX, physicalY
}

func renderTerminalRow(terminal *vt.Emulator, y, width int) string {
	var b strings.Builder
	b.WriteString(reset)
	lastStyle := ""
	for x := 0; x < width; {
		cell := terminal.CellAt(x, y)
		if cell == nil {
			b.WriteString(reset + " ")
			lastStyle = ""
			x++
			continue
		}
		style := cell.Style.String()
		if style != lastStyle {
			b.WriteString(reset)
			b.WriteString(style)
			lastStyle = style
		}
		content := cell.Content
		if content == "" {
			content = " "
		}
		if x+max(1, cell.Width) > width {
			b.WriteString(" ")
			x++
			continue
		}
		b.WriteString(content)
		x += max(1, cell.Width)
	}
	b.WriteString(reset)
	return b.String()
}

func diffStyle(line string) string {
	switch {
	case strings.HasPrefix(line, "diff --git "):
		return bold
	case strings.HasPrefix(line, "@@"):
		return cyan
	case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
		return green
	case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
		return red
	case strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---"):
		return yellow
	default:
		return ""
	}
}

func fitANSI(s string, width int) string {
	width = max(0, width)
	if ansiWidth(s) > width {
		s = ansi.Truncate(s, width, "") + reset
	}
	return s + strings.Repeat(" ", max(0, width-ansiWidth(s)))
}
func ansiWidth(s string) int { return ansi.StringWidth(s) }

func overlay(base string, x int, content string, width int) string {
	left := fitANSI(base, x)
	return fitANSI(left+content, width)
}

// Keep the review shortcuts visible across the full terminal width, wrapping
// whole shortcuts onto additional footer rows on narrower terminals.
func controlRows(width int, diffFocused bool, state ...*review.State) []string {
	controls := footerControls(state...)
	var rows []string
	row := " "
	for i, control := range controls {
		gap := ""
		if ansiWidth(row) > 1 {
			gap = "  "
		}
		if ansiWidth(row)+len(gap)+ansiWidth(control) > width && ansiWidth(row) > 1 {
			rows = append(rows, row)
			row, gap = " ", ""
		}
		separator := strings.LastIndex(control, ":")
		key, label := control[:separator], control[separator+1:]
		style := cyan + bold
		if !diffFocused && !review.IsGlobalHotkey(FooterControls[i].Key) && FooterControls[i].Key != "F6" {
			style = muted
		}
		if len(state) > 0 && state[0] != nil && state[0].FooterFocused && state[0].FooterIndex == i {
			row += gap + "\x1b[7m" + bold + key + ":" + label + reset + headerBG
		} else {
			row += gap + style + key + reset + headerBG + ":" + label
		}
	}
	return append(rows, row)
}

type FooterControl struct{ Key, Action, Label string }

var FooterControls = []FooterControl{
	{"Ctrl-G", "focus", "Switch panes"}, {"Ctrl-]", "new-agent", "New agent"},
	{"Ctrl-N", "next-agent", "Next agent"}, {"Ctrl-P", "previous-agent", "Previous agent"},
	{"Ctrl-W", "close-agent", "Close agent"}, {"Ctrl-Y", "next-attention", "Next attention"},
	{"1", "1", "Changes"}, {"2", "2", "Workspace"}, {"3", "3", "Files"}, {"4", "4", "Branch"},
	{"B", "B", "Context"}, {"b", "b", "Paste to agent"}, {"T", "T", "Checks"}, {"L", "L", "Timeline"},
	{"K", "K", "Checkpoint"}, {"a", "a", "Actions"}, {"F", "F", "Expand"},
	{"?", "?", "Configuration"}, {"Ctrl-Q", "quit-app", "Quit all"}, {"F6", "footer", "Bottom panel"},
}

func footerControls(state ...*review.State) []string {
	controls := make([]string, len(FooterControls))
	for i, control := range FooterControls {
		key := control.Key
		if len(state) > 0 && state[0] != nil {
			key = state[0].Binding(key)
		}
		controls[i] = review.KeyLabel(key) + ": " + control.Label
	}
	return controls
}

type footerPosition struct{ x, y, width int }

func footerPositions(width int, state ...*review.State) []footerPosition {
	positions := make([]footerPosition, len(FooterControls))
	row, column := 0, 1
	for i, label := range footerControls(state...) {
		gap := 0
		if column > 1 {
			gap = 2
		}
		if column+gap+ansiWidth(label) > width && column > 1 {
			row++
			column, gap = 1, 0
		}
		column += gap
		positions[i] = footerPosition{column, row, ansiWidth(label)}
		column += ansiWidth(label)
	}
	return positions
}

// FooterMove follows visual rows when the footer wraps. -1 returns to the pane.
func FooterMove(width, index int, key string, state *review.State) int {
	positions := footerPositions(width, state)
	index = min(max(0, index), len(positions)-1)
	switch key {
	case "left":
		return (index + len(positions) - 1) % len(positions)
	case "right", "tab":
		return (index + 1) % len(positions)
	case "home":
		return 0
	case "end":
		return len(positions) - 1
	case "up", "down":
		row := positions[index].y + 1
		if key == "up" {
			row = positions[index].y - 1
		}
		if row < 0 {
			return -1
		}
		best, distance := index, int(^uint(0)>>1)
		for i, p := range positions {
			delta := p.x - positions[index].x
			if delta < 0 {
				delta = -delta
			}
			if p.y == row && delta < distance {
				best, distance = i, delta
			}
		}
		return best
	}
	return index
}

// FooterStart keeps keyboard selection visible when only a few rows fit.
func FooterStart(width, height int, state *review.State) int {
	if state == nil || !state.FooterFocused {
		return 0
	}
	positions := footerPositions(width, state)
	index := min(max(0, state.FooterIndex), len(positions)-1)
	return max(0, positions[index].y-height+1)
}

func ControlKeyAt(width, x, y int, _ bool, state ...*review.State) string {
	for i, p := range footerPositions(width, state...) {
		if p.y == y && x >= p.x && x < p.x+p.width {
			return FooterControls[i].Action
		}
	}
	return ""
}

// Give review its own readable dark surface without changing the agent's
// default foreground/background or any of its native terminal attributes.
func reviewRow(text string, width int) string {
	base := "\x1b[48;5;234m\x1b[38;5;252m"
	return base + strings.ReplaceAll(fitANSI(text, width), reset, reset+base) + reset
}
