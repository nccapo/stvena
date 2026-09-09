package review

import "strings"

// GlobalHotkeyActions run from either pane. F6 stays fixed as a recovery path.
var GlobalHotkeyActions = []Action{
	{"Ctrl-G", "Switch panes", "Switch between the agent and review"},
	{"Ctrl-]", "New agent", "Choose a command for another agent terminal"},
	{"Ctrl-N", "Next agent", "Select the next agent terminal"},
	{"Ctrl-P", "Previous agent", "Select the previous agent terminal"},
	{"Ctrl-W", "Close agent", "Stop and close the current agent terminal"},
	{"Ctrl-Q", "Quit all", "Close Stvena and stop all running agents"},
}

func IsGlobalHotkey(key string) bool {
	for _, action := range GlobalHotkeyActions {
		if action.Key == key {
			return true
		}
	}
	return false
}

// ControlKey names the byte encodings used by ordinary terminal Ctrl chords.
func ControlKey(b byte) string {
	if b >= 1 && b <= 26 {
		return "Ctrl-" + string(rune('A'+b-1))
	}
	if b >= 28 && b <= 31 {
		return "Ctrl-" + string(rune('\\'+b-28))
	}
	return ""
}

func ControlByte(key string) byte {
	if key == "" {
		return 0
	}
	for b := byte(1); b <= 31; b++ {
		if ControlKey(b) == key {
			return b
		}
	}
	return 0
}

func validGlobalHotkey(key string) bool {
	b := ControlByte(key)
	// Keep interrupt, text-entry and existing review navigation keys available.
	return b != 0 && !strings.ContainsRune("\x03\x04\x08\x09\x0a\x0d\x15", rune(b))
}

func (s *State) GlobalAction(key string) string {
	if key == "" {
		return ""
	}
	for _, action := range GlobalHotkeyActions {
		if s.Binding(action.Key) == key {
			return action.Key
		}
	}
	return ""
}

func (s *State) EditingGlobalHotkey() bool {
	return s.Help && s.EditingHotkey && s.HelpIndex >= 0 && s.HelpIndex < len(HotkeyActions) && IsGlobalHotkey(HotkeyActions[s.HelpIndex].Key)
}
