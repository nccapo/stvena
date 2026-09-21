package app

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestAgentTerminalResizeDuringScrollSequence(t *testing.T) {
	// An IDE panel change can shrink a 21-row terminal to 15 rows while a
	// margin-setting escape sequence is only partly received from the PTY.
	sequence := "\x1b[1;21r\x1b[H\x1bM"
	for split := 0; split <= len(sequence); split++ {
		t.Run(fmt.Sprint(split), func(t *testing.T) {
			a := newAgentTerminal("test", 80, 21)
			t.Cleanup(func() { _ = a.virtual.Close() })
			_, _ = a.virtual.Write([]byte(sequence[:split]))
			a.virtual.Resize(50, 15)
			_, _ = a.virtual.Write([]byte(sequence[split:] + "\x1b[Halive"))
			for x, want := range "alive" {
				if cell := a.virtual.CellAt(x, 0); cell == nil || cell.Content != string(want) {
					t.Fatalf("cell %d = %v, want %q", x, cell, want)
				}
			}
		})
	}
}

func TestAgentTerminalScrollAfterResize(t *testing.T) {
	for _, alternate := range []bool{false, true} {
		for _, horizontal := range []bool{false, true} {
			for _, operation := range []string{"\x1bM", "\x1b[L", "\x1b[M", "\x1b[S", "\x1b[T"} {
				t.Run(fmt.Sprintf("alternate=%t/horizontal=%t/operation=%q", alternate, horizontal, operation), func(t *testing.T) {
					a := newAgentTerminal("test", 80, 40)
					t.Cleanup(func() { _ = a.virtual.Close() })
					write := func(data string) {
						t.Helper()
						if _, err := a.virtual.Write([]byte(data)); err != nil {
							t.Fatal(err)
						}
					}
					if alternate {
						write("\x1b[?1049h")
					}
					if horizontal {
						write("\x1b[?69h")
					}
					a.virtual.Resize(50, 26)
					// Output queued before the resize still uses the old dimensions.
					margins := "\x1b[1;40r"
					if horizontal {
						margins = "\x1b[1;80s"
					}
					// PTY reads can split an escape sequence at any byte.
					for i := range margins {
						write(margins[i : i+1])
					}
					write("\x1b[H" + operation + "alive")
					for x, want := range "alive" {
						cell := a.virtual.CellAt(x, 0)
						if cell == nil || cell.Content != string(want) {
							t.Fatalf("cell %d = %v, want %q", x, cell, want)
						}
					}
				})
			}
		}
	}
}

func TestAgentTerminalScrollMarginsPreserveContent(t *testing.T) {
	for _, tc := range []struct {
		name, sequence string
		want           [5]string
	}{
		{
			name:     "stale bottom is clamped",
			sequence: "\x1b[2;99r\x1b[2;1H\x1bM",
			want:     [5]string{"AAAAAAAA", "        ", "BBBBBBBB", "CCCCCCCC", "DDDDDDDD"},
		},
		{
			name:     "stale right is clamped",
			sequence: "\x1b[?69h\x1b[3;99s\x1b[1;3H\x1bM",
			want:     [5]string{"AA      ", "BBAAAAAA", "CCBBBBBB", "DDCCCCCC", "EEDDDDDD"},
		},
		{
			name:     "valid margins retain outside rows",
			sequence: "\x1b[2;4r\x1b[2;1H\x1bM",
			want:     [5]string{"AAAAAAAA", "        ", "BBBBBBBB", "CCCCCCCC", "EEEEEEEE"},
		},
		{
			name:     "missing defaults reset margins",
			sequence: "\x1b[2;4r\x1b[r\x1b[H\x1bM",
			want:     [5]string{"        ", "AAAAAAAA", "BBBBBBBB", "CCCCCCCC", "DDDDDDDD"},
		},
		{
			name:     "zero defaults reset margins",
			sequence: "\x1b[?69h\x1b[3;6s\x1b[0;0s\x1b[2;4r\x1b[0;0r\x1b[H\x1bM",
			want:     [5]string{"        ", "AAAAAAAA", "BBBBBBBB", "CCCCCCCC", "DDDDDDDD"},
		},
		{
			name:     "out of bounds start leaves previous margins",
			sequence: "\x1b[2;4r\x1b[50;99r\x1b[2;1H\x1bM",
			want:     [5]string{"AAAAAAAA", "        ", "BBBBBBBB", "CCCCCCCC", "EEEEEEEE"},
		},
		{
			name:     "cursor save still works with margin mode off",
			sequence: "\x1b[3;4H\x1b[s\x1b[H\x1b8X",
			want:     [5]string{"AAAAAAAA", "BBBBBBBB", "CCCXCCCC", "DDDDDDDD", "EEEEEEEE"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newAgentTerminal("test", 8, 5)
			t.Cleanup(func() { _ = a.virtual.Close() })
			for y, row := range []string{"AAAAAAAA", "BBBBBBBB", "CCCCCCCC", "DDDDDDDD", "EEEEEEEE"} {
				_, _ = a.virtual.Write([]byte(fmt.Sprintf("\x1b[%d;1H%s", y+1, row)))
			}
			_, _ = a.virtual.Write([]byte(tc.sequence))
			for y, row := range tc.want {
				for x, want := range row {
					cell := a.virtual.CellAt(x, y)
					if cell == nil || cell.Content != string(want) {
						t.Fatalf("cell (%d,%d) = %v, want %q", x, y, cell, want)
					}
				}
			}
		})
	}
}

func agentLines(t *testing.T, a *agentTerminal, count int) {
	t.Helper()
	for i := range count {
		a.write([]byte(fmt.Sprintf("line %d\r\n", i)))
	}
	if a.virtual.ScrollbackLen() == 0 {
		t.Fatal("output never reached scrollback")
	}
}

