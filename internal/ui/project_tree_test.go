package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/nccapo/stvena/internal/diffview"
	"github.com/nccapo/stvena/internal/review"
)

func TestProjectTreeRenderAndSwitchControl(t *testing.T) {
	s := review.State{Source: "project", Browser: true, FullFile: true}
	s.Update(diffview.Snapshot{Files: []diffview.File{{Path: "src/nested/file.go", Scope: diffview.ProjectScope}, {Path: "README.md", Scope: diffview.ProjectScope}}})
	for _, width := range []int{10, 35, 70, 120} {
		rows := renderReview(&s, width, 20, true)
		for _, row := range rows {
			if ansiWidth(row) != width {
				t.Fatal("tree overflowed the pane")
			}
		}
	}
	initial := ansi.Strip(strings.Join(renderReview(&s, 70, 20, true), "\n"))
	if !strings.Contains(initial, "▸ src/") || strings.Contains(initial, "file.go") {
		t.Fatal("tree did not start collapsed")
	}
	s.Key("right", 10)
	s.Key("right", 10)
	s.Key("right", 10)
	expanded := ansi.Strip(strings.Join(renderReview(&s, 70, 20, true), "\n"))
	if !strings.Contains(expanded, "▾ src/") || !strings.Contains(expanded, "▾ nested/") || !strings.Contains(expanded, "file.go") {
		t.Fatal("tree did not show nested children")
	}
	for _, focused := range []bool{false, true} {
		rows := controlRows(100, focused)
		if !strings.Contains(ansi.Strip(rows[0]), "Ctrl-G: Switch panes") {
			t.Fatal("ambiguous focus shortcut label")
		}
		if ControlKeyAt(100, 15, 0, focused) != "focus" {
			t.Fatal("switch label and click target diverged")
		}
	}
}
