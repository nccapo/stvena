package editor

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPresenceIsIgnoredUnlessItIsCurrentAndWellFormed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stvena-ide.json")
	if got := ReadPresence(path); got != "" {
		t.Fatalf("missing descriptor reported %q", got)
	}
	write := func(body string) {
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	write(`{"version":1,"ide":"VS Code","extension":"0.3.0","updatedAt":"` + now + `"}`)
	if got := ReadPresence(path); got != "VS Code" {
		t.Fatalf("live presence reported %q", got)
	}
	// A stale heartbeat means the editor is gone, not that it is connected.
	old := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
	write(`{"version":1,"ide":"VS Code","updatedAt":"` + old + `"}`)
	if got := ReadPresence(path); got != "" {
		t.Fatalf("stale presence reported %q", got)
	}
	write(`{"version":2,"ide":"VS Code","updatedAt":"` + now + `"}`)
	if got := ReadPresence(path); got != "" {
		t.Fatal("accepted an unknown presence version")
	}
	write(`not json`)
	if got := ReadPresence(path); got != "" {
		t.Fatal("accepted malformed presence")
	}
	// The name reaches the terminal UI, so control characters must not survive.
	write(`{"version":1,"ide":"VS\u001b[31m Code","updatedAt":"` + now + `"}`)
	if got := ReadPresence(path); got != "VS[31m Code" {
		t.Fatalf("control characters survived: %q", got)
	}
}

func TestTerminalNameReadsTheEditorsOwnEnvironment(t *testing.T) {
	env := func(values map[string]string) func(string) string {
		return func(name string) string { return values[name] }
	}
	if got := TerminalName(env(map[string]string{"TERM_PROGRAM": "vscode"})); got != "VS Code" {
		t.Fatalf("vscode terminal reported %q", got)
	}
	if got := TerminalName(env(map[string]string{"TERM_PROGRAM": "Apple_Terminal"})); got != "" {
		t.Fatalf("plain terminal reported %q", got)
	}
	// An explicit override wins, for editors that do not identify themselves.
	if got := TerminalName(env(map[string]string{"STVENA_IDE": "Zed", "TERM_PROGRAM": "vscode"})); got != "Zed" {
		t.Fatalf("override reported %q", got)
	}
}
