// Package review owns file selection and read-only review navigation.
package review

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"unicode"

	"github.com/nccapo/stvena/internal/checks"
	"github.com/nccapo/stvena/internal/diffview"
)

var Scopes = []diffview.Scope{"", diffview.Staged, diffview.Unstaged, diffview.Untracked}

type State struct {
	Snapshot                          diffview.Snapshot
	Indices                           []int
	Selected, Scroll, Horizontal      int
	ViewWidth                         int
	Scope                             int
	Query                             string
	Searching, PatchFocused, Help     bool
	Hotkeys                           map[string]string
	HelpIndex                         int
	EditingHotkey, HotkeysDirty       bool
	Browser, FullFile                 bool
	Content                           diffview.Content
	ContentKey                        string
	ContentLoading                    bool
	previousQuery                     string
	reviewed                          map[string][32]byte
	Latest                            diffview.Snapshot
	Pinned, SideBySide, Wrap          bool
	PinnedVersion                     string
	Checkpoint                        *Checkpoint
	selectionPinned                   bool
	Menu                              bool
	MenuIndex                         int
	Prompt, Input, Notice, Request    string
	Panel                             string
	PanelScroll                       int
	TextQuery                         string
	Selecting                         bool
	SelectionStart, TargetLine        int
	SelectionEnd                      int
	SelectionMouse                    bool
	SelectionSide                     byte
	Source                            string
	ProjectRows                       []ProjectEntry
	TreeIndex                         int
	ExpandedFolders                   map[string]bool
	LiveTree                          string
	Attachments                       []Attachment
	ContextQuestion                   string
	TrayIndex                         int
	Problems                          []checks.Problem
	ProblemIndex                      int
	CheckSourceChanged                bool
	CheckCommand                      string
	AgentLabel                        string
	AgentStatus                       string
	AgentDraft                        bool
	WelcomeFrame                      int
	Hunks                             map[string]bool
	History                           map[string]Record
	Comments                          []Comment
	LastCheck, CheckTree, CheckStatus string
	CheckLines                        []string
	CheckRunning                      bool
	ConfirmAction, ConfirmDetail      string
	savePath                          string
	retained                          map[string]bool
}

