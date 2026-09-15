package review

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/nccapo/stvena/internal/diffview"
)

const (
	maxPendingRejections = 50
	maxRejectionLines    = 20000
	maxReasonRunes       = 500
)

// Rejection is one change the user refused, recorded against the exact captured
// version they saw. The captured patch is kept rather than regenerated: a
// rejection queued now must revert what the user looked at, never whatever the
// file happens to contain when the queue is applied.
type Rejection struct {
	ID            string
	File          diffview.File
	Hunk          int // -1 rejects the whole file
	HunkID        string
	Line, EndLine int
	Reason        string
	CreatedAt     time.Time
	AppliedAt     time.Time
	Unreverted    string // why the working tree still holds it
}

// Key identifies the rejected change so the same hunk cannot queue twice.
func (r Rejection) Key() string {
	if r.HunkID != "" {
		return r.HunkID
	}
	return r.File.Key()
}

func (r Rejection) Pending() bool { return r.AppliedAt.IsZero() }

// Describe names the rejected range the way the agent and the review UI show it.
func (r Rejection) Describe() string {
	switch {
	case r.File.Scope == diffview.Untracked || r.File.Status == "A":
		return r.File.Path + " (new file, removed)"
	case r.File.Status == "D":
		return r.File.Path + " (restored)"
	case r.Hunk < 0:
		return r.File.Path + " (whole file)"
	case r.Line > 0 && r.EndLine > r.Line:
		return fmt.Sprintf("%s lines %d–%d", r.File.Path, r.Line, r.EndLine)
	case r.Line > 0:
		return fmt.Sprintf("%s line %d", r.File.Path, r.Line)
	}
	return fmt.Sprintf("%s hunk %d", r.File.Path, r.Hunk+1)
}

// Reject queues a change for reverting. Nothing touches the working tree here:
// the caller applies the queue at a turn boundary, so a running agent never has
// files changed underneath it.
func (s *State) Reject(f diffview.File, hunk int, line, endLine int, reason string) error {
	if f.Scope == diffview.ProjectScope || f.Scope == diffview.BranchScope {
		return fmt.Errorf("reject changes from This session (1) or Workspace (2)")
	}
	if f.Status == "U" {
		return fmt.Errorf("%s has unresolved conflicts; resolve them first", f.Path)
	}
	if f.Truncated {
		return fmt.Errorf("%s has an incomplete patch and cannot be rejected here", f.Path)
	}
	if f.Binary && f.Scope != diffview.Untracked {
		return fmt.Errorf("%s is binary; revert it with your Git client", f.Path)
	}
	hunkID := ""
	if hunk >= 0 {
		if hunkID = HunkID(f, hunk); hunkID == "" {
			return fmt.Errorf("select a hunk first")
		}
	}
	rejection := Rejection{
		ID: fmt.Sprintf("%d-%d", time.Now().UnixNano(), len(s.Rejections)), File: f, Hunk: hunk,
		HunkID: hunkID, Line: line, EndLine: endLine, Reason: trimReason(reason), CreatedAt: time.Now().UTC(),
	}
	for i, existing := range s.Rejections {
		if existing.Pending() && existing.Key() == rejection.Key() {
			// Re-rejecting the same range only updates the reason.
			if rejection.Reason != "" {
				s.Rejections[i].Reason = rejection.Reason
			}
			return nil
		}
	}
	// A whole-file rejection supersedes queued hunks of the same file.
	if hunk < 0 {
		s.Rejections = filterRejections(s.Rejections, func(r Rejection) bool {
			return !r.Pending() || r.File.Key() != f.Key() || r.HunkID == ""
		})
	}
	pending, lines := 0, 0
	for _, r := range s.Rejections {
		if r.Pending() {
			pending++
			lines += len(r.File.Lines)
		}
	}
	if pending >= maxPendingRejections || lines+len(f.Lines) > maxRejectionLines {
		return fmt.Errorf("too many pending rejections; apply or undo them before rejecting more")
	}
	s.Rejections = append(s.Rejections, rejection)
	return nil
}

// UndoRejection removes a queued rejection. Applied rejections are history and
// are undone by asking the agent, not by re-applying its patch here.
func (s *State) UndoRejection(id string) error {
	for _, r := range s.Rejections {
		if r.ID == id && !r.Pending() {
			return fmt.Errorf("that rejection was already applied")
		}
	}
	before := len(s.Rejections)
	s.Rejections = filterRejections(s.Rejections, func(r Rejection) bool { return r.ID != id || !r.Pending() })
	if len(s.Rejections) == before {
		return fmt.Errorf("no pending rejection to undo")
	}
	return nil
}

// PendingRejections lists queued rejections oldest first.
func (s *State) PendingRejections() []Rejection {
	var pending []Rejection
	for _, r := range s.Rejections {
		if r.Pending() {
			pending = append(pending, r)
		}
	}
	return pending
}

