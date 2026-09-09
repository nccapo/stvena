package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/nccapo/stvena/internal/review"
)

func TestHotkeyHelpScrollsAndHitTestsVisibleActions(t *testing.T) {
	for _, height := range []int{1, 5, 12, 30} {
		for _, index := range []int{0, 25, len(review.HotkeyActions) - 1} {
			s := review.State{Help: true, HelpIndex: index, Hotkeys: map[string]string{"a": "r"}}
			rows := renderReview(&s, 60, height, true)
			if len(rows) != height {
				t.Fatal("help exceeded the pane height")
			}
			selected := false
			for row, text := range rows {
				if ansi.StringWidth(text) != 60 {
					t.Fatal("help exceeded the pane width")
				}
				if action := HelpActionAt(&s, height, row); action >= 0 {
					if !strings.Contains(ansi.Strip(text), review.HotkeyActions[action].Name) {
						t.Fatal("click target did not match rendered action")
					}
					selected = selected || action == index
				}
			}
			if height >= 5 && !selected {
				t.Fatal("selected action scrolled out of view")
			}
		}
	}
}

func TestCustomHotkeyLabelsKeepCanonicalClickActions(t *testing.T) {
	s := review.State{Browser: true, Hotkeys: map[string]string{"N": "r", "b": "界", "v": "w", "w": "v"}}
	footer := ansi.Strip(renderReview(&s, 100, 20, true)[19])
	x := strings.Index(footer, "r: next unreviewed")
	if x < 0 || ReviewControlKeyAt(&s, 100, 20, x, 19) != "N" {
		t.Fatal("custom footer label and canonical click action diverged")
	}
	s.Panel = "Context preview"
	if !strings.Contains(panelControlBar(&s), "界: Paste to agent") || PanelControlKeyAt(&s, 100, 20, 1, 19) != "b" {
		t.Fatal("custom panel control did not retain its click action")
	}
	s.Panel, s.Menu = "", true
	if !strings.Contains(strings.Join(renderOverlay(&s, 100, 45), "\n"), "w     Diff / full file") {
		t.Fatal("Actions menu did not show the custom shortcut")
	}
	for _, width := range []int{35, 65, 100, 140} {
		rows := controlRows(width, true, &s)
		layout := NewLayoutOptions(width, 40, 58, false, &s)
		if layout.FooterHeight != len(rows) {
			t.Fatal("layout did not account for custom footer widths")
		}
		found := false
		for y, row := range rows {
			plain := ansi.Strip(row)
			for x := 0; x < width; x++ {
				if ControlKeyAt(width, x, y, true, &s) == "b" {
					found = true
					if !strings.Contains(plain, "界: Paste to agent") {
						t.Fatal("footer hit testing did not use custom label widths")
					}
				}
			}
		}
		if !found {
			t.Fatal("custom footer control has no click target")
		}
	}
}

func TestGlobalShortcutLabelsMatchFooterClickTargets(t *testing.T) {
	for _, width := range []int{35, 80, 140} {
		rows := controlRows(width, false)
		for key, action := range map[string]string{"G": "focus", "]": "new-agent", "N": "next-agent", "P": "previous-agent", "W": "close-agent", "Q": "quit-app"} {
			label := ("Ctrl-" + key) + ":"
			found := false
			for y, row := range rows {
				plain := ansi.Strip(row)
				if x := strings.Index(plain, label); x >= 0 {
					found = true
					if got := ControlKeyAt(width, x, y, false); got != action {
						t.Fatalf("%s clicked %s, want %s", label, got, action)
					}
				}
			}
			if !found {
				t.Fatalf("missing %s at width %d", label, width)
			}
		}
	}
}

func TestFooterNavigationAndCustomGlobalLabels(t *testing.T) {
	s := review.State{FooterFocused: true, Hotkeys: map[string]string{"Ctrl-G": "Ctrl-O"}}
	for _, width := range []int{35, 80, 140} {
		rows := controlRows(width, false, &s)
		if !strings.Contains(ansi.Strip(strings.Join(rows, "\n")), "Ctrl-O: Switch panes") || strings.Contains(ansi.Strip(strings.Join(rows, "\n")), "Ctrl-G:") {
			t.Fatal("footer did not reflect global remapping")
		}
		for index := range FooterControls {
			s.FooterIndex = index
			rows = controlRows(width, false, &s)
			start := FooterStart(width, 2, &s)
			if start >= len(rows) || !strings.Contains(strings.Join(rows[start:min(start+2, len(rows))], ""), "\x1b[7m") {
				t.Fatalf("selection %d invisible at width %d", index, width)
			}
			if next := FooterMove(width, index, "right", &s); next != (index+1)%len(FooterControls) {
				t.Fatal("Right did not traverse footer")
			}
		}
		if FooterMove(width, 0, "up", &s) != -1 {
			t.Fatal("Up did not leave first footer row")
		}
	}
}
