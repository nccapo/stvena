package review

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/nccapo/stvena/internal/diffview"
	"github.com/nccapo/stvena/internal/repo"
	"github.com/nccapo/stvena/internal/session"
)

type sharedProject struct {
	t    *testing.T
	root string
}

func (p sharedProject) write(name, text string) {
	p.t.Helper()
	if err := os.WriteFile(filepath.Join(p.root, name), []byte(text), 0644); err != nil {
		p.t.Fatal(err)
	}
}

func (p sharedProject) open() *session.Session {
	p.t.Helper()
	saved, err := session.Open(repo.Git(p.root), false, "")
	if err != nil {
		p.t.Fatal(err)
	}
	p.t.Cleanup(func() { saved.Close() })
	return saved
}

func (p sharedProject) view(saved *session.Session) diffview.Snapshot {
	p.t.Helper()
	tree, err := saved.Capture()
	if err != nil {
		p.t.Fatal(err)
	}
	return diffview.CompareTrees(repo.Git(p.root), saved.Baseline, tree)
}

func (p sharedProject) reviewer(view diffview.Snapshot) *State {
	p.t.Helper()
	s := &State{Source: "session"}
	if err := s.Load(repo.Git(p.root)); err != nil {
		p.t.Fatal(err)
	}
	s.Update(view)
	return s
}

func fileIn(t *testing.T, view diffview.Snapshot, path string) diffview.File {
	t.Helper()
	for _, f := range view.Files {
		if f.Path == path {
			return f
		}
	}
	t.Fatalf("%s is not in the view", path)
	return diffview.File{}
}

func save(t *testing.T, s *State) {
	t.Helper()
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
}

// Two Stvena sessions in one project used to keep their own review marks, and
// each save overwrote the other's. They now share them, even when the two
// started at different points and so show different changes.
func TestTwoSessionsShareReviewMarks(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	p := sharedProject{t: t, root: t.TempDir()}
	if out, err := exec.Command("git", "-C", p.root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("%s %v", out, err)
	}
	p.write("a.txt", "a\n")
	p.write("b.txt", "b\n")
	p.write("c.txt", "c\n")
	older := p.open()
	p.write("a.txt", "a changed before the second session\n")
	newer := p.open()
	p.write("b.txt", "b changed by the agent\n")
	p.write("c.txt", "c changed by the agent\n")
	viewA, viewB := p.view(older), p.view(newer)
	fileIn(t, viewA, "a.txt") // only the older session shows a.txt
	for _, f := range viewB.Files {
		if f.Path == "a.txt" {
			t.Fatal("the newer session should not show a.txt")
		}
	}
	a, b := p.reviewer(viewA), p.reviewer(viewB)

	// Accepting in one session accepts in the other.
	a.SetFileReviewed(fileIn(t, viewA, "a.txt"), true)
	a.SetFileReviewed(fileIn(t, viewA, "b.txt"), true)
	save(t, a)
	if !b.SyncMarks() || !b.Reviewed(fileIn(t, viewB, "b.txt")) {
		t.Fatal("an acceptance in one session did not reach the other")
	}
	if b.SyncMarks() {
		t.Fatal("an unchanged file was merged again")
	}

	// A session that does not show a change leaves its mark alone, even when
	// it refreshes and saves.
	b.Update(viewB)
	save(t, b)
	a.SyncMarks()
	a.Update(viewA)
	if !a.Reviewed(fileIn(t, viewA, "a.txt")) {
		t.Fatal("the other session dropped a mark for a change it does not show")
	}

	// Taking an acceptance back travels too, and touches nothing else.
	b.SetFileReviewed(fileIn(t, viewB, "b.txt"), false)
	save(t, b)
	if !a.SyncMarks() || a.Reviewed(fileIn(t, viewA, "b.txt")) || !a.Reviewed(fileIn(t, viewA, "a.txt")) {
		t.Fatal("undoing an acceptance did not travel, or took other marks with it")
	}

	// Two sessions marking before either has seen the other keep both marks.
	a.SetFileReviewed(fileIn(t, viewA, "b.txt"), true)
	b.SetFileReviewed(fileIn(t, viewB, "c.txt"), true)
	save(t, a)
	save(t, b)
	a.SyncMarks()
	if !a.Reviewed(fileIn(t, viewA, "c.txt")) || !a.Reviewed(fileIn(t, viewA, "b.txt")) {
		t.Fatal("concurrent marks overwrote each other")
	}
	fresh := p.reviewer(viewB)
	if !fresh.Reviewed(fileIn(t, viewB, "b.txt")) || !fresh.Reviewed(fileIn(t, viewB, "c.txt")) {
		t.Fatal("the saved file lost a mark")
	}

	// When the agent changes an accepted file again, it needs review again in
	// both sessions, and the old mark does not come back.
	p.write("b.txt", "b changed again\n")
	viewA2, viewB2 := p.view(older), p.view(newer)
	a.Update(viewA2)
	save(t, a)
	b.SyncMarks()
	b.Update(viewB2)
	if b.Reviewed(fileIn(t, viewB2, "b.txt")) || a.Reviewed(fileIn(t, viewA2, "b.txt")) {
		t.Fatal("a changed file kept its acceptance")
	}
	p.write("b.txt", "b changed by the agent\n")
	a.Update(p.view(older))
	b.SyncMarks()
	b.Update(p.view(newer))
	if b.Reviewed(fileIn(t, viewB, "b.txt")) {
		t.Fatal("an acceptance revived when the file returned to the accepted change")
	}
	if !b.Reviewed(fileIn(t, viewB, "c.txt")) {
		t.Fatal("an unrelated acceptance was lost")
	}
}

func TestSimultaneousSavesKeepBothSessionsMarks(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	p := sharedProject{t: t, root: t.TempDir()}
	if out, err := exec.Command("git", "-C", p.root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("%s %v", out, err)
	}
	names := []string{"f0.txt", "f1.txt", "f2.txt", "f3.txt", "f4.txt", "f5.txt", "f6.txt", "f7.txt"}
	for _, name := range names {
		p.write(name, name+"\n")
	}
	saved := p.open()
	for _, name := range names {
		p.write(name, name+" changed\n")
	}
	view := p.view(saved)
	sessions := []*State{p.reviewer(view), p.reviewer(view)}
	done := make(chan error, len(sessions))
	for i, s := range sessions {
		go func(i int, s *State) {
			var err error
			// Each session accepts every other file, saving after each one.
			for j := i; j < len(names) && err == nil; j += 2 {
				s.SetFileReviewed(fileIn(t, view, names[j]), true)
				err = s.Save()
			}
			done <- err
		}(i, s)
	}
	for range sessions {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	fresh := p.reviewer(view)
	for _, name := range names {
		if !fresh.Reviewed(fileIn(t, view, name)) {
			t.Fatalf("%s lost its acceptance when both sessions saved at once", name)
		}
	}
}
