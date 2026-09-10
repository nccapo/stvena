package editor

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/nccapo/stvena/internal/session"
)

func TestPublishCapturedEdits(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %s: %v", args, out, err)
		}
		return string(out)
	}
	git("init", "-q")
	write := func(name, value string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("a.txt", "one\ntwo\nthree\nfour\nold\n")
	write("delete.txt", "delete me\n")
	write("rename.txt", "rename me\n")
	saved, err := session.Open(root, false, "")
	if err != nil {
		t.Fatal(err)
	}
	defer saved.Close()
	publisher, err := Open(saved)
	if err != nil {
		t.Fatal(err)
	}
	read := func() State {
		t.Helper()
		data, err := os.ReadFile(publisher.path)
		if err != nil {
			t.Fatal(err)
		}
		var state State
		if err := json.Unmarshal(data, &state); err != nil {
			t.Fatal(err)
		}
		return state
	}
	publish := func() State {
		t.Helper()
		tree, err := saved.Capture()
		if err != nil {
			t.Fatal(err)
		}
		if err := publisher.Publish(tree, nil); err != nil {
			t.Fatal(err)
		}
		return read()
	}
	initial := publish()
	if initial.Sequence != 0 || len(initial.Files) != 0 || !initial.Active || initial.Version != 1 {
		t.Fatalf("initial state: %+v", initial)
	}
	activity := Activity{Session: saved.ID, ID: "read-1", Agent: "codex", Path: "a.txt", Line: 2, EndLine: 4, UpdatedAt: time.Now().UTC()}
	if err := session.AtomicJSON(ActivityPath(saved), activity); err != nil {
		t.Fatal(err)
	}
	if reading := publish(); reading.Sequence != 0 || reading.Activity == nil || reading.Activity.ID != "read-1" {
		t.Fatalf("read-only activity was not published: %+v", reading)
	}
	activity.UpdatedAt = time.Now().Add(-16 * time.Second)
	if err := session.AtomicJSON(ActivityPath(saved), activity); err != nil {
		t.Fatal(err)
	}
	if publish().Activity != nil {
		t.Fatal("expired activity still advertised")
	}
	activity.UpdatedAt, activity.Session = time.Now(), "another-session"
	if err := session.AtomicJSON(ActivityPath(saved), activity); err != nil {
		t.Fatal(err)
	}
	if publish().Activity != nil {
		t.Fatal("activity from another session advertised")
	}
	write("a.txt", "one\ntwo\nthree\nfour\nnew\n")
	write("added.txt", "added\n")
	write("binary.dat", "\x00\x01")
	if err := os.Remove(filepath.Join(root, "delete.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, "rename.txt"), filepath.Join(root, "renamed.txt")); err != nil {
		t.Fatal(err)
	}
	first := publish()
	files := map[string]Change{}
	for _, file := range first.Files {
		files[file.Path] = file
	}
	if first.Sequence != 1 || len(files) != 5 || files["a.txt"].Line != 5 ||
		files["added.txt"].Status != "A" || files["delete.txt"].Status != "D" ||
		files["renamed.txt"].OldPath != "rename.txt" || !files["binary.dat"].Binary {
		t.Fatalf("captured batch: %+v", first)
	}
	if first.ChangedAt.IsZero() || len(files["a.txt"].Ranges) != 1 || files["a.txt"].Ranges[0] != (LineRange{5, 5}) {
		t.Fatalf("missing edit range or timestamp: %+v", first)
	}
	if got := git("cat-file", "blob", files["a.txt"].Before); got != "one\ntwo\nthree\nfour\nold\n" {
		t.Fatalf("wrong before content: %q", got)
	}
	if idle := publish(); idle.Sequence != first.Sequence || !idle.UpdatedAt.After(first.UpdatedAt) {
		t.Fatalf("heartbeat lost last edit: %+v", idle)
	}
	write("a.txt", "one\ntwo\nthree\nfour\nnewer\n")
	// A failed descriptor replacement must not advance past an unseen capture.
	tree, err := saved.Capture()
	if err != nil {
		t.Fatal(err)
	}
	statePath := publisher.path
	publisher.path = filepath.Join(root, "missing-directory", "state.json")
	if err := publisher.Publish(tree, nil); err == nil {
		t.Fatal("expected descriptor write failure")
	}
	if publisher.state.Sequence != first.Sequence {
		t.Fatal("failed write advanced the published sequence")
	}
	publisher.path = statePath
	second := publish()
	if second.Sequence != 2 || len(second.Files) != 1 || second.Files[0].Before != files["a.txt"].After {
		t.Fatalf("must compare consecutive captures: %+v", second)
	}
	if err := publisher.Publish("", errors.New("capture failed")); err != nil {
		t.Fatal(err)
	}
	if failed := read(); failed.Error != "capture failed" || failed.Sequence != 2 {
		t.Fatalf("capture failure lost previous state: %+v", failed)
	}
	if recovered := publish(); recovered.Error != "" || recovered.Sequence != 2 {
		t.Fatalf("capture did not recover: %+v", recovered)
	}
	publisher.Close()
	if read().Active {
		t.Fatal("session still advertised as active after shutdown")
	}
}

func TestChangedLine(t *testing.T) {
	for _, test := range []struct {
		lines []string
		want  int
	}{
		{[]string{"--- a/file", "+++ b/file", "@@ -8,3 +8,3 @@", " context", "-old", "+new"}, 9},
		{[]string{"@@ -1 +0,0 @@", "-deleted"}, 1},
		{[]string{"@@ -0,0 +1 @@", "+new"}, 1},
		{[]string{"No text patch available"}, 1},
	} {
		if got := changedLine(test.lines); got != test.want {
			t.Errorf("changedLine(%q) = %d, want %d", test.lines, got, test.want)
		}
	}
}