// RejectionTargets groups hunks only when they share the same captured patch.
// An ordinal from a later capture must never select a hunk from an earlier one.
// All sections still go to one atomic git apply, including repeated paths.
func (s *State) RejectionTargets() []diffview.Target {
	order := map[string]int{}
	var targets []diffview.Target
	pending := s.PendingRejections()
	wholeFiles := map[string]bool{}
	for _, r := range pending {
		if r.Hunk < 0 {
			wholeFiles[r.File.Key()] = true
		}
	}
	for _, r := range pending {
		if r.Hunk >= 0 && wholeFiles[r.File.Key()] {
			continue
		}
		key := r.File.Key() + "\x00" + diffview.Revision(r.File)
		index, ok := order[key]
		if !ok {
			order[key] = len(targets)
			target := diffview.Target{File: r.File}
			if r.Hunk >= 0 {
				target.Hunks = []int{r.Hunk}
			}
			targets = append(targets, target)
			continue
		}
		if r.Hunk >= 0 && len(targets[index].Hunks) > 0 {
			targets[index].Hunks = append(targets[index].Hunks, r.Hunk)
			continue
		}
		// A whole-file rejection wins over individual hunks of the same file.
		targets[index].Hunks = nil
	}
	for i := range targets {
		sort.Ints(targets[i].Hunks)
	}
	return targets
}

// FinishRejections records the outcome of applying the queue. An unreverted
// batch is still reported to the agent; only the working tree is left alone.
func (s *State) FinishRejections(unreverted string) []Rejection {
	applied := make([]Rejection, 0, len(s.Rejections))
	now := time.Now().UTC()
	for i := range s.Rejections {
		if !s.Rejections[i].Pending() {
			continue
		}
		s.Rejections[i].AppliedAt = now
		s.Rejections[i].Unreverted = unreverted
		applied = append(applied, s.Rejections[i])
	}
	// Keep recent history for the draft and the timeline without unbounded growth.
	if len(s.Rejections) > maxPendingRejections*2 {
		s.Rejections = s.Rejections[len(s.Rejections)-maxPendingRejections*2:]
	}
	return applied
}

// RejectionDraft is the handoff message. It states what the working tree now
// contains, because the agent cannot see the revert happen.
func RejectionDraft(applied []Rejection) string {
	if len(applied) == 0 {
		return ""
	}
	var reverted, kept []Rejection
	for _, r := range applied {
		if r.Unreverted == "" {
			reverted = append(reverted, r)
		} else {
			kept = append(kept, r)
		}
	}
	var b strings.Builder
	if len(reverted) > 0 {
		b.WriteString("I rejected these changes and reverted them in the working tree. Do not re-apply them.\n\n")
		for _, r := range reverted {
			b.WriteString(bullet(r))
		}
	}
	if len(kept) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("I rejected these changes, but they are still in the working tree because the lines changed again")
		b.WriteString(" after I reviewed them. Please undo them yourself.\n\n")
		for _, r := range kept {
			b.WriteString(bullet(r))
		}
	}
	b.WriteString("\nEverything else you changed was kept.")
	return b.String()
}

func bullet(r Rejection) string {
	if r.Reason != "" {
		return "- " + r.Describe() + ": " + r.Reason + "\n"
	}
	return "- " + r.Describe() + "\n"
}

func trimReason(reason string) string {
	reason = strings.Join(strings.Fields(strings.ReplaceAll(reason, "\n", " ")), " ")
	if runes := []rune(reason); len(runes) > maxReasonRunes {
		reason = strings.TrimSpace(string(runes[:maxReasonRunes])) + "…"
	}
	return reason
}

func filterRejections(list []Rejection, keep func(Rejection) bool) []Rejection {
	out := list[:0]
	for _, r := range list {
		if keep(r) {
			out = append(out, r)
		}
	}
	return out
}

// rejectionKey drives the rejection tray. Undo is only offered while a
// rejection is still queued; once applied, the agent has been told.
func (s *State) rejectionKey(key string, visible int) bool {
	if s.Panel != "Rejections" {
		return false
	}
	pending := s.PendingRejections()
	switch key {
	case "esc", "D":
		s.Panel = ""
		s.PanelScroll, s.TrayIndex = 0, 0
	case "up", "k":
		s.TrayIndex = max(0, s.TrayIndex-1)
	case "down", "j":
		s.TrayIndex = min(max(0, len(pending)-1), s.TrayIndex+1)
	case "pagedown":
		s.PanelScroll += max(1, visible/2)
	case "pageup":
		s.PanelScroll = max(0, s.PanelScroll-max(1, visible/2))
	case "u", "d":
		if s.TrayIndex >= len(pending) {
			s.Notice = "No pending rejection selected"
			break
		}
		if err := s.UndoRejection(pending[s.TrayIndex].ID); err != nil {
			s.Notice = err.Error()
			break
		}
		s.TrayIndex = max(0, min(s.TrayIndex, len(s.PendingRejections())-1))
		s.Notice = "Rejection undone · the change stays in your working tree"
		s.Request = "save"
	case "b":
		if !s.RejectionUndelivered {
			s.Notice = "No rejection message is waiting to be sent"
			break
		}
		s.Request = "paste-draft"
	case "enter":
		if len(pending) == 0 {
			s.Notice = "No pending rejections"
			break
		}
		s.Request = "apply-rejections"
	}
	return true
}
