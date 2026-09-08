package app

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/nccapo/stvena/internal/checks"
	"github.com/nccapo/stvena/internal/diffview"
	"github.com/nccapo/stvena/internal/review"
	"github.com/nccapo/stvena/internal/ui"
)

func mouseState(t *testing.T, split, wrap bool) *screenState {
	t.Helper()
	s := &screenState{layout: ui.NewLayoutOptions(120, 30, 58, true), diffFocused: true, fullscreen: true, ratio: 58}
	f := diffview.File{Path: "file.go", Scope: diffview.Session, Lines: []string{"@@ -1,3 +1,3 @@", "-oldOne()", "-oldTwo()", "+newOne()", "+newTwo()", " context()"}}
	s.review.Update(diffview.Snapshot{Tree: strings.Repeat("a", 40), Files: []diffview.File{f}})
	s.review.SideBySide = split
	s.review.Wrap = wrap
	s.review.PatchFocused = true
	if !split {
		s.review.FullFile = true
		s.review.ContentKey = f.Key()
		s.review.Content = diffview.Content{Lines: []string{"first()", "second()", "third()", "fourth()", "fifth()"}}
	}
	return s
}
func mousePacket(s *screenState, button, x, row int, final byte) string {
	return fmt.Sprintf("\x1b[<%d;%d;%d%c", button, s.layout.DiffX+x+1, s.layout.DiffY+row+1, final)
}
func feedMouse(t *testing.T, s *screenState, packet string) {
	t.Helper()
	var child bytes.Buffer
	for _, b := range []byte(packet) {
		s.handleInput([]byte{b}, &child)
	}
	if child.Len() != 0 {
		t.Fatal("review mouse input leaked into CLI")
	}
}
func TestMouseDragSelectsWithoutMovingViewport(t *testing.T) {
	s := mouseState(t, false, false)
	// A single file occupies row 2; code begins at review row 5.
	feedMouse(t, s, mousePacket(s, 0, 20, 6, 'M'))
	feedMouse(t, s, mousePacket(s, 32, 20, 8, 'M'))
	feedMouse(t, s, mousePacket(s, 0, 20, 8, 'm'))
	if s.mouseDragging || s.review.Scroll != 0 || !s.review.Pinned {
		t.Fatal("drag jumped viewport, stayed active, or failed to pin")
	}
	if got := s.review.SelectedText(); got != "second()\nthird()\nfourth()" {
		t.Fatalf("selected wrong lines: %q", got)
	}
	message, err := s.review.SelectionMessage()
	if err != nil || !strings.Contains(message, "New lines: 2-4") || strings.Contains(message, "first()") {
		t.Fatalf("bad paste selection: %s %v", message, err)
	}
	// Refreshes cannot replace the code being dragged/reviewed.
	newer := s.review.Snapshot
	newer.Tree = strings.Repeat("b", 40)
	s.review.Update(newer)
	if s.review.Snapshot.Tree == newer.Tree {
		t.Fatal("live update changed selected version")
	}
	feedMouse(t, s, mousePacket(s, 32, 20, 5, 'M'))
	if s.review.SelectedText() != "second()\nthird()\nfourth()" {
		t.Fatal("hover after release changed selection")
	}
	var child bytes.Buffer
	s.handleInput([]byte("b"), &child)
	if s.review.Request != "paste-agent" {
		t.Fatal("b did not hand off mouse selection")
	}
}
func TestMouseReverseDragAndOutsideRelease(t *testing.T) {
	s := mouseState(t, false, false)
	feedMouse(t, s, mousePacket(s, 0, 20, 8, 'M'))
	feedMouse(t, s, mousePacket(s, 32, 20, 6, 'M'))
	feedMouse(t, s, mousePacket(s, 0, 20, s.layout.DiffHeight, 'm'))
	if got := s.review.SelectedText(); got != "second()\nthird()\nfourth()" {
		t.Fatalf("reverse selection: %q", got)
	}
	if s.mouseDragging || s.review.Request != "" {
		t.Fatal("outside release activated a control or left drag active")
	}
}
func TestSplitDragKeepsStartingSide(t *testing.T) {
	for _, side := range []struct {
		x            int
		want, absent string
	}{{15, "oldOne()\noldTwo()\ncontext()", "+newOne()"}, {85, "newOne()\nnewTwo()\ncontext()", "-oldOne()"}} {
		s := mouseState(t, true, false)
		s.review.Snapshot.Files[0].BeforeOID = strings.Repeat("1", 40)
		s.review.Snapshot.Files[0].AfterOID = strings.Repeat("2", 40)
		// Row 5 is hunk metadata. Rows 6-8 pair replacements and context.
		feedMouse(t, s, mousePacket(s, 0, side.x, 6, 'M'))
		feedMouse(t, s, mousePacket(s, 32, 120-side.x, 8, 'M'))
		feedMouse(t, s, mousePacket(s, 0, 120-side.x, 8, 'm'))
		if got := s.review.SelectedText(); got != side.want {
			t.Fatalf("side selection: %q", got)
		}
		message, err := s.review.SelectionMessage()
		if err != nil || strings.Contains(message, side.absent) {
			t.Fatalf("included opposite side: %s %v", message, err)
		}
		wantObject := strings.Repeat("2", 40)
		if side.x < 60 {
			wantObject = strings.Repeat("1", 40)
		}
		if !strings.Contains(message, "File object: "+wantObject) {
			t.Fatal("wrong selected-side object in draft")
		}
		if err = s.review.AddComment("Review these lines"); err != nil {
			t.Fatal(err)
		}
		if s.review.Comments[0].Old != (side.x < 60) {
			t.Fatal("comment anchored to wrong side")
		}
	}
}
func TestMouseIgnoresHeadersAndPreservesSelectionOnWheel(t *testing.T) {
	s := mouseState(t, false, false)
	feedMouse(t, s, mousePacket(s, 0, 20, 4, 'M'))
	if s.review.Selecting {
		t.Fatal("header started a source selection")
	}
	feedMouse(t, s, mousePacket(s, 0, 20, 6, 'M'))
	feedMouse(t, s, mousePacket(s, 0, 20, 6, 'm'))
	feedMouse(t, s, mousePacket(s, 65, 20, 8, 'M'))
	if s.review.Scroll != 1 || s.review.SelectedText() != "second()" {
		t.Fatal("wheel changed the selected code instead of scrolling")
	}
	s.review.Key("V", 10)
	if s.review.Selecting || s.review.SelectionMouse || s.review.SelectionSide != 0 {
		t.Fatal("V did not clear mouse selection")
	}
}

