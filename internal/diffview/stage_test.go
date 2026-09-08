package diffview

import (
	"github.com/nccapo/stvena/internal/session"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func stageGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s %v", args, out, err)
	}
	return string(out)
}
func TestStageHunkAndRejectChangedVersion(t *testing.T) {
	root := t.TempDir()
	stageGit(t, root, "init")
	before := []string{"one", "two", "3", "4", "5", "6", "7", "8", "9", "ten"}
	path := filepath.Join(root, "file.txt")
	if err := os.WriteFile(path, []byte(strings.Join(before, "\n")+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	stageGit(t, root, "add", ".")
	stageGit(t, root, "commit", "-m", "initial")
	after := append([]string{}, before...)
	after[0] = "ONE"
	after[9] = "TEN"
	data := []byte(strings.Join(after, "\n") + "\n")
	os.WriteFile(path, data, 0644)
	s := Collect(root)
	if s.Err != nil || len(s.Files) != 1 {
		t.Fatalf("collect: %+v", s)
	}
	f := s.Files[0]
	if err := Stage(root, f, 0); err != nil {
		t.Fatal(err)
	}
	index := stageGit(t, root, "show", ":file.txt")
	if !strings.Contains(index, "ONE") || strings.Contains(index, "TEN") {
		t.Fatalf("wrong staged hunk: %s", index)
	}
	work, _ := os.ReadFile(path)
	if string(work) != string(data) {
		t.Fatal("staging changed worktree")
	}
	if err := Stage(root, f, 1); err == nil {
		t.Fatal("accepted stale patch")
	}
	s = Collect(root)
	var staged File
	for _, f := range s.Files {
		if f.Scope == Staged {
			staged = f
		}
	}
	if err := Stage(root, staged, -1); err != nil {
		t.Fatal(err)
	}
	if got := stageGit(t, root, "diff", "--cached"); got != "" {
		t.Fatalf("unstage left patch: %s", got)
	}
	s = Collect(root)
	f = s.Files[0]
	os.WriteFile(path, []byte("changed again\n"), 0644)
	if err := Stage(root, f, -1); err == nil {
		t.Fatal("staged an unseen version")
	}
	if got := stageGit(t, root, "diff", "--cached"); got != "" {
		t.Fatal("failed validation mutated index")
	}
}

func TestStageCapturedNewBinaryWithUnusualPath(t *testing.T) {
	root := t.TempDir()
	stageGit(t, root, "init")
	name := "new\nimage.bin"
	data := []byte{1, 0, 2, 3}
	if err := os.WriteFile(filepath.Join(root, name), data, 0644); err != nil {
		t.Fatal(err)
	}
	captured, err := session.Open(root, false, "")
	if err != nil {
		t.Fatal(err)
	}
	defer captured.Close()
	view := Collect(root)
	view.FreezeContent(captured.Baseline)
	if len(view.Files) != 1 || !view.Files[0].Binary {
		t.Fatalf("missing binary: %+v", view)
	}
	if err = Stage(root, view.Files[0], -1); err != nil {
		t.Fatal(err)
	}
	got := stageGit(t, root, "show", ":"+name)
	if got != string(data) {
		t.Fatal("staging did not use captured bytes")
	}
	if err = Stage(root, view.Files[0], -1); err == nil {
		t.Fatal("stale untracked action overwrote existing index entry")
	}
	live, _ := os.ReadFile(filepath.Join(root, name))
	if string(live) != string(data) {
		t.Fatal("new-file staging changed working copy")
	}
}
