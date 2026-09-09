package review

import (
	"strings"
	"testing"
)

func TestCustomHotkeysRouteInputOnlyOnce(t *testing.T) {
	s := State{Hotkeys: map[string]string{"v": "w", "w": "v"}}
	if err := ValidateHotkeys(s.Hotkeys); err != nil {
		t.Fatal(err)
	}
	s.InputKey("w", 20)
	if !s.FullFile || s.Wrap {
		t.Fatal("swapped shortcut did not execute exactly one action")
	}
	s.InputKey("v", 20)
	if !s.Wrap {
		t.Fatal("other side of swap did not run")
	}
	s.Key("v", 20) // Mouse and menu actions remain canonical.
	if s.FullFile {
		t.Fatal("canonical action was remapped")
	}
	s = State{Hotkeys: map[string]string{"v": "r"}}
	s.InputKey("v", 20)
	if s.FullFile {
		t.Fatal("old shortcut is still active")
	}
	s.InputKey("r", 20)
	if !s.FullFile {
		t.Fatal("new shortcut did not run")
	}
}

func TestHotkeysPreserveTextEntryAndMenuActions(t *testing.T) {
	s := State{Hotkeys: map[string]string{"v": "r"}, Searching: true}
	s.InputKey("r", 20)
	if s.Query != "r" || s.FullFile {
		t.Fatal("file filter input was remapped")
	}
	s.Searching, s.Prompt = false, "Add comment"
	s.InputKey("r", 20)
	if s.Input != "r" || s.FullFile {
		t.Fatal("prompt input was remapped")
	}
	s.Prompt, s.Menu = "", true
	s.InputKey("r", 20)
	if s.Menu || !s.FullFile {
		t.Fatal("custom action menu shortcut failed")
	}
	s.FullFile, s.Menu = false, true
	for i, action := range Actions {
		if action.Key == "v" {
			s.MenuIndex = i
		}
	}
	s.InputKey("enter", 20)
	if !s.FullFile {
		t.Fatal("menu selection remapped its canonical action")
	}
}

func TestHotkeyEditorConflictCancelAndReset(t *testing.T) {
	s := State{Panel: "Context"}
	s.InputKey("?", 20)
	if !s.Help || s.Panel != "Context" {
		t.Fatal("help did not open over existing panel")
	}
	s.InputKey("enter", 20)
	s.InputKey("v", 20)
	if !s.EditingHotkey || !strings.Contains(s.Notice, "already assigned to Diff / full file") || len(s.Hotkeys) != 0 {
		t.Fatal("conflicting assignment was not rejected")
	}
	s.InputKey("esc", 20)
	if !s.Help || s.EditingHotkey {
		t.Fatal("Escape should cancel editing without closing help")
	}
	s.InputKey("enter", 20)
	s.InputKey("r", 20)
	if s.EditingHotkey || s.Binding("a") != "r" || !s.HotkeysDirty {
		t.Fatal("hotkey was not updated")
	}
	s.InputKey("backspace", 20)
	if s.Binding("a") != "a" {
		t.Fatal("selected default was not restored")
	}
	s.InputKey("enter", 20)
	s.InputKey("r", 20)
	s.InputKey("delete", 20)
	if len(s.Hotkeys) != 0 {
		t.Fatal("reset all did not clear overrides")
	}
	s.InputKey("?", 20)
	if s.Help || s.Panel != "Context" {
		t.Fatal("closing help did not return to the existing view")
	}
}

func TestHotkeyValidation(t *testing.T) {
	for _, key := range []string{"?", "j", "k", "esc", "enter", "", "ab", "\x07", "\n", "\u200b", "\u0301"} {
		if err := ValidateHotkeys(map[string]string{"v": key}); err == nil {
			t.Errorf("accepted reserved or invalid key %q", key)
		}
	}
	if err := ValidateHotkeys(map[string]string{"unknown": "r"}); err == nil {
		t.Fatal("accepted unknown action")
	}
	if err := ValidateHotkeys(map[string]string{"v": "界"}); err != nil {
		t.Fatal(err)
	}
	s := State{Hotkeys: map[string]string{"v": "r", "w": "v"}}
	if err := s.SetHotkey("v", "v"); err == nil || s.Binding("v") != "r" {
		t.Fatal("reset silently overwrote another shortcut")
	}
}

func TestGlobalHotkeyValidationAndCapture(t *testing.T) {
	for _, key := range []string{"", "r", "F6", "Ctrl-C", "Ctrl-D", "Ctrl-H", "Ctrl-I", "Ctrl-J", "Ctrl-M", "Ctrl-U", "Ctrl-[", "Ctrl-N"} {
		s := State{}
		if err := s.SetHotkey("Ctrl-G", key); err == nil {
			t.Errorf("accepted unavailable or conflicting global shortcut %q", key)
		}
	}
	s := State{Help: true}
	for i, action := range HotkeyActions {
		if action.Key == "Ctrl-G" {
			s.HelpIndex = i
			break
		}
	}
	s.InputKey("enter", 20)
	s.InputKey("Ctrl-O", 20)
	if s.EditingHotkey || s.Binding("Ctrl-G") != "Ctrl-O" || !s.HotkeysDirty {
		t.Fatal("global shortcut capture failed")
	}
	s.InputKey("backspace", 20)
	if s.Binding("Ctrl-G") != "Ctrl-G" {
		t.Fatal("reset did not restore global shortcut")
	}
	if err := s.SetHotkey("a", "Ctrl-O"); err == nil {
		t.Fatal("review shortcut accepted a global chord")
	}
}
