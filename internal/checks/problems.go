package checks

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

type Problem struct {
	Path         string
	Line, Column int
	Message      string
}

var colonLocation = regexp.MustCompile(`^(.+?):([0-9]+)(?::([0-9]+))?(?::|\s|$)(.*)$`)
var tsLocation = regexp.MustCompile(`^(.+)\(([0-9]+),([0-9]+)\):\s*(.*)$`)
var pythonLocation = regexp.MustCompile(`^File "([^"]+)", line ([0-9]+)(.*)$`)

// ParseProblems recognizes file:line[:column], tsc and Python locations. Paths
// outside the checkout are not navigable. Basenames are resolved unambiguously
// against the tested tree when the user opens them; raw logs remain authoritative.
func ParseProblems(output, work string) []Problem {
	var result []Problem
	seen := map[string]bool{}
	roots := []string{filepath.Clean(work)}
	if real, err := filepath.EvalSymlinks(work); err == nil {
		roots = append(roots, real)
	}
	for _, raw := range strings.Split(ansi.Strip(output), "\n") {
		text := strings.TrimSpace(raw)
		text = strings.TrimSpace(strings.TrimPrefix(text, "-->"))
		m := colonLocation.FindStringSubmatch(text)
		if m == nil {
			m = tsLocation.FindStringSubmatch(text)
		}
		if m == nil {
			if p := pythonLocation.FindStringSubmatch(text); p != nil {
				m = []string{p[0], p[1], p[2], "", p[3]}
			}
		}
		if m == nil {
			continue
		}
		name := strings.TrimSpace(m[1])
		name = strings.Trim(name, "()")
		if filepath.IsAbs(name) {
			found := false
			for _, root := range roots {
				if work == "" {
					break
				}
				rel, err := filepath.Rel(root, name)
				if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
					name = rel
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		name = filepath.Clean(name)
		if name == "." || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) || strings.ContainsAny(name, "\x00\r\n") {
			continue
		}
		line, _ := strconv.Atoi(m[2])
		column, _ := strconv.Atoi(m[3])
		if line < 1 {
			continue
		}
		key := name + ":" + m[2] + ":" + m[3] + ":" + m[4]
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, Problem{Path: filepath.ToSlash(name), Line: line, Column: column, Message: strings.TrimSpace(m[4])})
		if len(result) >= 500 {
			break
		}
	}
	return result
}
