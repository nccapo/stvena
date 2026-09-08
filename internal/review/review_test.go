package review

import (
	"testing"

	"github.com/nccapo/stvena/internal/diffview"
)

func sample() diffview.Snapshot {
	return diffview.Snapshot{Files: []diffview.File{
		{Path: "alpha.go", Scope: diffview.Staged, Lines: []string{"@@ -1 +1 @@", "-before", "+after"}},
		{Path: "alpha.go", Scope: diffview.Unstaged, Lines: []string{"@@ -1 +1 @@", "-after", "+again"}},
		{Path: "目录/新.go", Scope: diffview.Untracked, Lines: []string{"@@ -0,0 +1 @@", "+new"}},
	}}
}

func TestNextUnreviewedSkipsMarksAndFindsChangedVersions(t *testing.T) {
	var s State
	s.Update(sample())
	s.Key(" ", 10)
	s.Key("N", 10)
	if s.Selected != 1 || s.Browser || !s.PatchFocused {
		t.Fatal("next unreviewed did not open the next file")
	}
	s.Key(" ", 10)
	s.Key("N", 10)
	if s.Selected != 2 {
		t.Fatal("skipped an unreviewed file")
	}
	s.Key(" ", 10)
	s.Key("N", 10)
	if s.Selected != 2 || s.Notice != "All files in this view are reviewed" {
		t.Fatal("completed review did not stay put")
	}
	next := sample()
	next.Files[0].Lines = []string{"@@ -1 +1 @@", "-before", "+updated"}
	s.Update(next)
	s.Key("N", 10)
	if s.Selected != 0 {
		t.Fatal("changed file did not re-enter the review queue")
	}
	s.Key("tab", 10) // staged only
	s.Key(" ", 10)
	s.Key("N", 10)
	if s.Notice != "All files in this view are reviewed" {
		t.Fatal("next unreviewed escaped the active scope")
	}
}
func TestFilterScopeAndSelectionSurviveRefresh(t *testing.T) {
	var s State
	s.Update(sample())
	s.Key("n", 2)
	key := s.Current().Key()
	snapshot := sample()
	snapshot.Files = append([]diffview.File{{Path: "aaa", Scope: diffview.Untracked}}, snapshot.Files...)
	s.Update(snapshot)
	if s.Current().Key() != key {
		t.Fatal("refresh moved selection to a different file")
	}
	s.Key("tab", 2)
	if len(s.Indices) != 1 || s.Current().Scope != diffview.Staged {
		t.Fatal("staged filter failed")
	}
	s.Key("tab", 2)
	s.Key("tab", 2)
	s.Key("/", 2)
	for _, r := range "目录" {
		s.Key(string(r), 2)
	}
	s.Key("enter", 2)
	if len(s.Indices) != 1 || s.Current().Path != "目录/新.go" {
		t.Fatal("Unicode filter failed")
	}
	s.Key("/", 2)
	s.Key("backspace", 2)
	s.Key("esc", 2)
	if s.Query != "目录" {
		t.Fatal("cancel did not restore filter")
	}
	s.Key("esc", 2)
	if s.Query != "" {
		t.Fatal("escape did not clear filter")
	}
}
func TestReviewInvalidatedOnlyForChangedFile(t *testing.T) {
	var s State
	s.Update(sample())
	s.Key(" ", 2)
	s.Key("n", 2)
	s.Key(" ", 2)
	if s.ReviewedCount() != 2 {
		t.Fatal("could not mark both scopes")
	}
	snapshot := sample()
	snapshot.Files[0].Lines = []string{"new content"}
	s.Update(snapshot)
	if s.ReviewedCount() != 1 || !s.Reviewed(snapshot.Files[1]) {
		t.Fatal("wrong review markers invalidated")
	}
	s.Update(sample())
	if s.ReviewedCount() != 1 {
		t.Fatal("reverted content incorrectly restored review")
	}
	s.Update(diffview.Snapshot{})
	if len(s.reviewed) != 0 || s.Current() != nil {
		t.Fatal("removed files retained review state")
	}
}
func TestHunksScrollingAndFileNavigation(t *testing.T) {
	var s State
	s.Update(diffview.Snapshot{Files: []diffview.File{{Path: "file", Lines: []string{"header", "@@ -1 +1 @@", "-a", "+b", "@@ -9 +9 @@", "-c", "+d"}}, {Path: "next"}}})
	s.Key("]", 2)
	if s.Scroll != 1 || !s.PatchFocused {
		t.Fatalf("first hunk: %+v", s)
	}
	s.Key("]", 2)
	if s.Scroll != 4 {
		t.Fatal("second hunk not reached")
	}
	s.Key("[", 2)
	if s.Scroll != 1 {
		t.Fatal("previous hunk not reached")
	}
	s.Key("G", 2)
	if s.Scroll != 6 {
		t.Fatal("end not clamped")
	}
	s.Key("right", 2)
	s.Key("n", 2)
	if s.Current().Path != "next" || s.Scroll != 0 || s.Horizontal != 0 {
		t.Fatal("file navigation did not reset viewport")
	}
}
func TestNumberLinesHandlesHeadersAndSourcePrefixes(t *testing.T) {
	lines := NumberLines([]string{"--- a/file", "+++ b/file", "@@ -3,2 +7,2 @@", "--- removed", "+++ added", " context", "\\ No newline at end of file", "@@ -0,0 +1 @@", "+new"})
	if lines[0].Old != 0 || lines[1].New != 0 {
		t.Fatal("file headers numbered as content")
	}
	if lines[3].Old != 3 || lines[3].New != 0 || lines[3].Kind != '-' {
		t.Fatalf("bad removed line: %+v", lines[3])
	}
	if lines[4].New != 7 || lines[4].Old != 0 || lines[4].Kind != '+' {
		t.Fatalf("bad added line: %+v", lines[4])
	}
	if lines[5].Old != 4 || lines[5].New != 8 || lines[6].Old != 0 || lines[8].New != 1 {
		t.Fatalf("incorrect line positions: %+v", lines)
	}
}

