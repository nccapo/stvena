package diffview

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nccapo/stvena/internal/repo"
	"github.com/nccapo/stvena/internal/session"
)

// shadowRepo is a project Stvena snapshots privately, with one captured
// version already recorded.
func shadowRepo(t *testing.T) (repo.Workspace, *session.Session) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	t.Setenv("STVENA_HOME", filepath.Join(home, "stvena"))
	root := t.TempDir()
	for name, body := range map[string]string{"keep.txt": "one\ntwo\n", "gone.txt": "bye\n"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ws, _, err := repo.Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := session.Open(ws, false, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(saved.Close)
	return ws, saved
}

func TestCollectAllListsEverySinceTheAnchor(t *testing.T) {
	ws, saved := shadowRepo(t)
	if err := os.WriteFile(filepath.Join(ws.Root, "keep.txt"), []byte("one\nTWO\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws.Root, "added.txt"), []byte("new\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(ws.Root, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	tree, err := saved.Capture()
	if err != nil {
		t.Fatal(err)
	}
	s := CollectAll(ws, saved.Base(), tree)
	if s.Err != nil {
		t.Fatal(s.Err)
	}
	if s.FileCount != 3 {
		t.Fatalf("workspace view listed %d files: %+v", s.FileCount, s.Files)
	}
	want := map[string]string{"keep.txt": "M", "added.txt": "A", "gone.txt": "D"}
	for _, f := range s.Files {
		if f.Scope != Unstaged {
			// Review state is keyed by scope, so a project that later gains Git
			// must find the marks it already has.
			t.Fatalf("%s carried scope %q instead of the workspace scope", f.Path, f.Scope)
		}
		if want[f.Path] != f.Status {
			t.Fatalf("%s: status %q, wanted %q", f.Path, f.Status, want[f.Path])
		}
		delete(want, f.Path)
	}
	if len(want) != 0 {
		t.Fatalf("workspace view missed %v", want)
	}
}

func TestSessionAndProjectViewsWorkWithoutARepository(t *testing.T) {
	ws, saved := shadowRepo(t)
	if err := os.WriteFile(filepath.Join(ws.Root, "keep.txt"), []byte("one\nTWO\n"), 0600); err != nil {
		t.Fatal(err)
	}
	tree, err := saved.Capture()
	if err != nil {
		t.Fatal(err)
	}
	view := CompareTrees(ws, saved.Baseline, tree)
	if view.Err != nil || view.FileCount != 1 {
		t.Fatalf("session view: %+v", view)
	}
	project := Project(ws, tree)
	if project.Err != nil || project.FileCount != 2 {
		t.Fatalf("project view: %+v", project)
	}
	content := LoadContent(ws, findFile(t, view, "keep.txt", Session))
	if content.Err != nil {
		t.Fatal(content.Err)
	}
	if len(content.Lines) != 2 || content.Lines[1] != "TWO" {
		t.Fatalf("captured source: %+v", content.Lines)
	}
}

func TestRejectingAChangeWithoutAnIndex(t *testing.T) {
	ws, saved := shadowRepo(t)
	path := filepath.Join(ws.Root, "keep.txt")
	if err := os.WriteFile(path, []byte("one\nTWO\n"), 0600); err != nil {
		t.Fatal(err)
	}
	tree, err := saved.Capture()
	if err != nil {
		t.Fatal(err)
	}
	s := CollectAll(ws, saved.Base(), tree)
	notice, err := Revert(ws, []Target{{File: findFile(t, s, "keep.txt", Unstaged)}})
	if err != nil {
		t.Fatal(err)
	}
	// There is no index, so nothing can be left staged and nothing should be
	// claimed about one.
	if notice != "" {
		t.Fatalf("reject reported index state that does not exist: %q", notice)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "one\ntwo\n" {
		t.Fatalf("working file after reject: %q", data)
	}
}

func TestGitOnlyViewsExplainThemselves(t *testing.T) {
	ws, saved := shadowRepo(t)
	tree, err := saved.Capture()
	if err != nil {
		t.Fatal(err)
	}
	branch := BranchChanges(ws, tree)
	if branch.Err == nil {
		t.Fatal("branch comparison ran without a repository")
	}
	// The default-branch message would send the user after a branch that could
	// not help; this has to name the real reason.
	if got := branch.Err.Error(); got != "branch changes need a Git repository; run git init, then restart stvena" {
		t.Fatalf("branch error: %s", got)
	}
	if head := BranchHead(ws); head != "" {
		t.Fatalf("branch head without a repository: %q", head)
	}
	s := CollectAll(ws, saved.Base(), tree)
	err = Stage(ws, File{Path: "keep.txt", Scope: Unstaged, Status: "M"}, -1)
	if err == nil {
		t.Fatal("staging ran without an index")
	}
	if s.Branch != "" {
		t.Fatalf("workspace view named a branch: %q", s.Branch)
	}
}
