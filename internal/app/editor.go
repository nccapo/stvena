package app

import (
	"fmt"

	"github.com/nccapo/stvena/internal/diffview"
	"github.com/nccapo/stvena/internal/editor"
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
	err := s.editorReview.Publish(focus, files, hunks, s.review.NewerBatches)
	if err != nil && !s.editorReviewReported {
		s.review.Notice = "Editor review bridge unavailable: " + err.Error()
		s.editorReviewReported = true
		return true
	}
	s.editorReviewReported = err != nil
	return false
}

func (s *screenState) applyEditorRequest(request editor.Request) {
	if request.Action == "context" {
		if err := s.review.AddCapturedRange(s.projectView, request.Path, request.Line, request.EndLine); err != nil {
			s.review.Notice = "Editor context: " + err.Error()
			return
		}
		s.review.Request = ""
		if err := s.review.Save(); err != nil {
			s.review.Notice = err.Error()
			return
		}
		s.review.Notice = fmt.Sprintf("Collected %s:%d–%d from editor · B: context tray", request.Path, request.Line, request.EndLine)
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
	s.review.Notice = request.Path + " is not in the latest captured project"
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
