package ui

import (
	"fmt"
	"path"
	"strings"
	"unicode"

	"github.com/mattn/go-runewidth"
	"github.com/nccapo/stvena/internal/diffview"
	"github.com/nccapo/stvena/internal/review"
)

// ReviewSize reserves a compact file list and a persistent navigation footer.
func ReviewSize(height, files int) (fileRows, patchRows int) {
	if height < 7 {
		return 0, max(1, height-4)
	}
	fileRows = min(files, max(1, min(8, (height-5)/3)))
	return fileRows, max(1, height-fileRows-5)
}

func renderReview(s *review.State, width, height int, focused bool) []string {
	if height <= 0 {
		return nil
	}
	if WelcomeVisible(s) {
		return renderWelcome(s, width, height, focused)
	}
	bg := headerBG
	if focused {
		bg = focusedBG
	}
	var rows []string
	add := func(text string) { rows = append(rows, reviewRow(text, width)) }
	approx := ""
	if s.Snapshot.Approximate {
		approx = "~"
	}
	noun := "files"
	if s.Snapshot.FileCount == 1 {
		noun = "file"
	}
	if s.Checkpoint != nil {
		f, tf, h, th := s.CheckpointProgress()
		add(bg + bold + fmt.Sprintf(" Checkpoint · %d/%d files · %d/%d hunks", f, tf, h, th))
	} else if s.Source == "project" {
		add(bg + bold + fmt.Sprintf(" Project · %d %s", s.Snapshot.FileCount, noun) + reset + muted + "  /: find file")
	} else {
		add(bg + bold + fmt.Sprintf(" %d %s  %s+%d -%d", s.Snapshot.FileCount, noun, approx, s.Snapshot.Added, s.Snapshot.Deleted) + reset + bg + "  " + safeText(s.Snapshot.Branch))
	}
	if s.Help {
		for _, line := range []string{
			" REVIEW CONTROLS", " Ctrl-G     switch panes (agent / workspace)", " Ctrl-Q     quit stvena and stop the agent", " j/k ↑/↓    select file / scroll patch", " Enter      open selected file", " f          open file browser", " v          toggle diff / full file", " Backspace  focus file list", " n / p      next / previous file", " N          next unreviewed file", " Tab        all → staged → unstaged → new", " /          filter file paths", " Esc        cancel filter / return to list", " d/u PgDn/Up  half-page scroll", " g/G        first / last file or line", " [ / ]      previous / next hunk", " h/l ←/→    horizontal scroll", " 0          reset horizontal scroll", " Space      mark / unmark reviewed", " Drag       select source lines with the mouse", " V          start / clear code selection", " b          paste selected code into agent draft", " x / B      collect code / open context tray", " 3          browse all project files", " T then o   checks / navigable problems", " ? / Esc    close help", "", " Review marks clear when content changes.", " Incomplete/conflicted files cannot be marked.", " Counts sum staged + unstaged + untracked.", " ~ means a preview/count is incomplete.", " a          open all review actions", " F          expand review", " 1 / 2      session / workspace", " P          pin / resume live", " S / A      stage file / hunk (confirmation)",
		} {
			add(line)
		}
	} else {
		scope := "all"
		if s.Scope != 0 {
			scope = string(review.Scopes[s.Scope])
		}
		filter := ""
		if s.Query != "" || s.Searching {
			filter = "  /" + safeText(s.Query)
			if s.Searching {
				filter += "▏"
			}
		}
		source := s.Snapshot.Label
		if source == "" {
			source = "Workspace"
		}
		if s.Pinned {
			source += " · Pinned"
			pinnedVersion := s.PinnedVersion
			if pinnedVersion == "" {
				pinnedVersion = s.Snapshot.Version
			}
			if s.Latest.Version != pinnedVersion {
				source += " (updates available)"
			}
		}
		if !s.Pinned {
			source += " · Live"
		}
		if s.Source != "session" && s.Source != "project" {
			source += " · " + scope
		}
		if s.Checkpoint != nil {
			freshness := "live unchanged"
			if s.CheckpointNewer() {
				freshness = "NEW LIVE"
			}
			if s.Latest.Tree == "" && !s.CheckpointNewer() {
				freshness = "checking live…"
			}
			add(cyan + fmt.Sprintf(" Pinned · %s · %d comments · %d selections", freshness, len(s.CheckpointComments()), len(s.Attachments)))
		} else if s.Source == "project" {
			add(cyan + " " + source + filter)
		} else {
			add(cyan + " " + source + fmt.Sprintf(" · %d/%d reviewed", s.ReviewedCount(), len(s.Snapshot.Files)) + filter)
		}
		fileRows, patchRows := ReviewSize(height, s.FileListCount())
		if s.Browser {
			fileRows = min(len(s.Indices), max(0, height-3))
		}
		if s.Source == "project" {
			for _, row := range projectTreeRows(s, width, height) {
				add(row)
			}
			fileRows = 0
		}
		start := min(max(0, s.Selected-fileRows/2), max(0, len(s.Indices)-fileRows))
		for i := start; i < start+fileRows; i++ {
			f := s.Snapshot.Files[s.Indices[i]]
			marker, style := "  ", ""
			if i == s.Selected {
				marker, style = "› ", focusedBG+bold
			}
			check := " "
			if s.Reviewed(f) {
				check = "✓"
			} else if _, ok := s.History[f.Key()]; ok {
				check = "↻"
			}
			name := safeText(path.Base(f.Path))
			directory := path.Dir(f.Path)
			detail := ""
			if directory != "." {
				detail = "  " + safeText(directory)
			}
			if f.OldPath != "" {
				detail = "  ← " + safeText(f.OldPath)
			}
			status := map[string]string{"M": "Modified", "A": "Added", "D": "Deleted", "R": "Renamed", "C": "Copied", "U": "Conflict", "?": "New", "??": "New"}[f.Status]
			if status == "" {
				status = f.Status
			}
			if s.Source != "session" && f.Scope != diffview.Session {
				if status != "" {
					status += " · "
				}
				status += string(f.Scope)
			}
			stats := " " + status + "  " + fileStats(f) + " "
			badge, badgeColor := f.Status, muted
			switch badge {
			case "M":
				badgeColor = yellow
			case "A", "?", "??":
				badge, badgeColor = "A", green
			case "D", "U":
				badgeColor = red
			case "R", "C":
				badgeColor = cyan
			}
			left := style + marker + badgeColor + bold + fitANSI(safeText(badge), 1) + reset + style + " " + check + " " + bold + name + reset + style + muted + detail
			// Keep the filename visible before secondary directory and status details.
			if width >= 50 && s.Source != "project" {
				add(fitANSI(left, max(0, width-ansiWidth(stats))) + muted + stats)
			} else {
				add(left)
			}
		}
		if f := s.Current(); f != nil && !s.Browser {
			add(bg + viewTabs(s) + muted + "  " + safeText(f.Path))
			if height >= 7 {
				if s.Selecting {
					add(cyan + selectionSummary(s))
				} else if s.FullFile {
					add(muted + " LINE  Δ │ " + safeText(s.Content.Source))
				} else {
					add(muted + "   OLD   NEW │ PATCH")
				}
			}
			for _, line := range codeRows(s, width, patchRows) {
				add(line)
			}
		} else if s.Snapshot.Err == nil && len(s.Indices) == 0 {
			if s.Snapshot.FileCount == 0 {
				if s.Source == "session" {
					add(dim + " No changes in this session yet.")
					add(muted + " Existing edits are in Workspace (2).")
				} else {
					add(dim + " Working tree is clean.")
				}
				add(cyan + " Ctrl-G: Switch panes · a: Actions")
			} else {
				add(dim + " No files match this view. Esc clears filter.")
			}
		}
	}
	footer := reviewFooter(s)
	if s.Menu || s.Panel != "" || s.Prompt != "" || s.ConfirmAction != "" {
		return renderOverlay(s, width, height)
	}
	if len(rows) >= height {
		rows = rows[:max(0, height-1)]
	}
	for len(rows) < height-1 {
		add("")
	}
	add(footer)
	return rows
}
func fileStats(f diffview.File) string {
	if f.Status == "U" {
		return "conflict"
	}
	if f.Binary {
		return "binary"
	}
	prefix := ""
	if f.Truncated {
		prefix = "~"
	}
	return fmt.Sprintf("%s+%d -%d", prefix, f.Added, f.Deleted)
}

// Treat repository content as text, never as terminal escape sequences.
func safeText(text string) string {
	return strings.Map(func(r rune) rune {
		if r == '\t' {
			return ' '
		}
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf) {
			return '�'
		}
		return r
	}, strings.ReplaceAll(text, "\t", "    "))
}
func horizontalText(text string, columns int) string {
	for i, r := range text {
		width := runewidth.RuneWidth(r)
		if columns <= 0 {
			return text[i:]
		}
		if width > columns {
			return " " + text[i+len(string(r)):]
		}
		columns -= width
	}
	return ""
}
