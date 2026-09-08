package review

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nccapo/stvena/internal/diffview"
	"github.com/nccapo/stvena/internal/session"
)

func TestPinCommentsAndPersistentReview(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init").CombinedOutput(); err != nil {
		t.Fatalf("%s %v", out, err)
	}
	os.WriteFile(filepath.Join(root, "file.txt"), []byte("before\n"), 0644)
	saved, err := session.Open(root, false, "")
	if err != nil {
		t.Fatal(err)
	}
	defer saved.Close()
	os.WriteFile(filepath.Join(root, "file.txt"), []byte("review this\n"), 0644)
	tree, err := saved.Capture()
	if err != nil {
		t.Fatal(err)
	}
	var s State
	s.Source = "session"
	if err = s.Load(root); err != nil {
		t.Fatal(err)
	}
	view := diffview.CompareTrees(root, saved.Baseline, tree)
	s.Update(view)
	s.Key(" ", 10)
	if err = s.Save(); err != nil {
		t.Fatal(err)
	}
	s.Key("P", 10)
	os.WriteFile(filepath.Join(root, "file.txt"), []byte("agent changed it\n"), 0644)
	next, err := saved.Capture()
	if err != nil {
		t.Fatal(err)
	}
	live := diffview.CompareTrees(root, saved.Baseline, next)
	s.Update(live)
	if s.Snapshot.Tree != tree || s.Latest.Tree != next {
		t.Fatal("pin did not retain displayed snapshot")
	}
	s.Scroll = len(s.DisplayLines()) - 1
	if err = s.AddComment("Explain this change"); err != nil {
		t.Fatal(err)
	}
	if len(s.Comments) != 1 || s.Comments[0].Tree != tree || s.Comments[0].Start != 1 {
		t.Fatalf("comment anchored to wrong version: %+v", s.Comments)
	}
	if !s.CommentStale(s.Comments[0]) || !strings.Contains(s.ExportFeedback(), "OUTDATED") {
		t.Fatal("stale feedback not identified")
	}
	var reopened State
	reopened.Source = "session"
	if err = reopened.Load(root); err != nil {
		t.Fatal(err)
	}
	reopened.Update(view)
	if reopened.ReviewedCount() != 1 || len(reopened.Comments) != 1 || len(reopened.History) != 1 {
		t.Fatal("saved review not restored")
	}
	s.Key("P", 10)
	if s.Snapshot.Tree != next || s.ReviewedCount() != 0 {
		t.Fatal("live update did not invalidate changed review")
	}
}
func TestHunkReviewAndNavigation(t *testing.T) {
	var s State
	s.Update(diffview.Snapshot{Tree: strings.Repeat("a", 40), Files: []diffview.File{{Path: "file", Scope: diffview.Session, Lines: []string{"@@ -1 +1 @@", "-old", "+new", "@@ -9 +9 @@", "-other", "+changed"}}}})
	s.Key("H", 10)
	if s.ReviewedCount() != 0 {
		t.Fatal("one hunk marked entire file")
	}
	s.Key("]", 10)
	s.Key("H", 10)
	if s.ReviewedCount() != 1 {
		t.Fatal("all hunks not counted as reviewed")
	}
	s.Key(" ", 10)
	if s.ReviewedCount() != 0 {
		t.Fatal("file unmark retained hunk marks")
	}
	s.Key("G", 10)
	if s.Scroll != 5 {
		t.Fatal("last source line cannot be selected")
	}
	s.Key("V", 10)
	s.Key("up", 10)
	if s.SelectedText() != "other\nchanged" {
		t.Fatalf("range selection: %q", s.SelectedText())
	}
	s.Selecting = false
	s.TextQuery = "new"
	s.SearchNext(1)
	if s.Scroll != 2 {
		t.Fatal("source search failed")
	}
}
