package ui

import (
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
	"github.com/nccapo/stvena/internal/checks"
	"github.com/nccapo/stvena/internal/diffview"
	"github.com/nccapo/stvena/internal/review"
	"strings"
	"testing"
)

func TestLayoutUsesSideBySideWhenWide(t *testing.T) {
	layout := NewLayout(140, 40)
	if layout.Vertical {
		t.Fatal("wide layout should be side-by-side")
	}
	if layout.LeftWidth+1+layout.DiffWidth != 140 {
		t.Fatalf("columns do not add up: %#v", layout)
	}
}

func TestLayoutStacksWhenNarrow(t *testing.T) {
	layout := NewLayout(80, 30)
	if !layout.Vertical {
		t.Fatal("narrow layout should be stacked")
	}
	if layout.LeftWidth != 80 || layout.DiffWidth != 80 {
		t.Fatalf("stacked panes should use full width: %#v", layout)
	}
}

func TestANSIWidth(t *testing.T) {
	if got := ansiWidth("\x1b[31mhello\x1b[0m"); got != 5 {
		t.Fatalf("ansiWidth = %d, want 5", got)
	}
}

func TestOverlayPreservesBaseColor(t *testing.T) {
	got := overlay("\x1b[31mleft\x1b[0m", 4, "right", 9)
	if !strings.HasPrefix(got, "\x1b[31m") {
		t.Fatalf("overlay dropped base styling: %q", got)
	}
	if ansiWidth(got) != 9 {
		t.Fatalf("overlay width = %d, want 9", ansiWidth(got))
	}
}

func TestReviewRenderIncludesStatsNumbersAndNoContentEscapes(t *testing.T) {
	var s review.State
	s.Update(diffview.Snapshot{FileCount: 1, Added: 1, Deleted: 1, Branch: "main", Files: []diffview.File{{Path: "file.go", Scope: diffview.Unstaged, Status: "M", Added: 1, Deleted: 1, Lines: []string{"@@ -8 +9 @@", "-old", "+new\x1b[2J"}}}})
	rows := renderReview(&s, 60, 15, true)
	joined := strings.Join(rows, "\n")
	for _, want := range []string{"1 file", "+1 -1", "file.go", "    8", "    9", "new�[2J"} {
		if !strings.Contains(ansi.Strip(joined), want) {
			t.Errorf("render missing %q: %s", want, joined)
		}
	}
	if strings.Contains(joined, "\x1b[2J") {
		t.Fatal("repository content injected terminal command")
	}
	if len(rows) != 15 {
		t.Fatalf("wrong height: %d", len(rows))
	}
	for _, row := range rows {
		if ansiWidth(row) != 60 {
			t.Fatalf("row width %d: %q", ansiWidth(row), row)
		}
	}
}
func TestRenderFitsTinyAndWideTerminals(t *testing.T) {
	for _, size := range [][2]int{{1, 1}, {10, 3}, {40, 6}, {80, 24}, {140, 40}} {
		layout := NewLayout(size[0], size[1])
		terminal := vt.NewEmulator(layout.LeftWidth, layout.LeftHeight)
		var out strings.Builder
		Render(&out, terminal, &review.State{}, layout, true, true, nil)
		if out.Len() == 0 {
			t.Fatal("empty render")
		}
		for _, row := range renderReview(&review.State{}, size[0], size[1], false) {
			if ansiWidth(row) != size[0] {
				t.Errorf("invalid row width for %v: %q", size, row)
			}
		}
	}
}
func TestWideCharacterClippingKeepsPaneAligned(t *testing.T) {
	if got := ansiWidth(fitANSI("a界", 2)); got != 2 {
		t.Fatalf("clipped row width=%d", got)
	}
	if got := horizontalText("a界z", 2); got != " z" {
		t.Fatalf("wide horizontal clip=%q", got)
	}
}

func TestAgentRGBAndStylesSurviveFrameRendering(t *testing.T) {
	layout := NewLayout(140, 30)
	agent := vt.NewEmulator(layout.LeftWidth, layout.LeftHeight)
	// RGB components that the previous parser interpreted as red/yellow BG and
	// reset commands, followed by attributes that the old renderer discarded.
	input := "\x1b[38;2;41;43;49mRGB\x1b[0m plain\r\n" +
		"\x1b[1;2;3;4;7;9;38;5;81;48;2;12;34;56mstyled\x1b[0m default\r\n" +
		"\x1b[38:2::120:80:200mcolon\x1b[0m 界 é"
	_, _ = agent.Write([]byte(input))
	c := agent.CellAt(0, 0)
	r, g, b, _ := c.Style.Fg.RGBA()
	if r>>8 != 41 || g>>8 != 43 || b>>8 != 49 || c.Style.Bg != nil {
		t.Fatalf("RGB misparsed: %+v", c.Style)
	}
	if agent.CellAt(0, 1).Style.Attrs == 0 {
		t.Fatal("text attributes lost before render")
	}
	var frame strings.Builder
	Render(&frame, agent, &review.State{}, layout, false, true, nil)
	display := vt.NewEmulator(layout.Width, layout.Height)
	_, _ = display.Write([]byte(frame.String()))
	for y := 0; y < 3; y++ {
		for x := 0; x < 24; x++ {
			want, got := agent.CellAt(x, y), display.CellAt(x, layout.LeftY+y)
			if want == nil || got == nil {
				t.Fatalf("missing cell %d,%d", x, y)
			}
			if !want.Style.Equal(&got.Style) || want.Content != got.Content {
				t.Fatalf("cell %d,%d changed: want %+v got %+v", x, y, want, got)
			}
		}
	}
}

