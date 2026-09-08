package review

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nccapo/stvena/internal/diffview"
)

func TestContextCollectsDistinctSlicesAndKeepsTheirVersions(t *testing.T) {
	s := selectionState()
	s.Snapshot.Root = "/project"
	if err := s.CollectSelection(); err != nil {
		t.Fatal(err)
	}
	if err := s.CollectSelection(); err == nil {
		t.Fatal("duplicate selection accepted")
	}
	original := s.Attachments[0].Message
	s.Snapshot.Files[0].Path = "other.go"
	s.Snapshot.Files[0].Lines[2] = "+different()"
	if err := s.CollectSelection(); err != nil {
		t.Fatal(err)
	}
	if s.Attachments[0].Message != original {
		t.Fatal("navigation mutated earlier attachment")
	}
	s.ContextQuestion = "Explain both\x1b[201~\r"
	s.LiveTree = strings.Repeat("b", 40)
	message, err := s.ContextMessage()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"目录/a.go", "other.go", "different()", "earlier workspace snapshot", "My request:", "Explain both"} {
		if !strings.Contains(message, want) {
			t.Errorf("missing %s", want)
		}
	}
	if strings.ContainsAny(message, "\x1b\r") {
		t.Fatal("context can escape literal paste")
	}
	s.Panel = "Context"
	s.TrayIndex = 0
	s.Key("d", 10)
	if len(s.Attachments) != 1 || !strings.Contains(s.Attachments[0].Message, "other.go") {
		t.Fatal("removed the wrong item")
	}
	before := len(s.Attachments)
	if err := s.AddAttachment(Attachment{Message: strings.Repeat("x", 33<<10)}); err == nil || len(s.Attachments) != before {
		t.Fatal("oversize add damaged tray")
	}
}

func TestContextDraftPersistsAndProjectDoesNotEraseReviewMarks(t *testing.T) {
	var s State
	s.Update(sample())
	s.Key(" ", 10)
	before := s.reviewed[s.Current().Key()]
	s.Source = "project"
	s.Update(diffview.Snapshot{Files: []diffview.File{{Path: "other.go", Scope: diffview.ProjectScope}}})
	if s.reviewed[sample().Files[0].Key()] != before {
		t.Fatal("project browsing erased review marks")
	}
	s.Key(" ", 10)
	if s.Reviewed(*s.Current()) {
		t.Fatal("project file marked reviewed")
	}
	s.savePath = filepath.Join(t.TempDir(), "reviews.json")
	s.Attachments = []Attachment{{ID: "one", Label: "one", Message: "saved code"}}
	s.ContextQuestion = "Explain this"
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	// Read persisted fields independently; no repository or Git mutation required.
	data, err := os.ReadFile(s.savePath)
	if err != nil {
		t.Fatal(err)
	}
	var saved Saved
	if err = json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.Attachments) != 1 || saved.ContextQuestion != "Explain this" {
		t.Fatal("draft was not saved")
	}
}