func TestShiftClickExtendsMouseRange(t *testing.T) {
	s := mouseState(t, false, false)
	feedMouse(t, s, mousePacket(s, 0, 20, 6, 'M'))
	feedMouse(t, s, mousePacket(s, 0, 20, 6, 'm'))
	feedMouse(t, s, mousePacket(s, 4, 20, 8, 'M'))
	feedMouse(t, s, mousePacket(s, 4, 20, 8, 'm'))
	if s.review.SelectedText() != "second()\nthird()\nfourth()" || s.review.Scroll != 0 {
		t.Fatal("Shift-click lost the anchor or moved the viewport")
	}
}

func TestMouseViewTabsAndSelectionActions(t *testing.T) {
	s := mouseState(t, false, false)
	// View tabs occupy row 3 with one file. Clicking an active tab is a no-op.
	s.review.Scroll = 1
	feedMouse(t, s, mousePacket(s, 0, 16, 3, 'M'))
	if !s.review.FullFile || s.review.Scroll != 1 {
		t.Fatal("active full-file tab reset the view")
	}
	feedMouse(t, s, mousePacket(s, 0, 4, 3, 'M'))
	if s.review.FullFile {
		t.Fatal("Changes tab did not open the diff")
	}
	feedMouse(t, s, mousePacket(s, 0, 16, 3, 'M'))
	if !s.review.FullFile {
		t.Fatal("Full file tab did not restore full-file mode")
	}
	s.review.Scroll = 0
	feedMouse(t, s, mousePacket(s, 0, 20, 6, 'M'))
	feedMouse(t, s, mousePacket(s, 32, 20, 7, 'M'))
	feedMouse(t, s, mousePacket(s, 0, 20, 7, 'm'))
	selected := s.review.SelectedText()
	feedMouse(t, s, mousePacket(s, 0, 17, s.layout.DiffHeight-1, 'M'))
	if s.review.Request != "paste-agent" || s.review.SelectedText() != selected {
		t.Fatal("Add to agent button lost or failed to route the selection")
	}
}