func TestFullFileAndBrowserRender(t *testing.T) {
	f := diffview.File{Path: "file.go", Scope: diffview.Unstaged, Lines: []string{"@@ -2 +2 @@", "-old", "+changed"}}
	var s review.State
	s.Update(diffview.Snapshot{FileCount: 1, Files: []diffview.File{f}})
	s.Key("v", 10)
	s.ContentKey = f.Key()
	s.Content = diffview.Content{Source: "working tree", Lines: []string{"untouched first", "changed", "untouched last"}}
	text := strings.Join(renderReview(&s, 60, 15, true), "\n")
	for _, want := range []string{"[ Full file ]", "working tree", "untouched first", "untouched last", "    2 +"} {
		if !strings.Contains(ansi.Strip(text), want) {
			t.Errorf("full file missing %q: %s", want, text)
		}
	}
	s.Key("f", 10)
	text = strings.Join(renderReview(&s, 60, 15, true), "\n")
	if !strings.Contains(text, "Enter: open") || strings.Contains(text, "untouched first") {
		t.Fatal("file browser did not replace content view")
	}
}

func TestAdvancedViewsAndControlsFit(t *testing.T) {
	f := diffview.File{Path: "file.go", Scope: diffview.Session, Lines: []string{"@@ -1 +1 @@", "-func oldName() { return 100 }", "+func newName() { return 200 }"}}
	for _, width := range []int{20, 60, 120} {
		for _, mode := range []string{"split", "wrap", "menu", "comments", "prompt", "confirm", "context", "preview", "problems", "timeline"} {
			var s review.State
			s.Update(diffview.Snapshot{Files: []diffview.File{f}})
			s.PatchFocused = true
			switch mode {
			case "split":
				s.SideBySide = true
			case "wrap":
				s.Wrap = true
			case "menu":
				s.Menu = true
				s.MenuIndex = 20
			case "comments":
				s.Panel = "Comments"
			case "prompt":
				s.Prompt = "Run checks"
			case "context", "preview":
				s.Panel = "Context"
				s.Attachments = []review.Attachment{{Label: "src/界.go:2–4", Message: strings.Repeat("long source line ", 20)}}
				s.ContextQuestion = "Explain this code"
				if mode == "preview" {
					s.Panel = "Context preview"
				}
			case "problems":
				s.Panel = "Problems"
				s.Problems = []checks.Problem{{Path: "src/界.go", Line: 12, Message: "expected value"}}
			case "timeline":
				s.Panel = "Timeline"
				s.Timeline = []review.TimelineEntry{{ID: 1, FileCount: 2, Added: 3, Deleted: 1}}
			case "confirm":
				s.ConfirmAction = "stage-file"
				s.ConfirmDetail = "Stage file.go?"
			}
			rows := renderReview(&s, width, 14, true)
			if len(rows) != 14 {
				t.Fatalf("%s height %d", mode, len(rows))
			}
			for _, row := range rows {
				if ansiWidth(row) != width {
					t.Fatalf("%s row width %d != %d", mode, ansiWidth(row), width)
				}
			}
		}
	}
	if got := ansi.Strip(intraline("return 世界", "return 世人", green)); got != "return 世界" {
		t.Fatalf("intraline altered Unicode: %q", got)
	}
	layout := NewLayoutOptions(120, 30, 40, true)
	if layout.LeftWidth != 0 || layout.DiffWidth != 120 {
		t.Fatal("full review did not expand")
	}
}

func TestReviewInboxClearState(t *testing.T) {
	f := diffview.File{Path: "file.go", Scope: diffview.Session, Status: "M", Lines: []string{"@@ -1 +1 @@", "-old", "+new"}}
	var s review.State
	s.Source = "session"
	s.Update(diffview.Snapshot{Files: []diffview.File{f}, FileCount: 1})
	s.Key(" ", 10)
	s.Key("I", 10)
	text := ansi.Strip(strings.Join(renderReview(&s, 60, 15, true), "\n"))
	if !strings.Contains(text, "Review inbox is clear") {
		t.Fatalf("missing inbox empty state: %s", text)
	}
}
