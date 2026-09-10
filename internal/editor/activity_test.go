package editor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestReadCommand(t *testing.T) {
	for _, tc := range []struct {
		command, path string
		start, end    int
	}{
		{"sed -n '40,65p' 'space name.go'", "space name.go", 40, 65},
		{"sed -n \"9p\" a.go", "a.go", 9, 9},
		{"head -n 25 a.go", "a.go", 1, 25},
		{"cat a.go", "a.go", 1, 0},
		{"cat a.go b.go", "", 0, 0},
		{"cat $(echo a.go)", "", 0, 0},
		{"cat a.go && rm a.go", "", 0, 0},
		{"sed -n '8,2p' a.go", "", 0, 0},
		{"sed -n '1,5p' a.go > out", "", 0, 0},
		{"head -n -5 a.go", "", 0, 0},
		{"cat \"$FILE\"", "", 0, 0},
		{"rg -n pattern a.go", "", 0, 0},
	} {
		p, a, b := readCommand(tc.command)
		if p != tc.path || a != tc.start || b != tc.end {
			t.Errorf("%s: got %q %d %d", tc.command, p, a, b)
		}
	}
}

func TestRecordHook(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(t.TempDir(), "activity.json")
	t.Setenv("STVENA_ACTIVITY_PATH", target)
	t.Setenv("STVENA_ROOT", root)
	t.Setenv("STVENA_SESSION", "test-session")
	t.Setenv("STVENA_AGENT", "claude")
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("one\ntwo\nthree\n"), 0600); err != nil {
		t.Fatal(err)
	}
	record := func(tool string, input map[string]any) {
		t.Helper()
		data, _ := json.Marshal(map[string]any{"hook_event_name": "PostToolUse", "cwd": root, "tool_name": tool, "tool_use_id": "call-1", "tool_input": input})
		if err := RecordHook(strings.NewReader(string(data))); err != nil {
			t.Fatal(err)
		}
	}
	record("Read", map[string]any{"file_path": "a.go", "offset": 2, "limit": 2})
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	var got Activity
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Path != "a.go" || got.Line != 2 || got.EndLine != 3 || got.Session != "test-session" || got.Agent != "claude" {
		t.Fatalf("%+v", got)
	}
	outside := filepath.Join(t.TempDir(), "outside.go")
	if err := os.WriteFile(outside, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link.go")); err != nil {
		t.Fatal(err)
	}
	record("Read", map[string]any{"file_path": "link.go"})
	record("Read", map[string]any{"file_path": outside})
	record("Bash", map[string]any{"command": "rm a.go"})
	current, _ := os.ReadFile(target)
	if string(current) != string(data) {
		t.Fatal("unsupported or outside read replaced activity")
	}
	record("Bash", map[string]any{"command": "sed -n '1,2p' a.go"})
	current, _ = os.ReadFile(target)
	if err := json.Unmarshal(current, &got); err != nil {
		t.Fatal(err)
	}
	if got.Line != 1 || got.EndLine != 2 {
		t.Fatalf("%+v", got)
	}
}

func TestHookArgs(t *testing.T) {
	args := []string{"codex", "resume", "--last"}
	got := HookArgs(args, "/tmp/agent's dir/stvena")
	if got[1] != "-c" || !strings.Contains(got[2], "hooks.PostToolUse") || !reflect.DeepEqual(got[3:], args[1:]) {
		t.Fatal(got)
	}
	claude := HookArgs([]string{"claude", "--resume"}, "/tmp/stvena")
	var config map[string]any
	if claude[1] != "--settings" || json.Unmarshal([]byte(claude[2]), &config) != nil {
		t.Fatal(claude)
	}
	explicit := []string{"claude", "--settings", "custom.json"}
	if !reflect.DeepEqual(HookArgs(explicit, "/tmp/stvena"), explicit) {
		t.Fatal("overrode explicit settings")
	}
	custom := []string{"custom", "arg"}
	if !reflect.DeepEqual(HookArgs(custom, "/tmp/stvena"), custom) {
		t.Fatal("modified custom command")
	}
}

func TestChangedRanges(t *testing.T) {
	patch := []string{"@@ -1,4 +1,5 @@", " context", "-old", "+new", "+second", " context", "@@ -40,3 +41,2 @@", " context", "-deleted", " context"}
	want := []LineRange{{2, 3}, {42, 42}}
	if got := changedRanges(patch); !reflect.DeepEqual(got, want) {
		t.Fatalf("%+v != %+v", got, want)
	}
}
