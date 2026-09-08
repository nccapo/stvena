package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/nccapo/stvena/internal/diffview"
	"github.com/nccapo/stvena/internal/review"
)

func TestCheckpointProgressAndFinishWarning(t *testing.T) {
	var s review.State
	view := diffview.Snapshot{Tree: "captured", Files: []diffview.File{{Path: "file.go", Scope: diffview.Session, Lines: []string{"@@ -1 +1 @@", "-old", "+new"}}}}
	view.Finish()
	if err := s.StartCheckpoint(view); err != nil {
		t.Fatal(err)
	}
	s.Notice = ""
	s.Update(diffview.Snapshot{Tree: "newer"})
	render := func() string { return ansi.Strip(strings.Join(renderReview(&s, 64, 18, true), "\n")) }
	for _, want := range []string{"Checkpoint · 0/1 files · 0/1 hunks", "Pinned · NEW LIVE", "0 comments · 0 selections", "Z: Finish checkpoint", "P: Resume live"} {
		if !strings.Contains(render(), want) {
			t.Errorf("missing %q: %s", want, render())
		}
	}
	s.Key("Z", 10)
	if got := render(); !strings.Contains(got, "review incomplete") || !strings.Contains(got, "Esc: return to review") || strings.Contains(got, "staging index") {
		t.Fatalf("wrong warning: %s", got)
	}
	s.Key("esc", 10)
	s.Key(" ", 10)
	if !strings.Contains(render(), "Checkpoint · 1/1 files · 1/1 hunks") {
		t.Fatal("progress did not update")
	}
	seen := map[string]bool{}
	for _, a := range review.Actions {
		if seen[a.Key] {
			t.Fatalf("conflicting action shortcut %q", a.Key)
		}
		seen[a.Key] = true
	}
	if !seen["K"] || !seen["Z"] {
		t.Fatal("checkpoint actions not discoverable")
	}
}