func (s *State) Current() *diffview.File {
	if s.ProjectFolderSelected() {
		return nil
	}
	if s.Selected < 0 || s.Selected >= len(s.Indices) {
		return nil
	}
	return &s.Snapshot.Files[s.Indices[s.Selected]]
}
func (s *State) Update(snapshot diffview.Snapshot) {
	s.Latest = snapshot
	if s.Pinned {
		s.observeCheckpoint(snapshot)
		return
	}
	key := ""
	if f := s.Current(); f != nil {
		key = f.Key()
	}
	s.Snapshot = snapshot
	s.filter(key)
	if s.Source == "project" {
		return
	}
	// Once a reviewed file changes, it needs review even if later reverted.
	for _, f := range snapshot.Files {
		if old, ok := s.reviewed[f.Key()]; ok && old != fingerprint(f) {
			delete(s.reviewed, f.Key())
		}
	}
	active := map[string]bool{}
	for _, f := range snapshot.Files {
		active[f.Key()] = true
	}
	for key := range s.reviewed {
		isSession := strings.HasPrefix(key, string(diffview.Session)+"\x00")
		if (s.Source == "" || isSession == (s.Source == "session")) && !active[key] {
			delete(s.reviewed, key)
		}
	}
	// Hunk marks must not revive if changed code later returns to an old patch.
	activeHunks := map[string]bool{}
	for _, f := range snapshot.Files {
		for h := range HunkRanges(f.Lines) {
			activeHunks[HunkID(f, h)] = true
		}
	}
	for id := range s.Hunks {
		isSession := strings.HasPrefix(id, string(diffview.Session)+"\x00")
		if (s.Source == "" || isSession == (s.Source == "session")) && !activeHunks[id] {
			delete(s.Hunks, id)
		}
	}
}
func (s *State) filter(key string) {
	s.Indices = nil
	for i, f := range s.Snapshot.Files {
		if s.Scope != 0 && f.Scope != Scopes[s.Scope] {
			continue
		}
		if !strings.Contains(strings.ToLower(f.Path+" "+f.OldPath), strings.ToLower(s.Query)) {
			continue
		}
		s.Indices = append(s.Indices, i)
	}
	s.Selected = min(s.Selected, max(0, len(s.Indices)-1))
	found := false
	for i, index := range s.Indices {
		if s.Snapshot.Files[index].Key() == key {
			s.Selected = i
			found = true
			break
		}
	}
	if !found {
		s.Scroll, s.Horizontal = 0, 0
	}
	if s.Source == "project" {
		s.rebuildProjectTree("")
	}
}
func fingerprint(f diffview.File) [32]byte {
	return sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%d:%d:%t:%t:%x\x00%s", f.Status, f.OldPath, f.Added, f.Deleted, f.Binary, f.Truncated, f.ContentHash, strings.Join(f.Lines, "\n"))))
}
func (s *State) Reviewed(f diffview.File) bool {
	if f.Scope == diffview.ProjectScope {
		return false
	}
	hash, ok := s.reviewed[f.Key()]
	if ok && hash == fingerprint(f) {
		return true
	}
	hunks := HunkRanges(f.Lines)
	if len(hunks) == 0 || f.Truncated || f.Status == "U" {
		return false
	}
	for h := range hunks {
		if !s.Hunks[HunkID(f, h)] {
			return false
		}
	}
	return true
}
func (s *State) ReviewedCount() int {
	n := 0
	for _, f := range s.Snapshot.Files {
		if s.Reviewed(f) {
			n++
		}
	}
	return n
}
func (s *State) selectFile(delta int) {
	s.TargetLine = 0
	s.Selected = min(max(0, len(s.Indices)-1), max(0, s.Selected+delta))
	s.Scroll, s.Horizontal = 0, 0
	s.ClearSelection()
	s.RevealProjectFile()
}
func (s *State) Clamp(visible int) {
	length := 0
	if f := s.Current(); f != nil {
		length = len(s.DisplayLines())
	}
	s.Scroll = min(max(0, length-1), max(0, s.Scroll))
}

