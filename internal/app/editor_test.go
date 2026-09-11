package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nccapo/stvena/internal/diffview"
	"github.com/nccapo/stvena/internal/editor"
)

func TestEditorRequestsOpenStvenaAndCollectCapturedContext(t *testing.T) {
	root, baseline := workflowProject(t)
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package main\nfunc changed() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", root, "add", ".").CombinedOutput(); err != nil {
		t.Fatalf("git add: %s: %v", out, err)
	}
	out, err := exec.Command("git", "-C", root, "write-tree").Output()
	if err != nil {
		t.Fatal(err)
	}
	tree := strings.TrimSpace(string(out))
	s := screenState{root: root}
	s.sessionView = diffview.CompareTrees(root, baseline, tree)
	s.workspace = diffview.Collect(root)
	s.projectView = diffview.Project(root, tree)
	if err := s.review.Load(root); err != nil {
		t.Fatal(err)
	}
	s.applyEditorRequest(editor.Request{Action: "review", Path: "a.go", Line: 2, EndLine: 2})
	if s.review.Source != "session" || s.review.Current() == nil || s.review.Current().Path != "a.go" || s.review.Browser || !s.diffFocused {
		t.Fatalf("editor review did not open the TUI patch: %+v", s.review)
	}
	s.applyEditorRequest(editor.Request{Action: "context", Path: "b.go", Line: 2, EndLine: 2})
	if len(s.review.Attachments) != 1 || !strings.Contains(s.review.Attachments[0].Message, "func beta()") {
		t.Fatalf("editor context did not use captured project source: %+v", s.review.Attachments)
	}
}
