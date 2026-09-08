package diffview

import (
	"context"
	"fmt"
	"os/exec"
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
	lines := expected.Lines
	if hunk >= 0 {
		if expected.Status != "M" || strings.Contains(strings.Join(expected.Lines, "\n"), "\nold mode ") {
			return fmt.Errorf("stage added, deleted, renamed and mode changes as a whole file")
		}
		var starts []int
		for i, line := range lines {
			if strings.HasPrefix(line, "@@ ") {
				starts = append(starts, i)
			}
		}
		if hunk >= len(starts) {
			return fmt.Errorf("select a hunk first")
		}
		end := len(lines)
		if hunk+1 < len(starts) {
			end = starts[hunk+1]
		}
		lines = append(append([]string{}, lines[:starts[0]]...), lines[starts[hunk]:end]...)
	}
	patch := strings.Join(lines, "\n") + "\n"
	return applyToIndex(root, patch, expected.Scope == Staged)
}
func applyToIndex(root, patch string, reverse bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	args := []string{"--literal-pathspecs", "-C", root, "apply", "--cached", "--whitespace=nowarn"}
	if reverse {
		args = append(args, "--reverse")
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 2 * time.Second
	cmd.Stdin = strings.NewReader(patch)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("index unchanged: %s (%v)", strings.TrimSpace(string(out)), err)
	}
	return nil
}
