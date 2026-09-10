package attention

import (
	"reflect"
	"testing"
	"time"
)

func TestExecutionAndReviewTransitions(t *testing.T) {
	now := time.Now()
	s := State{}
	if s.Attention() != "idle" {
		t.Fatal(s)
	}
	s.Output(now)
	if s.Execution != Running {
		t.Fatal(s)
	}
	// Structured stop wins even if terminal output was received after the hook.
	s.Apply(Event{Kind: "Stop", At: now.Add(-time.Millisecond)})
	if s.Execution != Completed {
		t.Fatal(s)
	}
	s.Output(now.Add(time.Second))
	if s.Execution != Completed {
		t.Fatal("redraw restarted turn")
	}
	s.Apply(Event{Kind: "UserPromptSubmit", At: now.Add(time.Second)})
	s.Apply(Event{Kind: "PostToolUse", At: now.Add(2 * time.Second), Changes: []Change{{"a.go", "v1"}}})
	if s.Execution != Running || s.Review() != Changed || s.Unreviewed() != 1 {
		t.Fatal(s)
	}
	f := s.Files["a.go"]
	f.Ready = true
	s.Files["a.go"] = f
	if s.Attention() != "review_needed" || s.Execution != Running {
		t.Fatal(s)
	}
	s.Apply(Event{Kind: "Stop", At: now.Add(3 * time.Second)})
	if s.Attention() != "review_needed" || s.Execution != Completed {
		t.Fatal(s)
	}
	f.Reviewed = true
	s.Files["a.go"] = f
	if s.Review() != Reviewed || s.Attention() != "completed" {
		t.Fatal(s)
	}
	s.Apply(Event{Kind: "UserPromptSubmit", At: now.Add(4 * time.Second)})
	s.Exit(true, now.Add(5*time.Second))
	if s.Attention() != "error" {
		t.Fatal(s)
	}
	s.Apply(Event{Kind: "Stop", At: now.Add(6 * time.Second)})
	if s.Attention() != "error" {
		t.Fatal("late hook revived exited process")
	}
}
func TestQuietDoesNotInventWaitingOrCompletion(t *testing.T) {
	now := time.Now()
	s := State{}
	s.Output(now)
	if !s.Quiet(now.Add(4*time.Second)) || s.Execution != Idle {
		t.Fatal(s)
	}
	s.Apply(Event{Kind: "PreToolUse", At: now.Add(5 * time.Second)})
	if s.Quiet(now.Add(time.Hour)) || s.Execution != Running {
		t.Fatal("quiet interrupted known work")
	}
	s.Apply(Event{Kind: "waiting", At: now.Add(6 * time.Second)})
	if s.Attention() != "waiting" {
		t.Fatal(s)
	}
	s.Apply(Event{Kind: "PreToolUse", At: now.Add(7 * time.Second)})
	if s.Execution != Running {
		t.Fatal(s)
	}
}
func TestAttentionQueuePriorityAndCycling(t *testing.T) {
	states := []State{{Execution: Running}, {Execution: Completed}, {Execution: Waiting}, {Execution: Error}, {Files: map[string]File{"a": {Ready: true}}}, {Files: map[string]File{"b": {}}}, {Execution: Idle}}
	if got := Order(states); !reflect.DeepEqual(got, []int{3, 4, 2, 5, 0, 1}) {
		t.Fatal(got)
	}
	previous := -1
	for _, want := range []int{3, 4, 2, 5, 0, 1, 3} {
		previous = Next(states, previous)
		if previous != want {
			t.Fatalf("got %d want %d", previous, want)
		}
	}
	if Next([]State{{Execution: Idle}}, -1) != -1 {
		t.Fatal("idle in queue")
	}
}
func TestLateEventsAndSessionIsolation(t *testing.T) {
	now := time.Now()
	a, b := State{}, State{}
	a.Apply(Event{Kind: "PostToolUse", At: now, Changes: []Change{{"a", "new"}}})
	a.Apply(Event{Kind: "PreToolUse", At: now.Add(-time.Second), Changes: []Change{{"a", "old"}}})
	if a.Files["a"].Version != "new" || b.Unreviewed() != 0 {
		t.Fatal(a, b)
	}
	a.Exit(false, now.Add(time.Second))
	a.Apply(Event{Kind: "PostToolUse", At: now.Add(time.Millisecond), Changes: []Change{{"b", "last"}}})
	if a.Execution != Completed || a.Unreviewed() != 2 {
		t.Fatal("late changes lost at exit", a)
	}
}

func TestTurnFailurePersistsUntilNewWork(t *testing.T) {
	now := time.Now()
	s := State{}
	s.Apply(Event{Kind: "StopFailure", At: now})
	s.Apply(Event{Kind: "Stop", At: now.Add(time.Millisecond)})
	if s.Execution != Error {
		t.Fatal("Stop hid failure")
	}
	s.Exit(false, now.Add(2*time.Millisecond))
	if s.Execution != Error {
		t.Fatal("clean process exit hid turn failure")
	}
	s = State{}
	s.Apply(Event{Kind: "StopFailure", At: now})
	s.Apply(Event{Kind: "UserPromptSubmit", At: now.Add(time.Second)})
	if s.Execution != Running {
		t.Fatal("new turn did not recover")
	}
}
