// Package attention owns agent execution and human review state, independently
// of the terminal renderer. The application's event loop is its only writer.
package attention

import (
	"sort"
	"time"
)

type Execution string

const (
	Idle      Execution = "idle"
	Running   Execution = "running"
	Waiting   Execution = "waiting"
	Completed Execution = "completed"
	Error     Execution = "error"
)

type Review string

const (
	Clean        Review = "clean"
	Changed      Review = "changed"
	ReviewNeeded Review = "review_needed"
	Reviewed     Review = "reviewed"
)

type Change struct {
	Path    string `json:"path"`
	Version string `json:"version"`
}
type Event struct {
	Kind    string    `json:"kind"`
	At      time.Time `json:"at"`
	Changes []Change  `json:"changes,omitempty"`
}
type File struct {
	Version         string
	At              time.Time
	Ready, Reviewed bool
}
type State struct {
	Execution      Execution
	LastActivity   time.Time
	Files          map[string]File
	Hooked, Exited bool
	lastHook       time.Time
}

func (s *State) Apply(e Event) {
	if s.Files == nil {
		s.Files = map[string]File{}
	}
	for _, c := range e.Changes {
		old, ok := s.Files[c.Path]
		if !ok || !e.At.Before(old.At) {
			s.Files[c.Path] = File{Version: c.Version, At: e.At}
		}
	}
	if s.Exited || e.At.Before(s.lastHook) {
		return
	}
	s.Hooked = s.Hooked || e.Kind != "SessionStart"
	s.lastHook = e.At
	if e.At.After(s.LastActivity) {
		s.LastActivity = e.At
	}
	switch e.Kind {
	case "SessionStart":
		s.Execution = Idle
	case "UserPromptSubmit", "PreToolUse", "PostToolUse", "PostToolUseFailure":
		s.Execution = Running
	case "waiting":
		s.Execution = Waiting
	case "Stop":
		if s.Execution != Error {
			s.Execution = Completed
		}
	case "StopFailure":
		s.Execution = Error
	}
}
func (s *State) Output(now time.Time) {
	if s.Exited {
		return
	}
	if now.After(s.LastActivity) {
		s.LastActivity = now
	}
	// Once turn events arrive, prompt redraws cannot restart a finished turn.
	if !s.Hooked {
		s.Execution = Running
	}
}

func (s *State) Quiet(now time.Time) bool {
	// Silence means only no observed activity, never completion or user input.
	if !s.Hooked && !s.Exited && s.Execution == Running && now.Sub(s.LastActivity) > 3*time.Second {
		s.Execution = Idle
		return true
	}
	return false
}
func (s *State) Exit(failed bool, now time.Time) {
	failed = failed || s.Execution == Error
	s.Exited, s.LastActivity, s.Execution = true, now, Completed
	if failed {
		s.Execution = Error
	}
}
func (s State) Review() Review {
	if len(s.Files) == 0 {
		return Clean
	}
	for _, f := range s.Files {
		if !f.Reviewed && f.Ready {
			return ReviewNeeded
		}
	}
	for _, f := range s.Files {
		if !f.Reviewed {
			return Changed
		}
	}
	return Reviewed
}
func (s State) Unreviewed() int {
	n := 0
	for _, f := range s.Files {
		if !f.Reviewed {
			n++
		}
	}
	return n
}
func (s State) Attention() string {
	if s.Execution == Error {
		return "error"
	}
	r := s.Review()
	if r == ReviewNeeded {
		return string(r)
	}
	if s.Execution == Waiting {
		return "waiting"
	}
	if r == Changed {
		return string(r)
	}
	if s.Execution == "" {
		return "idle"
	}
	return string(s.Execution)
}
func (s State) Priority() int {
	return map[string]int{"error": 0, "review_needed": 1, "waiting": 2, "changed": 3, "running": 4, "completed": 5, "idle": 6}[s.Attention()]
}

// Order is stable within each priority. Idle sessions are not attention targets.
func Order(states []State) []int {
	var order []int
	for i, s := range states {
		if s.Attention() != "idle" {
			order = append(order, i)
		}
	}
	sort.SliceStable(order, func(i, j int) bool { return states[order[i]].Priority() < states[order[j]].Priority() })
	return order
}

// Next begins at the highest priority, then walks the queue on repeated presses.
// The caller resets previous when the user navigates normally.
func Next(states []State, previous int) int {
	order := Order(states)
	if len(order) == 0 {
		return -1
	}
	for i, index := range order {
		if index == previous {
			return order[(i+1)%len(order)]
		}
	}
	return order[0]
}
