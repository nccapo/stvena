package diffview

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCollectTrackedAndUntrackedChanges(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init", "-q")
	runGit(t, root, "config", "user.email", "test@example.com")
	runGit(t, root, "config", "user.name", "Test")
	writeFile(t, root, "tracked.txt", "before\n")
	runGit(t, root, "add", "tracked.txt")
	runGit(t, root, "commit", "-qm", "initial")

	writeFile(t, root, "tracked.txt", "after\n")
	writeFile(t, root, "new.txt", "one\ntwo\n")
	snapshot := Collect(root)
	if snapshot.Err != nil {
		t.Fatal(snapshot.Err)
	}
	if snapshot.FileCount != 2 {
		t.Fatalf("FileCount = %d, want 2", snapshot.FileCount)
	}
	var lines []string
	for _, f := range snapshot.Files {
		lines = append(lines, f.Lines...)
	}
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"-before", "+after", "+++ b/new.txt", "+one", "+two"} {
		if !strings.Contains(joined, want) {
			t.Errorf("diff does not contain %q:\n%s", want, joined)
		}
	}
}

func TestGitRootRejectsNonRepository(t *testing.T) {
	_, err := GitRoot(t.TempDir())
	if err == nil {
		t.Fatal("GitRoot returned nil error outside repository")
	}
}

