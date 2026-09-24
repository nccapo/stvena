package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/nccapo/stvena/internal/review"
)

func TestBriefOverlayShowsRowsStalenessAndControls(t *testing.T) {
	s := &review.State{Panel: "Brief", LiveTree: "new", Brief: []review.BriefItem{
		{Text: "Keep API", Evidence: []review.Evidence{{Kind: "check", Label: "go test", Command: "go test", CheckTree: "old", RanAt: time.Now()}}},
		{Text: "Reject expired sessions", Done: true},
	}}
	got := ansi.Strip(strings.Join(renderOverlay(s, 110, 18), "\n"))
	for _, want := range []string{"Task brief · 2 items · 1 checked · 1 stale", "[ ] Keep API", "[x] Reject expired sessions", "    check: go test", "stale: code changed since the run", "Enter: Open evidence", "Space: Mark by hand"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	if key := PanelControlKeyAt(s, 110, 18, 2, 17); key != "enter" {
		t.Fatalf("footer hit test: %q", key)
	}
	s.Brief = nil
	if got := ansi.Strip(strings.Join(renderOverlay(s, 110, 18), "\n")); !strings.Contains(got, "Only you can check items by hand") {
		t.Fatal("empty state missing")
	}
}