func TestContextWheelAndProblemClicksUseOverlayNotCode(t *testing.T) {
	s := mouseState(t, false, false)
	s.review.SelectWithMouse(1, 0, false)
	s.review.Attachments = []review.Attachment{{Label: "first", Message: "first"}, {Label: "second", Message: "second"}}
	s.review.Panel = "Context"
	s.review.Notice = ""
	feedMouse(t, s, mousePacket(s, 65, 20, 8, 'M'))
	if s.review.TrayIndex != 1 || s.review.Scroll != 0 {
		t.Fatal("context wheel scrolled source selection")
	}
	s.review.Panel = "Problems"
	s.review.Problems = []checks.Problem{{Path: "first.go", Line: 2}, {Path: "second.go", Line: 3}}
	feedMouse(t, s, mousePacket(s, 0, 10, 2, 'M'))
	if s.review.ProblemIndex != 1 || s.review.Request != "problem-open" {
		t.Fatal("click did not open the displayed problem")
	}
}

func TestMouseProjectFoldersToggleAndFilesOpen(t *testing.T) {
	s := screenState{layout: ui.NewLayout(140, 35), diffFocused: true}
	s.review.Source = "project"
	s.review.Browser = true
	s.review.FullFile = true
	s.review.Update(diffview.Snapshot{Tree: "captured", Files: []diffview.File{{Path: "src/nested/code.go", Scope: diffview.ProjectScope}, {Path: "root.go", Scope: diffview.ProjectScope}}})
	feedMouse(t, &s, mousePacket(&s, 0, 7, 2, 'M')) // src
	if !s.review.Browser || !s.review.ExpandedFolders["src"] {
		t.Fatal("folder click opened a source view")
	}
	feedMouse(t, &s, mousePacket(&s, 0, 9, 3, 'M')) // nested
	if !s.review.ExpandedFolders["src/nested"] {
		t.Fatal("nested folder did not expand")
	}
	feedMouse(t, &s, mousePacket(&s, 0, 12, 4, 'M')) // code.go
	if s.review.Browser || s.review.Current().Path != "src/nested/code.go" {
		t.Fatal("clicked wrong nested file")
	}
	s.review.ContentKey = s.review.Current().Key()
	s.review.Content = diffview.Content{Lines: []string{"first source line", "second source line"}}
	fileRows, _ := ui.ReviewSize(s.layout.DiffHeight, s.review.FileListCount())
	feedMouse(t, &s, mousePacket(&s, 0, 25, fileRows+5, 'M'))
	feedMouse(t, &s, mousePacket(&s, 0, 25, fileRows+5, 'm'))
	if s.review.SelectedText() != "second source line" {
		t.Fatal("folder rows displaced the code selection target")
	}
	s.review.Key("f", 10)
	feedMouse(t, &s, mousePacket(&s, 0, 7, 2, 'M'))
	if len(s.review.ProjectRows) != 2 || s.review.ExpandedFolders["src"] {
		t.Fatal("folder did not collapse back to root list")
	}
}
