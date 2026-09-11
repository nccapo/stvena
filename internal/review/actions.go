package review

import (
	"crypto/sha256"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/nccapo/stvena/internal/diffview"
)

type Action struct{ Key, Name, Hint string }

var Actions = []Action{
	{"K", "Review checkpoint", "Pin current session changes and review them before the next prompt"},
	{"Z", "Finish checkpoint", "Preview comments and collected context, then paste or copy one draft"},
	{"v", "Diff / full file", "Read changed lines or the complete file"},
	{"F", "Focus review", "Expand review to the full terminal"},
	{"1", "This session", "Changes observed since the agent started"},
	{"2", "Whole workspace", "All staged, unstaged and new files"},
	{"3", "Project files", "Browse and search every captured project file"},
	{"4", "Branch changes", "Committed and working changes since the default-branch merge base"},
	{"I", "Review inbox", "Show only files that still need review in the current source"},
	{"L", "Session timeline", "Inspect stable change batches observed during this Stvena session"},
	{"/", "Find text / file", "Search code, or filter paths in the file browser"},
	{":", "Go to line", "Jump directly to a source line"},
	{"s", "Side-by-side diff", "Compare old and new code"},
	{"w", "Wrap long lines", "Fit code to the available width"},
	{"P", "Pin / resume live", "Read a stable version while the agent continues"},
	{"R", "Changed since review", "Compare with the version you last reviewed"},
	{" ", "Mark file reviewed", "Remember this version of the selected file"},
	{"N", "Next unreviewed file", "Skip reviewed versions in the current file list"},
	{"H", "Mark hunk reviewed", "Remember just the current change block"},
	{"V", "Select code range", "Move to extend selection; press V to clear"},
	{"x", "Collect selected code", "Save a code slice in the context tray"},
	{"B", "Context tray", "Collect files and failures, add a request, preview and paste"},
	{"b", "Paste selection to agent", "Add code to the CLI draft, then type your request and send"},
	{"c", "Add comment", "Attach feedback to the selected lines"},
	{"C", "Review comments", "Inspect your saved feedback and stale anchors"},
	{"E", "Export feedback", "Copy an agent-ready feedback message"},
	{"y", "Copy code", "Copy selected source text"},
	{"Y", "Copy file path", "Copy the path of the selected file"},
	{"e", "Open in editor", "Open the current working file at this line"},
	{"t", "Run checks", "Test a captured code snapshot"},
	{"T", "Check results", "Read command output and version freshness"},
	{"S", "Stage / unstage file", "Explicitly change the selected file's staging"},
	{"A", "Stage / unstage hunk", "Apply just the current change block"},
	{"+", "Wider review pane", "Give code more room"},
	{"-", "Wider agent pane", "Give the agent more room"},
	{"q", "Quit review", "Exit after the agent finishes"},
}

