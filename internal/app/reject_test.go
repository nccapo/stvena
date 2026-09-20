package app

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nccapo/stvena/internal/attention"
	"github.com/nccapo/stvena/internal/diffview"
	"github.com/nccapo/stvena/internal/editor"
	"github.com/nccapo/stvena/internal/review"
	"github.com/nccapo/stvena/internal/session"

	"github.com/nccapo/stvena/internal/repo"
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
	for _, f := range diffview.Collect(repo.Git(root)).Files {
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
	s.ws.Root = root
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
	if !strings.Contains(s.pendingDraft, "not what I asked for") ||
		!strings.Contains(s.pendingDraft, "Do not re-apply") {
		t.Fatalf("agent was not told what was rejected:\n%s", s.pendingDraft)
	}
	if s.review.Request != "paste-draft" {
		t.Fatalf("handoff not requested, got %q", s.review.Request)
	}
}

func TestUnhookedAgentNeverAutoApplies(t *testing.T) {
	root, path, file := rejectRepo(t)
	s := terminalState(t)
	s.ws.Root = root
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
	s.ws.Root = root
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
	s.ws.Root = root
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
	if !strings.Contains(s.pendingDraft, "still in the working tree") ||
		!strings.Contains(s.pendingDraft, "wrong approach") {
		t.Fatalf("failed revert was not reported to the agent:\n%s", s.pendingDraft)
	}
	if len(s.review.PendingRejections()) != 0 {
		t.Fatal("failed batch left the queue pending")
	}
}

