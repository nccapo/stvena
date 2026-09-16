package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nccapo/stvena/internal/diffview"
	"github.com/nccapo/stvena/internal/editor"
	"github.com/nccapo/stvena/internal/review"

	"github.com/nccapo/stvena/internal/repo"
)

// Queue decisions from successive captures while the agent keeps editing.
// A newly inserted hunk changes ordinal positions but must not change targets.
func TestRejectionQueuePreservesTargetsAcrossCaptures(t *testing.T) {
	for _, scenario := range []struct {
		name                  string
		stale, staged, insert bool
	}{
		{name: "unstaged"},
		{name: "staged", staged: true},
		{name: "inserted_line", insert: true},
		{name: "stale_working_tree", stale: true},
		{name: "stale_index_and_working_tree", stale: true, staged: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			root := t.TempDir()
			rejectGit(t, root, "init")
			path := filepath.Join(root, "file.txt")
			lines := make([]string, 60)
			for i := range lines {
				lines[i] = fmt.Sprintf("line %02d", i+1)
			}
			write := func() {
				t.Helper()
				if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			capture := func() diffview.File {
				t.Helper()
				snapshot := diffview.Collect(repo.Git(root))
				if snapshot.Err != nil {
					t.Fatal(snapshot.Err)
				}
				for _, f := range snapshot.Files {
					if f.Scope == diffview.Unstaged {
						return f
					}
				}
				t.Fatal("missing changed file")
				return diffview.File{}
			}
			write()
			rejectGit(t, root, "add", ".")
			rejectGit(t, root, "commit", "-m", "initial")
			lines[19], lines[39], lines[59] = "REJECT TWENTY", "REJECT FORTY", "KEEP SIXTY"
			write()
			first := capture()
			var queue review.State
			if err := queue.Reject(first, 0, 20, 20, ""); err != nil {
				t.Fatal(err)
			}
			// An extra change before the queued hunk shifts the second target from index 1 to 2.
			lines[0] = "KEEP ONE"
			if scenario.insert {
				lines = append([]string{"EXTRA LINE"}, lines...)
			}
			write()
			second := capture()
			if len(review.HunkRanges(second.Lines)) != 4 {
				t.Fatal("fixture needs four separate hunks")
			}
			if err := queue.Reject(second, 2, 40, 40, ""); err != nil {
				t.Fatal(err)
			}
			if scenario.stale {
				lines[39] = "EDITED AGAIN"
				write()
			}
			if scenario.staged {
				rejectGit(t, root, "add", "file.txt")
			}
			beforeApply, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			_, applyErr := diffview.Revert(repo.Git(root), queue.RejectionTargets())
			if scenario.stale {
				if applyErr == nil {
					t.Fatal("stale batch succeeded")
				}
				got, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if string(got) != string(beforeApply) {
					t.Fatal("stale batch partially reverted the file")
				}
				if scenario.staged {
					staged, err := exec.Command("git", "-C", root, "show", ":file.txt").Output()
					if err != nil {
						t.Fatal(err)
					}
					if string(staged) != string(beforeApply) {
						t.Fatal("stale batch changed the index")
					}
				}
				return
			}
			if applyErr != nil {
				t.Fatal(applyErr)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(got), "KEEP SIXTY") {
				t.Error("reverted line 60, which was never rejected")
			}
			if strings.Contains(string(got), "REJECT FORTY") {
				t.Error("line 40 was rejected but survived a reported successful revert")
			}
			if !strings.Contains(string(got), "KEEP ONE") {
				t.Error("reverted new unrelated hunk")
			}
			if strings.Contains(string(got), "REJECT TWENTY") {
				t.Error("first queued hunk survived")
			}
			if scenario.staged {
				staged, err := exec.Command("git", "-C", root, "show", ":file.txt").Output()
				if err != nil {
					t.Fatal(err)
				}
				if string(staged) != string(got) {
					t.Fatal("index and working tree disagree after successful apply")
				}
			}

		})
	}
}