// topRow is the text the pane shows on its first row for the current view.
func topRow(a *agentTerminal) string {
	line := a.virtual.ScrollbackLen() - a.scrollback
	var b strings.Builder
	for x := range 16 {
		if cell := a.virtual.ScrollbackCellAt(x, line); cell != nil {
			b.WriteString(cell.Content)
		}
	}
	return strings.TrimSpace(b.String())
}

func TestAgentPaneScrollsWithWheelAndShiftPaging(t *testing.T) {
	s := terminalState(t)
	a := s.activeAgent()
	agentLines(t, a, s.layout.LeftHeight*3)
	// Wheel reports arrive as input: the agent pane must not forward them.
	wheel := func(button, x, y int) {
		s.handleInput([]byte(fmt.Sprintf("\x1b[<%d;%d;%dM", button, x+1, y+1)), s.agentInput)
	}
	wheel(64, s.layout.LeftX, s.layout.LeftY)
	if a.scrollback != wheelLines || s.review.AgentScroll != a.scrollback {
		t.Fatalf("wheel up: scrollback=%d review=%d", a.scrollback, s.review.AgentScroll)
	}
	wheel(65, s.layout.LeftX, s.layout.LeftY)
	if a.scrollback != 0 {
		t.Fatalf("wheel down did not return to the live screen: %d", a.scrollback)
	}
	// The review pane keeps its own wheel handling.
	wheel(64, s.layout.DiffX+1, s.layout.DiffY+1)
	if a.scrollback != 0 {
		t.Fatalf("review wheel moved the terminal: %d", a.scrollback)
	}
	s.handleInput([]byte("\x1b[5;2~"), s.agentInput)
	if a.scrollback != s.agentPage() {
		t.Fatalf("Shift-PageUp scrolled %d lines, want %d", a.scrollback, s.agentPage())
	}
	s.handleInput([]byte("\x1b[6;2~"), s.agentInput)
	if a.scrollback != 0 {
		t.Fatalf("Shift-PageDown left the view at %d", a.scrollback)
	}
	if got := a.input.(*bytes.Buffer).String(); got != "" {
		t.Fatalf("scroll keys reached the CLI: %q", got)
	}
}

func TestAgentScrollKeepsItsContentAndResumesOnTyping(t *testing.T) {
	s := terminalState(t)
	a := s.activeAgent()
	agentLines(t, a, s.layout.LeftHeight*3)
	s.scrollAgent(1 << 20)
	if a.scrollback != a.virtual.ScrollbackLen() {
		t.Fatalf("scrolling past the oldest line: %d of %d", a.scrollback, a.virtual.ScrollbackLen())
	}
	if topRow(a) == "" {
		t.Fatal("the oldest row is empty")
	}
	s.scrollAgent(-5)
	before := topRow(a)
	// Fresh output pushes the screen into scrollback; the view stays put.
	a.write([]byte("working\r\nstill working\r\n"))
	if got := topRow(a); got != before {
		t.Fatalf("view drifted to %q, want %q", got, before)
	}
	s.syncAgent()
	if s.review.AgentScroll != a.scrollback {
		t.Fatalf("review scroll=%d, terminal=%d", s.review.AgentScroll, a.scrollback)
	}
	s.handleInput([]byte("hello"), s.agentInput)
	if a.scrollback != 0 || s.review.AgentScroll != 0 {
		t.Fatalf("typing did not resume the live screen: %d/%d", a.scrollback, s.review.AgentScroll)
	}
	if got := a.input.(*bytes.Buffer).String(); got != "hello" {
		t.Fatalf("CLI received %q", got)
	}
}

func TestAlternateScreenAgentHasNoScrollback(t *testing.T) {
	s := terminalState(t)
	a := s.activeAgent()
	agentLines(t, a, s.layout.LeftHeight*2)
	a.write([]byte("\x1b[?1049h"))
	s.scrollAgent(4)
	if a.scrollback != 0 {
		t.Fatalf("alternate screen scrolled to %d", a.scrollback)
	}
	if s.review.Notice == "" {
		t.Fatal("no explanation for a terminal that cannot scroll")
	}
	a.write([]byte("\x1b[?1049l"))
	s.scrollAgent(4)
	if a.scrollback != 4 {
		t.Fatalf("scrolling stayed off after the full-screen view: %d", a.scrollback)
	}
	// A CLI that switches back to its own view must not keep the old offset.
	a.write([]byte("\x1b[?1049h"))
	if a.scrollback != 0 || !a.altScreen {
		t.Fatalf("view kept its offset in the alternate screen: %d", a.scrollback)
	}
}

func TestClickFocusesThePaneItLandsIn(t *testing.T) {
	s := terminalState(t)
	click := func(x, y int) {
		s.handleInput([]byte(fmt.Sprintf("\x1b[<0;%d;%dM", x+1, y+1)), s.agentInput)
	}
	click(s.layout.DiffX+1, s.layout.DiffY+1)
	if !s.diffFocused {
		t.Fatal("clicking review did not move focus there")
	}
	click(s.layout.LeftX, s.layout.LeftY)
	if s.diffFocused {
		t.Fatal("clicking the agent did not return focus to it")
	}
	if got := s.activeAgent().input.(*bytes.Buffer).String(); got != "" {
		t.Fatalf("clicks reached the CLI: %q", got)
	}
	// An open prompt keeps review's focus until it is answered.
	s.diffFocused, s.review.Prompt = true, "Search"
	click(s.layout.LeftX, s.layout.LeftY)
	if !s.diffFocused {
		t.Fatal("a click abandoned an open prompt")
	}
}
