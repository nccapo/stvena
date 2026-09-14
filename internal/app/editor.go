package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/nccapo/stvena/internal/diffview"
	"github.com/nccapo/stvena/internal/editor"
	"github.com/nccapo/stvena/internal/review"
)

func (s *screenState) publishEditorReview() bool {
	if s.editorReview == nil {
		return false
	}
	// Keep the previous focus while a replacement captured blob is loading;
	// publishing an intermediate nil would make the IDE marker flicker.
	if s.review.FullFile && s.review.ContentLoading {
		return false
	}
	var focus *editor.ReviewFocus
	if path, start, end, ok := s.review.WorkingRange(); ok && s.review.Snapshot.Tree != "" {
		focus = &editor.ReviewFocus{Tree: s.review.Snapshot.Tree, Source: s.review.Source, Path: path, Line: start, EndLine: end}
	}
	files, hunks := s.review.ReviewCounts(s.sessionView)
	published, truncated := s.reviewFilesForEditor()
	err := s.editorReview.Publish(editor.ReviewUpdate{
		Focus: focus, Tree: s.sessionView.Tree, Files: published, Truncated: truncated,
		Unreviewed: files, UnreviewedHunks: hunks, NewerBatches: s.review.NewerBatches,
		Pending: s.pendingRejectionState(), LastRequest: s.lastEditorRequest,
	})
	if err != nil && !s.editorReviewReported {
		s.review.Notice = "Editor review bridge unavailable: " + err.Error()
		s.editorReviewReported = true
		return true
	}
	s.editorReviewReported = err != nil
	return false
}

// ack records what Stvena did with an editor request. The extension can then
// report the outcome instead of only claiming that it sent something.
func (s *screenState) ack(request editor.Request, status, message string) {
	s.lastEditorRequest = &editor.RequestResult{
		ID: request.ID, Action: request.Action, Status: status, Message: message, At: time.Now().UTC(),
	}
	if message != "" {
		s.review.Notice = message
	}
}

// findEditorHunk locates the change an editor request names. A token that no
// longer matches means the hunk changed after the editor drew it, which is the
// one case where acting on the request would touch the wrong lines.
func (s *screenState) findEditorHunk(request editor.Request) (diffview.File, int, error) {
	var file diffview.File
	found := false
	for _, f := range s.sessionView.Files {
		if f.Path == request.Path || f.OldPath == request.Path {
			file, found = f, true
			break
		}
	}
	if !found {
		return file, -1, fmt.Errorf("%s has no captured changes in this session", request.Path)
	}
	if request.HunkID == "" {
		return file, -1, nil
	}
	for h := range review.HunkRanges(file.Lines) {
		if editor.HunkRef(review.HunkID(file, h)) == request.HunkID {
			return file, h, nil
		}
	}
	return file, -1, fmt.Errorf("that change block in %s has changed since your editor drew it; review it again", request.Path)
}

func (s *screenState) applyEditorRequest(request editor.Request) {
	switch request.Action {
	case "accept", "unaccept", "reject", "undo-reject":
		s.applyEditorDecision(request)
		return
	case "apply-rejections":
		if len(s.review.PendingRejections()) == 0 {
			s.ack(request, "refused", "No pending rejections")
			return
		}
		count := len(s.review.PendingRejections())
		s.applyRejections(true)
		s.ack(request, "applied", fmt.Sprintf("Applied %d rejection(s) from the editor", count))
		return
	case "next-unreviewed":
		s.review.Key("N", s.visibleLines())
		s.ack(request, "applied", "")
		return
	}
	if request.Action == "context" {
		if err := s.review.AddCapturedRange(s.projectView, request.Path, request.Line, request.EndLine); err != nil {
			s.ack(request, "refused", "Editor context: "+err.Error())
			return
		}
		s.review.Request = ""
		if err := s.review.Save(); err != nil {
			s.ack(request, "refused", err.Error())
			return
		}
		s.ack(request, "applied", fmt.Sprintf("Collected %s:%d–%d from editor · B: context tray", request.Path, request.Line, request.EndLine))
		return
	}

	if s.review.Checkpoint != nil {
		if !snapshotHasPath(s.review.Snapshot, request.Path) {
			s.review.Notice = "Checkpoint stays pinned; that editor file is outside its captured changes"
			return
		}
		s.review.Pinned = false
		s.review.ClearSelection()
		s.openEditorRange(s.review.Snapshot, s.review.Source, request.Path, request.Line, s.review.FullFile)
		s.review.Pinned = true
		return
	}
	for _, candidate := range []struct {
		snapshot diffview.Snapshot
		source   string
		full     bool
	}{
		{s.sessionView, "session", false},
		{s.workspace, "workspace", false},
		{s.projectView, "project", true},
	} {
		if snapshotHasPath(candidate.snapshot, request.Path) {
			s.review.Pinned = false
			s.review.ClearSelection()
			s.openEditorRange(candidate.snapshot, candidate.source, request.Path, request.Line, candidate.full)
			return
		}
	}
	s.ack(request, "refused", request.Path+" is not in the latest captured project")
}

func snapshotHasPath(snapshot diffview.Snapshot, path string) bool {
	for _, file := range snapshot.Files {
		if file.Path == path {
			return true
		}
	}
	return false
}

