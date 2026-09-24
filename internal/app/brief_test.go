package app

import (
	"strings"
	"testing"
	"time"

	"github.com/nccapo/stvena/internal/checks"
	"github.com/nccapo/stvena/internal/review"
)

func TestOpenBriefEvidenceKinds(t *testing.T) {
	s := mouseState(t, false, false)
	r := &s.review
	r.Brief = []review.BriefItem{{Text: "outcome", Evidence: []review.Evidence{
		{Kind: "code", Path: "file.go", Start: 2, Revision: "old"},
		{Kind: "check", Command: "go test", Status: "passed", CheckTree: "old", RanAt: time.Now()},
		{Kind: "comment", CommentID: "c1"},
	}}}
	r.Panel = "Brief"
	r.BriefIndex = 1
	s.openBriefEvidence()
	if r.Panel != "" || r.Browser || !r.PatchFocused || r.Selected != 0 || r.Scroll == 0 || !strings.Contains(r.Notice, "stale") {
		t.Fatalf("code navigation: %+v", r)
	}
	r.Panel, r.BriefIndex, r.Notice = "Brief", 2, ""
	s.openBriefEvidence()
	if r.Panel != "Checks" || !strings.Contains(r.Notice, "go test") {
		t.Fatal("check navigation failed")
	}
	r.Panel, r.BriefIndex, r.Notice = "Brief", 3, ""
	s.openBriefEvidence()
	if r.Panel != "Comments" || !strings.Contains(r.Notice, "deleted") {
		t.Fatal("deleted comment notice missing")
	}
	r.Comments = []review.Comment{{ID: "c1", Text: "note"}}
	r.Panel, r.Notice = "Brief", ""
	s.openBriefEvidence()
	if r.Panel != "Comments" || !strings.Contains(r.Notice, "note") {
		t.Fatal("comment navigation failed")
	}
	r.Brief[0].Evidence[0].Path = "missing.go"
	r.Panel, r.BriefIndex, r.Notice = "Brief", 1, ""
	s.openBriefEvidence()
	if r.Panel != "Brief" || !strings.Contains(r.Notice, "3: Project files") {
		t.Fatal("missing code path changed view")
	}
}

func TestSetCheckPreservesManualBriefState(t *testing.T) {
	s := mouseState(t, false, false)
	s.review.Brief = []review.BriefItem{{Text: "passes"}}
	finished := time.Now().UTC()
	s.setCheck(checks.Result{Command: "go test", Status: "passed", Tree: s.review.Snapshot.Tree, FinishedAt: finished})
	if !s.review.CheckFinishedAt.Equal(finished) {
		t.Fatal("finish time lost")
	}
	if err := s.review.AttachCheckEvidence(0); err != nil {
		t.Fatal(err)
	}
	if s.review.Brief[0].Done {
		t.Fatal("passing check marked brief")
	}
}

func TestBriefMouseRows(t *testing.T) {
	s := mouseState(t, false, false)
	s.review.Panel = "Brief"
	s.review.Brief = []review.BriefItem{{Text: "one", Evidence: []review.Evidence{{Kind: "check", Command: "go test", Label: "go test"}}}, {Text: "two"}}
	feedMouse(t, s, mousePacket(s, 0, 10, 3, 'M'))
	if s.review.BriefIndex != 2 || s.review.Panel != "Brief" {
		t.Fatal("item row click selected wrong row")
	}
	feedMouse(t, s, mousePacket(s, 0, 10, 2, 'M'))
	if s.review.BriefIndex != 1 || s.review.Request != "brief-open" {
		t.Fatal("evidence click did not open")
	}
	s.review.Panel = "Brief comments"
	s.review.Comments = []review.Comment{{ID: "c1", Text: "note"}}
	feedMouse(t, s, mousePacket(s, 0, 10, 1, 'M'))
	if s.review.Panel != "Brief" || len(s.review.Brief[0].Evidence) != 2 {
		t.Fatal("comment click did not attach")
	}
}
