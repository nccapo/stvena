package review

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nccapo/stvena/internal/diffview"
)

const (
	maxBriefItems     = 30
	maxBriefItemRunes = 200
	maxBriefEvidence  = 8
)

type Evidence struct {
	ID, Kind, Label             string
	Path, Scope, Revision, Tree string
	Start, End                  int
	Old                         bool
	Command, Status, CheckTree  string
	RanAt                       time.Time
	CommentID                   string
	AddedAt                     time.Time
}

type BriefItem struct {
	ID, Text             string
	Done                 bool
	CheckedAt, CreatedAt time.Time
	Evidence             []Evidence
}

type BriefRow struct{ Item, Evidence int }

func briefText(text string) (string, error) {
	text = strings.TrimSpace(pasteText(text))
	if text == "" {
		return "", fmt.Errorf("brief item text cannot be empty")
	}
	if utf8.RuneCountInString(text) > maxBriefItemRunes {
		return "", fmt.Errorf("brief item is limited to 200 characters")
	}
	return text, nil
}

func (s *State) AddBriefItem(text string) error {
	value, err := briefText(text)
	if err != nil {
		return err
	}
	if len(s.Brief) >= maxBriefItems {
		return fmt.Errorf("brief is limited to 30 items")
	}
	now := time.Now().UTC()
	s.Brief = append(s.Brief, BriefItem{ID: fmt.Sprint(now.UnixNano()), Text: value, CreatedAt: now})
	s.Request = "save"
	return nil
}
func (s *State) EditBriefItem(index int, text string) error {
	if index < 0 || index >= len(s.Brief) {
		return fmt.Errorf("select a brief item first")
	}
	value, err := briefText(text)
	if err != nil {
		return err
	}
	s.Brief[index].Text = value
	s.Request = "save"
	return nil
}
func (s *State) DeleteBriefItem(index int) {
	if index < 0 || index >= len(s.Brief) {
		return
	}
	s.Brief = append(s.Brief[:index], s.Brief[index+1:]...)
	s.Request = "save"
}
func (s *State) MoveBriefItem(index, delta int) {
	to := index + delta
	if index < 0 || index >= len(s.Brief) || to < 0 || to >= len(s.Brief) {
		return
	}
	s.Brief[index], s.Brief[to] = s.Brief[to], s.Brief[index]
	s.Request = "save"
}
func (s *State) ToggleBriefItemDone(index int) {
	if index < 0 || index >= len(s.Brief) {
		return
	}
	item := &s.Brief[index]
	item.Done = !item.Done
	item.CheckedAt = time.Time{}
	if item.Done {
		item.CheckedAt = time.Now().UTC()
	}
	s.Request = "save"
}
func (s *State) RemoveBriefEvidence(item, evidence int) {
	if item < 0 || item >= len(s.Brief) || evidence < 0 || evidence >= len(s.Brief[item].Evidence) {
		return
	}
	links := s.Brief[item].Evidence
	s.Brief[item].Evidence = append(links[:evidence], links[evidence+1:]...)
	s.Request = "save"
}
func (s *State) attachBriefEvidence(item int, e Evidence) error {
	if item < 0 || item >= len(s.Brief) {
		return fmt.Errorf("select a brief item first")
	}
	if len(s.Brief[item].Evidence) >= maxBriefEvidence {
		return fmt.Errorf("brief item is limited to 8 evidence links")
	}
	e.AddedAt = time.Now().UTC()
	e.ID = fmt.Sprint(e.AddedAt.UnixNano())
	s.Brief[item].Evidence = append(s.Brief[item].Evidence, e)
	s.Request = "save"
	return nil
}
func (s *State) AttachSelectionEvidence(item int) error {
	if item < 0 || item >= len(s.Brief) {
		return fmt.Errorf("select a brief item first")
	}
	if s.Snapshot.Tree == "" {
		return fmt.Errorf("wait for a captured version before attaching code")
	}
	if _, err := s.SelectionMessage(); err != nil {
		return err
	}
	f := s.Current()
	lines := s.DisplayLines()
	a, b := s.SelectedRange()
	first, last := 0, 0
	old := s.SelectionSide == 'o'
	if !old && a < len(lines) {
		old = lines[a].New == 0 && lines[a].Old > 0
	}
	for i := a; i <= b && i < len(lines); i++ {
		line := lines[i]
		if !s.SelectionIncludes(i, line) {
			continue
		}
		n := line.New
		if old {
			n = line.Old
		}
		if n == 0 {
			continue
		}
		if first == 0 {
			first = n
		}
		last = n
	}
	if first == 0 {
		return fmt.Errorf("select source lines before attaching code")
	}
	return s.attachBriefEvidence(item, Evidence{Kind: "code", Label: fmt.Sprintf("%s:%d–%d", f.Path, first, last), Path: f.Path, Scope: string(f.Scope), Revision: diffview.Revision(*f), Tree: s.Snapshot.Tree, Start: first, End: last, Old: old})
}
func (s *State) AttachCheckEvidence(item int) error {
	if s.CheckRunning || s.CheckCommand == "" || s.CheckFinishedAt.IsZero() {
		return fmt.Errorf("no completed check run to attach")
	}
	return s.attachBriefEvidence(item, Evidence{Kind: "check", Label: s.CheckCommand, Command: s.CheckCommand, Status: s.CheckStatus, CheckTree: s.CheckTree, RanAt: s.CheckFinishedAt})
}
func (s *State) AttachCommentEvidence(item int, commentID string) error {
	for _, c := range s.Comments {
		if c.ID == commentID {
			return s.attachBriefEvidence(item, Evidence{Kind: "comment", Label: fmt.Sprintf("%s:%d · %s", c.Path, c.Start, c.Text), CommentID: c.ID})
		}
	}
	return fmt.Errorf("saved comment is unavailable")
}
func (s *State) BriefEvidenceStale(e Evidence) (bool, string) {
	switch e.Kind {
	case "code":
		if s.revisionStale(e.Path, e.Scope, e.Revision) {
			return true, "code changed or missing"
		}
	case "check":
		if s.CheckCommand == e.Command && (s.CheckFinishedAt.After(e.RanAt) || s.CheckTree != e.CheckTree) {
			return true, "newer run"
		}
		if s.LiveTree != "" && e.CheckTree != s.LiveTree {
			return true, "code changed since the run"
		}
	case "comment":
		for _, c := range s.Comments {
			if c.ID == e.CommentID {
				if s.CommentStale(c) {
					return true, "code changed"
				}
				return false, ""
			}
		}
		return true, "comment deleted"
	}
	return false, ""
}
func (s *State) BriefRows() []BriefRow {
	var rows []BriefRow
	for i, item := range s.Brief {
		rows = append(rows, BriefRow{Item: i, Evidence: -1})
		for j := range item.Evidence {
			rows = append(rows, BriefRow{Item: i, Evidence: j})
		}
	}
	return rows
}
func (s *State) BriefCounts() (items, checked, stale int) {
	items = len(s.Brief)
	for _, item := range s.Brief {
		if item.Done {
			checked++
		}
		for _, e := range item.Evidence {
			if out, _ := s.BriefEvidenceStale(e); out {
				stale++
			}
		}
	}
	return
}
