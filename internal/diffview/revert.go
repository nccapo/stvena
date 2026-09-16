package diffview

import (
	"fmt"
	"sort"
	"strings"

	"github.com/nccapo/stvena/internal/repo"
)

// Target is one rejected change. An empty Hunks selects the whole file; the
// indices are positions of "@@ " lines in the displayed patch.
type Target struct {
	File  File
	Hunks []int
}

// Revert removes rejected changes from the working tree by reverse-applying
// their own patches. Every target is sent to one `git apply`, which verifies
// each context line and writes nothing unless all of them apply: a batch never
// leaves the working tree half reverted, and a hunk the agent edited again
// since capture is refused rather than force-applied.
//
// The index is reverted with the working tree whenever the same patch applies
// to both. A change reverted only in the working tree could otherwise still be
// committed from the index, so the returned notice names any path left behind.
func Revert(ws repo.Workspace, targets []Target) (notice string, err error) {
	if len(targets) == 0 {
		return "", fmt.Errorf("nothing to reject")
	}
	var patches []string
	for _, target := range targets {
		patch, err := revertPatch(ws, target)
		if err != nil {
			return "", err
		}
		patches = append(patches, patch)
	}
	combined := strings.Join(patches, "")
	// Stvena's own index in a project without Git is a capture artifact, not
	// something the user can commit from. Applying to it would only fail on a
	// stat cache that no longer matches the file we are about to rewrite.
	if ws.Git() {
		if err := applyPatch(ws, combined, "--index", "--reverse"); err == nil {
			return "", nil
		}
	}
	// A partially staged or untracked path cannot apply to the index. Reverting
	// the working tree alone is still the user's decision; report what remains.
	if err := applyPatch(ws, combined, "--reverse"); err != nil {
		return "", fmt.Errorf("working tree unchanged: these lines no longer match the captured version. "+
			"Review the file again before rejecting it (%w)", err)
	}
	if !ws.Git() {
		return "", nil
	}
	return stagedNotice(ws, targets), nil
}

func revertPatch(ws repo.Workspace, target Target) (string, error) {
	f := target.File
	switch {
	case f.Scope == ProjectScope || f.Scope == BranchScope:
		return "", fmt.Errorf("reject changes from This session (1) or Workspace (2)")
	case f.Status == "U":
		return "", fmt.Errorf("%s has unresolved conflicts; resolve them before rejecting changes", f.Path)
	case f.Truncated:
		return "", fmt.Errorf("%s has an incomplete patch and cannot be reverted here", f.Path)
	case f.Binary && f.Scope != Untracked:
		return "", fmt.Errorf("%s is binary; revert it with your Git client", f.Path)
	}
	if f.Scope == Untracked {
		if len(target.Hunks) > 0 {
			return "", fmt.Errorf("reject new files as a whole file")
		}
		return untrackedPatch(ws, f)
	}
	lines, err := selectHunks(f, target.Hunks, "reject")
	if err != nil {
		return "", err
	}
	return strings.Join(lines, "\n") + "\n", nil
}

// untrackedPatch describes the captured file as an addition. Reversed, it
// deletes the file the agent created.
func untrackedPatch(ws repo.Workspace, f File) (string, error) {
	if f.ContentRef == "" {
		return "", fmt.Errorf("wait for a captured version of %s before rejecting it", f.Path)
	}
	empty, err := gitOutput(ws, "hash-object", "-w", "-t", "tree", "--stdin")
	if err != nil {
		return "", err
	}
	patch, err := gitOutput(ws, "diff", "--no-ext-diff", "--no-textconv", "--no-color", "--binary",
		strings.TrimSpace(string(empty)), f.ContentRef, "--", f.Path)
	if err != nil {
		return "", err
	}
	if len(patch) == 0 {
		return "", fmt.Errorf("captured version of %s is unavailable", f.Path)
	}
	return string(patch), nil
}

// stagedNotice names reverted paths whose staged copy still holds the change.
func stagedNotice(ws repo.Workspace, targets []Target) string {
	paths := map[string]bool{}
	var args []string
	for _, target := range targets {
		for _, path := range []string{target.File.Path, target.File.OldPath} {
			if path != "" && !paths[path] {
				paths[path] = true
				args = append(args, path)
			}
		}
	}
	out, err := gitOutput(ws, append([]string{"diff", "--cached", "--name-only", "-z", "--"}, args...)...)
	if err != nil {
		return ""
	}
	var remaining []string
	for _, name := range splitNUL(out) {
		if name != "" && paths[name] {
			remaining = append(remaining, name)
		}
	}
	if len(remaining) == 0 {
		return ""
	}
	sort.Strings(remaining)
	return "Reverted in the working tree only; the staged copy still holds the change: " + strings.Join(remaining, ", ")
}

// LineSpan is an inclusive, one-based range of new-file lines.
type LineSpan struct {
	Start, End int
}

// HunkSpans returns the working-file lines each hunk of f changes, indexed the
// same way as review hunk indices: the order of "@@ " lines in the patch. A
// hunk that only deletes lines points at the surviving neighbour, which is
// where an editor should place its marker.
func HunkSpans(f File) []LineSpan {
	var spans []LineSpan
	line, inHunk := 1, false
	for _, text := range f.Lines {
		if strings.HasPrefix(text, "@@ ") {
			fields := strings.Fields(text)
			if len(fields) < 3 {
				continue
			}
			_, _ = fmt.Sscanf(strings.Split(fields[2], ",")[0], "+%d", &line)
			spans = append(spans, LineSpan{})
			inHunk = true
			continue
		}
		if !inHunk || len(spans) == 0 || text == "" {
			continue
		}
		span := &spans[len(spans)-1]
		switch text[0] {
		case '+', '-':
			current := max(1, line)
			if span.Start == 0 || current < span.Start {
				span.Start = current
			}
			if current > span.End {
				span.End = current
			}
			if text[0] == '+' {
				line++
			}
		case ' ':
			line++
		}
	}
	for i := range spans {
		if spans[i].Start == 0 {
			// A metadata-only hunk still needs a position in the file.
			spans[i] = LineSpan{Start: 1, End: 1}
		}
		if spans[i].End < spans[i].Start {
			spans[i].End = spans[i].Start
		}
	}
	return spans
}
