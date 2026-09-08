package app

import (
	"bytes"
	"errors"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/nccapo/stvena/internal/diffview"
	"github.com/nccapo/stvena/internal/ui"
)

func pasteState(writer io.Writer) *screenState {
	s := screenState{layout: ui.NewLayout(140, 40), ratio: 58, diffFocused: true, fullscreen: true, agentInput: writer, agentName: "claude", bracketedPaste: true}
	s.review.Update(diffview.Snapshot{Tree: strings.Repeat("a", 40), Files: []diffview.File{{Path: "file.go", Scope: diffview.Session, Lines: []string{"@@ -1 +1 @@", "-old()", "+new()"}}}})
	s.review.PatchFocused = true
	s.review.Scroll = 2
	return &s
}
func awaitPaste(t *testing.T, events <-chan any) agentPasteEvent {
	t.Helper()
	select {
	case e := <-events:
		return e.(agentPasteEvent)
	case <-time.After(3 * time.Second):
		t.Fatal("paste stalled")
		return agentPasteEvent{}
	}
}
func TestPasteTransfersDraftWithoutSubmittingOrClearingInput(t *testing.T) {
	var input bytes.Buffer
	input.WriteString("existing request")
	s := pasteState(&input)
	s.review.Snapshot.Root = t.TempDir()
	events := make(chan any, 1)
	stop := make(chan struct{})
	s.handleInput([]byte("bExplain this"), &input)
	if s.review.Request != "paste-agent" {
		t.Fatal("fast typing overwrote paste action")
	}
	s.pasteToAgent(events, stop)
	s.handleInput([]byte(" please"), &input)
	result := awaitPaste(t, events)
	s.workers.Wait()
	if result.err != nil {
		t.Fatal(result.err)
	}
	got := input.String()
	if !strings.Contains(got, "File path: "+strconv.Quote(filepath.Join(s.review.Snapshot.Root, "file.go"))) {
		t.Fatalf("paste lost the absolute file path: %q", got)
	}
	if !strings.HasPrefix(got, "existing request"+ansi.BracketedPasteStart) || !strings.HasSuffix(got, ansi.BracketedPasteEnd) {
		t.Fatalf("bad paste envelope: %q", got)
	}
	if strings.ContainsAny(got, "\r\x15") || strings.Count(got, ansi.BracketedPasteStart) != 1 || !strings.Contains(got, "+new()") {
		t.Fatalf("paste submitted, cleared input, or lost code: %q", got)
	}
	s.finishAgentPaste(result.err)
	if s.diffFocused || s.fullscreen {
		t.Fatal("did not return to native agent input")
	}
	queued := s.pasteInput
	s.pasteInput = nil
	s.handleInput(queued, &input)
	if !strings.HasSuffix(input.String(), ansi.BracketedPasteEnd+"Explain this please") {
		t.Fatalf("lost follow-up typing: %q", input.String())
	}
	s.handleInput([]byte{'\r'}, &input)
	if !strings.HasSuffix(input.String(), "please\r") {
		t.Fatal("user's Enter was not forwarded")
	}
}

func TestReturnToCodeAfterPastePreservesViewAndAllowsAnotherSlice(t *testing.T) {
	var input bytes.Buffer
	s := pasteState(&input)
	s.review.SelectWithMouse(2, 0, false)
	events := make(chan any, 1)
	stop := make(chan struct{})
	s.pasteToAgent(events, stop)
	s.finishAgentPaste(awaitPaste(t, events).err)
	s.workers.Wait()
	before := s.review
	s.handleInput([]byte{7}, &input)
	if !s.diffFocused || s.review.Browser || !s.review.PatchFocused ||
		s.review.Scroll != before.Scroll || s.review.Selected != before.Selected ||
		s.review.FullFile != before.FullFile || s.review.SelectionEnd != before.SelectionEnd ||
		!s.review.SelectionMouse || !s.review.Pinned {
		t.Fatal("returning from agent lost the open file, viewport or selection")
	}
	s.review.SelectWithMouse(1, 0, false)
	s.handleInput([]byte("b"), &input)
	s.pasteToAgent(events, stop)
	s.finishAgentPaste(awaitPaste(t, events).err)
	s.workers.Wait()
	if strings.Count(input.String(), ansi.BracketedPasteStart) != 2 || !strings.Contains(input.String(), "-old()") {
		t.Fatalf("second code slice did not reach the draft: %q", input.String())
	}
}
func TestPasteUnavailableDoesNotWriteToChild(t *testing.T) {
	for _, kind := range []string{"exited", "shell", "no-bracket-mode"} {
		t.Run(kind, func(t *testing.T) {
			var input bytes.Buffer
			s := pasteState(&input)
			switch kind {
			case "exited":
				s.exited = true
			case "shell":
				s.agentName = ""
			case "no-bracket-mode":
				s.bracketedPaste = false
			}
			s.pasteToAgent(make(chan any, 1), make(chan struct{}))
			if input.Len() != 0 || s.pastePending || !s.diffFocused || s.review.Notice == "" {
				t.Fatal("unavailable paste mutated child input or focus")
			}
		})
	}
}

type failedPaste struct{}

func (failedPaste) Write(p []byte) (int, error) { return len(p) / 2, errors.New("closed PTY") }
func TestPasteFailureKeepsSelectionAndDoesNotRetry(t *testing.T) {
	s := pasteState(failedPaste{})
	events := make(chan any, 1)
	s.pasteToAgent(events, make(chan struct{}))
	result := awaitPaste(t, events)
	s.workers.Wait()
	s.finishAgentPaste(result.err)
	if !s.diffFocused || s.pastePending || !strings.Contains(s.review.Notice, "incomplete") {
		t.Fatal("partial paste was hidden or retried")
	}
}
