package review

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nccapo/stvena/internal/diffview"
	"github.com/nccapo/stvena/internal/repo"
	"github.com/nccapo/stvena/internal/session"
)

func TestBriefMutationsAndLimits(t *testing.T) {
	var s State
	if err := s.AddBriefItem(" first "); err != nil {
		t.Fatal(err)
	}
	if err := s.AddBriefItem("second"); err != nil {
		t.Fatal(err)
	}
	if err := s.EditBriefItem(0, "changed"); err != nil {
		t.Fatal(err)
	}
	s.MoveBriefItem(0, 1)
	if s.Brief[1].Text != "changed" {
		t.Fatal(s.Brief)
	}
	s.ToggleBriefItemDone(1)
	if !s.Brief[1].Done || s.Brief[1].CheckedAt.IsZero() {
		t.Fatal("manual mark missing")
	}
	s.ToggleBriefItemDone(1)
	if s.Brief[1].Done || !s.Brief[1].CheckedAt.IsZero() {
		t.Fatal("manual unmark missing")
	}
	for len(s.Brief) < 30 {
		if err := s.AddBriefItem("item"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.AddBriefItem("overflow"); err == nil || len(s.Brief) != 30 {
		t.Fatal("31st item accepted")
	}
	old := s.Brief[0].Text
	if err := s.EditBriefItem(0, strings.Repeat("界", 201)); err == nil || s.Brief[0].Text != old {
		t.Fatal("201 rune edit accepted")
	}
	if err := s.EditBriefItem(0, "  "); err == nil || s.Brief[0].Text != old {
		t.Fatal("empty edit accepted")
	}
	for i := 0; i < 8; i++ {
		if err := s.attachBriefEvidence(0, Evidence{Kind: "check"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.attachBriefEvidence(0, Evidence{Kind: "check"}); err == nil || len(s.Brief[0].Evidence) != 8 {
		t.Fatal("ninth evidence accepted")
	}
	s.RemoveBriefEvidence(0, 0)
	if len(s.Brief[0].Evidence) != 7 {
		t.Fatal("evidence removal failed")
	}
	s.DeleteBriefItem(1)
	if len(s.Brief) != 29 || s.Request != "save" {
		t.Fatal("delete failed")
	}
}

func TestBriefEvidenceSourcesStalenessAndManualOnly(t *testing.T) {
	s := selectionState()
	if err := s.AddBriefItem("outcome"); err != nil {
		t.Fatal(err)
	}
	s.Browser = true
	if err := s.AttachSelectionEvidence(0); err == nil || len(s.Brief[0].Evidence) != 0 {
		t.Fatal("unavailable selection accepted")
	}
	s.Browser = false
	if err := s.AttachCheckEvidence(0); err == nil || len(s.Brief[0].Evidence) != 0 {
		t.Fatal("missing check accepted")
	}
	if err := s.AttachSelectionEvidence(0); err != nil {
		t.Fatal(err)
	}
	code := s.Brief[0].Evidence[0]
	if code.Kind != "code" || code.Path != "目录/a.go" || code.Tree != s.Snapshot.Tree || code.Start == 0 || code.Revision == "" {
		t.Fatalf("bad code evidence: %+v", code)
	}
	s.CheckCommand, s.CheckStatus, s.CheckTree, s.CheckFinishedAt = "go test", "passed", s.Snapshot.Tree, time.Now().UTC()
	if err := s.AttachCheckEvidence(0); err != nil {
		t.Fatal(err)
	}
	if s.Brief[0].Done {
		t.Fatal("passing check marked item")
	}
	s.Comments = []Comment{{ID: "c1", Path: code.Path, Scope: code.Scope, Revision: code.Revision}}
	if err := s.AttachCommentEvidence(0, "c1"); err != nil {
		t.Fatal(err)
	}
	if err := s.AttachCommentEvidence(0, "missing"); err == nil {
		t.Fatal("missing comment accepted")
	}
	s.CheckRunning = true
	if err := s.AttachCheckEvidence(0); err == nil {
		t.Fatal("running check accepted")
	}
	s.CheckRunning = false
	s.CheckFinishedAt = s.CheckFinishedAt.Add(time.Second)
	if stale, why := s.BriefEvidenceStale(s.Brief[0].Evidence[1]); !stale || why != "newer run" {
		t.Fatalf("same-command rerun: %v %s", stale, why)
	}
	s.CheckCommand = "different"
	s.LiveTree = "new tree"
	if stale, why := s.BriefEvidenceStale(s.Brief[0].Evidence[1]); !stale || why != "code changed since the run" {
		t.Fatalf("changed tree: %v %s", stale, why)
	}
	next := s.Snapshot
	next.Tree = "new tree"
	next.Files = append([]diffview.File(nil), next.Files...)
	next.Files[0].Lines = []string{"@@ -5 +8 @@", "-old()", "+different()"}
	s.Update(next)
	s.AgentStatus = "done"
	if s.Brief[0].Done || len(s.Brief[0].Evidence) != 3 {
		t.Fatal("snapshot or agent completion changed brief")
	}
	if stale, _ := s.BriefEvidenceStale(code); !stale {
		t.Fatal("code revision not stale")
	}
	if stale, _ := s.BriefEvidenceStale(s.Brief[0].Evidence[2]); !stale {
		t.Fatal("comment revision not stale")
	}
	s.Comments = nil
	if stale, why := s.BriefEvidenceStale(s.Brief[0].Evidence[2]); !stale || why != "comment deleted" {
		t.Fatal("deleted comment not stale")
	}
	if _, _, n := s.BriefCounts(); n != 3 {
		t.Fatalf("stale count = %d", n)
	}
	s.ToggleBriefItemDone(0)
	if !s.Brief[0].Done {
		t.Fatal("manual mark failed")
	}
}

func TestBriefPersistenceAndRetainedCodeTree(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init").CombinedOutput(); err != nil {
		t.Fatalf("%s: %v", out, err)
	}
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("before\n"), 0644); err != nil {
		t.Fatal(err)
	}
	capture, err := session.Open(repo.Git(root), false, "")
	if err != nil {
		t.Fatal(err)
	}
	defer capture.Close()
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("after\n"), 0644); err != nil {
		t.Fatal(err)
	}
	tree, err := capture.Capture()
	if err != nil {
		t.Fatal(err)
	}
	var s State
	if err := s.Load(repo.Git(root)); err != nil {
		t.Fatal(err)
	}
	s.Update(diffview.CompareTrees(repo.Git(root), capture.Baseline, tree))
	s.PatchFocused = true
	for i, line := range s.DisplayLines() {
		if line.New > 0 {
			s.Scroll = i
			break
		}
	}
	if err := s.AddBriefItem("keep API stable"); err != nil {
		t.Fatal(err)
	}
	if err := s.AttachSelectionEvidence(0); err != nil {
		t.Fatal(err)
	}
	s.ToggleBriefItemDone(0)
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	var loaded State
	if err := loaded.Load(repo.Git(root)); err != nil {
		t.Fatal(err)
	}
	if len(loaded.Brief) != 1 || !loaded.Brief[0].Done || loaded.Brief[0].Text != "keep API stable" || len(loaded.Brief[0].Evidence) != 1 || loaded.Brief[0].Evidence[0].Tree != tree {
		t.Fatalf("round trip: %+v", loaded.Brief)
	}
	if out, err := exec.Command("git", "-C", root, "cat-file", "-e", tree).CombinedOutput(); err != nil {
		t.Fatalf("tree missing: %s %v", out, err)
	}
	if !s.retained[tree] {
		t.Fatal("evidence tree not retained")
	}
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("changed again\n"), 0644); err != nil {
		t.Fatal(err)
	}
	nextTree, err := capture.Capture()
	if err != nil {
		t.Fatal(err)
	}
	s.Update(diffview.CompareTrees(repo.Git(root), capture.Baseline, nextTree))
	if stale, _ := s.BriefEvidenceStale(s.Brief[0].Evidence[0]); !stale {
		t.Fatal("changed code not stale")
	}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	var staleLoaded State
	if err := staleLoaded.Load(repo.Git(root)); err != nil {
		t.Fatal(err)
	}
	staleLoaded.Update(diffview.CompareTrees(repo.Git(root), capture.Baseline, nextTree))
	if len(staleLoaded.Brief[0].Evidence) != 1 {
		t.Fatal("stale evidence lost on save/load")
	}
	if stale, _ := staleLoaded.BriefEvidenceStale(staleLoaded.Brief[0].Evidence[0]); !stale {
		t.Fatal("reloaded evidence no longer stale")
	}
}