func TestStandaloneReviewAppliesImmediately(t *testing.T) {
	root, path, file := rejectRepo(t)
	s := &screenState{ws: repo.Git(root)}
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
	s.ws.Root = root
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
	s.pendingDraft = "rejection message"
	s.pasteOverride = s.pendingDraft
	s.pendingDraftKind = "rejections"
	s.finishAgentPaste(nil)
	if !strings.Contains(s.review.Notice, "press Enter") {
		t.Fatalf("default did not leave the draft to the user: %q", s.review.Notice)
	}
	if s.pendingDraft != "" {
		t.Fatal("delivered draft stayed queued")
	}

	// Opted in, with unsent user input: submitting would send that too.
	s.review.AutoSubmitRejections = true
	s.agentTyped = true
	s.pendingDraft = "rejection message"
	s.pasteOverride = s.pendingDraft
	s.pendingDraftKind = "rejections"
	s.finishAgentPaste(nil)
	if !strings.Contains(s.review.Notice, "unsent input") {
		t.Fatalf("submitted over the user's own input: %q", s.review.Notice)
	}

	// Opted in on a clean prompt: Stvena submits.
	s.agentTyped = false
	s.pendingDraft = "rejection message"
	s.pasteOverride = s.pendingDraft
	s.pendingDraftKind = "rejections"
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
	s.pendingDraft = "rejection message"
	s.pasteOverride = s.pendingDraft
	s.pendingDraftKind = "rejections"
	s.finishAgentPaste(errShortWrite{})
	if s.pendingDraft == "" {
		t.Fatal("draft lost after a failed handoff; the agent would never be told")
	}
	if !strings.Contains(s.review.Notice, "resends") {
		t.Fatalf("no recovery offered: %q", s.review.Notice)
	}
	s.review.Panel = "Rejections"
	s.review.RejectionUndelivered = true
	s.review.AdvancedKey("b", 10)
	if s.review.Request != "paste-draft" {
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
	s.ws.Root = root
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
	s.ws.Root = root
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

// editorState builds a screenState whose session view holds one two-hunk file.
func editorState(t *testing.T) (*screenState, diffview.File) {
	t.Helper()
	root := t.TempDir()
	rejectGit(t, root, "init")
	path := filepath.Join(root, "file.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\n3\n4\n5\n6\n7\n8\n9\nten\n"), 0644); err != nil {
		t.Fatal(err)
	}
	rejectGit(t, root, "add", ".")
	rejectGit(t, root, "commit", "-m", "initial")
	if err := os.WriteFile(path, []byte("ONE\ntwo\n3\n4\n5\n6\n7\n8\n9\nTEN\n"), 0644); err != nil {
		t.Fatal(err)
	}
	var file diffview.File
	for _, f := range diffview.Collect(repo.Git(root)).Files {
		if f.Scope == diffview.Unstaged {
			file = f
		}
	}
	s := &screenState{ws: repo.Git(root)}
	s.sessionView = diffview.Snapshot{Root: root, Files: []diffview.File{file}, Tree: "tree"}
	s.sessionView.Finish()
	return s, file
}

func editorRequest(action, path, hunkID string) editor.Request {
	return editor.Request{Version: 1, ID: action + "-1", Action: action, Path: path,
		Line: 1, EndLine: 1, HunkID: hunkID, UpdatedAt: time.Now().UTC()}
}

func TestEditorAcceptAndRejectUseTheSameStateAsTheKeys(t *testing.T) {
	s, file := editorState(t)
	first := editor.HunkRef(review.HunkID(file, 0))

	s.applyEditorRequest(editorRequest("accept", "file.txt", first))
	if !s.review.HunkReviewed(file, 0) {
		t.Fatal("editor accept did not mark the hunk reviewed")
	}
	if s.review.HunkReviewed(file, 1) {
		t.Fatal("accepting one hunk accepted another")
	}
	if s.lastEditorRequest == nil || s.lastEditorRequest.Status != "applied" {
		t.Fatalf("accept not acknowledged: %+v", s.lastEditorRequest)
	}

	second := editor.HunkRef(review.HunkID(file, 1))
	request := editorRequest("reject", "file.txt", second)
	request.Text = "wrong rename"
	s.applyEditorRequest(request)
	pending := s.review.PendingRejections()
	if len(pending) != 1 || pending[0].Reason != "wrong rename" {
		t.Fatalf("editor reject did not queue: %+v", pending)
	}
	if s.lastEditorRequest.Status != "queued" {
		t.Fatalf("reject acknowledged as %q", s.lastEditorRequest.Status)
	}
	// Queueing must not touch the working tree.
	if got, _ := os.ReadFile(filepath.Join(s.ws.Root, "file.txt")); !strings.Contains(string(got), "TEN") {
		t.Fatalf("editor reject wrote to disk immediately: %s", got)
	}

	s.applyEditorRequest(editorRequest("undo-reject", "file.txt", second))
	if len(s.review.PendingRejections()) != 0 {
		t.Fatal("editor undo did not clear the queue")
	}
	if s.lastEditorRequest.Status != "applied" {
		t.Fatalf("undo acknowledged as %q", s.lastEditorRequest.Status)
	}
}

func TestEditorRefusesAStaleHunkToken(t *testing.T) {
	s, file := editorState(t)
	stale := editor.HunkRef("this hunk no longer exists")
	s.applyEditorRequest(editorRequest("reject", "file.txt", stale))
	if len(s.review.PendingRejections()) != 0 {
		t.Fatal("acted on a hunk the editor could not have seen")
	}
	if s.lastEditorRequest == nil || s.lastEditorRequest.Status != "refused" ||
		!strings.Contains(s.lastEditorRequest.Message, "changed since") {
		t.Fatalf("stale token not refused clearly: %+v", s.lastEditorRequest)
	}

	s.applyEditorRequest(editorRequest("accept", "missing.txt", ""))
	if s.lastEditorRequest.Status != "refused" {
		t.Fatalf("unknown path accepted: %+v", s.lastEditorRequest)
	}
	// A whole-file request still works on the real file.
	s.applyEditorRequest(editorRequest("accept", "file.txt", ""))
	if !s.review.Reviewed(file) {
		t.Fatal("whole-file accept did not mark the file reviewed")
	}
}

func TestEditorAcceptAllTakesOnlyWhatTheEditorShowed(t *testing.T) {
	s, file := editorState(t)
	all := func(tree string) editor.Request {
		request := editorRequest("accept-all", "", "")
		request.Tree = tree
		return request
	}

	// The agent wrote after the editor counted: nothing is accepted.
	s.applyEditorRequest(all("an older tree"))
	if s.lastEditorRequest.Status != "refused" || s.review.HunkReviewed(file, 0) {
		t.Fatalf("accepted changes from a tree the editor never showed: %+v", s.lastEditorRequest)
	}

	// A queued rejection stays rejected; everything else is accepted.
	if err := s.review.Reject(file, 1, 10, 10, ""); err != nil {
		t.Fatal(err)
	}
	s.applyEditorRequest(all(s.sessionView.Tree))
	if s.lastEditorRequest.Status != "applied" || s.lastEditorRequest.Message != "Accepted 1 change in 1 file from the editor" {
		t.Fatalf("accept-all outcome: %+v", s.lastEditorRequest)
	}
	if !s.review.HunkReviewed(file, 0) || s.review.HunkReviewed(file, 1) {
		t.Fatal("accept-all did not skip the rejected hunk")
	}
	if len(s.review.PendingRejections()) != 1 {
		t.Fatal("accept-all dropped a pending rejection")
	}

	s.applyEditorRequest(all(s.sessionView.Tree))
	if s.lastEditorRequest.Status != "refused" {
		t.Fatalf("a second accept-all claimed to accept something: %+v", s.lastEditorRequest)
	}
}

func TestPublishedHunksNameTheLinesTheyReplaced(t *testing.T) {
	s, file := editorState(t)
	files, _ := s.reviewFilesForEditor()
	if len(files) != 1 || files[0].Before != file.BeforeOID || len(files[0].Before) != 40 {
		t.Fatalf("file does not name its before blob: %+v", files)
	}
	hunks := files[0].Hunks
	if len(hunks) != 2 {
		t.Fatalf("expected two hunks, got %+v", hunks)
	}
	// Each block replaced one line, numbered in the before blob.
	for i, want := range [][2]int{{1, 1}, {10, 1}} {
		if hunks[i].Added != 1 || len(hunks[i].Removed) != 1 || hunks[i].Removed[0] != want {
			t.Fatalf("hunk %d does not describe what it replaced: %+v", i, hunks[i])
		}
	}
	before, err := exec.Command("git", "-C", s.ws.Root, "cat-file", "blob", files[0].Before).Output()
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(before), "\n")
	if lines[0] != "one" || lines[9] != "ten" {
		t.Fatalf("removed runs do not point at the replaced lines: %q", lines)
	}

	// A decided block is never drawn, so it does not carry its old lines.
	s.review.SetHunkReviewed(file, 0, true)
	files, _ = s.reviewFilesForEditor()
	if files[0].Hunks[0].Removed != nil || files[0].Hunks[1].Removed == nil {
		t.Fatalf("decided block still describes its old lines: %+v", files[0].Hunks)
	}
}

