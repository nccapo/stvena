package review

import (
	"fmt"
	"unicode"
)

// HotkeyActions describes the character shortcuts. Named navigation keys and
// global terminal controls stay fixed so the editor is always accessible.
var HotkeyActions = append([]Action{
	{"a", "Actions menu", "Open all review actions"},
	{"f", "File browser", "Return to the file list"},
	{"n", "Next file", "Select the next file"},
	{"p", "Previous file", "Select the previous file"},
	{"d", "Page down / remove attachment", "Scroll half a page; remove the selected context attachment"},
	{"u", "Page up", "Scroll half a page"},
	{"g", "First file / line", "Jump to the beginning"},
	{"G", "Last file / line", "Jump to the end"},
	{"h", "Scroll left / parent folder", "Move left in code or the project tree"},
	{"l", "Scroll right / enter folder", "Move right in code or the project tree"},
	{"0", "Reset horizontal scroll", "Return to the start of the line"},
	{"[", "Previous change", "Jump to the previous hunk or full-file change"},
	{"]", "Next change", "Jump to the next hunk or full-file change"},
	{"m", "Next text match", "Find the next search result"},
	{"M", "Previous text match", "Find the previous search result"},
	{"i", "Edit context request", "Edit your question for the agent"},
	{"o", "Check problems", "Open source locations from check results"},
}, Actions...)

func (s *State) Binding(key string) string {
	if custom := s.Hotkeys[key]; custom != "" {
		return custom
	}
	return key
}

func KeyLabel(key string) string {
	if key == " " {
		return "Space"
	}
	return key
}

func validHotkey(key string) bool {
	r := []rune(key)
	return len(r) == 1 && (unicode.IsGraphic(r[0]) && !unicode.IsSpace(r[0]) && !unicode.IsMark(r[0]) || key == " ") && key != "?" && key != "j" && key != "k"
}

// ValidateHotkeys checks the effective map, including unchanged defaults.
func ValidateHotkeys(bindings map[string]string) error {
	known := make(map[string]bool, len(HotkeyActions))
	used := make(map[string]string, len(HotkeyActions))
	for _, action := range HotkeyActions {
		known[action.Key] = true
		key := action.Key
		if custom, ok := bindings[key]; ok {
			if !validHotkey(custom) {
				return fmt.Errorf("Use one printable key; ?, j and k stay fixed")
			}
			key = custom
		}
		if other, ok := used[key]; ok {
			return fmt.Errorf("%s is already assigned to %s", KeyLabel(key), other)
		}
		used[key] = action.Name
	}
	for key := range bindings {
		if !known[key] {
			return fmt.Errorf("unknown review shortcut %q", key)
		}
	}
	return nil
}

func (s *State) SetHotkey(action, key string) error {
	for _, other := range HotkeyActions {
		if other.Key != action && s.Binding(other.Key) == key {
			return fmt.Errorf("%s is already assigned to %s", KeyLabel(key), other.Name)
		}
	}
	bindings := make(map[string]string, len(s.Hotkeys)+1)
	for k, v := range s.Hotkeys {
		bindings[k] = v
	}
	bindings[action] = key
	if err := ValidateHotkeys(bindings); err != nil {
		return err
	}
	if key == action {
		delete(bindings, action)
	}
	s.Hotkeys = bindings
	s.HotkeysDirty = true
	return nil
}

// InputKey translates physical input once. Key remains the canonical action
// entry point for mouse controls, menu selection and internal navigation.
func (s *State) InputKey(key string, visible int) {
	if !s.Help && !s.Searching && s.Prompt == "" && s.ConfirmAction == "" {
		translated := key
		for _, action := range HotkeyActions {
			if s.Binding(action.Key) == key {
				translated = action.Key
				break
			}
			if action.Key == key {
				translated = ""
			}
		}
		key = translated
	}
	if key != "" {
		s.Key(key, visible)
	}
}

func (s *State) helpKey(key string) {
	s.Notice = ""
	s.HelpIndex = min(max(0, s.HelpIndex), len(HotkeyActions)-1)
	if s.EditingHotkey {
		if key == "esc" {
			s.EditingHotkey = false
			return
		}
		if err := s.SetHotkey(HotkeyActions[s.HelpIndex].Key, key); err != nil {
			s.Notice = err.Error()
			return
		}
		s.EditingHotkey = false
		return
	}
	switch key {
	case "?", "esc":
		s.Help = false
	case "up", "k":
		s.HelpIndex = max(0, s.HelpIndex-1)
	case "down", "j":
		s.HelpIndex = min(len(HotkeyActions)-1, s.HelpIndex+1)
	case "pageup":
		s.HelpIndex = max(0, s.HelpIndex-10)
	case "pagedown":
		s.HelpIndex = min(len(HotkeyActions)-1, s.HelpIndex+10)
	case "home":
		s.HelpIndex = 0
	case "end":
		s.HelpIndex = len(HotkeyActions) - 1
	case "enter":
		s.EditingHotkey = true
	case "backspace":
		action := HotkeyActions[s.HelpIndex]
		if err := s.SetHotkey(action.Key, action.Key); err != nil {
			s.Notice = err.Error()
		}
	case "delete":
		s.Hotkeys = nil
		s.HotkeysDirty = true
	}
}
