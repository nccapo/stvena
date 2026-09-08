package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"testing"

	"github.com/charmbracelet/x/vt"
	"github.com/nccapo/stvena/internal/diffview"
	"github.com/nccapo/stvena/internal/ui"
)

func TestVersion(t *testing.T) {
	oldStdout := os.Stdout
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = write
	t.Cleanup(func() { os.Stdout = oldStdout })

	oldVersion := Version
	Version = "1.2.3"
	t.Cleanup(func() { Version = oldVersion })
	if err := Run([]string{"--version"}); err != nil {
		t.Fatal(err)
	}
	if err := write.Close(); err != nil {
		t.Fatal(err)
	}
	output, err := io.ReadAll(read)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(output), "stvena 1.2.3\n"; got != want {
		t.Fatalf("version output = %q, want %q", got, want)
	}
}

func TestQuitAllWorksFromEitherPaneAndDuringPaste(t *testing.T) {
	for _, mode := range []string{"agent", "review", "paste", "exited"} {
		t.Run(mode, func(t *testing.T) {
			s := screenState{layout: ui.NewLayout(140, 40), diffFocused: mode == "review", pastePending: mode == "paste", exited: mode == "exited"}
			s.review.Prompt = "comment"
			var child bytes.Buffer
			s.handleInput([]byte("\x11ignored"), &child)
			if !s.dispatch(context.Background(), make(chan any, 1), make(chan struct{})) {
				t.Fatal("Ctrl-Q did not close the app")
			}
			if child.Len() != 0 || len(s.pasteInput) != 0 {
				t.Fatal("quit input leaked to the agent or pending paste")
			}
		})
	}
}

func TestInputRoutingAndFragmentedKeys(t *testing.T) {
	s := screenState{layout: ui.NewLayout(140, 40)}
	s.review.Update(diffview.Snapshot{Files: []diffview.File{{Path: "first"}, {Path: "second"}}})
	var child bytes.Buffer
	raw := "hello\x1b[A\t\x03"
	s.handleInput([]byte(raw), &child)
	if child.String() != raw {
		t.Fatalf("agent input altered: %q", child.String())
	}
	s.handleInput([]byte{7}, &child)
	for _, b := range []byte("\x1b[B") {
		s.handleInput([]byte{b}, &child)
	}
	if s.review.Selected != 1 {
		t.Fatal("fragmented arrow did not select next file")
	}
	s.handleInput([]byte("/"), &child)
	for _, b := range []byte("目录") {
		s.handleInput([]byte{b}, &child)
	}
	if s.review.Query != "目录" {
		t.Fatalf("fragmented UTF-8 corrupted: %q", s.review.Query)
	}
	s.handleInput([]byte{27}, &child)
	s.decodeInput(true)
	if s.review.Searching || s.review.Query != "" {
		t.Fatal("standalone Escape did not cancel search")
	}
	s.handleInput([]byte{7, 'x'}, &child)
	if s.diffFocused || child.String() != raw+"x" {
		t.Fatal("focus switch leaked review input to child")
	}
}

func TestContentRequestsFollowSelectionWithoutBlocking(t *testing.T) {
	s := screenState{layout: ui.NewLayout(140, 40)}
	s.review.Update(diffview.Snapshot{Root: t.TempDir(), Files: []diffview.File{{Path: "first"}, {Path: "second"}}})
	requests := make(chan contentRequest, 1)
	var child bytes.Buffer
	s.handleInput([]byte{7}, &child)
	if !s.review.Browser {
		t.Fatal("review focus did not open file picker")
	}
	s.handleInput([]byte("v"), &child)
	s.queueContent(requests)
	first := <-requests
	if first.file.Path != "first" || !s.review.ContentLoading {
		t.Fatal("full file not requested")
	}
	// Refreshing while the same file is loading must not perpetually supersede
	// a slower request; switching files must supersede it immediately.
	s.queueContent(requests)
	if len(requests) != 0 {
		t.Fatal("duplicated in-flight load")
	}
	s.handleInput([]byte("n"), &child)
	s.queueContent(requests)
	second := <-requests
	if second.file.Path != "second" || second.id <= first.id {
		t.Fatal("new selection did not supersede old request")
	}
}

func TestTerminalRepliesAndShutdown(t *testing.T) {
	virtual := vt.NewEmulator(80, 24)
	var replies bytes.Buffer
	stop := forwardTerminalReplies(virtual, &replies)
	for i := 0; i < 10; i++ {
		_, _ = virtual.Write([]byte("\x1b[6n"))
	}
	stop()
	if !bytes.Contains(replies.Bytes(), []byte("\x1b[1;1R")) {
		t.Fatalf("terminal query not answered: %q", replies.String())
	}
}

func TestReviewMouseOpensClickedFile(t *testing.T) {
	s := screenState{layout: ui.NewLayout(140, 40), diffFocused: true}
	s.review.Browser = true
	s.review.Update(diffview.Snapshot{Files: []diffview.File{{Path: "one"}, {Path: "two"}}})
	var child bytes.Buffer
	// Second file is the fourth review row (one-based physical coordinates).
	input := fmt.Sprintf("\x1b[<0;%d;%dM", s.layout.DiffX+2, s.layout.DiffY+4)
	for _, b := range []byte(input) {
		s.handleInput([]byte{b}, &child)
	}
	if s.review.Current().Path != "two" || s.review.Browser || !s.review.PatchFocused {
		t.Fatalf("mouse failed to open selected file: %+v", s.review)
	}
	if child.Len() != 0 {
		t.Fatal("review mouse leaked into agent")
	}
}