func TestEditorWholeFileRejectionCanBeUndoneFromItsHunkLens(t *testing.T) {
	s, file := editorState(t)
	s.applyEditorRequest(editorRequest("reject", file.Path, ""))
	published, _ := s.reviewFilesForEditor()
	if !published[0].Hunks[0].Rejected {
		t.Fatal("whole-file rejection missing from lens")
	}
	// The lens offers Undo with its hunk ID even when Reject All queued the file.
	s.applyEditorRequest(editorRequest("undo-reject", file.Path, published[0].Hunks[0].ID))
	if s.lastEditorRequest.Status != "applied" {
		t.Fatalf("visible Undo action refused: %s", s.lastEditorRequest.Message)
	}
	if len(s.review.PendingRejections()) != 0 {
		t.Fatal("whole-file rejection remained queued")
	}
	published, _ = s.reviewFilesForEditor()
	for _, h := range published[0].Hunks {
		if h.Rejected {
			t.Fatal("another hunk still painted rejected")
		}
	}
	if !strings.Contains(s.lastEditorRequest.Message, "file") {
		t.Fatal("undo did not describe whole-file scope")
	}
}

func TestEditorApplyRejectionsReportsFailure(t *testing.T) {
	s, file := editorState(t)
	s.applyEditorRequest(editorRequest("reject", file.Path, editor.HunkRef(review.HunkID(file, 0))))
	if err := os.WriteFile(filepath.Join(s.ws.Root, file.Path), []byte("changed again\n"), 0644); err != nil {
		t.Fatal(err)
	}
	s.applyEditorRequest(editorRequest("apply-rejections", "", ""))
	if s.lastEditorRequest.Status != "refused" || !strings.Contains(s.lastEditorRequest.Message, "could not be reverted") {
		t.Fatalf("failed revert acknowledged incorrectly: %+v", s.lastEditorRequest)
	}
	if !strings.Contains(s.pendingDraft, "Please undo them yourself") {
		t.Fatal("failed revert lost the agent handoff")
	}
	if len(s.review.PendingRejections()) != 0 {
		t.Fatal("failed batch left in queue")
	}
}

func TestEditorRejectLensOnNewFileRemovesTheFile(t *testing.T) {
	root, _, _ := rejectRepo(t)
	path := filepath.Join(root, "new.txt")
	if err := os.WriteFile(path, []byte("new agent file\n"), 0644); err != nil {
		t.Fatal(err)
	}
	rejectGit(t, root, "add", "new.txt")
	snapshot := diffview.Collect(repo.Git(root))
	if snapshot.Err != nil {
		t.Fatal(snapshot.Err)
	}
	var file diffview.File
	for _, f := range snapshot.Files {
		if f.Path == "new.txt" && f.Scope == diffview.Staged {
			file = f
		}
	}
	if file.Status != "A" {
		t.Fatal("fixture needs added file")
	}
	file.Scope = diffview.Session
	s := &screenState{ws: repo.Git(root)}
	s.sessionView = diffview.Snapshot{Root: root, Tree: "tree", Files: []diffview.File{file}}
	published, _ := s.reviewFilesForEditor()
	if len(published[0].Hunks) != 1 {
		t.Fatal("new file must expose a hunk lens")
	}
	s.applyEditorRequest(editorRequest("reject", file.Path, editor.HunkRef("stale addition")))
	if s.lastEditorRequest.Status != "refused" || len(s.review.PendingRejections()) != 0 {
		t.Fatal("stale new-file click widened into whole-file rejection")
	}
	s.applyEditorRequest(editorRequest("reject", file.Path, published[0].Hunks[0].ID))
	if s.lastEditorRequest.Status != "queued" {
		t.Fatalf("reject lens refused: %+v", s.lastEditorRequest)
	}
	s.applyRejections(false)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("new file survived its Reject lens: %+v", s.review.Rejections)
	}
}

func TestEditorApplyRejectionsPreservesStagedCopyWarning(t *testing.T) {
	s, file := editorState(t)
	rejectGit(t, s.ws.Root, "add", "file.txt")
	path := filepath.Join(s.ws.Root, "file.txt")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(content, []byte("later addition\n")...), 0644); err != nil {
		t.Fatal(err)
	}
	s.applyEditorRequest(editorRequest("reject", file.Path, editor.HunkRef(review.HunkID(file, 0))))
	s.applyEditorRequest(editorRequest("apply-rejections", "", ""))
	if s.lastEditorRequest.Status != "applied" || !strings.Contains(s.lastEditorRequest.Message, "staged copy still holds") {
		t.Fatalf("staged-copy warning was lost: %+v", s.lastEditorRequest)
	}
	staged, err := exec.Command("git", "-C", s.ws.Root, "show", ":file.txt").Output()
	if err != nil {
		t.Fatal(err)
	}
	working, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(staged), "ONE\n") || !strings.HasPrefix(string(working), "one\n") {
		t.Fatal("fixture did not exercise a working-tree-only revert")
	}
}
