package ui

import (
	"fmt"
	"sort"

	"github.com/nccapo/stvena/internal/attention"
	"github.com/nccapo/stvena/internal/review"
)

func agentBadge(a review.AgentSummary) string {
	label, style := "· IDLE", muted
	switch a.State.Attention() {
	case "error":
		label, style = "× ERROR", red
	case "review_needed":
		label, style = "! REVIEW", yellow
	case "waiting":
		label, style = "? WAITING", yellow
	case "changed":
		label, style = "+ CHANGED", yellow
	case "running":
		label, style = "● RUNNING", cyan
	case "completed":
		label, style = "✓ DONE", green
	}
	if n := a.State.Unreviewed(); n > 0 {
		label += fmt.Sprintf(" %df", n)
	}
	// Execution remains visible when review takes priority.
	if a.State.Execution == attention.Running && a.State.Attention() != "running" {
		label += " / RUN"
	}
	prefix := ""
	if a.Next {
		prefix = "→"
	}
	text := prefix + safeText(a.Label) + " " + style + label + reset
	if a.Active {
		text = "[" + bold + text + "]" + reset
	}
	return text
}

// Two calm rows at most. On overflow keep the active and highest priority
// terminals visible and state how many others can be reached by navigation.
func agentRows(s *review.State, width int) []string {
	if len(s.Agents) == 0 {
		return nil
	}
	// Choose which terminals fit by attention first, retaining active context.
	order := make([]int, len(s.Agents))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool {
		a, b := s.Agents[order[i]], s.Agents[order[j]]
		if a.Next != b.Next {
			return a.Next
		}
		if a.Active != b.Active {
			return a.Active
		}
		return a.State.Priority() < b.State.Priority()
	})
	selected := map[int]bool{}
	pack := func(overflow bool) []string {
		var rows []string
		row := " "
		for i, a := range s.Agents {
			if !selected[i] {
				continue
			}
			badge := agentBadge(a)
			if ansiWidth(row) > 1 && ansiWidth(row)+ansiWidth(badge)+3 > width {
				rows = append(rows, row)
				row = " "
			}
			row += badge + "   "
		}
		if overflow {
			more := fmt.Sprintf("+%d agents (%s)", len(s.Agents)-len(selected), s.Binding("Ctrl-Y"))
			if ansiWidth(row)+len(more) > width && ansiWidth(row) > 1 {
				rows = append(rows, row)
				row = " "
			}
			row += more
		}
		return append(rows, row)
	}
	for _, i := range order {
		selected[i] = true
		if len(pack(len(selected) < len(s.Agents))) > 2 {
			delete(selected, i)
		}
	}
	return pack(len(selected) < len(s.Agents))
}

func agentOverview(s *review.State) string {
	running, reviewing, waiting, errors := 0, 0, 0, 0
	for _, a := range s.Agents {
		if a.State.Execution == attention.Running {
			running++
		}
		if a.State.Unreviewed() > 0 {
			reviewing++
		}
		if a.State.Execution == attention.Waiting {
			waiting++
		}
		if a.State.Execution == attention.Error {
			errors++
		}
	}
	text := fmt.Sprintf("Agents:%d  Run:%d  Review:%d  Wait:%d", len(s.Agents), running, reviewing, waiting)
	if errors > 0 {
		text += fmt.Sprintf("  Error:%d", errors)
	}
	return text
}
