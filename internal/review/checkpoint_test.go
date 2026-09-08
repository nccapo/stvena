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

func checkpointSnapshot() diffview.Snapshot {
	s := diffview.Snapshot{Tree: strings.Repeat("a", 40), Files: []diffview.File{
		{Path: "src/a.go", Scope: diffview.Session, Lines: []string{"@@ -1 +1 @@", "-oldA()", "+newA()", "@@ -9 +9 @@", "-oldB()", "+newB()"}},
		{Path: "src/b.go", Scope: diffview.Session, Lines: []string{"@@ -3 +3 @@", "-oldC()", "+newC()"}},
	}}
	s.Finish()
	return s
}

func TestCheckpointPinsAndCountsExistingMarks(t *testing.T) {
	var s State
	view := checkpointSnapshot()
	s.Update(view)
	s.Key(" ", 10)
	s.Key("V", 10) // An existing temporary selection pin becomes explicit.
	if err := s.StartCheckpoint(view); err != nil {
		t.Fatal(err)
	}
	s.Key("V", 10)
	s.Key("V", 10)
	if !s.Pinned || s.Snapshot.Tree != view.Tree {
		t.Fatal("selection released checkpoint")
	}
	f, tf, h, th := s.CheckpointProgress()
	if f != 1 || tf != 2 || h != 2 || th != 3 {
		t.Fatalf("progress: %d/%d files, %d/%d hunks", f, tf, h, th)
	}
	s.Key("N", 10)
	if s.Current().Path != "src/b.go" {
		t.Fatal("next unreviewed missed second file")
	}
	s.Key("H", 10)
	if f, _, h, _ := s.CheckpointProgress(); f != 2 || h != 3 {
		t.Fatal("hunk mark did not update progress")
	}
	s.Key("p", 10)
	s.Key("H", 10)
	if s.Reviewed(*s.Current()) || s.HunkReviewed(*s.Current(), 0) || !s.HunkReviewed(*s.Current(), 1) {
		t.Fatal("unmarking one hunk did not preserve the other file-level reviewed hunk")
	}
	view.Files[0].Lines[2] = "+mutated()"
	if s.Snapshot.Files[0].Lines[2] != "+newA()" {
		t.Fatal("checkpoint aliases source slices")
	}
}

func TestCheckpointLiveChangesStayUnreviewedEvenAfterRevert(t *testing.T) {
	for _, revert := range []bool{false, true} {
		var s State
		original := checkpointSnapshot()
		if err := s.StartCheckpoint(original); err != nil {
			t.Fatal(err)
		}
		s.Key(" ", 10)
		live := checkpointSnapshot()
		live.Tree = strings.Repeat("b", 40)
		live.Files[0].Lines[2] = "+newer()"
		live.Finish()
		s.Update(live)
		if !s.CheckpointNewer() || s.Snapshot.Tree != original.Tree || !s.Reviewed(*s.Current()) {
			t.Fatal("live update replaced checkpoint or its marks")
		}
		if revert {
			s.Update(original)
		}
		s.Key("P", 10)
		if s.Pinned || s.Checkpoint != nil || s.Reviewed(*s.Current()) || s.HunkReviewed(*s.Current(), 0) {
			t.Fatal("changed hunk inherited checkpoint review")
		}
		if !s.HunkReviewed(*s.Current(), 1) {
			t.Fatal("unchanged hunk lost review")
		}
	}
}

