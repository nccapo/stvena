package session_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nccapo/stvena/internal/diffview"
	"github.com/nccapo/stvena/internal/session"
)

func git(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s %v", args, out, err)
	}
	return strings.TrimSpace(string(out))
}
func write(t *testing.T, root, path, text string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, path), []byte(text), 0644); err != nil {
		t.Fatal(err)
	}
}
func TestSessionSeparatesExistingEditsAndSurvivesCommit(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	write(t, root, "file.txt", "committed\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "initial")
	write(t, root, "file.txt", "staged\n")
	git(t, root, "add", ".")
	write(t, root, "file.txt", "preexisting\n")
	write(t, root, "new.txt", "existing new\n")
	indexPath := filepath.Join(root, ".git", "index")
	before, _ := os.ReadFile(indexPath)
	s, err := session.Open(root, false, "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	after, _ := os.ReadFile(indexPath)
	if !bytes.Equal(before, after) {
		t.Fatal("baseline capture changed user's staging index")
	}
	tree, err := s.Capture()
	if err != nil {
		t.Fatal(err)
	}
	initial := diffview.CompareTrees(root, s.Baseline, tree)
	if initial.Err != nil || len(initial.Files) != 0 {
		t.Fatalf("preexisting edits leaked: %+v", initial)
	}
	write(t, root, "file.txt", "agent change\n")
	tree, err = s.Capture()
	if err != nil {
		t.Fatal(err)
	}
	after, _ = os.ReadFile(indexPath)
	if !bytes.Equal(before, after) {
		t.Fatal("refresh changed index")
	}
	changed := diffview.CompareTrees(root, s.Baseline, tree)
	if changed.Err != nil || len(changed.Files) != 1 {
		t.Fatalf("session diff: %+v", changed)
	}
	patch := strings.Join(changed.Files[0].Lines, "\n")
	if !strings.Contains(patch, "-preexisting") || !strings.Contains(patch, "+agent change") {
		t.Fatal(patch)
	}
	frozen := diffview.LoadContent(root, changed.Files[0])
	write(t, root, "file.txt", "later\n")
	if c := diffview.LoadContent(root, changed.Files[0]); strings.Join(c.Lines, "") == "later" || c.Err != nil {
		t.Fatalf("snapshot changed: %+v", c)
	}
	if strings.Join(frozen.Lines, "") != "agent change" {
		t.Fatalf("wrong captured content: %+v", frozen)
	}
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "agent")
	tree, err = s.Capture()
	if err != nil {
		t.Fatal(err)
	}
	post := diffview.CompareTrees(root, s.Baseline, tree)
	if len(post.Files) != 1 {
		t.Fatalf("agent commit hid session diff: %+v", post)
	}
	reopened, err := session.Open(root, true, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if reopened.Baseline != s.Baseline {
		t.Fatal("lost baseline on reopen")
	}
	delta, err := diffview.CompareFileVersions(root, changed.Files[0], post.Files[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(delta.Lines, "\n"), "+later") {
		t.Fatalf("since-review delta missing: %+v", delta)
	}
}
func TestUnbornSession(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	write(t, root, "before", "existing\n")
	s, err := session.Open(root, false, "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	write(t, root, "after", "new\n")
	tree, err := s.Capture()
	if err != nil {
		t.Fatal(err)
	}
	view := diffview.CompareTrees(root, s.Baseline, tree)
	if view.Err != nil || len(view.Files) != 1 || view.Files[0].Path != "after" {
		t.Fatalf("unborn: %+v", view)
	}
}

func TestCapturePreservesRacyIndexTimestamp(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	// Emulate filesystems/Git builds with coarse stat comparisons. A same-size
	// write at the index timestamp must force Git to compare the actual bytes.
	git(t, root, "config", "core.trustctime", "false")
	git(t, root, "config", "core.checkStat", "minimal")
	stamp := time.Now().Add(-5 * time.Second).Truncate(time.Second)
	path := filepath.Join(root, "file.txt")
	write(t, root, "file.txt", "before\n")
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "initial")
	index := filepath.Join(root, ".git", "index")
	if err := os.Chtimes(index, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	s, err := session.Open(root, false, "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	write(t, root, "file.txt", "after!\n")
	if err = os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	tree, err := s.Capture()
	if err != nil {
		t.Fatal(err)
	}
	if got := git(t, root, "show", tree+":file.txt"); got != "after!" {
		t.Fatalf("same-size edit missed: %q", got)
	}
	info, err := os.Stat(index)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(stamp) {
		t.Fatal("capture changed the real index timestamp")
	}
}
