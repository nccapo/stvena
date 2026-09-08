package review

import (
	"fmt"
	"strings"

	"github.com/nccapo/stvena/internal/diffview"
)

// Checkpoint keeps the reviewed tree and its patch together across refreshes
// and restarts. Marks and feedback still belong to the existing review systems.
type Checkpoint struct {
	Snapshot               diffview.Snapshot
	StaleFiles, StaleHunks map[string]bool
	Draft                  string
}

func (s *State) StartCheckpoint(snapshot diffview.Snapshot) error {
	if s.Checkpoint != nil {
		return fmt.Errorf("Checkpoint already pinned · Z: finish · P: resume live")
	}
	if snapshot.Tree == "" || snapshot.Err != nil {
		return fmt.Errorf("Review checkpoint needs a successful session capture")
	}
	// Own the slices as well as the immutable Git object references.
	snapshot.Files = append([]diffview.File(nil), snapshot.Files...)
	for i := range snapshot.Files {
		snapshot.Files[i].Lines = append([]string(nil), snapshot.Files[i].Lines...)
	}
	s.Pinned, s.selectionPinned = false, false
	s.ClearSelection()
	s.Source = "session"
	s.Scope, s.Selected, s.Scroll, s.Horizontal = 0, 0, 0, 0
	s.Query, s.Panel, s.ContentKey = "", "", ""
	s.FullFile, s.Browser, s.PatchFocused = false, true, false
	s.Update(snapshot)
	s.Checkpoint = &Checkpoint{Snapshot: snapshot, StaleFiles: map[string]bool{}, StaleHunks: map[string]bool{}}
	s.Pinned, s.PinnedVersion = true, snapshot.Version
	s.Notice = "Checkpoint pinned · Space/H: reviewed · c: comment · x: collect · Z: finish"
	return nil
}

func (s *State) CheckpointNewer() bool {
	return s.Checkpoint != nil && (s.LiveTree != "" && s.LiveTree != s.Checkpoint.Snapshot.Tree ||
		s.Latest.Tree != "" && (s.Latest.Tree != s.Checkpoint.Snapshot.Tree || s.Latest.Version != s.Checkpoint.Snapshot.Version))
}

func (s *State) observeCheckpoint(live diffview.Snapshot) {
	c := s.Checkpoint
	if c == nil || live.Err != nil || live.Tree == "" {
		return
	}
	if c.StaleFiles == nil {
		c.StaleFiles = map[string]bool{}
	}
	if c.StaleHunks == nil {
		c.StaleHunks = map[string]bool{}
	}
	files, hunks := map[string]diffview.File{}, map[string]bool{}
	for _, f := range live.Files {
		files[f.Key()] = f
		for h := range HunkRanges(f.Lines) {
			hunks[HunkID(f, h)] = true
		}
	}
	for _, f := range c.Snapshot.Files {
		current, ok := files[f.Key()]
		if !ok || fingerprint(current) != fingerprint(f) {
			c.StaleFiles[f.Key()] = true
		}
		for h := range HunkRanges(f.Lines) {
			id := HunkID(f, h)
			if !hunks[id] {
				c.StaleHunks[id] = true
			}
		}
	}
}

func (s *State) ResumeCheckpoint() {
	if c := s.Checkpoint; c != nil {
		for key := range c.StaleFiles {
			delete(s.reviewed, key)
		}
		for id := range c.StaleHunks {
			delete(s.Hunks, id)
		}
	}
	s.Checkpoint = nil
	s.Pinned, s.selectionPinned = false, false
	s.ClearSelection()
	s.Panel = ""
	// A saved checkpoint can be resumed before the first live capture arrives.
	if s.Latest.Tree != "" {
		s.Update(s.Latest)
	}
	s.Request = "save"
}

func (s *State) HunkReviewed(f diffview.File, h int) bool {
	if hash, ok := s.reviewed[f.Key()]; ok && hash == fingerprint(f) {
		return true
	}
	return s.Hunks[HunkID(f, h)]
}

func (s *State) CheckpointProgress() (files, totalFiles, hunks, totalHunks int) {
	if s.Checkpoint == nil {
		return
	}
	for _, f := range s.Checkpoint.Snapshot.Files {
		totalFiles++
		if s.Reviewed(f) {
			files++
		}
		for h := range HunkRanges(f.Lines) {
			totalHunks++
			if s.HunkReviewed(f, h) {
				hunks++
			}
		}
	}
	return
}

func (s *State) CheckpointComments() []Comment {
	var comments []Comment
	if s.Checkpoint != nil {
		for _, c := range s.Comments {
			if c.Tree == s.Checkpoint.Snapshot.Tree {
				comments = append(comments, c)
			}
		}
	}
	return comments
}

func (s *State) FinishCheckpoint(allowIncomplete bool) {
	if s.Checkpoint == nil {
		s.Notice = "Start a Review checkpoint from Actions (K) first"
		return
	}
	f, tf, h, th := s.CheckpointProgress()
	if !allowIncomplete && (f < tf || h < th || s.Checkpoint.Snapshot.Approximate) {
		s.ConfirmAction = "finish-checkpoint"
		s.ConfirmDetail = fmt.Sprintf("%d files / %d hunks still unreviewed. Incomplete previews also need review.", tf-f, th-h)
		return
	}
	message, err := s.CheckpointMessage()
	if err != nil {
		s.Notice = err.Error()
		return
	}
	s.Checkpoint.Draft = message
	s.Panel, s.PanelScroll = "Checkpoint draft", 0
	s.Request = "save"
}

func (s *State) CheckpointMessage() (string, error) {
	if s.Checkpoint == nil {
		return "", fmt.Errorf("No checkpoint to finish")
	}
	c := s.Checkpoint
	f, tf, h, th := s.CheckpointProgress()
	var out strings.Builder
	fmt.Fprintf(&out, "\nReview checkpoint\nCaptured version: %s\nWorking-copy snapshot: %s\nReview progress: %d/%d files; %d/%d hunks.\n", c.Snapshot.Version, c.Snapshot.Tree, f, tf, h, th)
	fmt.Fprintf(&out, "Repository: %q\n", c.Snapshot.Root)
	if f < tf || h < th || c.Snapshot.Approximate {
		out.WriteString("Review is incomplete; unreviewed or truncated changes are not approved.\n")
	}
	out.WriteString("This review applies only to the captured version. Verify current files before editing; later changes have not been reviewed.\n")
	if s.CheckpointNewer() {
		out.WriteString("A newer live version exists.\n")
	}
	feedback := *s
	feedback.Comments = s.CheckpointComments()
	if len(feedback.Comments) > 0 {
		out.WriteString("\n" + feedback.ExportFeedback())
	}
	if len(s.Attachments) > 0 {
		context, err := s.ContextMessage()
		if err != nil {
			return "", err
		}
		out.WriteString(context)
	} else if strings.TrimSpace(s.ContextQuestion) != "" {
		fmt.Fprintf(&out, "\nMy request:\n%s\n", s.ContextQuestion)
	}
	if len(feedback.Comments) == 0 && len(s.Attachments) == 0 && strings.TrimSpace(s.ContextQuestion) == "" {
		out.WriteString("No corrections were recorded in this checkpoint.\n")
	}
	message := pasteText(out.String())
	if len(message) > 32<<10 {
		return "", fmt.Errorf("Checkpoint draft exceeds 32 KiB; shorten feedback or remove context attachments")
	}
	return message, nil
}
