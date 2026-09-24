package review

import (
	"strings"
	"testing"
	"time"
)

func TestBriefPanelKeys(t *testing.T) {
	s := selectionState()
	s.Key("J", 20)
	if s.Panel != "Brief" || s.BriefIndex != 0 {
		t.Fatal("J did not open brief")
	}
	s.Key("a", 20)
	if s.Prompt != "Brief item" {
		t.Fatal("add prompt missing")
	}
	for _, r := range "first" {
		s.Key(string(r), 20)
	}
	s.Key("enter", 20)
	if len(s.Brief) != 1 || s.Brief[0].Text != "first" {
		t.Fatal("add prompt failed")
	}
	s.Key("enter", 20)
	if !strings.Contains(s.Notice, "no evidence") {
		t.Fatal("item without evidence did not show notice")
	}
	s.Key("i", 20)
	if s.Input != "first" {
		t.Fatal("edit prompt not prefilled")
	}
	s.Input = "edited"
	s.Key("enter", 20)
	if s.Brief[0].Text != "edited" {
		t.Fatal("edit failed")
	}
	s.Key("x", 20)
	if len(s.Brief[0].Evidence) != 1 {
		t.Fatal("selection attachment failed")
	}
	s.CheckCommand, s.CheckStatus, s.CheckTree, s.CheckFinishedAt = "go test", "passed", s.Snapshot.Tree, time.Now()
	s.Key("t", 20)
	if len(s.Brief[0].Evidence) != 2 || s.Brief[0].Done {
		t.Fatal("check attachment changed completion")
	}
	s.Key("down", 20)
	s.Key("enter", 20)
	if s.Request != "brief-open" {
		t.Fatal("evidence did not request open")
	}
	s.Key("d", 20)
	if len(s.Brief[0].Evidence) != 1 || len(s.Brief) != 1 {
		t.Fatal("evidence delete removed item")
	}
	s.Key("up", 20)
	s.Key(" ", 20)
	if !s.Brief[0].Done {
		t.Fatal("Space did not mark")
	}
	s.Key(" ", 20)
	if s.Brief[0].Done {
		t.Fatal("Space did not unmark")
	}
	s.Key("a", 20)
	s.Input = "second"
	s.Key("enter", 20)
	s.Key("[", 20)
	if s.Brief[0].Text != "second" {
		t.Fatal("reorder failed")
	}
	s.Key("d", 20)
	if len(s.Brief) != 1 {
		t.Fatal("item delete failed")
	}
	s.Key("J", 20)
	if s.Panel != "" {
		t.Fatal("J did not close")
	}
}

func TestBriefPickerAndPanelIsolation(t *testing.T) {
	s := selectionState()
	s.AddBriefItem("one")
	s.Comments = []Comment{{ID: "one", Text: "note"}}
	s.Key("J", 20)
	s.Key("c", 20)
	if s.Panel != "Brief comments" {
		t.Fatal("picker not opened")
	}
	s.Key("enter", 20)
	if s.Panel != "Brief" || len(s.Brief[0].Evidence) != 1 {
		t.Fatal("picker did not attach")
	}
	s.Key("c", 20)
	s.Key("esc", 20)
	if len(s.Brief[0].Evidence) != 1 {
		t.Fatal("cancel attached evidence")
	}
	s.Key("pagedown", 20)
	if s.BriefIndex != 1 {
		t.Fatal("PageDown did not move over evidence")
	}
	s.Key("pageup", 20)
	if s.BriefIndex != 0 {
		t.Fatal("PageUp did not return to item")
	}
	before := s.Brief[0]
	s.Key("z", 20)
	if s.Panel != "Brief" || s.Brief[0].Text != before.Text || s.Scroll != 1 {
		t.Fatal("unhandled key leaked")
	}
	s.Key("T", 20)
	if s.Panel != "Checks" {
		t.Fatal("T did not open checks")
	}
	for _, key := range []string{"1", "2", "3", "4"} {
		s.Panel = "Brief"
		s.Key(key, 20)
		if s.Panel != "" || s.Request != key {
			t.Fatalf("%s did not change view", key)
		}
	}
}

func TestBriefHotkeysKeepExistingActions(t *testing.T) {
	if err := ValidateHotkeys(nil); err != nil {
		t.Fatal(err)
	}
	pinned := map[string]string{
		"K": "Review checkpoint", "Z": "Finish checkpoint", "v": "Diff / full file", "F": "Focus review", "1": "This session", "2": "Whole workspace", "3": "Project files", "4": "Branch changes", "I": "Review inbox", "L": "Session timeline", "/": "Find text / file", ":": "Go to line", "s": "Side-by-side diff", "w": "Wrap long lines", "P": "Pin / resume live", "R": "Changed since review", " ": "Mark file reviewed", "N": "Next unreviewed file", "H": "Mark hunk reviewed", "X": "Reject change", "D": "Rejections", "W": "Auto-send rejections", "O": "IDE mode", "V": "Select code range", "x": "Collect selected code", "B": "Context tray", "b": "Paste selection to agent", "c": "Add comment", "C": "Review comments", "E": "Export feedback", "y": "Copy code", "Y": "Copy file path", "e": "Open in editor", "t": "Run checks", "T": "Check results", "S": "Stage / unstage file", "A": "Stage / unstage hunk", "+": "Wider review pane", "-": "Wider agent pane", "q": "Quit review",
	}
	if len(Actions) != len(pinned)+1 {
		t.Fatalf("action count changed: %d", len(Actions))
	}
	for _, a := range Actions {
		if a.Key == "J" {
			if a.Name != "Task brief" {
				t.Fatal(a)
			}
			continue
		}
		if want := pinned[a.Key]; want != a.Name {
			t.Fatalf("shortcut changed: %q %q, want %q", a.Key, a.Name, want)
		}
		delete(pinned, a.Key)
	}
	if len(pinned) != 0 {
		t.Fatalf("actions removed: %v", pinned)
	}
	other := map[string]string{
		"a": "Actions menu", "f": "File browser", "n": "Next file", "p": "Previous file", "d": "Page down / remove attachment", "u": "Page up", "g": "First file / line", "G": "Last file / line", "h": "Scroll left / parent folder", "l": "Scroll right / enter folder", "0": "Reset horizontal scroll", "[": "Previous change", "]": "Next change", "m": "Next text match", "M": "Previous text match", "i": "Edit context request", "o": "Check problems",
		"Ctrl-G": "Switch panes", "Ctrl-]": "New agent", "Ctrl-N": "Next agent", "Ctrl-P": "Previous agent", "Ctrl-Y": "Next attention", "Ctrl-W": "Close agent", "Ctrl-Q": "Quit all",
	}
	if len(HotkeyActions) != len(Actions)+len(other) {
		t.Fatal("hotkey action count changed")
	}
	for _, a := range HotkeyActions {
		if a.Key == "J" {
			continue
		}
		if want, found := other[a.Key]; found {
			if want != a.Name {
				t.Fatalf("hotkey changed: %s = %s, want %s", a.Key, a.Name, want)
			}
			delete(other, a.Key)
		}
	}
	if len(other) != 0 {
		t.Fatalf("hotkeys removed: %v", other)
	}
	if strings.Contains(Actions[10].Name, "Task brief") == false {
		t.Fatal("J not after timeline")
	}
}
