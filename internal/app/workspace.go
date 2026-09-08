package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
	"github.com/nccapo/stvena/internal/checks"
	"github.com/nccapo/stvena/internal/diffview"
	"github.com/nccapo/stvena/internal/session"
	"github.com/nccapo/stvena/internal/ui"
)

func watchSnapshots(root string, rootErr error, saved *session.Session, events chan<- any, stop <-chan struct{}) {
	if saved == nil {
		watchDiff(root, rootErr, events, stop)
		return
	}
	var project diffview.Snapshot
	refresh := func() {
		tree, err := saved.Capture()
		workspace := diffview.Collect(root)
		workspace.Label = "Workspace"
		view := diffview.Snapshot{Root: root, Err: err, Label: "This session", UpdatedAt: time.Now()}
		if err == nil {
			view = diffview.CompareTrees(root, saved.Baseline, tree)
			view.Branch = workspace.Branch
			// A second capture detects writes during collection. Don't associate a live
			// patch with unrelated immutable file contents.
			after, e := saved.Capture()
			if e == nil && after == tree {
				workspace.FreezeContent(tree)
			} else {
				workspace.Err = fmt.Errorf("working copy changed during refresh; waiting for a stable view")
			}
		}
		if err == nil && (project.Tree != tree || project.Err != nil) {
			project = diffview.Project(root, tree)
		}
		deliveredProject := project
		if err != nil {
			deliveredProject.Err = fmt.Errorf("Project capture failed: %w", err)
		}
		select {
		case events <- diffEvent{snapshot: workspace, session: view, project: deliveredProject}:
		case <-stop:
		}
	}
	refresh()
	ticker := time.NewTicker(diffRefresh)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			refresh()
		case <-stop:
			return
		}
	}
}
func (s *screenState) updateSource() {
	if s.review.Source == "project" {
		project := s.projectView
		if project.Tree == "" && project.Err == nil {
			project.Root = s.root
			project.Err = fmt.Errorf("Waiting for project capture; if Git was initialized after launch, restart stvena")
		}
		s.review.Update(project)
	} else if s.review.Source == "session" && s.session != nil {
		s.review.Update(s.sessionView)
	} else {
		s.review.Update(s.workspace)
	}
}
func (s *screenState) relayout(width, height int) {
	s.layout = ui.NewLayoutOptions(width, height, s.ratio, s.fullscreen)
}
func (s *screenState) resizePTY(v *vt.Emulator, p *os.File) {
	// Keep the agent's last usable size while review occupies the full screen.
	if s.fullscreen {
		return
	}
	if v.Width() != s.layout.LeftWidth || v.Height() != s.layout.LeftHeight {
		v.Resize(s.layout.LeftWidth, s.layout.LeftHeight)
		if p != nil {
			_ = pty.Setsize(p, &pty.Winsize{Cols: uint16(s.layout.LeftWidth), Rows: uint16(s.layout.LeftHeight)})
		}
	}
}
func (s *screenState) setCheck(r checks.Result) {
	s.review.CheckCommand = r.Command
	s.review.Problems = r.Problems
	if r.Problems == nil {
		s.review.Problems = checks.ParseProblems(r.Output, "")
	}
	s.review.ProblemIndex = 0
	s.review.CheckSourceChanged = r.SourceChanged
	s.review.CheckTree = r.Tree
	s.review.CheckStatus = r.Status
	if r.SourceChanged {
		s.review.CheckStatus += " · command changed source"
	}
	s.review.LastCheck = r.Command
	s.review.CheckLines = strings.Split(r.Command+"\nSnapshot: "+r.Tree+"\n"+s.review.CheckStatus+fmt.Sprintf(" (exit %d)", r.ExitCode)+" · "+r.FinishedAt.Format(time.RFC3339)+"\n\n"+r.Output, "\n")
}
func (s *screenState) dispatch(ctx context.Context, events chan<- any, stop <-chan struct{}) bool {
	r := s.review.Request
	s.review.Request = ""
	send := func(message string, err error) {
		select {
		case events <- operationEvent{message: message, err: err}:
		case <-stop:
		}
	}
	switch r {
	case "quit-app":
		return true
	case "paste-agent", "paste-context":
		s.pasteToAgent(events, stop)
		return false
	case "quit":
		if s.exited {
			return true
		}
		s.review.Notice = "Agent is still running · Ctrl-Q closes stvena and stops the agent"
	case "fullscreen":
		s.fullscreen = !s.fullscreen
		s.relayout(s.layout.Width, s.layout.Height)
	case "+", "-":
		if r == "+" {
			s.ratio = max(25, s.ratio-5)
		} else {
			s.ratio = min(75, s.ratio+5)
		}
		s.relayout(s.layout.Width, s.layout.Height)
	case "1", "2", "3":
		if r == "1" && s.session == nil {
			s.review.Notice = "No session baseline available"
			break
		}
		s.review.Pinned = false
		s.review.Scope = 0
		s.review.ClearSelection()
		s.review.TargetLine = 0
		s.review.Query = ""
		s.review.Panel = ""
		s.review.Source = map[string]string{"1": "session", "2": "workspace", "3": "project"}[r]
		s.review.FullFile = r == "3"
		s.review.Browser, s.review.PatchFocused = true, false
		s.updateSource()
		s.review.Notice = ""
	case "copy", "path", "feedback", "copy-context":
		value := ""
		if r == "copy-context" {
			var err error
			value, err = s.review.ContextMessage()
			if err != nil {
				s.review.Notice = err.Error()
				break
			}
		}
		if r == "copy" {
			value = s.review.SelectedText()
		}
		if r == "path" {
			if f := s.review.Current(); f != nil {
				value = filepath.Join(s.root, f.Path)
			}
		}
		if r == "feedback" && len(s.review.Comments) > 0 {
			value = s.review.ExportFeedback()
		}
		if value == "" {
			s.review.Notice = "Nothing selected to copy"
			break
		}
		s.workers.Add(1)
		go func() { defer s.workers.Done(); send("Copied to clipboard", copyText(value)) }()
	case "editor":
		f := s.review.Current()
		if f == nil {
			break
		}
		path := filepath.Join(s.root, f.Path)
		n := 1
		lines := s.review.DisplayLines()
		if s.review.Scroll < len(lines) {
			n = max(1, lines[s.review.Scroll].New)
		}
		s.workers.Add(1)
		go func() { defer s.workers.Done(); send("Opened working file in editor", openEditor(path, n)) }()
	case "since-review":
		f := s.review.Current()
		if f == nil {
			break
		}
		previous, ok := s.review.History[f.Key()]
		if !ok {
			s.review.Notice = "Mark a version reviewed first"
			break
		}
		compared, err := diffview.CompareFileVersions(s.root, previous.File, *f)
		if err != nil {
			s.review.Notice = err.Error()
			break
		}
		snapshot := s.review.Snapshot
		snapshot.Files = []diffview.File{compared}
		snapshot.Label = "Since last review"
		snapshot.Finish()
		latest := s.review.Latest
		s.review.Pinned = false
		s.review.Scope = 0
		s.review.Update(snapshot)
		s.review.Latest = latest
		s.review.Pinned = true
		s.review.PinnedVersion = latest.Version
		s.review.FullFile = false
		s.review.Browser = false
		s.review.PatchFocused = true
		s.review.Scroll = 0
		s.review.Notice = "Changes since last review · P returns to live"
	case "problem-open", "problem-add":
		s.loadProblem(r == "problem-add", events, stop)
	case "check":
		if s.review.CheckRunning {
			s.review.Notice = "A check is already running"
			break
		}
		tree := s.review.Snapshot.Tree
		if tree == "" {
			s.review.Notice = "Wait for a captured Git snapshot"
			break
		}
		if err := session.Retain(s.root, tree); err != nil {
			s.review.Notice = err.Error()
			break
		}
		command := s.review.LastCheck
		s.review.CheckCommand = command
		s.review.CheckRunning = true
		s.review.CheckTree = tree
		s.review.CheckStatus = "Running"
		s.review.Problems = nil
		s.review.CheckLines = []string{command, "Preparing captured code…"}
		s.review.Panel = "Checks"
		s.workers.Add(1)
		go func() {
			defer s.workers.Done()
			checkCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
			defer cancel()
			result := checks.Run(checkCtx, s.root, tree, command)
			select {
			case events <- checkEvent{result}:
			case <-stop:
			}
		}()
	case "stage-file", "stage-hunk":
		f := s.review.Current()
		if f == nil {
			break
		}
		if f.Scope == diffview.ProjectScope {
			s.review.Notice = "Open Workspace (2) to stage changes"
			break
		}
		if s.review.Source == "session" || f.Scope == diffview.Session {
			s.review.Notice = "Press 2 for Workspace, then stage the file or hunk"
			break
		}
		if s.busy {
			s.review.Notice = "Wait for the current Git action"
			break
		}
		if s.review.Pinned {
			s.review.Notice = "Resume live updates (P) before staging"
			break
		}
		if s.review.Snapshot.Err != nil {
			s.review.Notice = "Wait for a successful refresh before staging"
			break
		}
		copy := *f
		s.pendingStage = &copy
		s.pendingHunk = -1
		if r == "stage-hunk" {
			s.pendingHunk = s.review.CurrentHunk()
			if s.pendingHunk < 0 {
				s.review.Notice = "Select a diff hunk first"
				break
			}
		}
		verb := "Stage"
		if f.Scope == diffview.Staged {
			verb = "Unstage"
		}
		what := "file"
		if s.pendingHunk >= 0 {
			what = "hunk"
		}
		s.review.ConfirmAction = r
		s.review.ConfirmDetail = fmt.Sprintf("%s %s: %s? Working files are kept.", verb, what, f.Path)
	case "confirm:stage-file", "confirm:stage-hunk":
		if s.pendingStage != nil && !s.busy {
			f, h := *s.pendingStage, s.pendingHunk
			s.pendingStage = nil
			s.busy = true
			s.workers.Add(1)
			go func() {
				defer s.workers.Done()
				err := diffview.Stage(s.root, f, h)
				select {
				case events <- operationEvent{message: "Index updated · refreshing changes", err: err, git: true}:
				case <-stop:
				}
			}()
		}
	}
	if r != "" {
		if err := s.review.Save(); err != nil {
			s.review.Notice = "Save review: " + err.Error()
		}
	}
	return false
}
func copyText(text string) error {
	var options [][]string
	if runtime.GOOS == "darwin" {
		options = [][]string{{"pbcopy"}}
	} else {
		options = [][]string{{"wl-copy"}, {"xclip", "-selection", "clipboard"}, {"xsel", "--clipboard", "--input"}}
	}
	for _, args := range options {
		if path, err := exec.LookPath(args[0]); err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, path, args[1:]...)
			cmd.Stdin = strings.NewReader(text)
			return cmd.Run()
		}
	}
	return fmt.Errorf("clipboard unavailable; install wl-copy or xclip")
}
func openEditor(path string, line int) error {
	if _, err := os.Lstat(path); err != nil {
		return fmt.Errorf("working file unavailable: %w", err)
	}
	// GUI editors can open without taking over the raw terminal or stopping CLI output.
	for _, name := range []string{"code", "cursor", "zed"} {
		if executable, err := exec.LookPath(name); err == nil {
			args := []string{"--goto", fmt.Sprintf("%s:%d", path, line)}
			if name == "zed" {
				args = []string{fmt.Sprintf("%s:%d", path, line)}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return exec.CommandContext(ctx, executable, args...).Run()
		}
	}
	return fmt.Errorf("install the code, cursor or zed shell command to open at a line; Y copies the path")
}