func TestPublishedReviewFilesCarryHunkStateAndPendingWait(t *testing.T) {
	s, file := editorState(t)
	s.review.SetHunkReviewed(file, 0, true)
	if err := s.review.Reject(file, 1, 10, 10, ""); err != nil {
		t.Fatal(err)
	}
	files, truncated := s.reviewFilesForEditor()
	if truncated || len(files) != 1 {
		t.Fatalf("unexpected published files: %+v truncated=%v", files, truncated)
	}
	hunks := files[0].Hunks
	if len(hunks) != 2 {
		t.Fatalf("expected two hunks, got %+v", hunks)
	}
	if !hunks[0].Reviewed || hunks[0].Rejected {
		t.Fatalf("first hunk state wrong: %+v", hunks[0])
	}
	if hunks[1].Reviewed || !hunks[1].Rejected {
		t.Fatalf("second hunk state wrong: %+v", hunks[1])
	}
	// Working-file coordinates, not diff-line offsets.
	if hunks[0].Start != 1 || hunks[1].End != 10 {
		t.Fatalf("hunks not located in the working file: %+v", hunks)
	}
	if hunks[0].ID == hunks[1].ID || len(hunks[0].ID) != 64 {
		t.Fatalf("hunk tokens are not distinct opaque refs: %+v", hunks)
	}

	// With no agent the queue is ready immediately; a running agent makes it wait.
	pending := s.pendingRejectionState()
	if pending == nil || pending.Count != 1 || pending.AppliesAt != "now" {
		t.Fatalf("pending state without an agent: %+v", pending)
	}
	live := terminalState(t)
	live.ws, live.sessionView, live.review = s.ws, s.sessionView, s.review
	live.activeAgent().attention.Hooked = true
	live.activeAgent().attention.Execution = attention.Running
	if pending = live.pendingRejectionState(); pending == nil || pending.AppliesAt != "turn-end" || pending.Reason == "" {
		t.Fatalf("running agent not reflected in pending state: %+v", pending)
	}
	live.activeAgent().attention.Hooked = false
	if pending = live.pendingRejectionState(); pending == nil || pending.AppliesAt != "manual" {
		t.Fatalf("unhooked agent should need a manual apply: %+v", pending)
	}
}

