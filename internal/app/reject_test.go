package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nccapo/stvena/internal/attention"
	"github.com/nccapo/stvena/internal/diffview"
)

func rejectGit(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=t@example.com")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %s %v", args, out, err)
	}
}

// rejectRepo builds a repository whose working tree holds one agent edit.
func rejectRepo(t *testing.T) (root, path string, file diffview.File) {
	t.Helper()
	root = t.TempDir()
	rejectGit(t, root, "init")
	path = filepath.Join(root, "file.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0644); err != nil {
		t.Fatal(err)
	}
	rejectGit(t, root, "add", ".")
	rejectGit(t, root, "commit", "-m", "initial")
	if err := os.WriteFile(path, []byte("AGENT\ntwo\nthree\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, f := range diffview.Collect(root).Files {
		if f.Scope == diffview.Unstaged {
			return root, path, f
		}
	}
	t.Fatal("no unstaged change")
	return
}

func TestRejectionsWaitForTheAgentToFinishItsTurn(t *testing.T) {
	root, path, file := rejectRepo(t)
	s := terminalState(t)
	s.root = root
	a := s.activeAgent()
	a.attention.Hooked = true
	a.attention.Execution = attention.Running

	if err := s.review.Reject(file, -1, 0, 0, "not what I asked for"); err != nil {
		t.Fatal(err)
	}
	if ready, reason := s.turnBoundary(); ready || !strings.Contains(reason, "finishes") {
		t.Fatalf("mid-turn revert allowed: ready=%v reason=%q", ready, reason)
	}
	if s.applyRejections(false) {
		t.Fatal("applied rejections while the agent was running")
	}
	if got, _ := os.ReadFile(path); !strings.Contains(string(got), "AGENT") {
		t.Fatalf("working tree changed underneath the agent: %s", got)
	}

	// A waiting agent is mid-turn too: it resumes as soon as the user answers.
	a.attention.Execution = attention.Waiting
	if ready, _ := s.turnBoundary(); ready {
		t.Fatal("reverted while the agent waited on a prompt")
	}

	a.attention.Execution = attention.Completed
	if ready, _ := s.turnBoundary(); !ready {
		t.Fatal("completed turn did not release the queue")
	}
	if !s.applyRejections(false) {
		t.Fatal("queue did not apply at the turn boundary")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "AGENT") {
		t.Fatalf("rejected change survived: %s", got)
	}
	if len(s.review.PendingRejections()) != 0 {
		t.Fatal("queue not cleared after applying")
	}
	if !strings.Contains(s.pendingRejectionDraft, "not what I asked for") ||
		!strings.Contains(s.pendingRejectionDraft, "Do not re-apply") {
		t.Fatalf("agent was not told what was rejected:\n%s", s.pendingRejectionDraft)
	}
	if s.review.Request != "paste-rejections" {
		t.Fatalf("handoff not requested, got %q", s.review.Request)
	}
}

func TestUnhookedAgentNeverAutoApplies(t *testing.T) {
	root, path, file := rejectRepo(t)
	s := terminalState(t)
	s.root = root
	a := s.activeAgent()
	// No hooks: silence means only that nothing was printed, never that the
	// turn ended, so the queue must never fire on its own.
	a.attention.Hooked = false
	a.attention.Execution = attention.Idle

	if err := s.review.Reject(file, -1, 0, 0, ""); err != nil {
		t.Fatal(err)
	}
	ready, reason := s.turnBoundary()
	if ready || !strings.Contains(reason, "does not report turn boundaries") {
		t.Fatalf("unhooked agent treated as idle: ready=%v reason=%q", ready, reason)
	}
	if s.applyRejections(false) {
		t.Fatal("auto-applied without turn events")
	}
	if got, _ := os.ReadFile(path); !strings.Contains(string(got), "AGENT") {
		t.Fatalf("working tree changed: %s", got)
	}

	// Apply now is the explicit override.
	if !s.applyRejections(true) {
		t.Fatal("explicit apply refused")
	}
	if got, _ := os.ReadFile(path); strings.Contains(string(got), "AGENT") {
		t.Fatalf("explicit apply did not revert: %s", got)
	}
}

func TestExitedAgentReleasesTheQueue(t *testing.T) {
	root, _, file := rejectRepo(t)
	s := terminalState(t)
	s.root = root
	a := s.activeAgent()
	a.attention.Hooked = false
	a.exited = true

	if err := s.review.Reject(file, -1, 0, 0, ""); err != nil {
		t.Fatal(err)
	}
	if ready, reason := s.turnBoundary(); !ready {
		t.Fatalf("exited agent still blocked the queue: %q", reason)
	}
}

func TestRejectionStillReachesTheAgentWhenTheRevertFails(t *testing.T) {
	root, path, file := rejectRepo(t)
	s := terminalState(t)
	s.root = root
	s.activeAgent().attention.Hooked = true
	s.activeAgent().attention.Execution = attention.Completed

	if err := s.review.Reject(file, -1, 0, 0, "wrong approach"); err != nil {
		t.Fatal(err)
	}
	// The agent edits the same lines again before the rejection is applied.
	if err := os.WriteFile(path, []byte("AGENT AGAIN\ntwo\nthree\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if !s.applyRejections(false) {
		t.Fatal("apply did not run")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "AGENT AGAIN") {
		t.Fatalf("force-applied a stale patch: %s", got)
	}
	if !strings.Contains(s.pendingRejectionDraft, "still in the working tree") ||
		!strings.Contains(s.pendingRejectionDraft, "wrong approach") {
		t.Fatalf("failed revert was not reported to the agent:\n%s", s.pendingRejectionDraft)
	}
	if len(s.review.PendingRejections()) != 0 {
		t.Fatal("failed batch left the queue pending")
	}
}

func TestStandaloneReviewAppliesImmediately(t *testing.T) {
	root, path, file := rejectRepo(t)
	s := &screenState{root: root}
	if err := s.review.Reject(file, -1, 0, 0, ""); err != nil {
		t.Fatal(err)
	}
	if ready, _ := s.turnBoundary(); !ready {
		t.Fatal("no agent, yet the queue waited")
	}
	if !s.applyRejections(false) {
		t.Fatal("queue did not apply without an agent")
	}
	if got, _ := os.ReadFile(path); strings.Contains(string(got), "AGENT") {
		t.Fatalf("change not reverted: %s", got)
	}
}

func TestMultipleAgentsAllMustBeAtRest(t *testing.T) {
	root, _, file := rejectRepo(t)
	s := terminalState(t)
	s.root = root
	first := s.activeAgent()
	first.attention.Hooked = true
	first.attention.Execution = attention.Completed
	s.agentShortcut(0x1d)
	s.agentPickerKey("l")
	second := s.activeAgent()
	second.attention.Hooked = true
	second.attention.Execution = attention.Running

	if err := s.review.Reject(file, -1, 0, 0, ""); err != nil {
		t.Fatal(err)
	}
	// The working tree is shared, so a second running agent still blocks.
	if ready, _ := s.turnBoundary(); ready {
		t.Fatal("reverted while another agent was running")
	}
	second.attention.Execution = attention.Completed
	if ready, _ := s.turnBoundary(); !ready {
		t.Fatal("queue stayed blocked after every agent finished")
	}
}

func TestAutoSubmitOnlyWhenTheUserHasNotTyped(t *testing.T) {
	s := terminalState(t)
	s.agentName = "codex"
	s.review.AgentDraft = false

	// Default: the message waits in the draft for the user to send.
	s.pendingRejectionDraft = "rejection message"
	s.pasteOverride = s.pendingRejectionDraft
	s.finishAgentPaste(nil)
	if !strings.Contains(s.review.Notice, "press Enter") {
		t.Fatalf("default did not leave the draft to the user: %q", s.review.Notice)
	}
	if s.pendingRejectionDraft != "" {
		t.Fatal("delivered draft stayed queued")
	}

	// Opted in, with unsent user input: submitting would send that too.
	s.review.AutoSubmitRejections = true
	s.agentTyped = true
	s.pendingRejectionDraft = "rejection message"
	s.pasteOverride = s.pendingRejectionDraft
	s.finishAgentPaste(nil)
	if !strings.Contains(s.review.Notice, "unsent input") {
		t.Fatalf("submitted over the user's own input: %q", s.review.Notice)
	}

	// Opted in on a clean prompt: Stvena submits.
	s.agentTyped = false
	s.pendingRejectionDraft = "rejection message"
	s.pasteOverride = s.pendingRejectionDraft
	s.finishAgentPaste(nil)
	if !strings.Contains(s.review.Notice, "Rejections sent") {
		t.Fatalf("auto-submit did not run: %q", s.review.Notice)
	}
	if s.agentTyped {
		t.Fatal("typing state survived a submit")
	}
}

func TestFailedHandoffKeepsTheRejectionDraftForResending(t *testing.T) {
	s := terminalState(t)
	s.agentName = "codex"
	s.pendingRejectionDraft = "rejection message"
	s.pasteOverride = s.pendingRejectionDraft
	s.finishAgentPaste(errShortWrite{})
	if s.pendingRejectionDraft == "" {
		t.Fatal("draft lost after a failed handoff; the agent would never be told")
	}
	if !strings.Contains(s.review.Notice, "resends") {
		t.Fatalf("no recovery offered: %q", s.review.Notice)
	}
	s.review.Panel = "Rejections"
	s.review.RejectionUndelivered = true
	s.review.AdvancedKey("b", 10)
	if s.review.Request != "paste-rejections" {
		t.Fatalf("tray did not offer a resend, request=%q", s.review.Request)
	}
}

type errShortWrite struct{}

func (errShortWrite) Error() string { return "short write" }

// The X key must reach the queue and the tray must reach the working tree.
// This exercises the real key → request → dispatch wiring, not the helpers.
func TestRejectKeyAndTrayDriveTheWholeFlow(t *testing.T) {
	root, path, file := rejectRepo(t)
	s := terminalState(t)
	s.root = root
	s.review.Snapshot = diffview.Snapshot{Root: root, Files: []diffview.File{file}, Tree: "tree"}
	s.review.Snapshot.Finish()
	s.review.Indices = []int{0}
	s.review.Selected = 0
	s.review.PatchFocused = true
	a := s.activeAgent()
	a.attention.Hooked = true
	a.attention.Execution = attention.Running

	events, stop := make(chan any, 4), make(chan struct{})
	s.review.AdvancedKey("X", 20)
	if s.review.Request != "reject" {
		t.Fatalf("X did not request a rejection, got %q", s.review.Request)
	}
	s.dispatch(context.Background(), events, stop)
	if len(s.review.PendingRejections()) != 1 {
		t.Fatalf("X did not queue a rejection: %+v", s.review.PendingRejections())
	}
	if got, _ := os.ReadFile(path); !strings.Contains(string(got), "AGENT") {
		t.Fatalf("queueing already touched the working tree: %s", got)
	}

	// The tray's Apply now overrides the wait on a running agent.
	s.review.AdvancedKey("D", 20)
	if s.review.Panel != "Rejections" {
		t.Fatalf("D did not open the tray, panel=%q", s.review.Panel)
	}
	s.review.AdvancedKey("enter", 20)
	if s.review.Request != "apply-rejections" {
		t.Fatalf("tray Enter did not request an apply, got %q", s.review.Request)
	}
	s.dispatch(context.Background(), events, stop)
	if got, _ := os.ReadFile(path); strings.Contains(string(got), "AGENT") {
		t.Fatalf("Apply now did not revert: %s", got)
	}
	if len(s.review.PendingRejections()) != 0 {
		t.Fatal("queue survived Apply now")
	}
}

// Undo must take a queued rejection out before it can reach the working tree.
func TestTrayUndoKeepsTheChange(t *testing.T) {
	root, path, file := rejectRepo(t)
	s := terminalState(t)
	s.root = root
	if err := s.review.Reject(file, -1, 0, 0, ""); err != nil {
		t.Fatal(err)
	}
	s.review.Panel = "Rejections"
	s.review.AdvancedKey("u", 20)
	if len(s.review.PendingRejections()) != 0 {
		t.Fatal("undo did not remove the rejection")
	}
	if !s.applyRejections(true) {
		t.Fatal("apply should report the empty queue")
	}
	if got, _ := os.ReadFile(path); !strings.Contains(string(got), "AGENT") {
		t.Fatalf("undone rejection still reverted the file: %s", got)
	}
}
