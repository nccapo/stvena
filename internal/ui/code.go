package ui

import (
	"fmt"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/nccapo/stvena/internal/diffview"
	"github.com/nccapo/stvena/internal/review"
)

var syntaxCache struct {
	key   string
	lines []string
}

func syntaxLines(s *review.State, lines []review.NumberedLine) []string {
	f := s.Current()
	if f == nil {
		return nil
	}
	key := f.Key() + diffview.Revision(*f) + fmt.Sprint(s.FullFile) + s.ContentKey + fmt.Sprint(s.ContentLoading)
	if s.FullFile {
		key += fmt.Sprint(len(s.Content.Lines)) + s.Content.Source
	}
	if syntaxCache.key == key {
		return syntaxCache.lines
	}
	var raw strings.Builder
	for _, line := range lines {
		text := line.Text
		if !s.FullFile && (line.Kind == '+' || line.Kind == '-' || line.Kind == ' ') {
			text = text[1:]
		}
		raw.WriteString(safeText(text))
		raw.WriteByte('\n')
	}
	source := raw.String()
	rendered := strings.Split(strings.TrimSuffix(source, "\n"), "\n")
	if len(source) < 1<<20 {
		lexer := lexers.Match(f.Path)
		if lexer != nil {
			iterator, err := chroma.Coalesce(lexer).Tokenise(nil, source)
			if err == nil {
				var out strings.Builder
				style := styles.Get("dracula")
				for token := iterator(); token != chroma.EOF; token = iterator() {
					entry := style.Get(token.Type)
					color := ""
					if entry.Colour.IsSet() {
						color = fmt.Sprintf("\x1b[38;2;%d;%d;%dm", entry.Colour.Red(), entry.Colour.Green(), entry.Colour.Blue())
					}
					for i, part := range strings.Split(token.Value, "\n") {
						if i > 0 {
							out.WriteByte('\n')
						}
						if part != "" {
							out.WriteString(color + part + reset)
						}
					}
				}
				rendered = strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
			}
		}
	}
	syntaxCache.key, syntaxCache.lines = key, rendered
	return rendered
}

func codeRows(s *review.State, width, height int) []string {
	rows, _ := codeRowsMapped(s, width, height)
	return rows
}
func codeRowsMapped(s *review.State, width, height int) ([]string, [][2]int) {
	lines := s.DisplayLines()
	syntax := syntaxLines(s, lines)
	if s.SideBySide && !s.FullFile && width >= 60 {
		return splitRows(s, lines, width, height)
	}
	var rows []string
	var hits [][2]int
	for i := s.Scroll; i < len(lines) && len(rows) < height; i++ {
		line := lines[i]
		old, next := "", ""
		if line.Old > 0 {
			old = fmt.Sprint(line.Old)
		}
		if line.New > 0 {
			next = fmt.Sprint(line.New)
		}
		gutter := fmt.Sprintf(" %5s %5s │ ", old, next)
		if s.FullFile {
			number := next
			if s.Content.Old {
				number = old
			}
			marker := " "
			if line.Kind != 0 {
				marker = string(line.Kind)
			}
			gutter = fmt.Sprintf(" %5s %s │ ", number, marker)
		}
		text := safeText(line.Text)
		if i < len(syntax) {
			text = syntax[i]
		}
		if !s.FullFile && (line.Kind == '+' || line.Kind == '-' || line.Kind == ' ') {
			text = string(line.Kind) + text
		}
		style := reset
		if line.Kind == '+' {
			style = green
		} else if line.Kind == '-' {
			style = red
		} else if line.Kind == '@' {
			style = cyan
		}
		if line.Kind == '@' {
			text = cyan + safeText(line.Text)
		}
		if s.Selecting && s.SelectionIncludes(i, line) {
			text = "\x1b[7m" + strings.ReplaceAll(text, reset, reset+"\x1b[7m")
		}
		if s.TextQuery != "" && strings.Contains(strings.ToLower(line.Text), strings.ToLower(s.TextQuery)) {
			gutter = "›" + gutter[1:]
			style = yellow
		}
		available := max(1, width-ansiWidth(gutter))
		if s.Wrap {
			wrapped := strings.Split(ansi.Hardwrap(text, available, true), "\n")
			for j, part := range wrapped {
				if len(rows) >= height {
					break
				}
				g := gutter
				if j > 0 {
					g = strings.Repeat(" ", max(0, ansiWidth(gutter)-2)) + "↪ "
				}
				rows = append(rows, muted+g+reset+style+part+reset)
				hits = append(hits, [2]int{i, i})
			}
		} else {
			rows = append(rows, muted+gutter+reset+style+ansi.Cut(text, s.Horizontal, s.Horizontal+available)+reset)
			hits = append(hits, [2]int{i, i})
		}
	}
	return rows, hits
}

