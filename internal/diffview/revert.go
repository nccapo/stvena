package diffview

import (
	"fmt"
	"sort"
	"strings"
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
func Revert(root string, targets []Target) (notice string, err error) {
	if len(targets) == 0 {
		return "", fmt.Errorf("nothing to reject")
	}
	var patches []string
	for _, target := range targets {
		patch, err := revertPatch(root, target)
		if err != nil {
			return "", err
		}
		patches = append(patches, patch)
	}
	combined := strings.Join(patches, "")
	if err := applyPatch(root, combined, "--index", "--reverse"); err == nil {
		return "", nil
	}
	// A partially staged or untracked path cannot apply to the index. Reverting
	// the working tree alone is still the user's decision; report what remains.
	if err := applyPatch(root, combined, "--reverse"); err != nil {
		return "", fmt.Errorf("working tree unchanged: these lines no longer match the captured version. "+
			"Review the file again before rejecting it (%w)", err)
	}
	return stagedNotice(root, targets), nil
}

func revertPatch(root string, target Target) (string, error) {
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
		return untrackedPatch(root, f)
	}
	lines, err := selectHunks(f, target.Hunks, "reject")
	if err != nil {
		return "", err
	}
	return strings.Join(lines, "\n") + "\n", nil
}

// untrackedPatch describes the captured file as an addition. Reversed, it
// deletes the file the agent created.
func untrackedPatch(root string, f File) (string, error) {
	if f.ContentRef == "" {
		return "", fmt.Errorf("wait for a captured version of %s before rejecting it", f.Path)
	}
	empty, err := gitOutput(root, "hash-object", "-w", "-t", "tree", "--stdin")
	if err != nil {
		return "", err
	}
	patch, err := gitOutput(root, "diff", "--no-ext-diff", "--no-textconv", "--no-color", "--binary",
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
func stagedNotice(root string, targets []Target) string {
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
	out, err := gitOutput(root, append([]string{"diff", "--cached", "--name-only", "-z", "--"}, args...)...)
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
