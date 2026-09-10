package app

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nccapo/stvena/internal/diffview"
	"github.com/nccapo/stvena/internal/editor"
	"github.com/nccapo/stvena/internal/session"
)

func TestChangesRefreshFromExternalProcess(t *testing.T) {
	root, _ := workflowProject(t)
	saved, err := session.Open(root, true, "")
	if err != nil {
		t.Fatal(err)
	}
	defer saved.Close()
	events, stop, done := make(chan any, 1), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		watchSnapshots(root, nil, saved, events, stop)
	}()
	defer func() { close(stop); <-done }()
	s := screenState{root: root, session: saved}
	s.review.Source = "session"
	await := func(matches func(diffEvent) bool) diffEvent {
		t.Helper()
		timer := time.NewTimer(10 * time.Second)
		defer timer.Stop()
		for {
			select {
			case event := <-events:
				value := event.(diffEvent)
				s.sessionView = value.session
				s.updateSource()
				if value.session.Err != nil {
					t.Fatal(value.session.Err)
				}
				if matches(value) {
					return value
				}
			case <-timer.C:
				t.Fatalf("external edits did not reach Changes: %+v", s.review.Snapshot)
			}
		}
	}
	await(func(e diffEvent) bool { return e.session.Tree != "" })
	for _, script := range []string{
		"printf 'package main\nfunc changed() {}\n' > a.go; printf 'new file\n' > new.txt; rm b.go",
		"printf 'package main\nfunc updated() {}\n' > a.go; mv new.txt renamed.txt",
	} {
		cmd := exec.Command("sh", "-c", script)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("external writer: %s %v", out, err)
		}
		want, added := "+func changed() {}", "new.txt"
		if strings.Contains(script, "updated") {
			want, added = "+func updated() {}", "renamed.txt"
		}
		e := await(func(e diffEvent) bool {
			found := map[string]diffview.File{}
			for _, f := range s.review.Snapshot.Files {
				found[f.Path] = f
			}
			return strings.Contains(strings.Join(found["a.go"].Lines, "\n"), want) &&
				found["b.go"].Status == "D" && found[added].Status == "A" && e.snapshot.Err == nil
		})
		if e.project.Tree != e.session.Tree || e.snapshot.Tree != e.session.Tree {
			t.Fatal("views did not refresh to the same captured files")
		}
		data, err := os.ReadFile(filepath.Join(root, ".git", "stvena-live.json"))
		if err != nil {
			t.Fatal(err)
		}
		var live editor.State
		if err := json.Unmarshal(data, &live); err != nil {
			t.Fatal(err)
		}
		if !live.Active || live.Session != saved.ID || live.Sequence == 0 || live.Error != "" {
			t.Fatalf("watcher did not publish editor state: %+v", live)
		}
		found := false
		for _, change := range live.Files {
			if change.Path == "a.go" && change.Line == 2 {
				found = true
			}
		}
		if !found {
			t.Fatalf("editor bridge missed edited line: %+v", live.Files)
		}
	}
}
