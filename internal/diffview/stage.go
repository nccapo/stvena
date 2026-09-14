package diffview

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"syscall"
	"time"
)

// Stage applies the displayed patch to the index, leaving working files alone.
// Git locks the index and verifies patch context before writing it.
func Stage(root string, expected File, hunk int) error {
	if expected.Scope == Session {
		return fmt.Errorf("switch to Workspace (2) to stage changes")
	}
	if expected.Truncated || expected.Status == "U" {
		return fmt.Errorf("incomplete or conflicted changes cannot be staged here")
	}
	current := Collect(root)
	if current.Err != nil {
		return current.Err
	}
	found := false
	for _, f := range current.Files {
		if f.Key() == expected.Key() {
			found = Revision(f) == Revision(expected)
			break
		}
	}
	if !found {
		return fmt.Errorf("file changed since this view; resume live and review it again")
	}
	if expected.Scope == Untracked {
		if hunk >= 0 {
			return fmt.Errorf("stage new files as a whole file")
		}
		if expected.ContentRef == "" {
			return fmt.Errorf("wait for a captured version before staging")
		}
		empty, err := gitOutput(root, "hash-object", "-w", "-t", "tree", "--stdin")
		if err != nil {
			return err
		}
		patch, err := gitOutput(root, "diff", "--no-ext-diff", "--no-textconv", "--no-color", "--binary", strings.TrimSpace(string(empty)), expected.ContentRef, "--", expected.Path)
		if err != nil {
			return err
		}
		if len(patch) == 0 {
			return fmt.Errorf("captured file unavailable")
		}
		return applyToIndex(root, string(patch), false)
	}

	if expected.Binary {
		return fmt.Errorf("binary staging is not yet supported; use your Git client")
	}
	var hunks []int
	if hunk >= 0 {
		hunks = []int{hunk}
	}
	lines, err := selectHunks(expected, hunks, "stage")
	if err != nil {
		return err
	}
	patch := strings.Join(lines, "\n") + "\n"
	return applyToIndex(root, patch, expected.Scope == Staged)
}

// selectHunks returns the patch lines for whole-file application, or the file
// header followed by the requested hunks. Hunk indices are the positions of
// "@@ " lines in the displayed patch, which is what the review UI counts.
func selectHunks(f File, hunks []int, verb string) ([]string, error) {
	if len(hunks) == 0 {
		return f.Lines, nil
	}
	if f.Status != "M" || strings.Contains(strings.Join(f.Lines, "\n"), "\nold mode ") {
		return nil, fmt.Errorf("%s added, deleted, renamed and mode changes as a whole file", verb)
	}
	var starts []int
	for i, line := range f.Lines {
		if strings.HasPrefix(line, "@@ ") {
			starts = append(starts, i)
		}
	}
	ordered := append([]int{}, hunks...)
	sort.Ints(ordered)
	lines := append([]string{}, f.Lines[:starts[0]]...)
	previous := -1
	for _, h := range ordered {
		if h < 0 || h >= len(starts) {
			return nil, fmt.Errorf("select a hunk first")
		}
		if h == previous {
			continue
		}
		previous = h
		end := len(f.Lines)
		if h+1 < len(starts) {
			end = starts[h+1]
		}
		lines = append(lines, f.Lines[starts[h]:end]...)
	}
	return lines, nil
}
func applyToIndex(root, patch string, reverse bool) error {
	options := []string{"--cached"}
	if reverse {
		options = append(options, "--reverse")
	}
	return applyPatch(root, patch, options...)
}

func applyPatch(root, patch string, options ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	args := append([]string{"--literal-pathspecs", "-C", root, "apply", "--whitespace=nowarn"}, options...)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 2 * time.Second
	cmd.Stdin = strings.NewReader(patch)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s (%v)", strings.TrimSpace(string(out)), err)
	}
	return nil
}