// Key accepts a decoded key or a Unicode character; no keys mutate Git/files.
func (s *State) Key(key string, visible int) {
	if key == "?" && !s.Help && !s.Searching && s.Prompt == "" && s.ConfirmAction == "" {
		s.Help, s.EditingHotkey = true, false
		s.Notice = ""
		return
	}
	if s.Selecting && !s.Menu && s.Prompt == "" && s.Panel == "" && !s.Help && !s.Searching {
		switch key {
		case "f", "esc", "backspace", "n", "p", "v", "s", "view-file", "view-diff":
			s.ClearSelection()
		}
	}
	if s.SelectionMouse && !s.Menu && s.Prompt == "" && s.Panel == "" && !s.Help && !s.Searching {
		switch key {
		case "up", "down", "j", "k", "pageup", "pagedown", "d", "u", "g", "G", "home", "end", "[", "]":
			s.Scroll = s.SelectionEnd
			s.ClearSelection()
		case "v", "s", "w", "f", "backspace", "n", "p", "tab", "1", "2", "R", "esc", ":", "/":
			s.ClearSelection()
		}
	}

	if s.Prompt == "" && s.ConfirmAction == "" {
		s.Notice = ""
	}
	if s.Searching {
		switch key {
		case "esc":
			s.Query = s.previousQuery
			s.Searching = false
		case "enter":
			s.Searching = false
		case "backspace":
			r := []rune(s.Query)
			if len(r) > 0 {
				s.Query = string(r[:len(r)-1])
			}
		default:
			if len([]rune(key)) == 1 && !unicode.IsControl([]rune(key)[0]) {
				s.Query += key
			}
		}
		s.Selected, s.Scroll = 0, 0
		s.filter("")
		return
	}
	if s.Help {
		s.helpKey(key)
		return
	}
	if s.Panel != "" && s.Prompt == "" && s.ConfirmAction == "" && !s.Menu {
		switch key {
		case "1", "2", "3":
			s.Panel = ""
			s.Request = key
			return
		case "T":
			if s.Panel != "Checks" {
				s.Panel = "Checks"
				s.PanelScroll = 0
				return
			}
		}
	}
	if s.projectTreeKey(key, visible) {
		return
	}
	if s.Source == "project" && s.Panel == "" && s.Prompt == "" {
		switch key {
		case "v", "s", "view-diff", "tab", " ", "H", "N", "R":
			s.Notice = "Project files · 1: session changes · 2: workspace changes"
			return
		}
	}
	if s.AdvancedKey(key, visible) {
		s.Clamp(visible)
		return
	}
	switch key {
	case "view-file", "view-diff":
		if s.FullFile != (key == "view-file") {
			s.Key("v", visible)
		}
		return
	case "f":
		s.TargetLine = 0
		s.Browser, s.PatchFocused = true, false
		s.RevealProjectFile()
	case "v":
		target := 0
		lines := s.DisplayLines()
		for i := s.Scroll; i < len(lines); i++ {
			if lines[i].New > 0 {
				target = lines[i].New
				break
			}
			if lines[i].Old > 0 {
				target = lines[i].Old
				break
			}
		}
		s.FullFile = !s.FullFile
		s.Browser, s.PatchFocused = false, true
		s.Scroll, s.Horizontal = 0, 0
		if target > 0 {
			if s.FullFile {
				s.TargetLine = target
			} else {
				closest, distance := 0, int(^uint(0)>>1)
				for i, line := range s.DisplayLines() {
					n := line.New
					if n == 0 {
						n = line.Old
					}
					if n > 0 {
						d := n - target
						if d < 0 {
							d = -d
						}
						if d < distance {
							closest, distance = i, d
						}
					}
				}
				s.Scroll = closest
			}
		}

	case "/":
		s.Searching = true
		s.previousQuery = s.Query
	case "esc":
		if s.PatchFocused {
			s.PatchFocused, s.Browser = false, true
			s.RevealProjectFile()
		} else {
			s.Query = ""
			s.filter("")
		}
	case "tab":
		if s.Source == "session" {
			break
		}
		s.Scope = (s.Scope + 1) % len(Scopes)
		s.Selected = 0
		s.filter("")
	case "enter":
		s.PatchFocused, s.Browser = true, false
	case "backspace":
		s.TargetLine = 0
		s.PatchFocused, s.Browser = false, true
		s.RevealProjectFile()
	case "n":
		s.selectFile(1)
	case "p":
		s.selectFile(-1)
	case "j", "down":
		if s.PatchFocused {
			s.MoveLine(1)
		} else {
			s.selectFile(1)
		}
	case "k", "up":
		if s.PatchFocused {
			s.MoveLine(-1)
		} else {
			s.selectFile(-1)
		}
	case "d", "pagedown":
		if s.Browser {
			s.selectFile(max(1, visible/2))
			break
		}
		s.Scroll += max(1, visible/2)
		s.PatchFocused = true
	case "u", "pageup":
		if s.Browser {
			s.selectFile(-max(1, visible/2))
			break
		}
		s.Scroll -= max(1, visible/2)
		s.PatchFocused = true
	case "g", "home":
		if s.PatchFocused {
			s.Scroll = 0
		} else {
			s.Selected = 0
			s.Scroll, s.Horizontal = 0, 0
		}
	case "G", "end":
		if s.PatchFocused {
			if f := s.Current(); f != nil {
				s.Scroll = len(s.DisplayLines())
			}
		} else {
			s.selectFile(len(s.Indices))
		}
	case "h", "left":
		s.Horizontal = max(0, s.Horizontal-8)
	case "l", "right":
		s.Horizontal += 8
	case "0":
		s.Horizontal = 0
	case "[", "]":
		s.PatchFocused, s.Browser = true, false
		lines := s.DisplayLines()
		step := 1
		if key == "[" {
			step = -1
		}
		for i := s.Scroll + step; i >= 0 && i < len(lines); i += step {
			isChange := lines[i].Kind == '@'
			if s.FullFile {
				isChange = lines[i].Kind != 0 && lines[i].Kind != ' ' && (i == 0 || lines[i-1].Kind != lines[i].Kind)
			}
			if isChange {
				s.Scroll = i
				break
			}
		}
	case " ":
		s.Request = "save"
		if f := s.Current(); f != nil && !f.Truncated && f.Status != "U" {
			if s.reviewed == nil {
				s.reviewed = make(map[string][32]byte)
			}
			if s.Reviewed(*f) {
				delete(s.reviewed, f.Key())
				for h := range HunkRanges(f.Lines) {
					delete(s.Hunks, HunkID(*f, h))
				}
			} else {
				s.reviewed[f.Key()] = fingerprint(*f)
				if s.Hunks == nil {
					s.Hunks = map[string]bool{}
				}
				for h := range HunkRanges(f.Lines) {
					s.Hunks[HunkID(*f, h)] = true
				}
				s.Remember(*f)
			}
		}
	}
	s.Clamp(visible)
}

