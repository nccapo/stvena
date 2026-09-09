package app

import (
	"bytes"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/nccapo/stvena/internal/review"
)

// handleInput recognizes optional Command aliases forwarded by the terminal.
// Ctrl remains the default: a terminal app may consume Command keys itself.
// Buffer escape sequences across reads, but keep normal input/pastes batched.
func (s *screenState) handleInput(data []byte, child io.Writer) {
	data = append(s.keyboardPending, data...)
	s.keyboardPending = nil
	decoded := make([]byte, 0, len(data))
	flush := func() {
		s.handleLegacyInput(decoded, child)
		decoded = decoded[:0]
		if s.agentInput != nil {
			child = s.agentInput
		}
	}
	for len(data) > 0 {
		if data[0] != 0x1b {
			decoded = append(decoded, data[0])
			data = data[1:]
			continue
		}
		end := 1
		if len(data) > 1 && data[1] == '[' {
			end = 2
			for end < len(data) && data[end] >= 0x20 && data[end] <= 0x3f {
				end++
			}
		}
		if end == len(data) && len(data) < 128 {
			s.keyboardPending = append([]byte(nil), data...)
			s.lastInput = time.Now()
			break
		}
		if end < len(data) && end > 1 {
			end++
		}
		sequence := data[:end]
		switch string(sequence) {
		case ansi.BracketedPasteStart:
			s.keyboardPasting = true
		case ansi.BracketedPasteEnd:
			s.keyboardPasting = false
		default:
			if !s.keyboardPasting {
				if string(sequence) == "\x1b[17~" {
					flush()
					if s.review.Request == "quit-app" {
						return
					}
					if s.review.Request == "paste-agent" || s.review.Request == "paste-context" || s.review.Request == "paste-checkpoint" {
						s.pasteInput = append(s.pasteInput, data...)
						return
					}
					if s.pastePending {
						s.pasteInput = append(s.pasteInput, sequence...)
					} else {
						s.toggleFooter()
					}
					data = data[end:]
					continue
				}
				if key, ok := commandShortcut(sequence); ok {
					flush()
					if s.review.Request == "quit-app" {
						return
					}
					if s.review.Request == "paste-agent" || s.review.Request == "paste-context" || s.review.Request == "paste-checkpoint" {
						s.pasteInput = append(s.pasteInput, data...)
						return
					}
					// Command aliases follow the configured Ctrl chord. They must
					// not resurrect an old binding after the user frees that key.
					if key != 0 && s.review.GlobalAction(review.ControlKey(key)) == "" && !s.review.EditingGlobalHotkey() {
						decoded = append(decoded, sequence...)
						data = data[end:]
						continue
					}
					if key != 0 {
						decoded = append(decoded, key)
					}
					data = data[end:]
					continue
				}
			}
		}
		decoded = append(decoded, sequence...)
		data = data[end:]
	}
	s.handleLegacyInput(decoded, child)
}

func (s *screenState) flushKeyboardInput() {
	data := s.keyboardPending
	s.keyboardPending = nil
	if len(data) > 0 {
		s.handleLegacyInput(data, s.agentInput)
	}
}

// commandShortcut maps only the six global shortcuts. Unknown sequences and
// additional modifiers retain their original bytes; key releases are consumed.
func commandShortcut(sequence []byte) (byte, bool) {
	if !bytes.HasPrefix(sequence, []byte("\x1b[")) || sequence[len(sequence)-1] != 'u' {
		return 0, false
	}
	params := strings.Split(string(sequence[2:len(sequence)-1]), ";")
	if len(params) != 2 {
		return 0, false
	}
	code, _, _ := strings.Cut(params[0], ":")
	key, err := strconv.Atoi(code)
	if err != nil {
		return 0, false
	}
	mod, event, _ := strings.Cut(params[1], ":")
	modifier, err := strconv.Atoi(mod)
	// Ignore Caps Lock and Num Lock, but no extra held modifiers.
	if err != nil || modifier < 1 || (modifier-1)&^192 != 8 {
		return 0, false
	}
	var control byte
	switch key {
	case 'g':
		control = 0x07
	case ']':
		control = 0x1d
	case 'n':
		control = 0x0e
	case 'p':
		control = 0x10
	case 'w':
		control = 0x17
	case 'q':
		control = 0x11
	default:
		return 0, false
	}
	switch event {
	case "", "1":
		return control, true
	case "2", "3": // Do not repeat pane switches, terminal creation or closes.
		return 0, true
	default:
		return 0, false
	}
}
