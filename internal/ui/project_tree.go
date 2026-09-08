package ui

import (
	"path"
	"strings"

	"github.com/nccapo/stvena/internal/review"
)

func ProjectTreeWindow(s *review.State, height int) (int, int) {
	count := min(len(s.ProjectRows), max(0, height-3))
	if !s.Browser {
		count, _ = ReviewSize(height, len(s.ProjectRows))
	}
	start := min(max(0, s.TreeIndex-count/2), max(0, len(s.ProjectRows)-count))
	return start, count
}

func projectTreeRows(s *review.State, width, height int) []string {
	start, count := ProjectTreeWindow(s, height)
	rows := make([]string, 0, count)
	for i := start; i < start+count; i++ {
		entry := s.ProjectRows[i]
		style, marker := "", "  "
		if i == s.TreeIndex {
			style, marker = focusedBG+bold, "› "
		}
		indent := strings.Repeat("  ", min(entry.Depth, max(0, width/2-5)))
		icon, color := "  ", ""
		if entry.Directory {
			icon, color = "▸ ", cyan
			if s.ProjectFolderExpanded(entry.Path) {
				icon = "▾ "
			}
		}
		label := safeText(path.Base(entry.Path))
		if entry.Directory {
			label += "/"
		}
		rows = append(rows, style+marker+indent+color+icon+label)
	}
	return rows
}