func TestCheckpointDraftCombinesExactCommentsAndContext(t *testing.T) {
	var s State
	if err := s.StartCheckpoint(checkpointSnapshot()); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"Handle the empty input", "Keep the error"} {
		s.Key("enter", 10)
		s.Scroll = 2
		if err := s.AddComment(text); err != nil {
			t.Fatal(err)
		}
		if err := s.CollectSelection(); err != nil {
			t.Fatal(err)
		}
		s.Key("n", 10)
	}
	s.Comments = append(s.Comments, Comment{Tree: "other", Text: "unrelated old comment"})
	s.ContextQuestion = "Please fix both issues.\x1b[201~\r"
	s.Key("Z", 10)
	if s.ConfirmAction != "finish-checkpoint" || s.Checkpoint.Draft != "" {
		t.Fatal("incomplete review did not warn")
	}
	s.Key("esc", 10)
	if s.Panel != "" || !s.Pinned {
		t.Fatal("cancel did not return to pinned review")
	}
	s.Key("Z", 10)
	s.Key("enter", 10)
	if s.Panel != "Checkpoint draft" || s.Request != "save" {
		t.Fatal("finish did not open preview")
	}
	draft := s.Checkpoint.Draft
	for _, want := range []string{"src/a.go:1-1", "src/b.go:3-3", "Handle the empty input", "Keep the error", "newA()", "newC()", "New lines: 1-1", "New lines: 3-3", s.Snapshot.Tree, s.Snapshot.Version, "Please fix both issues.", "Review is incomplete"} {
		if !strings.Contains(draft, want) {
			t.Errorf("draft missing %q", want)
		}
	}
	if strings.Contains(draft, "unrelated old comment") || strings.ContainsAny(draft, "\x1b\r") {
		t.Fatal("draft contains unrelated comments or terminal controls")
	}
	before := draft
	live := checkpointSnapshot()
	live.Tree = "new live"
	s.Update(live)
	if s.Checkpoint.Draft != before {
		t.Fatal("live update replaced previewed draft")
	}
}

func TestCheckpointCompleteFinishAndCaptureGuard(t *testing.T) {
	var s State
	if err := s.StartCheckpoint(diffview.Snapshot{}); err == nil || s.Pinned {
		t.Fatal("uncaptured checkpoint accepted")
	}
	if err := s.StartCheckpoint(checkpointSnapshot()); err != nil {
		t.Fatal(err)
	}
	s.Key(" ", 10)
	s.Key("n", 10)
	s.Key(" ", 10)
	s.Key("Z", 10)
	if s.ConfirmAction != "" || s.Panel != "Checkpoint draft" || strings.Contains(s.Checkpoint.Draft, "Review is incomplete") {
		t.Fatal("complete review did not go straight to draft")
	}
}

func TestSavedCheckpointReopensCapturedCodeAndStaleMarks(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatalf("%s: %v", out, err)
	}
	write := func(text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("before\n")
	saved, err := session.Open(root, false, "")
	if err != nil {
		t.Fatal(err)
	}
	defer saved.Close()
	write("captured\n")
	tree, err := saved.Capture()
	if err != nil {
		t.Fatal(err)
	}
	var s State
	if err := s.Load(root); err != nil {
		t.Fatal(err)
	}
	if err := s.StartCheckpoint(diffview.CompareTrees(root, saved.Baseline, tree)); err != nil {
		t.Fatal(err)
	}
	s.Key(" ", 10)
	s.Key("enter", 10)
	s.Scroll = len(s.DisplayLines()) - 1
	if err := s.AddComment("Fix this exact line"); err != nil {
		t.Fatal(err)
	}
	if err := s.CollectSelection(); err != nil {
		t.Fatal(err)
	}
	s.Key("Z", 10)
	write("live\n")
	liveTree, err := saved.Capture()
	if err != nil {
		t.Fatal(err)
	}
	live := diffview.CompareTrees(root, saved.Baseline, liveTree)
	s.Update(live)
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	var reopened State
	if err := reopened.Load(root); err != nil {
		t.Fatal(err)
	}
	reopened.Update(live)
	if !reopened.Pinned || reopened.Snapshot.Tree != tree || reopened.ReviewedCount() != 1 || !reopened.CheckpointNewer() || reopened.Checkpoint.Draft != s.Checkpoint.Draft {
		t.Fatal("saved checkpoint state not restored")
	}
	content := diffview.LoadContent(root, *reopened.Current())
	if content.Err != nil || strings.Join(content.Lines, "\n") != "captured" {
		t.Fatalf("reopened file is not captured code: %+v", content)
	}
	if len(reopened.CheckpointComments()) != 1 || len(reopened.Attachments) != 1 {
		t.Fatal("saved feedback lost")
	}
	reopened.Key("P", 10)
	if reopened.ReviewedCount() != 0 {
		t.Fatal("reopened live version claimed reviewed")
	}
	if err := reopened.Save(); err != nil {
		t.Fatal(err)
	}
	var resumed State
	if err := resumed.Load(root); err != nil {
		t.Fatal(err)
	}
	if resumed.Checkpoint != nil {
		t.Fatal("resumed checkpoint reopened again")
	}
}
