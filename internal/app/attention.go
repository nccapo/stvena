package app

import (
	"fmt"
	"sort"
	"time"

	"github.com/nccapo/stvena/internal/attention"
	"github.com/nccapo/stvena/internal/diffview"
	"github.com/nccapo/stvena/internal/review"
)

type attentionEvent struct {
	agent  *agentTerminal
	events []attention.Event
}

func watchAttention(a *agentTerminal, dir string, events chan<- any, stop <-chan struct{}) {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			batch, err := attention.Drain(dir)
			if err == nil && len(batch) > 0 {
				select {
				case events <- attentionEvent{a, batch}:
				case <-stop:
					return
				case <-a.attentionStop:
					return
				}
			}
		case <-stop:
			return
		case <-a.attentionStop:
			return
		}
	}
}
func (s *screenState) applyAttention(e attentionEvent) {
	if e.agent.closed {
		return
	}
	before := e.agent.attention.Priority()
	for _, event := range e.events {
		e.agent.attention.Apply(event)
		for _, change := range event.Changes {
			if e.agent.attention.Files[change.Path].At.Equal(event.At) {
				delete(e.agent.reviewFiles, change.Path)
			}
		}
	}
	s.attentionChanged(e.agent, before)
	s.refreshAttention()
}

// Review acknowledgement is version-specific and shared with the existing
// file/hunk workflow. Merely selecting a terminal never acknowledges anything.
func (s *screenState) refreshAttention() {
	for _, a := range s.agents {
		before := a.attention.Priority()
		for path, item := range a.attention.Files {
			if item.Reviewed {
				continue
			}
			if s.sessionView.Err == nil && s.sessionView.Tree != "" && !s.sessionView.UpdatedAt.Before(item.At) {
				found := false
				for _, f := range s.sessionView.Files {
					if f.Path == path || f.OldPath == path {
						// The shared working copy may already include a later edit to this
						// same file. Reviewing its current full diff covers both authors.
						a.reviewFiles[path] = f
						found = true
						item.Ready = true
						break
					}
				}
				if !found && !s.sessionView.Approximate {
					// An add followed by a delete (or a revert to the session baseline)
					// can disappear between captures. Keep an explicit acknowledgement
					// item instead of leaving CHANGED stuck with nothing to open.
					a.reviewFiles[path] = diffview.File{Path: path, Scope: diffview.Session, Status: "M", AfterOID: item.Version,
						Lines: []string{"Agent changed this path; no net changes remain since session start."}}
					item.Ready = true
				}
			}
			if f, ok := a.reviewFiles[path]; ok && item.Ready {
				record := s.review.History[f.Key()]
				item.Reviewed = !record.At.Before(item.At) && s.review.Reviewed(f)
			}
			a.attention.Files[path] = item
		}
		s.attentionChanged(a, before)
	}
	s.syncAttention()
}
func (s *screenState) attentionChanged(a *agentTerminal, before int) {
	after := a.attention.Priority()
	if before != after && (a == s.attentionPrevious || after < before && after <= 3) {
		s.attentionPrevious = nil
	}
}

func (s *screenState) syncAttention() {
	s.review.Agents = nil
	if s.agentSerial == nil {
		s.agentSerial = map[string]int{}
	}
	for i, a := range s.agents {
		if a.label == "" {
			s.agentSerial[a.name]++
			a.label = fmt.Sprintf("%s %d", a.name, s.agentSerial[a.name])
		}
		s.review.Agents = append(s.review.Agents, review.AgentSummary{
			Label: a.label, State: a.attention, Active: i == s.activeAgentIndex,
		})
	}
	if len(s.review.Agents) > 0 {
		best := -1
		for i, a := range s.agents {
			if a.attention.Attention() != "idle" && (best < 0 || a.attention.Priority() < s.agents[best].attention.Priority()) {
				best = i
			}
		}
		if best >= 0 {
			s.review.Agents[best].Next = true
		}
	}
	// Labels have stable identities, so closing a terminal never renumbers it.
	s.relayout(s.layout.Width, s.layout.Height)
}
func (s *screenState) nextAttention() {
	var states []attention.State
	previous := -1
	for i, a := range s.agents {
		states = append(states, a.attention)
		if a == s.attentionPrevious {
			previous = i
		}
	}
	index := attention.Next(states, previous)
	if index < 0 {
		s.review.Notice = "No agents need attention"
		return
	}
	a := s.agents[index]
	s.selectAgent(index)
	s.attentionPrevious = a
	s.review.FooterFocused = false
	if a.attention.Unreviewed() == 0 {
		s.review.Notice = a.label + " · " + a.attention.Attention()
		if a.attention.Execution == attention.Error && a.exited {
			s.review.Notice += " · " + a.status
		}
		return
	}
	if s.review.Pinned || s.review.Prompt != "" || s.review.ConfirmAction != "" || s.review.Searching {
		s.review.Notice = "Agent selected · finish or resume the current review to open its changes"
		return
	}
	var files []diffview.File
	for path, item := range a.attention.Files {
		if !item.Reviewed {
			if f, ok := a.reviewFiles[path]; ok {
				files = append(files, f)
			}
		}
	}
	if len(files) == 0 {
		s.review.Notice = "Agent selected · waiting for its captured changes"
		return
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	// Select the relevant file in the normal session source, preserving editor
	// opening, checkpoints and all existing review controls.
	s.review.Source = "session"
	s.review.Scope = 0
	s.review.Query = ""
	s.review.Help, s.review.Menu = false, false
	s.review.Panel = ""
	s.review.ClearSelection()
	s.updateSource()
	found := false
	for i, index := range s.review.Indices {
		if s.review.Snapshot.Files[index].Path == files[0].Path {
			s.review.Selected = i
			found = true
			break
		}
	}
	if !found {
		snapshot := s.sessionView
		snapshot.Files = files
		snapshot.Label = a.label + " review"
		snapshot.Finish()
		latest := s.review.Latest
		s.review.Update(snapshot)
		s.review.Latest = latest
		s.review.Pinned = true
		s.review.PinnedVersion = latest.Version
	}
	s.review.Browser, s.review.FullFile = false, false
	s.review.PatchFocused = true
	s.review.Scroll = 0
	for i, line := range s.review.DisplayLines() {
		if line.Kind == '@' {
			s.review.Scroll = i
			break
		}
	}
	s.diffFocused = true
	s.review.Notice = fmt.Sprintf("%s · %d unreviewed files · %s: mark file reviewed", a.label, a.attention.Unreviewed(), review.KeyLabel(s.review.Binding(" ")))
}
