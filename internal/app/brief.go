package app

import "fmt"

func (s *screenState) openBriefEvidence() {
	r := &s.review
	rows := r.BriefRows()
	if r.BriefIndex < 0 || r.BriefIndex >= len(rows) {
		r.Notice = "No brief evidence selected"
		return
	}
	row := rows[r.BriefIndex]
	links := r.Brief[row.Item].Evidence
	index := row.Evidence
	if index < 0 {
		index = 0
	}
	if index >= len(links) {
		r.Notice = "This item has no evidence yet"
		return
	}
	e := links[index]
	switch e.Kind {
	case "code":
		file := -1
		for i, f := range r.Snapshot.Files {
			if f.Path == e.Path {
				file = i
				break
			}
		}
		if file < 0 {
			r.Notice = "Code path is unavailable in this view · 3: Project files"
			return
		}
		selected := -1
		for i, n := range r.Indices {
			if n == file {
				selected = i
				break
			}
		}
		if selected < 0 {
			r.Indices = append(r.Indices, file)
			selected = len(r.Indices) - 1
		}
		r.Selected, r.Browser, r.PatchFocused, r.Panel = selected, false, true, ""
		r.GoToLine(e.Start)
		if stale, reason := r.BriefEvidenceStale(e); stale {
			r.Notice = "Evidence stale: " + reason
		}
	case "check":
		r.Panel = "Checks"
		if r.CheckCommand != e.Command || r.CheckTree != e.CheckTree || !r.CheckFinishedAt.Equal(e.RanAt) {
			r.Notice = fmt.Sprintf("Recorded check: %s · %s · %s", e.Command, e.Status, e.CheckTree)
		}
	case "comment":
		r.Panel = "Comments"
		for _, c := range r.Comments {
			if c.ID == e.CommentID {
				r.Notice = "Referenced comment: " + c.Text
				return
			}
		}
		r.Notice = "Referenced comment was deleted"
	default:
		r.Notice = "Evidence kind is unavailable"
	}
}