// NumberedLine keeps source positions separate from text (including +++/---
// source lines, which are additions/deletions once inside a hunk).
type NumberedLine struct {
	Text     string
	Old, New int
	Kind     byte
}

func NumberLines(lines []string) []NumberedLine {
	result := make([]NumberedLine, 0, len(lines))
	old, next, oldRemaining, newRemaining := 0, 0, 0, 0
	for _, line := range lines {
		row := NumberedLine{Text: line}
		if strings.HasPrefix(line, "@@ ") {
			fields := strings.Fields(line)
			if len(fields) >= 3 {
				old, oldRemaining = parseRange(fields[1])
				next, newRemaining = parseRange(fields[2])
			}
			row.Kind = '@'
		} else if len(line) > 0 && (oldRemaining > 0 || newRemaining > 0) {
			row.Kind = line[0]
			switch line[0] {
			case '+':
				row.New = next
				next++
				newRemaining--
			case '-':
				row.Old = old
				old++
				oldRemaining--
			case ' ':
				row.Old, row.New = old, next
				old++
				next++
				oldRemaining--
				newRemaining--
			}
		}
		result = append(result, row)
	}
	return result
}
func parseRange(s string) (start, count int) {
	count = 1
	if strings.Contains(s, ",") {
		_, _ = fmt.Sscanf(s[1:], "%d,%d", &start, &count)
	} else {
		_, _ = fmt.Sscanf(s[1:], "%d", &start)
	}
	return
}

// DisplayLines annotates the selected version without interpreting source text
// as patch metadata. Removed text is available in the diff or deleted-file view.
func (s *State) DisplayLines() []NumberedLine {
	f := s.Current()
	if f == nil {
		return nil
	}
	if !s.FullFile {
		return NumberLines(f.Lines)
	}
	if s.ContentKey != f.Key() || s.ContentLoading && s.Content.Lines == nil {
		return []NumberedLine{{Text: "Loading full file…"}}
	}
	if s.Content.Err != nil {
		return []NumberedLine{{Text: s.Content.Err.Error()}}
	}
	if s.Content.Binary {
		return []NumberedLine{{Text: "Binary file — no text view available."}}
	}
	if len(s.Content.Lines) == 0 {
		return []NumberedLine{{Text: "Empty file."}}
	}
	rows := make([]NumberedLine, len(s.Content.Lines))
	for i, text := range s.Content.Lines {
		rows[i] = NumberedLine{Text: text, New: i + 1}
		if s.Content.Old {
			rows[i].Old, rows[i].New = i+1, 0
		}
	}
	if f.Scope == diffview.Untracked {
		for i := range rows {
			rows[i].Kind = '+'
		}
		return rows
	}
	next := 1
	for _, line := range NumberLines(f.Lines) {
		if line.Kind == '@' {
			fields := strings.Fields(line.Text)
			if len(fields) > 2 {
				next, _ = parseRange(fields[2])
				next = max(1, next)
			}
		}
		number := line.New
		mark := line.Kind
		if s.Content.Old {
			number = line.Old
		} else if mark == '-' {
			number, mark = next, '!'
		}
		if line.New > 0 {
			next = line.New + 1
		}
		if mark != '+' && mark != '-' && mark != '!' {
			continue
		}
		// A deletion at EOF is marked on the last remaining line.
		if mark == '!' {
			number = min(number, len(rows))
		}
		if number > 0 && number <= len(rows) && rows[number-1].Kind != '+' {
			rows[number-1].Kind = mark
		}
	}
	return rows
}