func (s *screenState) openEditorRange(snapshot diffview.Snapshot, source, path string, line int, full bool) {
	s.review.Source = source
	s.review.Inbox = false
	s.review.Scope = 0
	s.review.Query = ""
	s.review.Panel = ""
	s.review.FullFile = full
	s.review.TargetLine = 0
	s.review.Update(snapshot)
	for visible, index := range s.review.Indices {
		if snapshot.Files[index].Path == path {
			s.review.Selected = visible
			break
		}
	}
	s.review.Browser = false
	s.review.PatchFocused = true
	s.review.Scroll = 0
	if full {
		s.review.TargetLine = line
	} else {
		s.review.GoToLine(line)
	}
	s.diffFocused = true
	s.review.FooterFocused = false
	s.review.Notice = fmt.Sprintf("Reviewing %s:%d from editor", path, line)
}

// reviewFilesForEditor locates every change of the session view in the ordinary
// working file, with the review mark and queued rejection that apply to it.
// The editor renders and acts on these; it never receives patch text.
func (s *screenState) reviewFilesForEditor() ([]editor.ReviewFile, bool) {
	if s.sessionView.Tree == "" || s.sessionView.Err != nil {
		return nil, false
	}
	rejectedFiles, rejectedHunks := map[string]bool{}, map[string]bool{}
	for _, r := range s.review.PendingRejections() {
		if r.HunkID == "" {
			rejectedFiles[r.File.Key()] = true
			continue
		}
		rejectedHunks[r.HunkID] = true
	}
	files := make([]editor.ReviewFile, 0, len(s.sessionView.Files))
	hunkBudget, truncated := editor.MaxReviewHunks, false
	for _, f := range s.sessionView.Files {
		if len(files) >= editor.MaxReviewFiles {
			truncated = true
			break
		}
		entry := editor.ReviewFile{
			Path: f.Path, OldPath: f.OldPath, Status: f.Status, Binary: f.Binary,
			Reviewed: s.review.Reviewed(f), Rejected: rejectedFiles[f.Key()],
		}
		if !f.Binary && !f.Truncated && f.Status != "U" {
			spans := diffview.HunkSpans(f)
			for h, span := range spans {
				if hunkBudget <= 0 {
					truncated = true
					break
				}
				hunkBudget--
				id := review.HunkID(f, h)
				entry.Hunks = append(entry.Hunks, editor.ReviewHunk{
					ID: editor.HunkRef(id), Start: span.Start, End: span.End,
					Reviewed: s.review.HunkReviewed(f, h),
					Rejected: entry.Rejected || rejectedHunks[id],
				})
			}
		}
		files = append(files, entry)
	}
	return files, truncated
}

// pendingRejectionState tells the editor how many rejections are queued and
// what they are waiting for, so it can show the wait instead of looking stuck.
func (s *screenState) pendingRejectionState() *editor.PendingRejections {
	pending := s.review.PendingRejections()
	if len(pending) == 0 {
		return nil
	}
	ready, reason := s.turnBoundary()
	state := &editor.PendingRejections{Count: len(pending), AppliesAt: "turn-end", Reason: reason}
	if ready {
		state.AppliesAt, state.Reason = "now", ""
	} else if strings.Contains(reason, "does not report turn boundaries") {
		// Nothing will release this queue on its own; only the user can.
		state.AppliesAt = "manual"
	}
	return state
}

// applyEditorDecision handles accept, reject and undo-reject from the editor.
// Accepting is the same review mark the Space and H keys write, and rejecting
// goes through the same queue as X, so neither surface can drift from the other.
func (s *screenState) applyEditorDecision(request editor.Request) {
	file, hunk, err := s.findEditorHunk(request)
	if err != nil {
		s.ack(request, "refused", err.Error())
		return
	}
	what := "file"
	if hunk >= 0 {
		what = "hunk"
	}
	switch request.Action {
	case "accept", "unaccept":
		reviewed := request.Action == "accept"
		if hunk >= 0 {
			s.review.SetHunkReviewed(file, hunk, reviewed)
		} else {
			s.review.SetFileReviewed(file, reviewed)
		}
		verb := "Accepted"
		if !reviewed {
			verb = "Un-accepted"
		}
		s.ack(request, "applied", fmt.Sprintf("%s %s in %s from the editor", verb, what, request.Path))
	case "reject":
		start, end := request.Line, request.EndLine
		if err := s.review.Reject(file, hunk, start, end, request.Text); err != nil {
			s.ack(request, "refused", err.Error())
			return
		}
		// Queued, not applied: the working tree is only touched at a turn
		// boundary, so say which of the two happened.
		if ready, reason := s.turnBoundary(); !ready {
			s.review.RejectionStatus = reason
			s.ack(request, "queued", fmt.Sprintf("Rejected %s in %s · %s", what, request.Path, reason))
		} else {
			s.ack(request, "queued", fmt.Sprintf("Rejected %s in %s · applying", what, request.Path))
		}
	case "undo-reject":
		id := ""
		for _, r := range s.review.PendingRejections() {
			if r.File.Path != file.Path {
				continue
			}
			if (hunk < 0 && r.HunkID == "") || (hunk >= 0 && r.HunkID == review.HunkID(file, hunk)) {
				id = r.ID
				break
			}
		}
		if id == "" {
			s.ack(request, "refused", "No pending rejection for that change")
			return
		}
		if err := s.review.UndoRejection(id); err != nil {
			s.ack(request, "refused", err.Error())
			return
		}
		s.ack(request, "applied", fmt.Sprintf("Undid the rejection of %s in %s", what, request.Path))
	}
	if err := s.review.Save(); err != nil {
		s.ack(request, "refused", err.Error())
	}
}
