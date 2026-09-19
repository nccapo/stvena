package editor

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nccapo/stvena/internal/session"

	"github.com/nccapo/stvena/internal/repo"
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
	saved, err := session.Open(repo.Git(root), false, "")
	if err != nil {
		t.Fatal(err)
	}
	defer saved.Close()
	publisher, err := OpenReview(saved)
	if err != nil {
		t.Fatal(err)
	}
	focus := &ReviewFocus{Tree: saved.Baseline, Source: "session", Path: "file.txt", Line: 2, EndLine: 2}
	update := ReviewUpdate{Focus: focus, Tree: saved.Baseline, Unreviewed: 3, UnreviewedHunks: 4, NewerBatches: 1,
		Files: []ReviewFile{{Path: "file.txt", Status: "M", Hunks: []ReviewHunk{{ID: HunkRef("file.txt:0"), Start: 2, End: 2}}}}}
	if err := publisher.Publish(update); err != nil {
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
	if len(state.Files) != 1 || len(state.Files[0].Hunks) != 1 || state.Files[0].Hunks[0].Start != 2 {
		t.Fatalf("per-hunk state not published: %+v", state.Files)
	}
	if len(state.Features) == 0 {
		t.Fatal("features not advertised")
	}
	if err := publisher.Publish(update); err != nil || publisher.state.Sequence != 1 {
		t.Fatalf("unchanged review advanced: %+v %v", publisher.state, err)
	}
	// A changed hunk mark is a real change and must advance the sequence.
	changed := update
	changed.Files = []ReviewFile{{Path: "file.txt", Status: "M", Reviewed: true,
		Hunks: []ReviewHunk{{ID: HunkRef("file.txt:0"), Start: 2, End: 2, Reviewed: true}}}}
	if err := publisher.Publish(changed); err != nil || publisher.state.Sequence != 2 {
		t.Fatalf("hunk mark did not advance the sequence: %+v %v", publisher.state, err)
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
	// accept-all names no location, only the tree the editor counted from.
	all := Request{Version: 1, Session: saved.ID, ID: "request-3", Action: "accept-all",
		Tree: strings.Repeat("a", 40), UpdatedAt: time.Now().UTC()}
	if err := session.AtomicJSON(reader.path, all); err != nil {
		t.Fatal(err)
	}
	if got, err := reader.Poll(); err != nil || got == nil || got.Tree != all.Tree {
		t.Fatalf("accept-all request: %+v %v", got, err)
	}
	all.ID, all.Tree = "request-4", "HEAD"
	if err := session.AtomicJSON(reader.path, all); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Poll(); err == nil {
		t.Fatal("a tree that is not an object ID was accepted")
	}
	publisher.Close()
	data, err = os.ReadFile(publisher.path)
	if err != nil || json.Unmarshal(data, &state) != nil || state.Active {
		t.Fatalf("review publisher remained active: %+v %v", state, err)
	}
}
