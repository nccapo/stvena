package ui

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/nccapo/stvena/internal/diffview"
	"github.com/nccapo/stvena/internal/review"
)

func TestReviewControlsMatchVisibleLabelsAndYieldToErrors(t *testing.T) {
	var s review.State
	s.Update(diffview.Snapshot{Tree: "captured", Files: []diffview.File{{Path: "src/file.go", Lines: []string{"@@ -1 +1 @@", "-old", "+new"}}}})
	for _, mode := range []string{"browser", "code", "selection"} {
		s.Browser = mode == "browser"
		if mode == "selection" {
			s.SelectWithMouse(2, 0, false)
		}
		for _, width := range []int{10, 35, 65, 120} {
			rows := renderReview(&s, width, 20, true)
			footer := ansi.Strip(rows[19])
			for x := 0; x < width; x++ {
				key := ReviewControlKeyAt(&s, width, 20, x, 19)
				if key == "" {
					continue
				}
				found := false
				for _, c := range reviewControls(&s) {
					if c.key == key && strings.Contains(footer, c.label) {
						found = true
					}
				}
				if !found {
					t.Fatalf("invisible control %q at %d in %q", key, x, footer)
				}
			}
		}
	}
	s.Notice = "Paste may be incomplete"
	if !strings.Contains(reviewFooter(&s), s.Notice) || ReviewControlKeyAt(&s, 80, 20, 4, 19) != "" {
		t.Fatal("selection hid an operation error or left an invisible button")
	}
	s.Snapshot.Err = errors.New("refresh failed")
	if !strings.Contains(reviewFooter(&s), "refresh failed") {
		t.Fatal("selection hid the snapshot error")
	}
}

func TestPinnedViewOffersResumeWithoutSelection(t *testing.T) {
	for _, browser := range []bool{false, true} {
		s := review.State{Pinned: true, Browser: browser}
		s.Snapshot = diffview.Snapshot{Tree: "captured"}
		rows := renderReview(&s, 35, 20, true)
		if !strings.Contains(ansi.Strip(rows[19]), "P: Resume live") || ReviewControlKeyAt(&s, 35, 20, 4, 19) != "P" {
			t.Fatalf("browser=%t: pinned view has no visible resume control", browser)
		}
	}
}
