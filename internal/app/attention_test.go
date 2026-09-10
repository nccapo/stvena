package app

import (
	"context"
	"testing"
	"time"

	"github.com/nccapo/stvena/internal/attention"
	"github.com/nccapo/stvena/internal/diffview"
	"github.com/nccapo/stvena/internal/session"
)

func attentionFile(path, body string) diffview.File {
	return diffview.File{Path: path, Scope: diffview.Session, Status: "M", Added: 1, Deleted: 1, AfterOID: body, Lines: []string{"@@ -1 +1 @@", "-old", "+" + body}}
}
func TestBackgroundAgentReviewAcknowledgementAndIsolation(t *testing.T) {
	s := terminalState(t)
	a := s.activeAgent()
	s.agentShortcut(0x1d)
	s.agentPickerKey("l")
	b := s.activeAgent()
	s.selectAgent(0)
	s.session = &session.Session{}
	now := time.Now().Add(-time.Second)
	s.applyAttention(attentionEvent{b, []attention.Event{{Kind: "PostToolUse", At: now, Changes: []attention.Change{{Path: "b.go", Version: "v1"}}}, {Kind: "Stop", At: now.Add(time.Millisecond)}}})
	if s.activeAgent() != a || s.diffFocused || a.attention.Unreviewed() != 0 || b.attention.Review() != attention.Changed {
		t.Fatal("background event leaked or stole focus")
	}
	f := attentionFile("b.go", "v1")
	s.sessionView = diffview.Snapshot{Files: []diffview.File{f}, UpdatedAt: time.Now(), Tree: "tree"}
	s.sessionView.Finish()
	s.refreshAttention()
	if b.attention.Attention() != "review_needed" || b.attention.Execution != attention.Completed {
		t.Fatal(b.attention)
	}
	s.selectAgent(1)
	s.refreshAttention()
	if b.attention.Unreviewed() != 1 {
		t.Fatal("opening terminal cleared review")
	}
	s.selectAgent(0)
	s.handleInput([]byte{0x19}, s.agentInput)
	if s.activeAgent() != b || !s.diffFocused || s.review.Current() == nil || s.review.Current().Path != "b.go" {
		t.Fatal("next attention did not open relevant diff")
	}
	s.review.Key(" ", 20)
	s.dispatch(context.Background(), make(chan any, 1), make(chan struct{}))
	if b.attention.Review() != attention.Reviewed || b.attention.Attention() != "completed" {
		t.Fatal("file acknowledgement did not clear", b.attention)
	}
	// A later edit by A to the same file must not reopen B's completed review.
	s.applyAttention(attentionEvent{a, []attention.Event{{Kind: "PostToolUse", At: time.Now(), Changes: []attention.Change{{Path: "b.go", Version: "v2"}}}}})
	s.sessionView.Files[0] = attentionFile("b.go", "v2")
	s.sessionView.UpdatedAt = time.Now()
	s.sessionView.Finish()
	s.updateSource()
	s.refreshAttention()
	if a.attention.Unreviewed() != 1 || b.attention.Unreviewed() != 0 {
		t.Fatal("later writer contaminated acknowledged owner")
	}
}
func TestPinnedOldReviewCannotAcknowledgeNewChanges(t *testing.T) {
	s := terminalState(t)
	a := s.activeAgent()
	now := time.Now().Add(-time.Second)
	old := attentionFile("a.go", "v1")
	s.sessionView = diffview.Snapshot{Files: []diffview.File{old}, UpdatedAt: now, Tree: "old"}
	s.review.Source = "session"
	s.review.Update(s.sessionView)
	s.review.Pinned = true
	s.applyAttention(attentionEvent{a, []attention.Event{{Kind: "PostToolUse", At: now.Add(time.Millisecond), Changes: []attention.Change{{Path: "a.go", Version: "v2"}}}}})
	s.sessionView = diffview.Snapshot{Files: []diffview.File{attentionFile("a.go", "v2")}, UpdatedAt: time.Now(), Tree: "new"}
	s.refreshAttention()
	s.review.Key(" ", 20)
	s.refreshAttention()
	if a.attention.Unreviewed() != 1 {
		t.Fatal("stale pinned acknowledgement cleared new version")
	}
	s.nextAttention()
	if !s.review.Pinned || s.review.Snapshot.Tree != "old" {
		t.Fatal("attention replaced pinned review")
	}
}
func TestAllHunksAcknowledgeAttention(t *testing.T) {
	s := terminalState(t)
	a := s.activeAgent()
	now := time.Now().Add(-time.Second)
	f := attentionFile("a.go", "v1")
	f.Lines = append(f.Lines, "@@ -8 +8 @@", "-old2", "+new2")
	s.applyAttention(attentionEvent{a, []attention.Event{{Kind: "PostToolUse", At: now, Changes: []attention.Change{{Path: "a.go", Version: "v1"}}}}})
	s.sessionView = diffview.Snapshot{Files: []diffview.File{f}, UpdatedAt: time.Now(), Tree: "tree"}
	s.review.Source = "session"
	s.review.Update(s.sessionView)
	s.refreshAttention()
	s.review.Key("H", 20)
	s.refreshAttention()
	if a.attention.Unreviewed() != 1 {
		t.Fatal("one hunk cleared entire file")
	}
	s.review.Scroll = 3
	s.review.Key("H", 20)
	s.refreshAttention()
	if a.attention.Unreviewed() != 0 {
		t.Fatal("all hunks did not acknowledge file")
	}
}
func TestAttentionRemappingAndQueueNavigation(t *testing.T) {
	s := terminalState(t)
	s.agentShortcut(0x1d)
	s.agentPickerKey("l")
	s.agentShortcut(0x1d)
	s.agentPickerKey("c")
	s.agents[0].attention.Execution = attention.Waiting
	s.agents[1].attention.Execution = attention.Error
	s.agents[2].attention.Execution = attention.Running
	s.review.Hotkeys = map[string]string{"Ctrl-Y": "Ctrl-X"}
	s.selectAgent(0)
	s.handleInput([]byte{0x19}, s.agentInput)
	if s.activeAgentIndex != 0 {
		t.Fatal("old chord still active")
	}
	s.handleInput([]byte{0x18}, s.agentInput)
	if s.activeAgentIndex != 1 {
		t.Fatal("did not choose error")
	}
	s.handleInput([]byte{0x18}, s.agentInput)
	if s.activeAgentIndex != 0 {
		t.Fatal("did not cycle to waiting")
	}
	s.handleInput([]byte{0x18}, s.agentInput)
	if s.activeAgentIndex != 2 {
		t.Fatal("did not cycle to running")
	}
	s.agentShortcut(0x10)
	s.activateFooter("next-attention")
	if s.activeAgentIndex != 1 {
		t.Fatal("manual navigation did not reset priority queue")
	}
}

