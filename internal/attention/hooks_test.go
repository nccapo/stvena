package attention

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func hookRepo(t *testing.T) (string, string) {
	t.Helper()
	root, dir := t.TempDir(), t.TempDir()
	if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	t.Setenv("STVENA_ROOT", root)
	t.Setenv("STVENA_ATTENTION_DIR", dir)
	return root, dir
}
func hook(t *testing.T, kind, tool, id string, input map[string]any) {
	t.Helper()
	data, _ := json.Marshal(map[string]any{"hook_event_name": kind, "tool_name": tool, "tool_use_id": id, "tool_input": input})
	if err := RecordHook(strings.NewReader(string(data))); err != nil {
		t.Fatal(err)
	}
}
func TestHooksAttributeOnlyExplicitChangedFiles(t *testing.T) {
	root, dir := hookRepo(t)
	write := func(p, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, p), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("a.go", "before")
	write("b.go", "before")
	hook(t, "PreToolUse", "Edit", "a", map[string]any{"file_path": "a.go"})
	hook(t, "PreToolUse", "Read", "b", map[string]any{"file_path": "b.go"})
	write("a.go", "after")
	write("b.go", "external edit")
	hook(t, "PostToolUse", "Edit", "a", nil)
	hook(t, "PostToolUse", "Read", "b", nil)
	hook(t, "Stop", "", "", nil)
	events, err := Drain(dir)
	if err != nil {
		t.Fatal(err)
	}
	var changes []Change
	for _, e := range events {
		changes = append(changes, e.Changes...)
	}
	if len(events) != 5 || len(changes) != 1 || changes[0].Path != "a.go" {
		t.Fatal(events)
	}
	if events[len(events)-1].Kind != "Stop" {
		t.Fatal("lifecycle out of order")
	}
	if events, err := Drain(dir); err != nil || len(events) != 0 {
		t.Fatal("events replayed", events, err)
	}
}
func TestPatchAddDeleteRenameAndNoOp(t *testing.T) {
	root, dir := hookRepo(t)
	if err := os.WriteFile(filepath.Join(root, "old.go"), []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	patch := "*** Begin Patch\n*** Update File: old.go\n*** Move to: new.go\n@@\n-before\n+after\n*** Add File: added.go\n+added\n*** End Patch"
	hook(t, "PreToolUse", "apply_patch", "patch", map[string]any{"command": patch})
	for _, p := range []string{"new.go", "added.go"} {
		if err := os.WriteFile(filepath.Join(root, p), []byte("after"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(filepath.Join(root, "old.go")); err != nil {
		t.Fatal(err)
	}
	hook(t, "PostToolUse", "apply_patch", "patch", nil)
	hook(t, "PreToolUse", "Write", "noop", map[string]any{"file_path": "new.go"})
	hook(t, "PostToolUse", "Write", "noop", nil)
	events, _ := Drain(dir)
	n := 0
	for _, e := range events {
		n += len(e.Changes)
	}
	if n != 3 {
		t.Fatal(events)
	}
}
func TestHookPathsAndNotifications(t *testing.T) {
	root, dir := hookRepo(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"../outside", "link/new.go", ".git/config", outside} {
		if got := safePath(root, root, path); got != "" {
			t.Fatal(path, got)
		}
	}
	if got := safePath(root, root, "new/child.go"); got != "new/child.go" {
		t.Fatal(got)
	}
	for _, kind := range []string{"permission_prompt", "idle_prompt", "auth_success"} {
		if err := RecordHook(strings.NewReader(`{"hook_event_name":"Notification","notification_type":"` + kind + `"}`)); err != nil {
			t.Fatal(err)
		}
	}
	events, _ := Drain(dir)
	if len(events) != 2 || events[0].Kind != "waiting" {
		t.Fatal(events)
	}
}
func TestSeparateTerminalEventDirectories(t *testing.T) {
	root, a := hookRepo(t)
	b := t.TempDir()
	hook(t, "PreToolUse", "Write", "same-id", map[string]any{"file_path": "a.go"})
	t.Setenv("STVENA_ATTENTION_DIR", b)
	hook(t, "PreToolUse", "Read", "same-id", map[string]any{"file_path": "a.go"})
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	hook(t, "PostToolUse", "Read", "same-id", nil)
	t.Setenv("STVENA_ATTENTION_DIR", a)
	hook(t, "PostToolUse", "Write", "same-id", nil)
	ea, _ := Drain(a)
	eb, _ := Drain(b)
	if len(ea[1].Changes) != 1 || len(eb[1].Changes) != 0 {
		t.Fatal(ea, eb)
	}
}

func TestIgnoredFilesDoNotBecomeUnreviewableItems(t *testing.T) {
	root, dir := hookRepo(t)
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("ignored.txt\n"), 0600); err != nil {
		t.Fatal(err)
	}
	hook(t, "PreToolUse", "Write", "ignored", map[string]any{"file_path": "ignored.txt"})
	if err := os.WriteFile(filepath.Join(root, "ignored.txt"), []byte("ignored"), 0600); err != nil {
		t.Fatal(err)
	}
	hook(t, "PostToolUse", "Write", "ignored", nil)
	events, _ := Drain(dir)
	for _, e := range events {
		if len(e.Changes) > 0 {
			t.Fatal(events)
		}
	}
}

func TestFailedEditDoesNotClaimAnotherWritersChanges(t *testing.T) {
	root, dir := hookRepo(t)
	hook(t, "PreToolUse", "Edit", "failed", map[string]any{"file_path": "a.go"})
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("another agent wrote this"), 0600); err != nil {
		t.Fatal(err)
	}
	hook(t, "PostToolUseFailure", "Edit", "failed", nil)
	events, _ := Drain(dir)
	for _, e := range events {
		if len(e.Changes) > 0 {
			t.Fatal(events)
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) > 0 {
		t.Fatal("tool baseline leaked", entries)
	}
}
