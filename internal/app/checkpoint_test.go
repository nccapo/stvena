package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/nccapo/stvena/internal/diffview"
)

func TestCheckpointActionsUseSessionCaptureAndProtectPin(t *testing.T) {
	s := pasteState(&bytes.Buffer{})
	s.sessionView = s.review.Snapshot
	s.sessionView.Finish()
	s.review.Source = "workspace"
	s.review.Key("K", 10)
	events, stop := make(chan any, 1), make(chan struct{})
	s.dispatch(context.Background(), events, stop)
	if s.review.Checkpoint == nil || s.review.Source != "session" || s.review.Snapshot.Version != s.sessionView.Version {
		t.Fatal("checkpoint did not start on captured session")
	}
	for _, request := range []string{"2", "3", "since-review", "problem-open"} {
		s.review.Request = request
		s.dispatch(context.Background(), events, stop)
		if !s.review.Pinned || s.review.Source != "session" || s.review.Snapshot.Version != s.sessionView.Version {
			t.Fatalf("%s replaced checkpoint", request)
		}
	}
}

func TestCheckpointHandoffPreparesOneLiteralDraft(t *testing.T) {
	for _, agent := range []string{"codex", "claude"} {
		t.Run(agent, func(t *testing.T) {
			var child bytes.Buffer
			child.WriteString("existing input")
			s := pasteState(&child)
			s.agentName = agent
			if err := s.review.StartCheckpoint(s.review.Snapshot); err != nil {
				t.Fatal(err)
			}
			s.review.Key(" ", 10)
			s.review.ContextQuestion = "Correct the edge case."
			s.review.Key("Z", 10)
			draft := s.review.Checkpoint.Draft
			events, stop := make(chan any, 1), make(chan struct{})
			s.handleInput([]byte("bAnd explain why"), &child)
			if s.review.Request != "paste-checkpoint" {
				t.Fatal("typing overwrote checkpoint handoff")
			}
			s.dispatch(context.Background(), events, stop)
			e := awaitPaste(t, events)
			s.workers.Wait()
			if e.err != nil {
				t.Fatal(e.err)
			}
			if want := "existing input" + ansi.BracketedPasteStart + draft + ansi.BracketedPasteEnd; child.String() != want {
				t.Fatalf("handoff submitted, lost or changed draft: %q", child.String())
			}
			s.finishAgentPaste(e.err)
			if s.diffFocused || !s.review.AgentDraft || !s.review.Pinned {
				t.Fatal("handoff lost draft focus or checkpoint pin")
			}
			queued := s.pasteInput
			s.pasteInput = nil
			s.handleInput(queued, &child)
			if !strings.HasSuffix(child.String(), ansi.BracketedPasteEnd+"And explain why") {
				t.Fatal("follow-up input lost")
			}
		})
	}
}

func TestStandaloneCheckpointExportsWithoutAgentHandoff(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "clipboard.txt")
	for _, name := range []string{"pbcopy", "wl-copy", "xclip", "xsel"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n/bin/cat > '"+output+"'\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
	var child bytes.Buffer
	s := pasteState(&child)
	s.exited = true
	if err := s.review.StartCheckpoint(s.review.Snapshot); err != nil {
		t.Fatal(err)
	}
	s.review.Key(" ", 10)
	s.review.Key("Z", 10)
	s.review.Key("b", 10)
	events, stop := make(chan any, 1), make(chan struct{})
	s.dispatch(context.Background(), events, stop)
	select {
	case e := <-events:
		operation, ok := e.(operationEvent)
		if !ok || operation.err != nil {
			t.Fatalf("export failed or attempted handoff: %+v", e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("export stalled")
	}
	s.workers.Wait()
	got, err := os.ReadFile(output)
	if err != nil || string(got) != s.review.Checkpoint.Draft || child.Len() != 0 || s.pastePending {
		t.Fatalf("standalone draft not exported: %v", err)
	}
}

func TestCheckpointIgnoresLateProblemNavigation(t *testing.T) {
	s := pasteState(&bytes.Buffer{})
	if err := s.review.StartCheckpoint(s.review.Snapshot); err != nil {
		t.Fatal(err)
	}
	s.review.Panel = "Problems"
	s.applyProblem(problemEvent{snapshot: diffview.Snapshot{Tree: "other"}})
	if s.review.Source != "session" || s.review.Snapshot.Tree != s.review.Checkpoint.Snapshot.Tree || !s.review.Pinned {
		t.Fatal("late problem result replaced checkpoint")
	}
}