func TestIncompleteFilesCannotBeMarkedReviewed(t *testing.T) {
	for _, f := range []diffview.File{{Path: "large", Truncated: true}, {Path: "conflict", Status: "U"}} {
		var s State
		s.Update(diffview.Snapshot{Files: []diffview.File{f}})
		s.Key(" ", 3)
		if s.ReviewedCount() != 0 {
			t.Fatal("incomplete file marked reviewed")
		}
	}
}
func TestBinaryContentChangeClearsReview(t *testing.T) {
	var s State
	f := diffview.File{Path: "image", Binary: true, ContentHash: [32]byte{1}, Lines: []string{"Binary file"}}
	s.Update(diffview.Snapshot{Files: []diffview.File{f}})
	s.Key(" ", 3)
	f.ContentHash = [32]byte{2}
	s.Update(diffview.Snapshot{Files: []diffview.File{f}})
	if s.ReviewedCount() != 0 {
		t.Fatal("binary change retained review")
	}
}

func TestFullFileNavigationAndChangeMarkers(t *testing.T) {
	f := diffview.File{Path: "file", Scope: diffview.Unstaged, Lines: []string{"@@ -2,2 +2,1 @@", "-removed", " unchanged", "@@ -8 +7 @@", "-old", "+new"}}
	var s State
	s.Update(diffview.Snapshot{Files: []diffview.File{f, {Path: "next"}}})
	s.Key("f", 3)
	if !s.Browser {
		t.Fatal("f did not open browser")
	}
	s.Key("enter", 3)
	s.Key("v", 3)
	if s.Browser || !s.FullFile || !s.PatchFocused {
		t.Fatal("full view not opened")
	}
	s.ContentKey = f.Key()
	s.Content = diffview.Content{Lines: []string{"one", "unchanged", "three", "four", "five", "six", "new", "eight", "nine"}}
	lines := s.DisplayLines()
	if lines[1].Kind != '!' || lines[6].Kind != '+' || lines[0].New != 1 || lines[0].Kind != 0 {
		t.Fatalf("incorrect full-file marks: %+v", lines)
	}
	s.Key("]", 3)
	if s.Scroll != 1 {
		t.Fatal("deletion marker not reached")
	}
	s.Key("]", 3)
	if s.Scroll != 6 {
		t.Fatal("next change not reached")
	}
	s.Key("v", 3)
	if s.FullFile {
		t.Fatal("v did not return to diff")
	}
	s.Key("n", 3)
	if s.Current().Path != "next" {
		t.Fatal("file navigation failed from diff")
	}
}
