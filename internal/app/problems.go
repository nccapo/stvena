package app

import (
	"fmt"
	"strings"

	"github.com/nccapo/stvena/internal/checks"
	"github.com/nccapo/stvena/internal/diffview"
	"github.com/nccapo/stvena/internal/review"
)

type problemEvent struct {
	id         int
	changed    bool
	snapshot   diffview.Snapshot
	problem    checks.Problem
	attachment *review.Attachment
	err        error
}

func resolveProblem(files []diffview.File, name string) (diffview.File, error) {
	for _, f := range files {
		if f.Path == name {
			return f, nil
		}
	}
	var matches []diffview.File
	for _, f := range files {
		if strings.HasSuffix(f.Path, "/"+name) {
			matches = append(matches, f)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return diffview.File{}, fmt.Errorf("ambiguous path %s; use Project files to locate the source", name)
	}
	return diffview.File{}, fmt.Errorf("%s is not in the tested snapshot (it may be generated)", name)
}

func (s *screenState) loadProblem(attach bool, events chan<- any, stop <-chan struct{}) {
	if s.review.ProblemIndex < 0 || s.review.ProblemIndex >= len(s.review.Problems) {
		s.review.Notice = "No navigable problem selected"
		return
	}
	p := s.review.Problems[s.review.ProblemIndex]
	root, tree, command := s.root, s.review.CheckTree, s.review.CheckCommand
	changed := s.review.CheckSourceChanged
	s.problemID++
	id := s.problemID
	s.review.Notice = "Loading tested source…"
	s.workers.Add(1)
	go func() {
		defer s.workers.Done()
		e := problemEvent{problem: p, id: id, changed: changed}
		project := diffview.Project(root, tree)
		e.err = project.Err
		if e.err == nil {
			var f diffview.File
			f, e.err = resolveProblem(project.Files, p.Path)
			if e.err == nil {
				e.problem.Path = f.Path
				project.Files = []diffview.File{f}
				project.Label = "Tested source"
				project.Finish()
				e.snapshot = project
				if attach {
					message := fmt.Sprintf("\nCheck command: %s\nTested snapshot: %s\nReported location: %s:%d:%d\nDiagnostic: %s\n", command, tree, f.Path, p.Line, p.Column, p.Message)
					if changed {
						message += "The command changed captured source; this is the starting version, and the reported line may have moved.\n"
					}
					var selection review.State
					selection.Update(project)
					selection.FullFile = true
					selection.PatchFocused = true
					selection.Content = diffview.LoadContent(root, f)
					selection.ContentKey = f.Key()
					selection.Scroll = max(0, p.Line-4)
					selection.Selecting = true
					selection.SelectionStart = p.Line + 2
					selection.Clamp(10)
					if p.Line > len(selection.Content.Lines) {
						message += "Reported line is outside the starting source version.\n"
					} else if code, err := selection.SelectionMessage(); err == nil {
						message += code
					} else {
						message += "Source preview unavailable: " + err.Error() + "\n"
					}
					e.attachment = &review.Attachment{Label: fmt.Sprintf("Problem · %s:%d", f.Path, p.Line), Tree: tree, Message: message}
				}
			}
		}
		if attach && e.attachment == nil {
			message := fmt.Sprintf("Check command: %s\nTested snapshot: %s\nReported location: %s:%d\nDiagnostic: %s\nSource unavailable: %v\n", command, tree, p.Path, p.Line, p.Message, e.err)
			e.attachment = &review.Attachment{Label: fmt.Sprintf("Problem · %s:%d", p.Path, p.Line), Tree: tree, Message: message}
			e.err = nil
		}
		select {
		case events <- e:
		case <-stop:
		}
	}()
}

func (s *screenState) applyProblem(e problemEvent) {
	if e.attachment == nil && (e.id != s.problemID || s.review.Panel != "Problems") {
		return
	}
	if e.err != nil {
		s.review.Notice = e.err.Error()
		return
	}
	if e.attachment != nil {
		if err := s.review.AddAttachment(*e.attachment); err != nil {
			s.review.Notice = err.Error()
		}
		if err := s.review.Save(); err != nil {
			s.review.Notice = err.Error()
		}
		return
	}
	if s.review.Panel != "Problems" {
		return
	}
	if s.review.Checkpoint != nil {
		s.review.Notice = "Checkpoint stays pinned · P: resume live to open tested source"
		return
	}
	// Keep the tested tree visible even when the live working copy is newer.
	s.review.Pinned = false
	s.review.Scope = 0
	s.review.Query = ""
	s.review.ClearSelection()
	s.review.Source = "project"
	s.review.Update(e.snapshot)
	s.review.Latest = s.projectView
	s.review.Pinned = true
	s.review.PinnedVersion = s.projectView.Version
	s.review.Panel = ""
	s.review.FullFile = true
	s.review.Browser = false
	s.review.PatchFocused = true
	s.review.Scroll = 0
	s.review.TargetLine = e.problem.Line
	s.review.Notice = "Tested source · P: current files · T: checks"
	if e.changed {
		s.review.Notice = "Starting source · check changed code; reported line may differ · T: checks"
	}
}
