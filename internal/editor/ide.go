package editor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nccapo/stvena/internal/session"
)

// Presence is written by an editor extension to say it is watching this
// repository. The terminal's own environment is not enough: several editors
// report themselves as VS Code, and the extension may not be installed in any
// of them, so IDE mode waits for something that is actually connected.
type Presence struct {
	Version   int       `json:"version"`
	IDE       string    `json:"ide"`
	Extension string    `json:"extension"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// PresencePath is the descriptor an editor extension writes to.
func PresencePath(saved *session.Session) (string, error) {
	return filepath.Join(saved.WS.DescriptorDir(), "stvena-ide.json"), nil
}

// ReadPresence returns the connected editor's name, or an empty string. A
// missing, malformed or stale descriptor simply means no editor is watching;
// this never reports an error, because IDE mode is an offer, not a requirement.
func ReadPresence(path string) string {
	data, err := os.ReadFile(path)
	if err != nil || len(data) > 8*1024 {
		return ""
	}
	var presence Presence
	if json.Unmarshal(data, &presence) != nil || presence.Version != 1 {
		return ""
	}
	if time.Since(presence.UpdatedAt) > 30*time.Second || time.Since(presence.UpdatedAt) < -time.Minute {
		return ""
	}
	return safeName(presence.IDE)
}

// TerminalName identifies the editor whose integrated terminal Stvena is
// running in, from the environment that editor sets.
func TerminalName(environ func(string) string) string {
	if environ("STVENA_IDE") != "" {
		return safeName(environ("STVENA_IDE"))
	}
	switch strings.ToLower(environ("TERM_PROGRAM")) {
	case "vscode":
		// Cursor, Windsurf and other forks all report vscode here, so the name
		// is only a hint; ReadPresence is what confirms a real connection.
		return "VS Code"
	}
	return ""
}

// safeName keeps a descriptor-supplied name printable and short: it is written
// by another process and ends up in the terminal UI.
func safeName(name string) string {
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, name)
	if len([]rune(name)) > 32 {
		name = string([]rune(name)[:32])
	}
	return strings.TrimSpace(name)
}
