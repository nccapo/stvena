package review

import (
	"fmt"

	"github.com/nccapo/stvena/internal/diffview"
)

// WorkingRange identifies the real working-file range represented by the
// review cursor. Removed-only rows fall back to the nearest surviving line.
func (s *State) WorkingRange() (path string, start, end int, ok bool) {
	f := s.Current()
	if f == nil || s.Browser || f.Status == "D" || (s.FullFile && s.ContentLoading) {
		return "", 0, 0, false
	}
	lines := s.DisplayLines()
	if len(lines) == 0 {
		return "", 0, 0, false
	}
	a, b := s.SelectedRange()
	for i := a; i <= b; i++ {
		line := lines[i]
		if (s.Selecting && !s.SelectionIncludes(i, line)) || line.New < 1 {
			continue
		}
		if start == 0 || line.New < start {
			start = line.New
		}
		if line.New > end {
			end = line.New
		}
	}
	if start == 0 {
		for distance := 1; distance < len(lines); distance++ {
			for _, i := range []int{a + distance, a - distance} {
				if i >= 0 && i < len(lines) && lines[i].New > 0 {
					return f.Path, lines[i].New, lines[i].New, true
				}
			}
		}
		return "", 0, 0, false
	}
	return f.Path, start, end, true
}

// AddCapturedRange collects an IDE selection from the immutable project tree.
// It reuses SelectionMessage so IDE- and TUI-created context have identical
// provenance and size limits.
func (s *State) AddCapturedRange(snapshot diffview.Snapshot, path string, start, end int) error {
	if snapshot.Tree == "" || snapshot.Err != nil {
		return fmt.Errorf("wait for a captured project version before collecting editor context")
	}
	if start < 1 || end < start {
		return fmt.Errorf("invalid editor selection")
	}
	index := -1
	for i := range snapshot.Files {
		if snapshot.Files[i].Path == path {
			index = i
			break
		}
	}
	if index < 0 {
		return fmt.Errorf("%s is not in the captured project", path)
	}
	f := snapshot.Files[index]
	content := diffview.LoadContent(snapshot.Root, f)
	if content.Err != nil {
		return content.Err
	}
	if content.Binary {
		return fmt.Errorf("binary files cannot be added as text context")
	}
	if end > len(content.Lines) {
		return fmt.Errorf("editor selection is outside the captured file; wait for refresh and try again")
	}
	temporary := State{
		Snapshot:       snapshot,
		Indices:        []int{index},
		PatchFocused:   true,
		FullFile:       true,
		Content:        content,
		ContentKey:     f.Key(),
		Selecting:      true,
		SelectionStart: start - 1,
		Scroll:         end - 1,
		SelectionSide:  'n',
	}
	message, err := temporary.SelectionMessage()
	if err != nil {
		return err
	}
	return s.AddAttachment(Attachment{
		Label:   fmt.Sprintf("%s:%d–%d", path, start, end),
		Tree:    snapshot.Tree,
		Message: message,
	})
}
