package ui

import (
	"github.com/nccapo/stvena/internal/diffview"
	"github.com/nccapo/stvena/internal/review"
	"strings"
	"testing"
)

func TestWrappedSourceHitMapping(t *testing.T) {
	var s review.State
	f := diffview.File{Path: "file.txt", Scope: diffview.Session}
	s.Update(diffview.Snapshot{Files: []diffview.File{f}})
	s.FullFile = true
	s.ContentKey = f.Key()
	s.Wrap = true
	s.Content = diffview.Content{Lines: []string{strings.Repeat("界", 20), "short"}}
	// Width 30 leaves 19 code columns: this source line occupies 3 visual rows.
	rows := renderReview(&s, 30, 20, true)
	if len(rows) != 20 {
		t.Fatal("unexpected review height")
	}
	for row, want := range map[int]int{5: 0, 6: 0, 7: 0, 8: 1} {
		got, _, ok := CodeHitAt(&s, 30, 20, 20, row, 0)
		if !ok || got != want {
			t.Fatalf("row %d maps to %d/%v, want %d", row, got, ok, want)
		}
	}
	if _, _, ok := CodeHitAt(&s, 30, 20, 20, 9, 0); ok {
		t.Fatal("blank space became source code")
	}
	s.ContentLoading = true
	if _, _, ok := CodeHitAt(&s, 30, 20, 20, 5, 0); ok {
		t.Fatal("loading content was selectable")
	}
}
func TestSplitWrappedHitMapping(t *testing.T) {
	var s review.State
	s.SideBySide = true
	s.Wrap = true
	s.Update(diffview.Snapshot{Files: []diffview.File{{Path: "file.txt", Scope: diffview.Session, Lines: []string{"@@ -1 +1 @@", "-short", "+" + strings.Repeat("long", 20)}}}})
	if got, side, ok := CodeHitAt(&s, 80, 20, 60, 7, 0); !ok || got != 2 || side != 'n' {
		t.Fatalf("wrapped new side: %d %c %v", got, side, ok)
	}
	if _, _, ok := CodeHitAt(&s, 80, 20, 10, 7, 0); ok {
		t.Fatal("empty old continuation selected source")
	}
	if _, _, ok := CodeHitAt(&s, 80, 20, 38, 6, 0); ok {
		t.Fatal("split divider selected source")
	}
}