func TestEditorPromptBuildsAnAgentDraftFromCapturedSource(t *testing.T) {
	root := t.TempDir()
	rejectGit(t, root, "init")
	if err := os.WriteFile(filepath.Join(root, "f.go"), []byte("package main\n\nfunc greet() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	rejectGit(t, root, "add", ".")
	rejectGit(t, root, "commit", "-m", "initial")
	saved, err := session.Open(repo.Git(root), false, "")
	if err != nil {
		t.Fatal(err)
	}
	defer saved.Close()
	tree, err := saved.Capture()
	if err != nil {
		t.Fatal(err)
	}
	s := &screenState{ws: repo.Git(root)}
	s.review.UseWorkspace(s.ws)
	s.projectView = diffview.Project(s.ws, tree)

	request := editorRequest("prompt", "f.go", "")
	request.Line, request.EndLine = 3, 3
	request.Text = "  why is this exported?  "
	s.applyEditorRequest(request)
	if s.lastEditorRequest == nil || s.lastEditorRequest.Status != "refused" || s.pendingDraft != "" {
		t.Fatalf("a question without a running agent was accepted: %+v", s.lastEditorRequest)
	}
	s.agentInput, s.agentName = io.Discard, "claude"
	s.applyEditorRequest(request)

	if s.lastEditorRequest == nil || s.lastEditorRequest.Status != "applied" {
		t.Fatalf("prompt not acknowledged: %+v", s.lastEditorRequest)
	}
	if s.pendingDraftKind != "prompt" {
		t.Fatalf("draft queued as %q", s.pendingDraftKind)
	}
	// The captured source travels with the question, and the question is trimmed.
	if !strings.Contains(s.pendingDraft, "func greet()") {
		t.Fatalf("captured source missing from the draft:\n%s", s.pendingDraft)
	}
	if !strings.HasSuffix(s.pendingDraft, "why is this exported?") {
		t.Fatalf("question missing or untrimmed:\n%s", s.pendingDraft)
	}
	if s.review.Request != "paste-draft" {
		t.Fatalf("handoff not requested, got %q", s.review.Request)
	}
	// A rejection resend must not be offered for a question.
	if s.review.RejectionUndelivered {
		t.Fatal("a question was tracked as an undelivered rejection")
	}

	empty := editorRequest("prompt", "f.go", "")
	empty.Line, empty.EndLine = 3, 3
	s.applyEditorRequest(empty)
	if s.lastEditorRequest.Status != "refused" {
		t.Fatalf("empty question accepted: %+v", s.lastEditorRequest)
	}

	// Paste is the editor's Drag+b: the selection alone, no question needed.
	s.pendingDraft, s.pendingDraftKind, s.review.Request = "", "", ""
	paste := editorRequest("paste", "f.go", "")
	paste.Line, paste.EndLine = 3, 3
	s.applyEditorRequest(paste)
	if s.lastEditorRequest.Status != "applied" {
		t.Fatalf("paste not acknowledged: %+v", s.lastEditorRequest)
	}
	if s.pendingDraftKind != "paste" || s.review.Request != "paste-draft" {
		t.Fatalf("paste queued as %q with request %q", s.pendingDraftKind, s.review.Request)
	}
	if !strings.Contains(s.pendingDraft, "func greet()") || !strings.HasSuffix(strings.TrimSpace(s.pendingDraft), "```") {
		t.Fatalf("paste should be the captured selection alone:\n%s", s.pendingDraft)
	}

	outside := editorRequest("paste", "missing.go", "")
	outside.Line, outside.EndLine = 1, 1
	s.applyEditorRequest(outside)
	if s.lastEditorRequest.Status != "refused" {
		t.Fatalf("paste of a file outside the capture accepted: %+v", s.lastEditorRequest)
	}
}