func TestRevertedBeforeCaptureStillHasReviewAcknowledgement(t *testing.T) {
	s := terminalState(t)
	a := s.activeAgent()
	s.session = &session.Session{}
	s.applyAttention(attentionEvent{a, []attention.Event{{Kind: "PostToolUse", At: time.Now().Add(-time.Second), Changes: []attention.Change{{Path: "gone.go", Version: "deleted"}}}}})
	s.sessionView = diffview.Snapshot{Tree: "tree", UpdatedAt: time.Now()}
	s.refreshAttention()
	s.nextAttention()
	if s.review.Current() == nil || s.review.Current().Path != "gone.go" || !s.review.Pinned {
		t.Fatal("no acknowledgement for reverted change")
	}
	s.review.Key(" ", 20)
	s.refreshAttention()
	if a.attention.Unreviewed() != 0 {
		t.Fatal("reverted change could not be acknowledged")
	}
}

func TestCaptureStartedBeforeEditDoesNotMakeItReviewable(t *testing.T) {
	s := terminalState(t)
	a := s.activeAgent()
	now := time.Now()
	s.sessionView = diffview.Snapshot{Tree: "old", UpdatedAt: now.Add(-time.Second)}
	s.applyAttention(attentionEvent{a, []attention.Event{{Kind: "PostToolUse", At: now, Changes: []attention.Change{{Path: "a.go", Version: "new"}}}}})
	if a.attention.Review() != attention.Changed || len(a.reviewFiles) != 0 {
		t.Fatal("older capture made new edit reviewable")
	}
	s.sessionView = diffview.Snapshot{Tree: "new", UpdatedAt: now.Add(time.Millisecond), Files: []diffview.File{attentionFile("a.go", "new")}}
	s.refreshAttention()
	if a.attention.Review() != attention.ReviewNeeded {
		t.Fatal(a.attention)
	}
}

func TestNewUrgentStateResetsAttentionCycle(t *testing.T) {
	s := terminalState(t)
	first := s.activeAgent()
	s.agentShortcut(0x1d)
	s.agentPickerKey("l")
	second := s.activeAgent()
	first.attention.Execution = attention.Running
	second.attention.Execution = attention.Waiting
	s.nextAttention()
	if s.activeAgent() != second {
		t.Fatal("waiting not selected")
	}
	s.applyAttention(attentionEvent{first, []attention.Event{{Kind: "StopFailure", At: time.Now()}}})
	s.nextAttention()
	if s.activeAgent() != first {
		t.Fatal("new error skipped")
	}
	s.review.Browser = false
	s.diffFocused = true
	s.agentExited(first, nil)
	if s.review.Browser || !s.diffFocused {
		t.Fatal("exit interrupted review")
	}
}

func TestRenamedPathsUseTheCapturedRenameDiff(t *testing.T) {
	s := terminalState(t)
	a := s.activeAgent()
	s.session = &session.Session{}
	s.applyAttention(attentionEvent{a, []attention.Event{{Kind: "PostToolUse", At: time.Now().Add(-time.Second), Changes: []attention.Change{{Path: "old.go", Version: "deleted"}, {Path: "new.go", Version: "new"}}}}})
	f := attentionFile("new.go", "new")
	f.OldPath = "old.go"
	f.Status = "R"
	s.sessionView = diffview.Snapshot{Tree: "tree", UpdatedAt: time.Now(), Files: []diffview.File{f}}
	s.refreshAttention()
	s.nextAttention()
	if s.review.Pinned || a.reviewFiles["old.go"].OldPath != "old.go" {
		t.Fatal("rename mistaken for no-net-change")
	}
	s.review.Key(" ", 20)
	s.refreshAttention()
	if a.attention.Unreviewed() != 0 {
		t.Fatal("rename acknowledgement left old path pending")
	}
}
