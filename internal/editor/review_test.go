package editor

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/nccapo/stvena/internal/session"
)

func TestReviewPublisherAndEditorRequests(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %s: %v", out, err)
	}
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("one\ntwo\n"), 0600); err != nil {
		t.Fatal(err)
	}
	saved, err := session.Open(root, false, "")
	if err != nil {
		t.Fatal(err)
	}
	defer saved.Close()
	publisher, err := OpenReview(saved)
	if err != nil {
		t.Fatal(err)
	}
	focus := &ReviewFocus{Tree: saved.Baseline, Source: "session", Path: "file.txt", Line: 2, EndLine: 2}
	if err := publisher.Publish(focus, 3, 4, 1); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(publisher.path)
	if err != nil {
		t.Fatal(err)
	}
	var state ReviewState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	if state.Sequence != 1 || state.Focus == nil || state.Focus.Path != "file.txt" || state.UnreviewedHunks != 4 {
		t.Fatalf("review state: %+v", state)
	}
	if err := publisher.Publish(focus, 3, 4, 1); err != nil || publisher.state.Sequence != 1 {
		t.Fatalf("unchanged review advanced: %+v %v", publisher.state, err)
	}

	reader, err := OpenRequests(saved)
	if err != nil {
		t.Fatal(err)
	}
	request := Request{Version: 1, Session: saved.ID, ID: "request-1", Action: "context", Path: "file.txt", Line: 1, EndLine: 2, UpdatedAt: time.Now().UTC()}
	if err := session.AtomicJSON(reader.path, request); err != nil {
		t.Fatal(err)
	}
	got, err := reader.Poll()
	if err != nil || got == nil || got.Action != "context" || got.EndLine != 2 {
		t.Fatalf("request: %+v %v", got, err)
	}
	if duplicate, err := reader.Poll(); err != nil || duplicate != nil {
		t.Fatalf("duplicate request returned: %+v %v", duplicate, err)
	}
	reopenedReader, err := OpenRequests(saved)
	if err != nil {
		t.Fatal(err)
	}
	if stale, err := reopenedReader.Poll(); err != nil || stale != nil {
		t.Fatalf("request replayed after reader reopen: %+v %v", stale, err)
	}
	request.ID, request.Path = "request-2", "../outside"
	if err := session.AtomicJSON(reader.path, request); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Poll(); err == nil {
		t.Fatal("unsafe editor path accepted")
	}
	publisher.Close()
	data, err = os.ReadFile(publisher.path)
	if err != nil || json.Unmarshal(data, &state) != nil || state.Active {
		t.Fatalf("review publisher remained active: %+v %v", state, err)
	}
}
