package app

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/nccapo/stvena/internal/checks"
	"github.com/nccapo/stvena/internal/diffview"
	"github.com/nccapo/stvena/internal/session"
	"github.com/nccapo/stvena/internal/ui"
)

func workflowProject(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	cmd := exec.Command("git", "init", "-q", root)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("init: %s %v", out, err)
	}
	for name, body := range map[string]string{"a.go": "package main\nfunc alpha() {}\n", "b.go": "package main\nfunc beta() {}\n", ".gitignore": "ignored.txt\n", "ignored.txt": "private\n"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	saved, err := session.Open(root, false, "")
	if err != nil {
		t.Fatal(err)
	}
	defer saved.Close()
	tree, err := saved.Capture()
	if err != nil {
		t.Fatal(err)
	}
	return root, tree
}

func TestProjectToMultiFileContextToAgentDraft(t *testing.T) {
	root, tree := workflowProject(t)
	var child bytes.Buffer
	s := screenState{root: root, layout: ui.NewLayout(160, 40), ratio: 58, diffFocused: true, agentName: "claude", agentInput: &child, bracketedPaste: true}
	s.projectView = diffview.Project(root, tree)
	s.review.LiveTree = tree
	for _, f := range s.projectView.Files {
		if f.Path == "ignored.txt" {
			t.Fatal("ignored file in explorer")
		}
	}
	events, stop := make(chan any, 4), make(chan struct{})
	ctx := context.Background()
	s.handleInput([]byte("3"), &child)
	s.dispatch(ctx, events, stop)
	if s.review.Source != "project" || !s.review.FullFile || !s.review.Browser {
		t.Fatal("Files did not open the project browser")
	}
	for _, name := range []string{"a.go", "b.go"} {
		s.review.Key("f", 10)
		s.review.Key("/", 10)
		for s.review.Query != "" {
			s.review.Key("backspace", 10)
		}
		for _, r := range name {
			s.review.Key(string(r), 10)
		}
		s.review.Key("enter", 10)
		s.review.Key("enter", 10)
		f := s.review.Current()
		if f == nil || f.Path != name {
			t.Fatalf("quick open %s: %+v", name, f)
		}
		s.review.Content = diffview.LoadContent(root, *f)
		s.review.ContentKey = f.Key()
		s.review.ContentLoading = false
		s.review.SelectWithMouse(1, 0, false)
		s.review.Key("x", 10)
		s.dispatch(ctx, events, stop)
	}
	if len(s.review.Attachments) != 2 {
		t.Fatalf("lost attachments: %s", s.review.Notice)
	}
	s.review.Key("B", 10)
	s.review.Key("i", 10)
	for _, r := range "Explain both functions" {
		s.review.Key(string(r), 10)
	}
	s.review.Key("enter", 10)
	s.dispatch(ctx, events, stop)
	s.review.Key("enter", 10)
	if s.review.Panel != "Context preview" {
		t.Fatal("missing draft preview")
	}
	s.handleInput([]byte("b and compare them"), &child)
	s.dispatch(ctx, events, stop)
	result := awaitPaste(t, events)
	s.workers.Wait()
	s.finishAgentPaste(result.err)
	if result.err != nil {
		t.Fatal(result.err)
	}
	got := child.String()
	for _, want := range []string{filepath.Join(root, "a.go"), filepath.Join(root, "b.go"), "alpha()", "beta()", "Explain both functions"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in draft", want)
		}
	}
	if strings.Count(got, ansi.BracketedPasteStart) != 1 || !strings.HasSuffix(got, ansi.BracketedPasteEnd) || strings.Contains(got, "\r") {
		t.Fatal("draft was submitted or not literal")
	}
	if len(s.review.Attachments) != 2 || len(s.pasteInput) == 0 {
		t.Fatal("paste lost recoverable draft or buffered typing")
	}
}

func awaitProblem(t *testing.T, events <-chan any) problemEvent {
	t.Helper()
	select {
	case e := <-events:
		return e.(problemEvent)
	case <-time.After(5 * time.Second):
		t.Fatal("problem load stalled")
		return problemEvent{}
	}
}

func TestProblemsOpenTestedSourceAndCollectFailure(t *testing.T) {
	root, tree := workflowProject(t)
	s := screenState{root: root, projectView: diffview.Project(root, tree)}
	s.review.LiveTree = tree
	result := checks.Run(context.Background(), root, tree, "printf 'a.go:2:3: expected alpha to pass\\n'; exit 1")
	if len(result.Problems) != 1 {
		t.Fatalf("check locations: %+v", result)
	}
	s.setCheck(result)
	// Today's source is different; the diagnostic must still open tested code.
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("different live source\n"), 0600); err != nil {
		t.Fatal(err)
	}
	events, stop := make(chan any, 2), make(chan struct{})
	s.review.Panel = "Problems"
	s.review.LastCheck = "a different command draft"
	s.loadProblem(true, events, stop)
	e := awaitProblem(t, events)
	s.workers.Wait()
	s.applyProblem(e)
	if len(s.review.Attachments) != 1 {
		t.Fatalf("failure not collected: %s", s.review.Notice)
	}
	message := s.review.Attachments[0].Message
	if !strings.Contains(message, "alpha()") || !strings.Contains(message, result.Command) || strings.Contains(message, "different live source") || strings.Contains(message, s.review.LastCheck) {
		t.Fatalf("wrong failure context: %s", message)
	}
	s.loadProblem(false, events, stop)
	e = awaitProblem(t, events)
	s.workers.Wait()
	s.applyProblem(e)
	if !s.review.Pinned || s.review.Snapshot.Tree != tree || s.review.TargetLine != 2 || s.review.Current().Path != "a.go" {
		t.Fatal("failure did not open pinned source at the reported line")
	}
	s.review.Panel = "Problems"
	s.review.Problems = []checks.Problem{{Path: "generated.go", Line: 9, Message: "generated failure"}}
	s.loadProblem(true, events, stop)
	e = awaitProblem(t, events)
	s.workers.Wait()
	s.applyProblem(e)
	if len(s.review.Attachments) != 2 || !strings.Contains(s.review.Attachments[1].Message, "Source unavailable") {
		t.Fatal("generated failure could not be collected")
	}
}

func TestProblemPathResolutionRejectsAmbiguousBasenames(t *testing.T) {
	files := []diffview.File{{Path: "one/file.go"}, {Path: "two/file.go"}}
	if _, err := resolveProblem(files, "file.go"); err == nil {
		t.Fatal("guessed the wrong package")
	}
	if f, err := resolveProblem(files, "two/file.go"); err != nil || f.Path != "two/file.go" {
		t.Fatal("exact path did not resolve")
	}
}
