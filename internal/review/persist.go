package review

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nccapo/stvena/internal/diffview"
	"github.com/nccapo/stvena/internal/session"
)

type Comment struct {
	ID, Path, Scope, Revision, Tree, Text, Code string
	Start, End                                  int
	Old                                         bool
	CreatedAt                                   time.Time
}
type Record struct {
	File diffview.File
	Tree string
	At   time.Time
}
type Saved struct {
	Checkpoint      *Checkpoint `json:",omitempty"`
	Reviewed        map[string][32]byte
	Hunks           map[string]bool
	History         map[string]Record
	Comments        []Comment
	LastCheck       string
	Attachments     []Attachment
	ContextQuestion string
}

func (s *State) Load(root string) error {
	if s.Snapshot.Root == "" {
		s.Snapshot.Root = root
	}
	dir, err := session.RepoDir(root)
	if err != nil {
		return err
	}
	s.savePath = filepath.Join(dir, "reviews.json")
	data, err := os.ReadFile(s.savePath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var saved Saved
	if err = json.Unmarshal(data, &saved); err != nil {
		return fmt.Errorf("saved reviews could not be read: %w", err)
	}
	s.Attachments, s.ContextQuestion = saved.Attachments, saved.ContextQuestion
	s.reviewed, s.Hunks, s.History, s.Comments, s.LastCheck = saved.Reviewed, saved.Hunks, saved.History, saved.Comments, saved.LastCheck
	if saved.Checkpoint != nil {
		c := saved.Checkpoint
		if err := s.StartCheckpoint(c.Snapshot); err != nil {
			return err
		}
		s.Checkpoint = c
		s.Latest = diffview.Snapshot{}
		s.Notice = "Saved checkpoint reopened · Z: finish · P: resume live"
	}
	return nil
}
func (s *State) Save() error {
	if s.savePath == "" {
		return nil
	}
	if s.retained == nil {
		s.retained = map[string]bool{}
	}
	retain := func(tree string) error {
		if tree == "" || s.retained[tree] {
			return nil
		}
		if err := session.Retain(s.Snapshot.Root, tree); err != nil {
			return err
		}
		s.retained[tree] = true
		return nil
	}
	for _, r := range s.History {
		if err := retain(r.Tree); err != nil {
			return err
		}
	}
	for _, a := range s.Attachments {
		if err := retain(a.Tree); err != nil {
			return err
		}
	}
	for _, c := range s.Comments {
		if err := retain(c.Tree); err != nil {
			return err
		}
	}
	if s.Checkpoint != nil {
		if err := retain(s.Checkpoint.Snapshot.Tree); err != nil {
			return err
		}
		// Removed files and old-side full-file views also need their blobs retained.
		for _, f := range s.Checkpoint.Snapshot.Files {
			if strings.Trim(f.BeforeOID, "0") != "" {
				if err := retain(f.BeforeOID); err != nil {
					return err
				}
			}
		}
	}
	return session.AtomicJSON(s.savePath, Saved{Checkpoint: s.Checkpoint, Reviewed: s.reviewed, Hunks: s.Hunks, History: s.History, Comments: s.Comments, LastCheck: s.LastCheck, Attachments: s.Attachments, ContextQuestion: s.ContextQuestion})
}
func (s *State) Remember(f diffview.File) {
	if s.History == nil {
		s.History = map[string]Record{}
	}
	s.History[f.Key()] = Record{File: f, Tree: s.Snapshot.Tree, At: time.Now()}
}
func (s *State) AddComment(text string) error {
	f := s.Current()
	if f == nil {
		return fmt.Errorf("select a file first")
	}
	lines := s.DisplayLines()
	if len(lines) == 0 {
		return fmt.Errorf("no code to comment on")
	}
	start, end := s.SelectedRange()
	first, last := lines[start], lines[end]
	old := s.SelectionSide == 'o' || first.New == 0 && first.Old > 0
	a, b := first.New, last.New
	if old {
		a = first.Old
		b = last.Old
	}
	for i, line := range lines[start : end+1] {
		if !s.SelectionIncludes(start+i, line) {
			continue
		}
		if old && line.New > 0 && line.Old == 0 || !old && line.Old > 0 && line.New == 0 {
			return fmt.Errorf("select a range on one side of the diff; comment on removed and added lines separately")
		}
	}
	if a == 0 {
		return fmt.Errorf("select a source line, not a diff header")
	}
	if b == 0 {
		b = a
	}
	c := Comment{ID: fmt.Sprint(time.Now().UnixNano()), Path: f.Path, Scope: string(f.Scope), Revision: diffview.Revision(*f), Tree: s.Snapshot.Tree, Text: text, Code: s.SelectedText(), Start: a, End: b, Old: old, CreatedAt: time.Now()}
	s.Comments = append(s.Comments, c)
	return s.Save()
}
func (s *State) CommentStale(c Comment) bool {
	for _, f := range s.Latest.Files {
		if f.Path == c.Path && string(f.Scope) == c.Scope {
			return diffview.Revision(f) != c.Revision
		}
	}
	if s.Latest.Root != "" {
		return true
	}
	for _, f := range s.Snapshot.Files {
		if f.Path == c.Path && string(f.Scope) == c.Scope {
			return diffview.Revision(f) != c.Revision
		}
	}
	return true
}
func (s *State) ExportFeedback() string {
	var out strings.Builder
	out.WriteString("Please address the following review comments. Check the current code before applying each suggestion.\n\n")
	for i, c := range s.Comments {
		label := "captured version"
		if s.CommentStale(c) {
			label = "OUTDATED — verify the code before acting"
		}
		side := "new"
		if c.Old {
			side = "old"
		}
		fmt.Fprintf(&out, "%d. %s:%d-%d (%s side; %s)\n   Snapshot: %s\n   %s\n\n", i+1, c.Path, c.Start, c.End, side, label, c.Tree, c.Text)
		for _, line := range strings.Split(c.Code, "\n") {
			fmt.Fprintf(&out, "    %s\n", line)
		}
		out.WriteString("\n")
	}
	return out.String()
}
