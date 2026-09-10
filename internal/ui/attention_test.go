package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/nccapo/stvena/internal/attention"
	"github.com/nccapo/stvena/internal/review"
)

func TestAgentIndicatorsAndOverview(t *testing.T) {
	s := review.State{Agents: []review.AgentSummary{
		{Label: "Claude 1", Active: true, State: attention.State{Execution: attention.Running}},
		{Label: "Claude 2", State: attention.State{Execution: attention.Completed}},
		{Label: "Codex 1", Next: true, State: attention.State{Execution: attention.Running, Files: map[string]attention.File{"a": {Ready: true}, "b": {Ready: true}}}},
	}}
	text := ansi.Strip(strings.Join(agentRows(&s, 140), "\n"))
	for _, want := range []string{"[Claude 1 ● RUNNING]", "Claude 2 ✓ DONE", "→Codex 1 ! REVIEW 2f / RUN"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in %s", want, text)
		}
	}
	if got := agentOverview(&s); !strings.Contains(got, "Agents:3  Run:2  Review:1  Wait:0") {
		t.Fatal(got)
	}
	before := NewLayoutOptions(140, 40, 58, false, &s)
	s.Agents[2].State.Files = nil
	s.Agents[0].State.Execution = attention.Idle
	if after := NewLayoutOptions(140, 40, 58, false, &s); before != after {
		t.Fatal("status change resized PTY")
	}
}
func TestAttentionOverflowKeepsHighestPriorityVisible(t *testing.T) {
	s := review.State{}
	for i := range 30 {
		s.Agents = append(s.Agents, review.AgentSummary{Label: "Claude", Active: i == 0})
	}
	s.Agents[29].Next = true
	s.Agents[29].State.Execution = attention.Error
	for _, width := range []int{40, 80, 140} {
		rows := agentRows(&s, width)
		text := ansi.Strip(strings.Join(rows, "\n"))
		if len(rows) > 2 || !strings.Contains(text, "× ERROR") || !strings.Contains(text, "agents (") {
			t.Fatalf("width %d: %s", width, text)
		}
	}
}
