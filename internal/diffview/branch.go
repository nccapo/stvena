package diffview

import (
	"fmt"
	"strings"
	"time"

	"github.com/nccapo/stvena/internal/repo"
)

// BranchChanges compares the captured working tree with the merge base of HEAD
// and the repository's local default branch. It includes committed and
// uncommitted work without fetching or changing the user's repository.
func BranchChanges(ws repo.Workspace, after string) Snapshot {
	s := Snapshot{Root: ws.Root, Tree: after, Label: "Branch changes", UpdatedAt: time.Now()}
	if !ws.Git() {
		// Stop here rather than at defaultBranch, whose "set origin/HEAD or
		// create main/master" would send the user after a branch that could
		// not help: there is no repository to hold one.
		s.Err = fmt.Errorf("branch changes need a Git repository; run git init, then restart stvena")
		return s
	}
	if !validOID(after) {
		s.Err = fmt.Errorf("branch changes need a captured Git snapshot")
		return s
	}
	if out, err := gitOutput(ws, "symbolic-ref", "--quiet", "--short", "HEAD"); err == nil {
		s.Branch = strings.TrimSpace(string(out))
	} else if out, err := gitOutput(ws, "rev-parse", "--short", "HEAD"); err == nil {
		s.Branch = strings.TrimSpace(string(out)) + " (detached)"
	}
	baseRef, baseName, err := defaultBranch(ws)
	if err != nil {
		s.Err = err
		return s
	}
	head, err := gitOutput(ws, "rev-parse", "--verify", "HEAD")
	if err != nil {
		s.Err = fmt.Errorf("branch changes need at least one commit")
		return s
	}
	base, err := gitOutput(ws, "merge-base", strings.TrimSpace(string(head)), baseRef)
	if err != nil {
		s.Err = fmt.Errorf("find merge base with %s: %w", baseName, err)
		return s
	}
	s.Files, s.Err = collectDiff(ws, BranchScope, []string{strings.TrimSpace(string(base)), after})
	s.Label = "Branch changes from " + baseName
	s.Finish()
	return s
}

// BranchHead identifies commits that can change branch comparison even when the
// captured tree is unchanged, such as committing the current working tree.
func BranchHead(ws repo.Workspace) string {
	if !ws.Git() {
		return ""
	}
	out, err := gitOutput(ws, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func defaultBranch(ws repo.Workspace) (ref, name string, err error) {
	if out, e := gitOutput(ws, "symbolic-ref", "--quiet", "refs/remotes/origin/HEAD"); e == nil {
		ref = strings.TrimSpace(string(out))
		if ref != "" {
			if _, verifyErr := gitOutput(ws, "show-ref", "--verify", "--quiet", ref); verifyErr != nil {
				ref = ""
			}
		}
		if ref != "" {
			return ref, strings.TrimPrefix(ref, "refs/remotes/"), nil
		}
	}
	for _, candidate := range []struct{ ref, name string }{
		{"refs/remotes/origin/main", "origin/main"},
		{"refs/heads/main", "main"},
		{"refs/remotes/origin/master", "origin/master"},
		{"refs/heads/master", "master"},
	} {
		if _, e := gitOutput(ws, "show-ref", "--verify", "--quiet", candidate.ref); e == nil {
			return candidate.ref, candidate.name, nil
		}
	}
	return "", "", fmt.Errorf("default branch not found locally (set origin/HEAD or create main/master)")
}
