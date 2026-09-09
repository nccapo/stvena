package app

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nccapo/stvena/internal/review"
	"github.com/nccapo/stvena/internal/session"
	"github.com/nccapo/stvena/internal/ui"
)

func TestConfigureHotkeyWithMouseAndPersist(t *testing.T) {
	s := screenState{root: t.TempDir(), ratio: 58, diffFocused: true}
	s.relayout(140, 40)
	var child bytes.Buffer
	// Click the persistent ? control, then the first action in the editor.
	clicked := false
	for y := 0; y < s.layout.FooterHeight && !clicked; y++ {
		for x := 0; x < s.layout.Width; x++ {
			if ui.ControlKeyAt(s.layout.Width, x, y, true, &s.review) == "?" {
				s.mouse(fmt.Sprintf("<0;%d;%dM", x+1, s.layout.FooterY+y+1))
				clicked = true
				break
			}
		}
	}
	if !clicked || !s.review.Help {
		t.Fatal("clicking ? did not open hotkey configuration")
	}
	s.mouse(fmt.Sprintf("<0;%d;%dM", s.layout.DiffX+2, s.layout.DiffY+4))
	if !s.review.EditingHotkey {
		t.Fatal("clicking an action did not start editing")
	}
	s.mouse(fmt.Sprintf("<65;%d;%dM", s.layout.DiffX+2, s.layout.DiffY+4))
	if s.review.Notice != "" || !s.review.EditingHotkey {
		t.Fatal("mouse wheel was captured as a replacement key")
	}
	s.handleInput([]byte("r"), &child)
	s.dispatch(context.Background(), make(chan any, 1), make(chan struct{}))
	if s.review.Binding("a") != "r" || !strings.Contains(s.review.Notice, "saved") {
		t.Fatalf("binding was not saved: %s", s.review.Notice)
	}
	reopened := screenState{root: s.root, layout: ui.NewLayout(140, 40)}
	reopened.loadPreferences()
	if reopened.review.Binding("a") != "r" || reopened.ratio != 58 {
		t.Fatal("preferences did not restore the custom binding and layout")
	}
	s.handleInput([]byte("?r"), &child)
	if !s.review.Menu {
		t.Fatal("configured key did not open Actions")
	}
	s.diffFocused = false
	s.handleInput([]byte("ar"), &child)
	if child.String() != "ar" {
		t.Fatal("custom bindings altered agent input")
	}
}

func TestHotkeyPreferencesRejectConflictsAndReadOldLayout(t *testing.T) {
	s := screenState{root: t.TempDir(), layout: ui.NewLayout(140, 40)}
	dir, err := session.RepoDir(s.root)
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range []string{
		`{"Ratio":65,"Wrap":true,"SideBySide":true,"Hotkeys":{"v":"w"}}`,
		`{"Ratio":65,"Wrap":true,"SideBySide":true}`,
	} {
		s.review.Hotkeys = map[string]string{"v": "r"}
		if err := os.WriteFile(filepath.Join(dir, "layout.json"), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		s.loadPreferences()
		if len(s.review.Hotkeys) != 0 || s.ratio != 65 || !s.review.Wrap || !s.review.SideBySide {
			t.Fatal("invalid or absent hotkeys broke existing preferences")
		}
	}
}

func TestHotkeySaveFailureKeepsBindingAndReportsError(t *testing.T) {
	s := screenState{root: t.TempDir(), ratio: 58, review: review.State{Help: true}}
	s.relayout(140, 40)
	dir, err := session.RepoDir(s.root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "layout.json"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := s.review.SetHotkey("v", "r"); err != nil {
		t.Fatal(err)
	}
	s.dispatch(context.Background(), make(chan any, 1), make(chan struct{}))
	if !strings.Contains(s.review.Notice, "could not save") || s.review.Binding("v") != "r" {
		t.Fatal("save failure was hidden or discarded the active shortcut")
	}
}

func TestConfiguredHotkeysSurviveCtrlQAndReopen(t *testing.T) {
	for _, mode := range []string{"immediate", "review", "agent", "paste", "editing"} {
		t.Run(mode, func(t *testing.T) {
			s := &screenState{root: t.TempDir(), ratio: 58, diffFocused: true}
			s.relayout(140, 40)
			var child bytes.Buffer
			events, stop := make(chan any, 1), make(chan struct{})
			// Configure Actions and Files through the editor. The immediate case
			// receives Ctrl-Q in the same input chunk as the new assignments.
			input := "?\rr\x1b[B\rz"
			if mode == "immediate" {
				s.handleInput([]byte(input+"\x11"), &child)
			} else {
				s.handleInput([]byte(input), &child)
				s.dispatch(context.Background(), events, stop)
				switch mode {
				case "agent":
					s.handleInput([]byte{7}, &child)
				case "paste":
					s.pastePending = true
				case "editing":
					s.handleInput([]byte("\r"), &child)
				}
				s.handleInput([]byte{0x11}, &child)
			}
			if !s.dispatch(context.Background(), events, stop) {
				t.Fatal("Ctrl-Q did not quit")
			}
			want := map[string]string{"a": "r", "f": "z"}
			// Verify the quit dispatch saved even the immediate edit before Run's
			// deferred save, then exercise that save and a second quit/reopen.
			for restart := 0; restart < 2; restart++ {
				reopened := &screenState{root: s.root, layout: ui.NewLayout(140, 40), diffFocused: true}
				reopened.loadPreferences()
				if !maps.Equal(reopened.review.Hotkeys, want) {
					t.Fatalf("restart %d: hotkeys = %v, want %v", restart, reopened.review.Hotkeys, want)
				}
				if err := s.savePreferences(); err != nil {
					t.Fatal(err)
				}
				reopened.handleInput([]byte("a"), &child)
				if reopened.review.Menu {
					t.Fatal("default shortcut was reactivated on restart")
				}
				reopened.handleInput([]byte("r\x11"), &child)
				if !reopened.review.Menu || !reopened.dispatch(context.Background(), events, stop) {
					t.Fatal("configured shortcut or Ctrl-Q stopped working after restart")
				}
				s = reopened
			}
			if child.Len() != 0 {
				t.Fatal("configuration or Ctrl-Q input leaked to the agent")
			}
		})
	}
}
