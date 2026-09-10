package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/nccapo/stvena/internal/diffview"
	"github.com/nccapo/stvena/internal/review"
)

func TestWelcomeYieldsToChangesAndReviewControls(t *testing.T) {
	s := review.State{AgentStatus: "Running"}
	if !WelcomeVisible(&s) {
		t.Fatal("missing startup welcome")
	}
	for _, mode := range []string{"changes", "error", "filter", "search", "scope", "pin", "help", "menu", "panel", "prompt", "confirm", "finished", "standalone"} {
		t.Run(mode, func(t *testing.T) {
			s := review.State{AgentStatus: "Running"}
			switch mode {
			case "changes":
				s.Update(diffview.Snapshot{Files: []diffview.File{{Path: "main.go"}}})
			case "error":
				s.Snapshot.Err = errors.New("refresh failed")
			case "filter":
				s.Query = "main"
			case "search":
				s.Searching = true
			case "scope":
				s.Scope = 1
			case "pin":
				s.Pinned = true
			case "help":
				s.Help = true
			case "menu":
				s.Menu = true
			case "panel":
				s.Panel = "Checks"
			case "prompt":
				s.Prompt = "Run checks"
			case "confirm":
				s.ConfirmAction = "stage-file"
			case "finished":
				s.AgentStatus = "Finished"
			case "standalone":
				s.AgentStatus = "Review"
			}
			if WelcomeVisible(&s) {
				t.Fatal("welcome hides review state")
			}
		})
	}
}

func TestWelcomeFitsAndAnimationKeepsTextStationary(t *testing.T) {
	for _, size := range [][2]int{{1, 1}, {20, 5}, {40, 10}, {34, 21}, {34, 22}, {34, 27}, {58, 30}, {100, 40}} {
		s := review.State{AgentStatus: "Running", Snapshot: diffview.Snapshot{UpdatedAt: time.Now()}}
		var previous string
		for frame := 0; frame < 6; frame++ {
			s.WelcomeFrame = frame
			rows := renderReview(&s, size[0], size[1], false)
			if len(rows) != size[1] {
				t.Fatalf("wrong height for %v", size)
			}
			for _, row := range rows {
				if ansiWidth(row) != size[0] {
					t.Fatalf("welcome overflows %v", size)
				}
			}
			text := ansi.Strip(strings.Join(rows, "\n"))
			if frame > 0 && text != previous {
				t.Fatal("animation moves content")
			}
			previous = text
		}
	}
}
