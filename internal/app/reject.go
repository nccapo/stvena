package app

import (
	"fmt"
	"strings"

	"github.com/nccapo/stvena/internal/attention"
	"github.com/nccapo/stvena/internal/diffview"
	"github.com/nccapo/stvena/internal/review"
)

// turnBoundary reports whether rejected changes can be reverted right now.
//
// Reverting under a working agent is the one thing that reliably corrupts a
// session: the agent's next edit is built on lines that no longer exist, its
// tests fail for reasons it cannot see, and it commonly re-applies the very
// change the user rejected. So the queue waits until every live agent is at
// rest. The returned reason explains the wait, and Apply now overrides it.
func (s *screenState) turnBoundary() (bool, string) {
	var waiting []string
	unhooked := false
	for _, a := range s.agents {
		if a.closed || a.exited || a.attention.Exited {
			continue
		}
		switch {
		case !a.attention.Hooked:
			// Silence is not completion. Without turn events there is no boundary
			// to wait for, so the user decides when it is safe.
			unhooked = true
			waiting = append(waiting, a.label)
		case a.attention.Execution == attention.Running || a.attention.Execution == attention.Waiting:
			waiting = append(waiting, a.label)
		}
	}
	if len(waiting) == 0 {
		return true, ""
	}
	if unhooked {
		return false, fmt.Sprintf("%s does not report turn boundaries · Actions → Apply rejections", strings.Join(waiting, ", "))
	}
	return false, fmt.Sprintf("Rejections apply when %s finishes · Actions → Apply rejections", strings.Join(waiting, ", "))
}

// applyRejections reverts every queued rejection as one patch and hands the
// agent a message describing what changed. `force` skips the turn-boundary
// wait; the user asked for it explicitly.
func (s *screenState) applyRejections(force bool) bool {
	pending := s.review.PendingRejections()
	if len(pending) == 0 {
		if force {
			s.review.Notice = "No pending rejections"
			return true
		}
		return false
	}
	if ready, reason := s.turnBoundary(); !ready && !force {
		s.review.RejectionStatus = reason
		return false
	}

	_ = s.applyRejectionBatch()
	return true
}

// applyRejectionBatch records the outcome and queues the handoff even if Git
// refuses the batch. Its error lets the editor report the actual outcome.
func (s *screenState) applyRejectionBatch() error {
	notice, err := diffview.Revert(s.root, s.review.RejectionTargets())
	unreverted := ""
	if err != nil {
		unreverted = err.Error()
	}
	applied := s.review.FinishRejections(unreverted)
	s.review.RejectionStatus = ""

	draft := review.RejectionDraft(applied)
	switch {
	case err != nil:
		s.review.Notice = fmt.Sprintf("%d rejection(s) could not be reverted; the agent is being told instead", len(applied))
	case notice != "":
		s.review.Notice = notice
	default:
		s.review.Notice = fmt.Sprintf("Reverted %d rejected change(s)", len(applied))
	}
	if saveErr := s.review.Save(); saveErr != nil {
		s.review.Notice = saveErr.Error()
		if err == nil {
			err = saveErr
		}
	}
	if draft != "" {
		s.queueAgentDraft(draft, "rejections")
	}
	return err
}

// rejectCurrent queues the selected file or hunk. Nothing is reverted here.
func (s *screenState) rejectCurrent(wholeFile bool, reason string) {
	f := s.review.Current()
	if f == nil {
		s.review.Notice = "Select a change to reject"
		return
	}
	// In the diff, X rejects the change block under the cursor. In the file
	// list, or anywhere without one, it rejects the whole file.
	hunk := -1
	if !wholeFile && s.review.PatchFocused && !s.review.Browser {
		hunk = s.review.CurrentHunk()
	}
	start, end := 0, 0
	if path, first, last, ok := s.review.WorkingRange(); ok && path == f.Path {
		start, end = first, last
	}
	if err := s.review.Reject(*f, hunk, start, end, reason); err != nil {
		s.review.Notice = err.Error()
		return
	}
	if err := s.review.Save(); err != nil {
		s.review.Notice = err.Error()
		return
	}
	what := "file"
	if hunk >= 0 {
		what = "hunk"
	}
	queued := len(s.review.PendingRejections())
	if ready, why := s.turnBoundary(); !ready {
		s.review.RejectionStatus = why
		s.review.Notice = fmt.Sprintf("Rejected %s · %d pending · %s", what, queued, why)
		return
	}
	s.review.Notice = fmt.Sprintf("Rejected %s · %d pending · applying", what, queued)
}

// queueAgentDraft holds an assembled message for delivery to the agent. It is
// pasted, never submitted, so the user reads it before anything is sent.
func (s *screenState) queueAgentDraft(text, kind string) {
	s.pendingDraft, s.pendingDraftKind = text, kind
	s.review.RejectionUndelivered = kind == "rejections"
	s.review.Request = "paste-draft"
}
