package review

import (
	"strings"
	"testing"

	"github.com/nccapo/stvena/internal/diffview"
)

func rejectFile(path string, hunks int) diffview.File {
	lines := []string{"diff --git a/" + path + " b/" + path, "--- a/" + path, "+++ b/" + path}
	for i := 0; i < hunks; i++ {
		start := 1 + i*10
		lines = append(lines, "@@ -"+itoa(start)+" +"+itoa(start)+" @@", "-old"+itoa(i), "+new"+itoa(i))
	}
	return diffview.File{Path: path, Scope: diffview.Session, Status: "M", Lines: lines, Added: hunks, Deleted: hunks}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var out []byte
	for n > 0 {
		out = append([]byte{byte('0' + n%10)}, out...)
		n /= 10
	}
	return string(out)
}

func TestRejectQueuesWithoutTouchingTheWorkingTree(t *testing.T) {
	var s State
	f := rejectFile("a.go", 2)
	if err := s.Reject(f, 0, 12, 14, "  breaks the   contract\n"); err != nil {
		t.Fatal(err)
	}
	pending := s.PendingRejections()
	if len(pending) != 1 {
		t.Fatalf("expected one pending rejection, got %d", len(pending))
	}
	if pending[0].Reason != "breaks the contract" {
		t.Fatalf("reason not normalised: %q", pending[0].Reason)
	}
	if !strings.Contains(pending[0].Describe(), "a.go lines 12–14") {
		t.Fatalf("unexpected description: %s", pending[0].Describe())
	}

	// The same hunk cannot queue twice; re-rejecting only updates the reason.
	if err := s.Reject(f, 0, 12, 14, "still wrong"); err != nil {
		t.Fatal(err)
	}
	if pending = s.PendingRejections(); len(pending) != 1 || pending[0].Reason != "still wrong" {
		t.Fatalf("duplicate hunk queued: %+v", pending)
	}

	// A second hunk of the same file joins one target.
	if err := s.Reject(f, 1, 22, 24, ""); err != nil {
		t.Fatal(err)
	}
	targets := s.RejectionTargets()
	if len(targets) != 1 || len(targets[0].Hunks) != 2 {
		t.Fatalf("hunks not grouped into one target: %+v", targets)
	}

	// Rejecting the whole file supersedes its queued hunks.
	if err := s.Reject(f, -1, 0, 0, ""); err != nil {
		t.Fatal(err)
	}
	if pending = s.PendingRejections(); len(pending) != 1 || pending[0].Hunk != -1 {
		t.Fatalf("whole-file rejection did not supersede hunks: %+v", pending)
	}
	if targets = s.RejectionTargets(); len(targets) != 1 || len(targets[0].Hunks) != 0 {
		t.Fatalf("expected a whole-file target: %+v", targets)
	}
}

func TestRejectRefusesUnsupportedChanges(t *testing.T) {
	var s State
	for _, f := range []diffview.File{
		{Path: "a.go", Scope: diffview.ProjectScope, Status: "M"},
		{Path: "a.go", Scope: diffview.BranchScope, Status: "M"},
		{Path: "a.go", Scope: diffview.Session, Status: "U"},
		{Path: "a.go", Scope: diffview.Session, Status: "M", Truncated: true},
		{Path: "a.bin", Scope: diffview.Session, Status: "M", Binary: true},
	} {
		if err := s.Reject(f, -1, 0, 0, ""); err == nil {
			t.Fatalf("accepted %+v", f)
		}
	}
	if len(s.PendingRejections()) != 0 {
		t.Fatal("refused rejection was queued anyway")
	}
	if err := s.Reject(rejectFile("a.go", 1), 5, 0, 0, ""); err == nil {
		t.Fatal("accepted an out-of-range hunk")
	}
}

func TestUndoRejectionOnlyWhilePending(t *testing.T) {
	var s State
	f := rejectFile("a.go", 1)
	if err := s.Reject(f, 0, 1, 2, ""); err != nil {
		t.Fatal(err)
	}
	id := s.PendingRejections()[0].ID
	if err := s.UndoRejection("missing"); err == nil {
		t.Fatal("undid an unknown rejection")
	}
	if err := s.UndoRejection(id); err != nil {
		t.Fatal(err)
	}
	if len(s.PendingRejections()) != 0 {
		t.Fatal("rejection survived undo")
	}

	if err := s.Reject(f, 0, 1, 2, ""); err != nil {
		t.Fatal(err)
	}
	applied := s.FinishRejections("")
	if len(applied) != 1 || len(s.PendingRejections()) != 0 {
		t.Fatalf("finish did not clear the queue: %+v", applied)
	}
	if err := s.UndoRejection(applied[0].ID); err == nil {
		t.Fatal("undid an applied rejection")
	}
}

func TestRejectionDraftSeparatesRevertedFromUnreverted(t *testing.T) {
	var s State
	if err := s.Reject(rejectFile("a.go", 1), 0, 12, 14, "wrong contract"); err != nil {
		t.Fatal(err)
	}
	draft := RejectionDraft(s.FinishRejections(""))
	if !strings.Contains(draft, "reverted them in the working tree") ||
		!strings.Contains(draft, "a.go lines 12–14: wrong contract") ||
		!strings.Contains(draft, "Everything else you changed was kept.") {
		t.Fatalf("unexpected reverted draft:\n%s", draft)
	}

	var stale State
	if err := stale.Reject(rejectFile("b.go", 1), -1, 0, 0, ""); err != nil {
		t.Fatal(err)
	}
	draft = RejectionDraft(stale.FinishRejections("patch does not apply"))
	if !strings.Contains(draft, "still in the working tree") || !strings.Contains(draft, "undo them yourself") {
		t.Fatalf("unreverted rejection not reported:\n%s", draft)
	}
	if strings.Contains(draft, "Do not re-apply them") {
		t.Fatalf("claimed a revert that did not happen:\n%s", draft)
	}
	if RejectionDraft(nil) != "" {
		t.Fatal("empty batch produced a draft")
	}
}

func TestRejectDescribesCreatedAndDeletedFiles(t *testing.T) {
	created := diffview.File{Path: "new.go", Scope: diffview.Untracked, Status: "?", Lines: []string{"@@ -0,0 +1 @@", "+hello"}}
	deleted := diffview.File{Path: "gone.go", Scope: diffview.Session, Status: "D", Lines: []string{"@@ -1 +0,0 @@", "-bye"}}
	var s State
	if err := s.Reject(created, -1, 0, 0, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Reject(deleted, -1, 0, 0, ""); err != nil {
		t.Fatal(err)
	}
	pending := s.PendingRejections()
	if len(pending) != 2 {
		t.Fatalf("expected two rejections, got %d", len(pending))
	}
	if !strings.Contains(pending[0].Describe(), "new file, removed") {
		t.Fatalf("created file described as %q", pending[0].Describe())
	}
	if !strings.Contains(pending[1].Describe(), "restored") {
		t.Fatalf("deleted file described as %q", pending[1].Describe())
	}
}

func TestRejectionQueueIsBounded(t *testing.T) {
	var s State
	for i := 0; i < maxPendingRejections+5; i++ {
		f := rejectFile("file"+itoa(i)+".go", 1)
		if err := s.Reject(f, -1, 0, 0, ""); err != nil {
			if i < maxPendingRejections {
				t.Fatalf("refused rejection %d too early: %v", i, err)
			}
			return
		}
	}
	t.Fatalf("queue grew past its limit: %d", len(s.PendingRejections()))
}