func (s *State) AdvancedKey(key string, visible int) bool {
	if s.ConfirmAction != "" {
		if key == "enter" {
			if s.ConfirmAction == "finish-checkpoint" {
				s.FinishCheckpoint(true)
			} else {
				s.Request = "confirm:" + s.ConfirmAction
			}
			s.ConfirmAction = ""
		} else if key == "esc" {
			s.ConfirmAction = ""
			s.Notice = "Cancelled"
		}
		return true
	}
	if s.Menu {
		switch key {
		case "esc", "a":
			s.Menu = false
		case "j", "down":
			s.MenuIndex = min(len(Actions)-1, s.MenuIndex+1)
		case "k", "up":
			s.MenuIndex = max(0, s.MenuIndex-1)
		case "enter":
			s.Menu = false
			s.Key(Actions[s.MenuIndex].Key, visible)
		default:
			for _, a := range Actions {
				if a.Key == key {
					s.Menu = false
					s.Key(key, visible)
					break
				}
			}
		}
		return true
	}
	if s.Prompt != "" {
		switch key {
		case "esc":
			s.Prompt = ""
			s.Input = ""
		case "backspace":
			r := []rune(s.Input)
			if len(r) > 0 {
				s.Input = string(r[:len(r)-1])
			}
		case "enter":
			value, kind := s.Input, s.Prompt
			s.Prompt = ""
			s.Input = ""
			switch kind {
			case "Find in code":
				s.TextQuery = value
				s.SearchNext(1)
			case "Go to line":
				n, err := strconv.Atoi(value)
				if err != nil || n < 1 {
					s.Notice = "Enter a positive line number"
				} else {
					s.GoToLine(n)
				}
			case "Add comment":
				if strings.TrimSpace(value) != "" {
					if err := s.AddComment(value); err != nil {
						s.Notice = err.Error()
					} else {
						s.Notice = "Comment saved · E exports feedback"
					}
				}
			case "Context request":
				s.ContextQuestion = pasteText(value)
				s.Request = "save"
			case "Run checks":
				if strings.TrimSpace(value) != "" {
					s.LastCheck = value
					s.Request = "check"
				}
			}
		default:
			rs := []rune(key)
			if len(rs) == 1 && !unicode.IsControl(rs[0]) {
				s.Input += key
			}
		}
		return true
	}
	if key == "Z" && s.Checkpoint != nil {
		s.Panel = ""
		s.FinishCheckpoint(false)
		return true
	}
	if s.contextKey(key, visible) {
		return true
	}
	if s.Panel == "Checkpoint draft" {
		switch key {
		case "b", "y":
			s.Request = map[string]string{"b": "paste-checkpoint", "y": "copy-checkpoint"}[key]
		case "i":
			s.Panel = "Context"
			s.Prompt, s.Input = "Context request", s.ContextQuestion
		case "esc":
			s.Panel, s.PanelScroll = "", 0
		case "j", "down":
			s.PanelScroll++
		case "k", "up":
			s.PanelScroll = max(0, s.PanelScroll-1)
		case "d", "pagedown":
			s.PanelScroll += max(1, visible/2)
		case "u", "pageup":
			s.PanelScroll = max(0, s.PanelScroll-max(1, visible/2))
		}
		return true
	}
	if s.Panel == "Problems" {
		switch key {
		case "esc", "T":
			s.Panel = "Checks"
			s.PanelScroll = 0
		case "j", "down":
			s.ProblemIndex = min(max(0, len(s.Problems)-1), s.ProblemIndex+1)
		case "k", "up":
			s.ProblemIndex = max(0, s.ProblemIndex-1)
		case "enter":
			s.Request = "problem-open"
		case "x":
			s.Request = "problem-add"
		case "B":
			s.Panel = "Context"
			s.PanelScroll = 0
		case "t":
			s.Prompt = "Run checks"
			s.Input = s.LastCheck
		}
		return true
	}
	if s.Panel == "Timeline" {
		switch key {
		case "esc", "L":
			s.Panel = ""
		case "j", "down":
			s.TimelineIndex = min(max(0, len(s.Timeline)-1), s.TimelineIndex+1)
		case "k", "up":
			s.TimelineIndex = max(0, s.TimelineIndex-1)
		case "enter":
			if len(s.Timeline) > 0 {
				s.Request = "timeline-open"
			}
		}
		return true
	}
	if s.Panel != "" {
		switch key {
		case "o":
			if s.Panel == "Checks" {
				s.Panel = "Problems"
				s.PanelScroll = 0
			}
		case "t":
			if s.Panel == "Checks" {
				s.Prompt = "Run checks"
				s.Input = s.LastCheck
			}
		case "B":
			s.Panel = "Context"
			s.PanelScroll = 0
		case "esc", "C", "T":
			s.Panel = ""
			s.PanelScroll = 0
		case "j", "down":
			s.PanelScroll++
		case "k", "up":
			s.PanelScroll = max(0, s.PanelScroll-1)
		case "d", "pagedown":
			s.PanelScroll += max(1, visible/2)
		case "u", "pageup":
			s.PanelScroll = max(0, s.PanelScroll-max(1, visible/2))
		case "g":
			s.PanelScroll = 0
		case "E":
			s.Request = "feedback"
		}
		return true
	}
	switch key {
	case "K":
		s.Request = "checkpoint"
	case "Z":
		s.FinishCheckpoint(false)
	case "N":
		if len(s.Indices) == 0 {
			s.Notice = "No files to review in this view"
			return true
		}
		for step := 1; step <= len(s.Indices); step++ {
			index := (s.Selected + step) % len(s.Indices)
			if !s.Reviewed(s.Snapshot.Files[s.Indices[index]]) {
				s.selectFile(index - s.Selected)
				s.Browser, s.PatchFocused = false, true
				return true
			}
		}
		s.Notice = "All files in this view are reviewed"
	case "x":
		if err := s.CollectSelection(); err != nil {
			s.Notice = err.Error()
		}
	case "B":
		s.Panel = "Context"
		s.PanelScroll = 0
	case "I":
		if s.Source == "project" {
			s.Notice = "Review inbox is available for change views"
			return true
		}
		s.Inbox = !s.Inbox
		s.Selected, s.Scroll, s.Horizontal = 0, 0, 0
		s.ClearSelection()
		s.filter("")
		if s.Inbox {
			files, hunks, changed := s.ReviewInboxCounts()
			s.Notice = fmt.Sprintf("Review inbox · %d files · %d hunks · %d changed again", files, hunks, changed)
		} else {
			s.Notice = "Showing all changes"
		}
	case "L":
		s.Panel = "Timeline"
		s.TimelineIndex = max(0, len(s.Timeline)-1)
	case "a":
		s.Menu = true
	case ":":
		s.Prompt = "Go to line"
		s.Input = ""
	case "/":
		if s.PatchFocused && !s.Browser {
			s.Prompt = "Find in code"
			s.Input = s.TextQuery
		} else {
			return false
		}
	case "m":
		s.SearchNext(1)
	case "M":
		s.SearchNext(-1)
	case "V":
		if s.Snapshot.Tree == "" {
			s.Notice = "Selection needs a captured version"
			return true
		}
		if s.Selecting {
			s.ClearSelection()
			s.Notice = ""
			return true
		} else {
			s.Selecting = true
			if !s.Pinned {
				s.Pinned = true
				s.selectionPinned = true
				s.PinnedVersion = s.Snapshot.Version
				s.Latest = s.Snapshot
			}
			s.SelectionStart = s.Scroll
		}
		s.Notice = "Pinned · arrows: select · b: paste to agent · y: copy · P: live"
	case "c":
		if s.Snapshot.Tree == "" {
			s.Notice = "Comments need a captured version"
			return true
		}
		if !s.Pinned {
			s.Pinned = true
			s.PinnedVersion = s.Snapshot.Version
			s.Latest = s.Snapshot
			s.Notice = "Commenting on a pinned version · P resumes live"
		}
		s.Prompt = "Add comment"
		s.Input = ""
	case "C":
		s.Panel = "Comments"
		s.PanelScroll = 0
	case "E":
		s.Request = "feedback"
	case "b":
		s.Request = "paste-agent"
	case "y":
		s.Request = "copy"
	case "Y":
		s.Request = "path"
	case "e":
		s.Request = "editor"
	case "t":
		s.Prompt = "Run checks"
		s.Input = s.LastCheck
	case "T":
		s.Panel = "Checks"
		s.PanelScroll = 0
	case "F":
		s.Request = "fullscreen"
	case "+", "-":
		s.Request = key
	case "1", "2", "3", "4":
		s.Request = key
	case "S":
		s.Request = "stage-file"
	case "A":
		s.Request = "stage-hunk"
	case "q":
		s.Request = "quit"
	case "s":
		s.SideBySide = !s.SideBySide
		s.FullFile = false
	case "w":
		s.Wrap = !s.Wrap
	case "P":
		if s.Checkpoint != nil {
			s.ResumeCheckpoint()
			s.Notice = "Live updates resumed · changed versions need review again"
			return true
		}
		if s.Snapshot.Tree == "" && !s.Pinned {
			s.Notice = "Pinning needs a captured version; wait for session capture"
			return true
		}
		if s.Pinned {
			s.Pinned = false
			s.ClearSelection()
			s.Update(s.Latest)
			s.Notice = "Live updates resumed"
		} else {
			s.Pinned = true
			s.PinnedVersion = s.Snapshot.Version
			s.Latest = s.Snapshot
			s.Notice = "Pinned version · agent keeps working · P resumes live"
		}
	case "R":
		s.Request = "since-review"
	case "H":
		f := s.Current()
		if f == nil {
			return true
		}
		hunks := HunkRanges(f.Lines)
		h := s.CurrentHunk()
		if h < 0 || h >= len(hunks) {
			s.Notice = "Move to a diff hunk first"
			return true
		}
		if s.Hunks == nil {
			s.Hunks = map[string]bool{}
		}
		id := HunkID(*f, h)
		wasReviewed := s.HunkReviewed(*f, h)
		if hash, ok := s.reviewed[f.Key()]; ok && hash == fingerprint(*f) {
			for h := range hunks {
				s.Hunks[HunkID(*f, h)] = true
			}
		}
		delete(s.reviewed, f.Key())
		s.Hunks[id] = !wasReviewed
		s.Remember(*f)
		if s.Inbox && s.Reviewed(*f) {
			s.filter("")
		}
		s.Notice = "Hunk review updated"
		s.Request = "save"
	default:
		return false
	}
	return true
}
func (s *State) SelectedRange() (int, int) {
	lines := s.DisplayLines()
	if len(lines) == 0 {
		return 0, 0
	}
	cursor := s.Scroll
	if s.SelectionMouse {
		cursor = s.SelectionEnd
	}
	start, end := min(cursor, len(lines)-1), min(cursor, len(lines)-1)
	if s.Selecting {
		start = min(s.SelectionStart, len(lines)-1)
		if start > end {
			start, end = end, start
		}
	}
	return max(0, start), max(0, end)
}
func (s *State) SelectedText() string {
	lines := s.DisplayLines()
	if len(lines) == 0 {
		return ""
	}
	a, b := s.SelectedRange()
	var out []string
	for i, line := range lines[a : b+1] {
		if !s.SelectionIncludes(a+i, line) {
			continue
		}
		text := line.Text
		if !s.FullFile {
			if line.Kind != '+' && line.Kind != '-' && line.Kind != ' ' {
				continue
			}
			text = strings.TrimPrefix(text, string(line.Kind))
		}
		out = append(out, text)
	}
	return strings.Join(out, "\n")
}
func (s *State) SearchNext(direction int) {
	lines := s.DisplayLines()
	if s.TextQuery == "" || len(lines) == 0 {
		return
	}
	for n := 1; n <= len(lines); n++ {
		i := (s.Scroll + direction*n) % len(lines)
		if i < 0 {
			i += len(lines)
		}
		if strings.Contains(strings.ToLower(lines[i].Text), strings.ToLower(s.TextQuery)) {
			s.Scroll = i
			s.Notice = "Match · m/M: next/previous"
			return
		}
	}
	s.Notice = "No matching text"
}
func (s *State) GoToLine(n int) {
	for i, line := range s.DisplayLines() {
		if line.New == n || line.New == 0 && line.Old == n {
			s.Scroll = i
			s.PatchFocused = true
			s.Browser = false
			return
		}
	}
	if !s.FullFile {
		s.FullFile = true
		s.TargetLine = n
		s.PatchFocused = true
		s.Browser = false
		s.Scroll = 0
	} else {
		s.Notice = "Line is outside this file"
	}
}
func HunkRanges(lines []string) [][2]int {
	var ranges [][2]int
	for i, line := range lines {
		if strings.HasPrefix(line, "@@ ") {
			if len(ranges) > 0 {
				ranges[len(ranges)-1][1] = i
			}
			ranges = append(ranges, [2]int{i, len(lines)})
		}
	}
	return ranges
}
func HunkID(f diffview.File, h int) string {
	ranges := HunkRanges(f.Lines)
	if h < 0 || h >= len(ranges) {
		return ""
	}
	r := ranges[h]
	sum := sha256.Sum256([]byte(strings.Join(f.Lines[r[0]:r[1]], "\n")))
	return fmt.Sprintf("%s:%x", f.Key(), sum)
}
func (s *State) CurrentHunk() int {
	f := s.Current()
	if f == nil {
		return -1
	}
	pos := s.Scroll
	if s.FullFile {
		lines := s.DisplayLines()
		if pos >= len(lines) {
			return -1
		}
		number := lines[pos].New
		pos = -1
		for i, line := range NumberLines(f.Lines) {
			if number > 0 && line.New == number {
				pos = i
				break
			}
		}
	}
	for i, r := range HunkRanges(f.Lines) {
		if pos >= r[0] && pos < r[1] {
			return i
		}
	}
	return -1
}

