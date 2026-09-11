package diffview

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBranchChangesIncludesCommittedAndWorkingTreeChanges(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init", "-q", "-b", "main")
	runGit(t, root, "config", "user.name", "Stvena Test")
	runGit(t, root, "config", "user.email", "test@example.invalid")
	writeFile(t, root, "base.txt", "base\n")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-qm", "base")
	runGit(t, root, "switch", "-qc", "feature")
	writeFile(t, root, "base.txt", "committed\n")
	runGit(t, root, "commit", "-qam", "feature")
	writeFile(t, root, "working.txt", "uncommitted\n")

	after := captureTree(t, root)
	s := BranchChanges(root, after)
	if s.Err != nil {
		t.Fatal(s.Err)
	}
	if s.Label != "Branch changes from main" || s.Branch != "feature" || s.FileCount != 2 {
		t.Fatalf("branch snapshot: %+v", s)
	}
	if findFile(t, s, "base.txt", BranchScope).Status != "M" || findFile(t, s, "working.txt", BranchScope).Status != "A" {
		t.Fatalf("branch files: %+v", s.Files)
	}
}

func TestBranchChangesTracksCommitWhenTreeStaysTheSame(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init", "-q", "-b", "main")
	runGit(t, root, "config", "user.name", "Stvena Test")
	runGit(t, root, "config", "user.email", "test@example.invalid")
	writeFile(t, root, "file.txt", "base\n")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-qm", "base")
	writeFile(t, root, "file.txt", "working\n")
	tree := captureTree(t, root)
	beforeHead := BranchHead(root)
	if BranchChanges(root, tree).FileCount != 1 {
		t.Fatal("dirty default branch should compare against HEAD")
	}
	runGit(t, root, "commit", "-qam", "working")
	if BranchHead(root) == beforeHead || BranchChanges(root, tree).FileCount != 0 {
		t.Fatal("commit with the same tree did not refresh branch comparison")
	}
}

func TestBranchChangesExplainsMissingDefaultBranch(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init", "-q", "-b", "topic")
	runGit(t, root, "config", "user.name", "Stvena Test")
	runGit(t, root, "config", "user.email", "test@example.invalid")
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("one\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-qm", "first")
	if got := BranchChanges(root, captureTree(t, root)); got.Err == nil {
		t.Fatalf("missing default branch was accepted: %+v", got)
	}
}

func captureTree(t *testing.T, root string) string {
	t.Helper()
	runGit(t, root, "add", "-A")
	defer runGit(t, root, "reset", "-q")
	out, err := gitOutput(root, "write-tree")
	if err != nil {
		t.Fatal(err)
	}
	return string(out[:len(out)-1])
}
