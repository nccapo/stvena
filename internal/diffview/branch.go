package diffview

import (
	"fmt"
	"strings"
	"time"
)

// BranchChanges compares the captured working tree with the merge base of HEAD
// and the repository's local default branch. It includes committed and
// uncommitted work without fetching or changing the user's repository.
func BranchChanges(root, after string) Snapshot {
	s := Snapshot{Root: root, Tree: after, Label: "Branch changes", UpdatedAt: time.Now()}
	if !validOID(after) {
		s.Err = fmt.Errorf("branch changes need a captured Git snapshot")
		return s
	}
	if out, err := gitOutput(root, "symbolic-ref", "--quiet", "--short", "HEAD"); err == nil {
		s.Branch = strings.TrimSpace(string(out))
	} else if out, err := gitOutput(root, "rev-parse", "--short", "HEAD"); err == nil {
		s.Branch = strings.TrimSpace(string(out)) + " (detached)"
	}
	baseRef, baseName, err := defaultBranch(root)
	if err != nil {
		s.Err = err
		return s
	}
	head, err := gitOutput(root, "rev-parse", "--verify", "HEAD")
	if err != nil {
		s.Err = fmt.Errorf("branch changes need at least one commit")
		return s
	}
	base, err := gitOutput(root, "merge-base", strings.TrimSpace(string(head)), baseRef)
	if err != nil {
		s.Err = fmt.Errorf("find merge base with %s: %w", baseName, err)
		return s
	}
	s.Files, s.Err = collectDiff(root, BranchScope, []string{strings.TrimSpace(string(base)), after})
	s.Label = "Branch changes from " + baseName
	s.Finish()
	return s
}

// BranchHead identifies commits that can change branch comparison even when the
// captured tree is unchanged, such as committing the current working tree.
func BranchHead(root string) string {
	out, err := gitOutput(root, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func defaultBranch(root string) (ref, name string, err error) {
	if out, e := gitOutput(root, "symbolic-ref", "--quiet", "refs/remotes/origin/HEAD"); e == nil {
		ref = strings.TrimSpace(string(out))
		if ref != "" {
			if _, verifyErr := gitOutput(root, "show-ref", "--verify", "--quiet", ref); verifyErr != nil {
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
		if _, e := gitOutput(root, "show-ref", "--verify", "--quiet", candidate.ref); e == nil {
			return candidate.ref, candidate.name, nil
		}
	}
	return "", "", fmt.Errorf("default branch not found locally (set origin/HEAD or create main/master)")
}