func runGit(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func writeFile(t *testing.T, root, name, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func repo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	runGit(t, root, "init", "-q")
	runGit(t, root, "config", "user.email", "test@example.com")
	runGit(t, root, "config", "user.name", "Test")
	return root
}
func collectOK(t *testing.T, root string) Snapshot {
	t.Helper()
	s := Collect(root)
	if s.Err != nil {
		t.Fatal(s.Err)
	}
	return s
}
func findFile(t *testing.T, s Snapshot, path string, scope Scope) File {
	t.Helper()
	for _, f := range s.Files {
		if f.Path == path && f.Scope == scope {
			return f
		}
	}
	t.Fatalf("missing %s %q in %+v", scope, path, s.Files)
	return File{}
}
func TestSeparateIndexAndWorktreeEvenWhenNetChangeIsZero(t *testing.T) {
	root := repo(t)
	writeFile(t, root, "file.txt", "original\n")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-qm", "initial")
	writeFile(t, root, "file.txt", "staged\n")
	runGit(t, root, "add", ".")
	writeFile(t, root, "file.txt", "original\n")
	s := collectOK(t, root)
	if s.FileCount != 1 || len(s.Files) != 2 || s.Added != 2 || s.Deleted != 2 {
		t.Fatalf("wrong totals: %+v", s)
	}
	for _, scope := range []Scope{Staged, Unstaged} {
		f := findFile(t, s, "file.txt", scope)
		if f.Added != 1 || f.Deleted != 1 {
			t.Fatalf("bad stats: %+v", f)
		}
	}
	if !strings.Contains(strings.Join(findFile(t, s, "file.txt", Staged).Lines, "\n"), "+staged") {
		t.Fatal("staged patch missing")
	}
	if !strings.Contains(strings.Join(findFile(t, s, "file.txt", Unstaged).Lines, "\n"), "-staged") {
		t.Fatal("unstaged patch missing")
	}
}
func TestUnbornRepositoryIncludesStagedFiles(t *testing.T) {
	root := repo(t)
	writeFile(t, root, "staged.txt", "one\ntwo\n")
	runGit(t, root, "add", ".")
	writeFile(t, root, "new.txt", "new\n")
	s := collectOK(t, root)
	if s.FileCount != 2 || s.Added != 3 {
		t.Fatalf("wrong totals: %+v", s)
	}
	if f := findFile(t, s, "staged.txt", Staged); f.Status != "A" || !strings.Contains(strings.Join(f.Lines, "\n"), "+two") {
		t.Fatalf("missing staged addition: %+v", f)
	}
}
func TestRenameDeletionBinaryAndUnusualPaths(t *testing.T) {
	root := repo(t)
	for _, name := range []string{"old.txt", "deleted.txt", "tab\tline\n.txt", "binary.dat"} {
		writeFile(t, root, name, "before\n")
	}
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-qm", "initial")
	runGit(t, root, "mv", "old.txt", "renamed file.txt")
	if err := os.Remove(filepath.Join(root, "deleted.txt")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, root, "tab\tline\n.txt", "after\n")
	writeFile(t, root, "binary.dat", "\x00\x01\x02")
	s := collectOK(t, root)
	f := findFile(t, s, "renamed file.txt", Staged)
	if f.OldPath != "old.txt" || f.Status != "R" {
		t.Fatalf("bad rename: %+v", f)
	}
	f = findFile(t, s, "deleted.txt", Unstaged)
	if f.Status != "D" || f.Deleted != 1 || !strings.Contains(strings.Join(f.Lines, "\n"), "-before") {
		t.Fatalf("bad deletion: %+v", f)
	}
	if !findFile(t, s, "binary.dat", Unstaged).Binary {
		t.Fatal("binary not detected")
	}
	f = findFile(t, s, "tab\tline\n.txt", Unstaged)
	if f.Added != 1 || !strings.Contains(strings.Join(f.Lines, "\n"), "+after") {
		t.Fatalf("bad unusual filename: %+v", f)
	}
}
func TestUntrackedLimitsAndSymlinks(t *testing.T) {
	root := repo(t)
	writeFile(t, root, ".gitignore", "ignored\n")
	writeFile(t, root, "ignored", "secret")
	writeFile(t, root, "empty", "")
	writeFile(t, root, "large", strings.Repeat("line\n", 100000))
	outside := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(outside, []byte("must not read target"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	s := collectOK(t, root)
	if s.FileCount != 4 {
		t.Fatalf("ignored file counted: %+v", s)
	}
	if !findFile(t, s, "large", Untracked).Truncated || !s.Approximate {
		t.Fatal("missing truncation indication")
	}
	if findFile(t, s, "empty", Untracked).Added != 0 {
		t.Fatal("empty file counted as one line")
	}
	f := findFile(t, s, "link", Untracked)
	if strings.Contains(strings.Join(f.Lines, "\n"), "must not read target") {
		t.Fatal("followed symlink")
	}
}
func TestAllUntrackedFilesCountedPastPreviewLimit(t *testing.T) {
	root := repo(t)
	for i := 0; i < maxUntrackedFiles+2; i++ {
		writeFile(t, root, fmt.Sprintf("file%03d", i), "new\n")
	}
	s := collectOK(t, root)
	if s.FileCount != maxUntrackedFiles+2 || len(s.Files) != maxUntrackedFiles+2 || !s.Approximate {
		t.Fatalf("incomplete inventory: %+v", s)
	}
}
func TestMergeConflict(t *testing.T) {
	root := repo(t)
	writeFile(t, root, "conflict", "base\n")
	writeFile(t, root, "z-file", "before\n")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-qm", "initial")
	runGit(t, root, "checkout", "-qb", "other")
	writeFile(t, root, "conflict", "other\n")
	runGit(t, root, "commit", "-qam", "other")
	runGit(t, root, "checkout", "-qb", "main-test", "HEAD~1")
	writeFile(t, root, "conflict", "main\n")
	runGit(t, root, "commit", "-qam", "main")
	_ = exec.Command("git", "-C", root, "merge", "other").Run()
	writeFile(t, root, "z-file", "after\n")
	s := collectOK(t, root)
	f := findFile(t, s, "z-file", Unstaged)
	if !strings.Contains(strings.Join(f.Lines, "\n"), "+after") {
		t.Fatalf("patch associated with wrong path: %+v", s.Files)
	}
	found := false
	for _, f := range s.Files {
		if f.Path == "conflict" && f.Status == "U" {
			found = true
		}
	}
	if !found {
		t.Fatal("conflict status missing")
	}
}