// PairLines maps visual side-by-side rows back to the authoritative patch lines.
// -1 represents an empty side. Metadata and context use the same index twice.
func PairLines(lines []NumberedLine) [][2]int {
	var pairs [][2]int
	for i := 0; i < len(lines); {
		if lines[i].Kind != '-' && lines[i].Kind != '+' {
			pairs = append(pairs, [2]int{i, i})
			i++
			continue
		}
		var old, next []int
		for i < len(lines) && lines[i].Kind == '-' {
			old = append(old, i)
			i++
		}
		for i < len(lines) && lines[i].Kind == '+' {
			next = append(next, i)
			i++
		}
		for n := 0; n < max(len(old), len(next)); n++ {
			pair := [2]int{-1, -1}
			if n < len(old) {
				pair[0] = old[n]
			}
			if n < len(next) {
				pair[1] = next[n]
			}
			pairs = append(pairs, pair)
		}
	}
	return pairs
}
func (s *State) MoveLine(delta int) {
	if !s.SideBySide || s.FullFile || s.ViewWidth > 0 && s.ViewWidth < 60 {
		s.Scroll += delta
		return
	}
	pairs := PairLines(s.DisplayLines())
	if len(pairs) == 0 {
		return
	}
	current := 0
	for i, p := range pairs {
		if p[0] == s.Scroll || p[1] == s.Scroll {
			current = i
			break
		}
	}
	p := pairs[min(len(pairs)-1, max(0, current+delta))]
	s.Scroll = p[1]
	if s.Scroll < 0 {
		s.Scroll = p[0]
	}
}

