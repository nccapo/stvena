package review

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

// SelectionMessage snapshots the selected context before focus returns to the
// agent. Patch prefixes keep removed and added code distinct in mixed ranges.
func (s *State) SelectionMessage() (string, error) {
	f := s.Current()
	if f == nil || s.Browser || !s.PatchFocused {
		return "", fmt.Errorf("open a file and select source lines first (V, then arrows)")
	}
	if s.FullFile && (s.ContentLoading || s.ContentKey != f.Key() || s.Content.Err != nil || s.Content.Binary) {
		return "", fmt.Errorf("wait for a readable full-file version")
	}
	lines := s.DisplayLines()
	if len(lines) == 0 {
		return "", fmt.Errorf("no code selected")
	}
	a, b := s.SelectedRange()
	var code []string
	oldStart, oldEnd, newStart, newEnd := 0, 0, 0, 0
	for i, line := range lines[a : b+1] {
		if !s.SelectionIncludes(a+i, line) {
			continue
		}
		if s.SelectionSide == 'o' {
			line.New = 0
		}
		if s.SelectionSide == 'n' {
			line.Old = 0
		}
		if line.Old == 0 && line.New == 0 {
			continue
		}
		if line.Old > 0 {
			if oldStart == 0 {
				oldStart = line.Old
			}
			oldEnd = line.Old
		}
		if line.New > 0 {
			if newStart == 0 {
				newStart = line.New
			}
			newEnd = line.New
		}
		code = append(code, line.Text)
	}
	if len(code) == 0 {
		return "", fmt.Errorf("select source lines, not a diff header")
	}
	source := pasteText(strings.Join(code, "\n"))
	longest, run := 2, 0
	for _, r := range source {
		if r == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	fence := strings.Repeat("`", longest+1)
	var out strings.Builder
	fmt.Fprintf(&out, "\nSelected code from %s\n", strconv.Quote(f.Path))
	if s.Snapshot.Root != "" {
		fmt.Fprintf(&out, "File path: %s\n", strconv.Quote(filepath.Join(s.Snapshot.Root, f.Path)))
	}
	fmt.Fprintf(&out, "Scope: %s\n", f.Scope)
	if oldStart > 0 {
		fmt.Fprintf(&out, "Old lines: %d-%d\n", oldStart, oldEnd)
	}
	if newStart > 0 {
		fmt.Fprintf(&out, "New lines: %d-%d\n", newStart, newEnd)
	}
	if f.OldPath != "" {
		fmt.Fprintf(&out, "Previous path: %s\n", strconv.Quote(f.OldPath))
	}
	if s.Snapshot.Tree != "" {
		fmt.Fprintf(&out, "Working-copy snapshot: %s\n", s.Snapshot.Tree)
	}
	blob := f.AfterOID
	if s.FullFile && s.Content.Old || s.SelectionSide == 'o' {
		blob = f.BeforeOID
	}
	if strings.Trim(blob, "0") != "" {
		fmt.Fprintf(&out, "File object: %s\n", blob)
	}
	out.WriteString("Selected context may be from an earlier version; check the current file before editing.\n\n")
	language := ""
	if !s.FullFile {
		language = "diff"
	}
	fmt.Fprintf(&out, "%s%s\n%s\n%s\n\n", fence, language, source, fence)
	// Strip terminal controls from metadata as well as source. In particular, a
	// repository must not be able to close the bracketed-paste envelope.
	message := pasteText(out.String())
	if len(message) > 32<<10 {
		return "", fmt.Errorf("selection exceeds 32 KiB; select fewer lines or use y to copy")
	}
	return message, nil
}
func pasteText(text string) string {
	return strings.Map(func(r rune) rune {
		if r != '\n' && r != '\t' && (unicode.IsControl(r) || unicode.In(r, unicode.Cf)) {
			return '�'
		}
		return r
	}, text)
}
