package ui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/charmbracelet/x/vt"
	"github.com/nccapo/stvena/internal/review"
)

func TestAgentPickerVisibleAtNarrowAndWideSizes(t *testing.T) {
	for _, width := range []int{80, 140} {
		layout := NewLayout(width, 24)
		terminal := vt.NewEmulator(layout.LeftWidth, layout.LeftHeight)
		defer terminal.Close()
		picker := &AgentPicker{Options: []string{"Launch command: codex --model custom", "Codex", "Claude Code"}, Index: 2, Error: "claude not found"}
		var out bytes.Buffer
		Render(&out, terminal, &review.State{}, layout, false, true, picker)
		display := vt.NewEmulator(width, 24)
		defer display.Close()
		_, _ = display.Write(out.Bytes())
		var screen strings.Builder
		for y := 0; y < 24; y++ {
			for x := 0; x < width; x++ {
				if cell := display.CellAt(x, y); cell != nil {
					screen.WriteString(cell.Content)
				}
			}
			screen.WriteByte('\n')
		}
		for _, want := range []string{"New agent", "Launch command: codex --model custom", "› Claude Code", "Enter: start", "Esc: cancel", "claude not found"} {
			if !strings.Contains(screen.String(), want) {
				t.Fatalf("width %d missing %q:\n%s", width, want, screen.String())
			}
		}
		if strings.Contains(out.String(), "\x1b[?25h") {
			t.Fatal("agent cursor visible through chooser")
		}
		if AgentPickerChoiceAt(picker, layout, 4, 4) != 2 || AgentPickerChoiceAt(picker, layout, 4, 1) != -1 {
			t.Fatal("chooser hit testing disagrees with visible rows")
		}
	}
}
