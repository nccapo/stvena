package ui

import "github.com/nccapo/stvena/internal/review"

// CodeHitAt uses the renderer's row map, including wrapped continuation rows.
// Coordinates are relative to the review pane. A locked side keeps a drag in
// its starting column even if the pointer crosses the split divider.
func CodeHitAt(s *review.State, width, height, x, y int, lockedSide byte) (int, byte, bool) {
	if s.Browser || s.Help || s.Menu || s.Panel != "" || s.Prompt != "" || s.ConfirmAction != "" || x < 0 || x >= width {
		return 0, 0, false
	}
	if s.FullFile && s.ContentLoading {
		return 0, 0, false
	}
	files, patch := ReviewSize(height, s.FileListCount())
	top := files + 3
	if height >= 7 {
		top++
	}
	if y < top || y >= min(height-1, top+patch) {
		return 0, 0, false
	}
	_, hits := codeRowsMapped(s, width, patch)
	row := y - top
	if row >= len(hits) {
		return 0, 0, false
	}
	side := byte(0)
	index := hits[row][0]
	if s.SideBySide && !s.FullFile && width >= 60 {
		half := (width - 3) / 2
		side = lockedSide
		if side == 0 {
			if x < half {
				side = 'o'
			} else if x >= half+3 {
				side = 'n'
			} else {
				return 0, 0, false
			}
		}
		if side == 'n' {
			index = hits[row][1]
		}
	}
	lines := s.DisplayLines()
	if index < 0 || index >= len(lines) || lines[index].Old == 0 && lines[index].New == 0 {
		return 0, 0, false
	}
	if side == 'o' && lines[index].Old == 0 || side == 'n' && lines[index].New == 0 {
		return 0, 0, false
	}
	return index, side, true
}
