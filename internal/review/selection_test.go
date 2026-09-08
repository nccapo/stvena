package review

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/nccapo/stvena/internal/diffview"
)

func selectionState() State {
	var s State
	s.Update(diffview.Snapshot{Tree: strings.Repeat("a", 40), Files: []diffview.File{{Path: "目录/a.go", OldPath: "old.go", Scope: diffview.Session, Lines: []string{"@@ -5,2 +8,2 @@", "-old()", "+new()", " context()"}}}})
	s.PatchFocused = true
	s.Scroll = 1
	s.SelectionStart = 3
	s.Selecting = true
	return s
}
func TestSelectionMessagePreservesCodeSidesAndAnchors(t *testing.T) {
	s := selectionState()
	s.Snapshot.Root = filepath.Join(t.TempDir(), "project with spaces")
	message, err := s.SelectionMessage()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"File path: " + strconv.Quote(filepath.Join(s.Snapshot.Root, "目录/a.go")), "Previous path: \"old.go\"", "Old lines: 5-6", "New lines: 8-9", "```diff\n-old()\n+new()\n context()\n```", s.Snapshot.Tree} {
		if !strings.Contains(message, want) {
			t.Errorf("missing %q in %s", want, message)
		}
	}

}
func TestFullFileSelectionAndPasteControlEscaping(t *testing.T) {
	s := selectionState()
	s.FullFile = true
	s.ContentKey = s.Current().Key()
	s.Content = diffview.Content{Lines: []string{"untouched", "```\x1b[201~\r\x07", "世界\tcode"}}
	s.Scroll = 2
	s.SelectionStart = 1
	message, err := s.SelectionMessage()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"New lines: 2-3", "````\n```�[201~��\n世界\tcode\n````"} {
		if !strings.Contains(message, want) {
			t.Errorf("missing %q in %q", want, message)
		}
	}
	if strings.ContainsAny(message, "\x1b\r\x07") || strings.Contains(message, "untouched") {
		t.Fatal("sent terminal controls or unselected source")
	}
	s.ContentLoading = true
	if _, err = s.SelectionMessage(); err == nil {
		t.Fatal("sent a loading placeholder")
	}
}
func TestSelectionRejectsHeadersBrowserAndOversize(t *testing.T) {
	s := selectionState()
	s.Selecting = false
	s.Scroll = 0
	if _, err := s.SelectionMessage(); err == nil {
		t.Fatal("header accepted as code")
	}
	s.Scroll = 1
	s.Browser = true
	if _, err := s.SelectionMessage(); err == nil {
		t.Fatal("browser accepted as code selection")
	}
	s.Browser = false
	s.Snapshot.Files[0].Lines[1] = "-" + strings.Repeat("x", 33<<10)
	if _, err := s.SelectionMessage(); err == nil {
		t.Fatal("unbounded paste accepted")
	}
}