func (s *State) ClearSelection() {
	s.Selecting, s.SelectionMouse, s.SelectionSide = false, false, 0
	resume := s.selectionPinned && s.Pinned
	s.selectionPinned = false
	if resume {
		s.Pinned = false
		s.Update(s.Latest)
	}
}
func (s *State) SelectionIncludes(index int, line NumberedLine) bool {
	a, b := s.Scroll, s.Scroll
	if s.SelectionMouse {
		a, b = s.SelectionEnd, s.SelectionEnd
	}
	if s.Selecting {
		a = s.SelectionStart
	}
	if a > b {
		a, b = b, a
	}
	if index < a || index > b {
		return false
	}
	return s.SelectionSide == 0 || s.SelectionSide == 'o' && line.Old > 0 || s.SelectionSide == 'n' && line.New > 0
}

func (s *State) SelectWithMouse(index int, side byte, extend bool) {
	if !extend || !s.Selecting {
		s.SelectionStart = index
		s.SelectionSide = side
	}
	s.SelectionEnd = index
	s.SelectionMouse = true
	s.Selecting = true
	s.PatchFocused = true
	s.Browser = false
	if !s.Pinned {
		s.Pinned = true
		s.selectionPinned = true
		s.PinnedVersion = s.Snapshot.Version
		s.Latest = s.Snapshot
	}
	s.Notice = "Selected lines · b: paste to agent · y: copy · V: clear · P: live"
}