// Keep source row order while pairing corresponding removed and added lines.
// Each visual pair is reachable from either source row during navigation.
func splitRows(s *review.State, lines []review.NumberedLine, width, height int) ([]string, [][2]int) {
	half := (width - 3) / 2
	syntax := syntaxLines(s, lines)

	cell := func(index int, side bool, other int) []string {
		if index < 0 {
			return []string{strings.Repeat(" ", half)}
		}
		line := lines[index]
		n := line.Old
		if side {
			n = line.New
		}
		number := ""
		if n > 0 {
			number = fmt.Sprint(n)
		}
		text := safeText(strings.TrimPrefix(line.Text, string(line.Kind)))
		color := ""
		if line.Kind == '+' {
			color = green
		} else if line.Kind == '-' {
			color = red
		}
		if other >= 0 && line.Kind != lines[other].Kind {
			text = intraline(text, safeText(strings.TrimPrefix(lines[other].Text, string(lines[other].Kind))), color)
		} else if index < len(syntax) {
			text = syntax[index]
		}
		if s.Selecting && s.SelectionIncludes(index, line) && (s.SelectionSide == 0 || side && s.SelectionSide == 'n' || !side && s.SelectionSide == 'o') {
			text = "\x1b[7m" + strings.ReplaceAll(text, reset, reset+"\x1b[7m")
		}
		gutter := fmt.Sprintf(" %5s │ ", number)
		if index == s.Scroll {
			gutter = "›" + gutter[1:]
		}
		parts := []string{ansi.Cut(text, s.Horizontal, s.Horizontal+max(1, half-9))}
		if s.Wrap {
			parts = strings.Split(ansi.Hardwrap(text, max(1, half-9), true), "\n")
		}
		result := make([]string, 0, len(parts))
		for j, part := range parts {
			g := gutter
			if j > 0 {
				g = "       ↪ "
			}
			result = append(result, fitANSI(muted+g+reset+color+part+reset, half))
		}
		return result
	}
	pairs := review.PairLines(lines)
	first := 0
	for i, p := range pairs {
		if p[0] == s.Scroll || p[1] == s.Scroll {
			first = i
			break
		}
	}
	var rows []string
	var hits [][2]int
	for _, p := range pairs[first:] {
		if len(rows) >= height {
			break
		}
		index := max(p[0], p[1])
		line := lines[index]
		if p[0] == p[1] && line.Kind != ' ' {
			rows = append(rows, cyan+safeText(line.Text)+reset)
			hits = append(hits, p)
			continue
		}
		old, next := cell(p[0], false, p[1]), cell(p[1], true, p[0])
		for i := 0; i < max(len(old), len(next)) && len(rows) < height; i++ {
			left, right := strings.Repeat(" ", half), strings.Repeat(" ", half)
			if i < len(old) {
				left = old[i]
			}
			if i < len(next) {
				right = next[i]
			}
			rows = append(rows, left+muted+" │ "+reset+right)
			hit := p
			if i >= len(old) {
				hit[0] = -1
			}
			if i >= len(next) {
				hit[1] = -1
			}
			hits = append(hits, hit)
		}
	}
	return rows, hits
}

func intraline(text, other, color string) string {
	a, b := []rune(text), []rune(other)
	prefix := 0
	for prefix < min(len(a), len(b)) && a[prefix] == b[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < min(len(a), len(b))-prefix && a[len(a)-1-suffix] == b[len(b)-1-suffix] {
		suffix++
	}
	if prefix == len(a)-suffix {
		return text
	}
	return string(a[:prefix]) + "\x1b[4;1m" + string(a[prefix:len(a)-suffix]) + reset + color + string(a[len(a)-suffix:])
}
