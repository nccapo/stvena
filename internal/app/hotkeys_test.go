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

func isolateHotkeys(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	previous := preferencesConfigDir
	preferencesConfigDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { preferencesConfigDir = previous })
	return filepath.Join(dir, "stvena", "hotkeys.json")
}

func TestConfigureHotkeyWithMouseAndPersist(t *testing.T) {
	isolateHotkeys(t)
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
	s.review.HelpIndex = 0 // Exercise the review-key section after opening Configuration.
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
	isolateHotkeys(t)
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
	path := isolateHotkeys(t)
	s := screenState{root: t.TempDir(), ratio: 58, review: review.State{Help: true}}
	s.relayout(140, 40)
	if err := os.MkdirAll(path, 0700); err != nil {
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
			isolateHotkeys(t)
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

func TestHotkeysSharedAcrossProjectsWithoutSharingLayout(t *testing.T) {
	path := isolateHotkeys(t)
	first := screenState{root: t.TempDir(), ratio: 65}
	first.review.Wrap = true
	first.review.SideBySide = true
	if err := first.review.SetHotkey("a", "r"); err != nil {
		t.Fatal(err)
	}
	if err := first.savePreferences(); err != nil {
		t.Fatal(err)
	}
	second := screenState{root: t.TempDir(), ratio: 58}
	second.loadPreferences() // This repository has never had a layout file.
	if second.review.Binding("a") != "r" || second.ratio != 58 || second.review.Wrap || second.review.SideBySide {
		t.Fatalf("shared hotkeys or independent layout failed: binding=%q ratio=%d wrap=%t sideBySide=%t", second.review.Binding("a"), second.ratio, second.review.Wrap, second.review.SideBySide)
	}
	if err := second.review.SetHotkey("a", "z"); err != nil {
		t.Fatal(err)
	}
	if err := second.savePreferences(); err != nil {
		t.Fatal(err)
	}
	// An older window closing must not overwrite a more recent customization.
	if err := first.savePreferences(); err != nil {
		t.Fatal(err)
	}
	reopened := screenState{root: first.root}
	reopened.loadPreferences()
	if reopened.review.Binding("a") != "z" || reopened.ratio != 65 || !reopened.review.Wrap || !reopened.review.SideBySide {
		t.Fatal("stale window overwrote global settings or lost project layout")
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("global config permissions: %v, %v", info, err)
	}
}

func TestLegacyHotkeysMigrateOnceAndResetGlobally(t *testing.T) {
	isolateHotkeys(t)
	first := screenState{root: t.TempDir()}
	second := screenState{root: t.TempDir()}
	for _, s := range []*screenState{&first, &second} {
		dir, err := session.RepoDir(s.root)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "layout.json"), []byte(`{"Ratio":65,"Hotkeys":{"a":"r"}}`), 0600); err != nil {
			t.Fatal(err)
		}
	}
	first.loadPreferences()
	fresh := screenState{root: t.TempDir()}
	fresh.loadPreferences()
	if first.review.Binding("a") != "r" || fresh.review.Binding("a") != "r" {
		t.Fatal("legacy customization was not migrated for other projects")
	}
	first.review.Help = true
	first.review.InputKey("delete", 20)
	if err := first.savePreferences(); err != nil {
		t.Fatal(err)
	}
	second.loadPreferences()
	if second.review.Binding("a") != "a" || len(second.review.Hotkeys) != 0 {
		t.Fatal("old project settings resurrected a globally reset binding")
	}
}

func TestInvalidGlobalHotkeysKeepLayoutAndDoNotFallBackToLegacy(t *testing.T) {
	path := isolateHotkeys(t)
	s := screenState{root: t.TempDir()}
	dir, err := session.RepoDir(s.root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "layout.json"), []byte(`{"Ratio":65,"Wrap":true,"Hotkeys":{"a":"r"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	for _, data := range []string{`{"Hotkeys":{"v":"w"}}`, `{"Hotkeys":`} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		s.loadPreferences()
		if len(s.review.Hotkeys) != 0 || s.ratio != 65 || !s.review.Wrap || !strings.Contains(s.review.Notice, "Invalid saved hotkeys") {
			t.Fatalf("invalid global config mishandled: %s", s.review.Notice)
		}
		if err := s.savePreferences(); err != nil {
			t.Fatal(err)
		}
		saved, err := os.ReadFile(path)
		if err != nil || string(saved) != data {
			t.Fatal("unedited invalid config overwritten")
		}
	}
}
