package diffview

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nccapo/stvena/internal/session"
)

func revertRepo(t *testing.T, lines []string) (root, path string) {
	t.Helper()
	root = t.TempDir()
	stageGit(t, root, "init")
	path = filepath.Join(root, "file.txt")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	stageGit(t, root, "add", ".")
	stageGit(t, root, "commit", "-m", "initial")
	return root, path
}

func write(t *testing.T, path string, lines []string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func only(t *testing.T, root string, scope Scope) File {
	t.Helper()
	s := Collect(root)
	if s.Err != nil {
		t.Fatalf("collect: %v", s.Err)
	}
	for _, f := range s.Files {
		if f.Scope == scope {
			return f
		}
	}
	t.Fatalf("no %s file in %+v", scope, s.Files)
	return File{}
}

func TestRevertHunkKeepsOtherHunks(t *testing.T) {
	before := []string{"one", "two", "3", "4", "5", "6", "7", "8", "9", "ten"}
	root, path := revertRepo(t, before)
	after := append([]string{}, before...)
	after[0], after[9] = "ONE", "TEN"
	write(t, path, after)

	f := only(t, root, Unstaged)
	notice, err := Revert(root, []Target{{File: f, Hunks: []int{0}}})
	if err != nil {
		t.Fatalf("revert first hunk: %v", err)
	}
	if notice != "" {
		t.Fatalf("unexpected notice: %s", notice)
	}
	got := read(t, path)
	if strings.Contains(got, "ONE") {
		t.Fatalf("rejected hunk survived: %s", got)
	}
	if !strings.Contains(got, "TEN") {
		t.Fatalf("revert removed an accepted hunk: %s", got)
	}
}

func TestRevertWholeFileAndBatchIsAtomic(t *testing.T) {
	before := []string{"alpha", "beta"}
	root, path := revertRepo(t, before)
	second := filepath.Join(root, "other.txt")
	write(t, second, []string{"kept"})
	stageGit(t, root, "add", "other.txt")
	stageGit(t, root, "commit", "-m", "second")

	write(t, path, []string{"ALPHA", "beta"})
	write(t, second, []string{"CHANGED"})

	s := Collect(root)
	var targets []Target
	for _, f := range s.Files {
		if f.Scope == Unstaged {
			targets = append(targets, Target{File: f})
		}
	}
	if len(targets) != 2 {
		t.Fatalf("expected two changed files, got %d", len(targets))
	}
	// One stale target must abandon the whole batch, leaving both files alone.
	stale := targets[0]
	write(t, path, []string{"moved on", "beta"})
	if _, err := Revert(root, []Target{{File: stale.File}, targets[1]}); err == nil {
		t.Fatal("reverted a stale batch")
	}
	if got := read(t, second); !strings.Contains(got, "CHANGED") {
		t.Fatalf("failed batch reverted a file anyway: %s", got)
	}

	// The same batch succeeds once every target matches the working tree.
	s = Collect(root)
	targets = nil
	for _, f := range s.Files {
		if f.Scope == Unstaged {
			targets = append(targets, Target{File: f})
		}
	}
	if _, err := Revert(root, targets); err != nil {
		t.Fatalf("revert batch: %v", err)
	}
	if got := read(t, second); !strings.Contains(got, "kept") {
		t.Fatalf("second file not reverted: %s", got)
	}
	if got := read(t, path); !strings.Contains(got, "alpha") || strings.Contains(got, "moved on") {
		t.Fatalf("first file not reverted: %s", got)
	}
}

func TestRevertRefusesChangedLines(t *testing.T) {
	before := []string{"one", "two", "three"}
	root, path := revertRepo(t, before)
	write(t, path, []string{"ONE", "two", "three"})
	f := only(t, root, Unstaged)

	// The agent edits the same region again before the rejection is applied.
	write(t, path, []string{"ONE AGAIN", "two", "three"})
	if _, err := Revert(root, []Target{{File: f}}); err == nil {
		t.Fatal("force-applied a stale patch")
	}
	if got := read(t, path); !strings.Contains(got, "ONE AGAIN") {
		t.Fatalf("refused revert still wrote: %s", got)
	}
}

func TestRevertDeletesCreatedFileAndRestoresDeletedFile(t *testing.T) {
	root, path := revertRepo(t, []string{"kept"})
	created := filepath.Join(root, "new.txt")
	write(t, created, []string{"agent wrote this"})

	captured, err := session.Open(root, false, "")
	if err != nil {
		t.Fatal(err)
	}
	defer captured.Close()
	view := Collect(root)
	view.FreezeContent(captured.Baseline)
	var f File
	for _, candidate := range view.Files {
		if candidate.Scope == Untracked {
			f = candidate
		}
	}
	if f.ContentRef == "" {
		t.Fatalf("no captured untracked file: %+v", view.Files)
	}
	if _, err := Revert(root, []Target{{File: f}}); err != nil {
		t.Fatalf("revert created file: %v", err)
	}
	if _, err := os.Stat(created); !os.IsNotExist(err) {
		t.Fatalf("created file survived rejection: %v", err)
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	deleted := only(t, root, Unstaged)
	if deleted.Status != "D" {
		t.Fatalf("expected a deletion, got %q", deleted.Status)
	}
	if _, err := Revert(root, []Target{{File: deleted}}); err != nil {
		t.Fatalf("revert deletion: %v", err)
	}
	if got := read(t, path); !strings.Contains(got, "kept") {
		t.Fatalf("deleted file not restored: %s", got)
	}
}

func TestRevertReportsStagedCopyLeftBehind(t *testing.T) {
	before := []string{"one", "two", "three"}
	root, path := revertRepo(t, before)
	write(t, path, []string{"ONE", "two", "three"})
	stageGit(t, root, "add", "file.txt")
	// A further working-tree edit makes the index and working tree disagree, so
	// the combined --index apply cannot succeed.
	write(t, path, []string{"ONE", "two", "THREE"})

	var unstaged File
	for _, f := range Collect(root).Files {
		if f.Scope == Unstaged {
			unstaged = f
		}
	}
	notice, err := Revert(root, []Target{{File: unstaged}})
	if err != nil {
		t.Fatalf("revert: %v", err)
	}
	if !strings.Contains(notice, "file.txt") {
		t.Fatalf("no staged-copy notice: %q", notice)
	}
	if got := read(t, path); strings.Contains(got, "THREE") {
		t.Fatalf("working tree not reverted: %s", got)
	}
}

func TestRevertRefusesUnsupportedScopes(t *testing.T) {
	root, _ := revertRepo(t, []string{"one"})
	for _, scope := range []Scope{ProjectScope, BranchScope} {
		if _, err := Revert(root, []Target{{File: File{Path: "file.txt", Scope: scope, Status: "M"}}}); err == nil {
			t.Fatalf("accepted %s scope", scope)
		}
	}
	conflicted := File{Path: "file.txt", Scope: Unstaged, Status: "U"}
	if _, err := Revert(root, []Target{{File: conflicted}}); err == nil {
		t.Fatal("accepted a conflicted file")
	}
	if _, err := Revert(root, nil); err == nil {
		t.Fatal("accepted an empty batch")
	}
}
