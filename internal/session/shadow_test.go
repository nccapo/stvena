package session_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nccapo/stvena/internal/repo"
	"github.com/nccapo/stvena/internal/session"
)

func shadowWorkspace(t *testing.T) repo.Workspace {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	t.Setenv("STVENA_HOME", filepath.Join(home, "stvena"))
	root := t.TempDir()
	ws, _, err := repo.Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if ws.Git() {
		t.Fatal("a folder without Git was treated as a repository")
	}
	return ws
}

func TestCaptureWithoutARepository(t *testing.T) {
	ws := shadowWorkspace(t)
	write(t, ws.Root, "a.go", "package main\n")
	saved, err := session.Open(ws, false, "")
	if err != nil {
		t.Fatal(err)
	}
	defer saved.Close()
	if saved.Baseline == "" {
		t.Fatal("no baseline was captured")
	}
	// The durable anchor is what the workspace view compares against, so it has
	// to exist from the first capture and outlive the session.
	if saved.Base() != saved.Baseline {
		t.Fatalf("anchor %q is not the first capture %q", saved.Base(), saved.Baseline)
	}
	write(t, ws.Root, "a.go", "package main\n\nfunc main() {}\n")
	next, err := saved.Capture()
	if err != nil {
		t.Fatal(err)
	}
	if next == saved.Baseline {
		t.Fatal("an edited working tree captured the same version")
	}
	// Nothing may appear in the user's folder.
	if _, err := os.Stat(filepath.Join(ws.Root, ".git")); !os.IsNotExist(err) {
		t.Fatal("capturing created a Git repository in the project")
	}
	entries, err := os.ReadDir(ws.Root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "a.go" {
		t.Fatalf("the project gained files: %v", entries)
	}
}

// The anchor is written once. A second session in the same folder still
// compares against the version Stvena first saw, the way a repository compares
// against HEAD rather than against the moment the editor opened.
func TestTheAnchorSurvivesASecondSession(t *testing.T) {
	ws := shadowWorkspace(t)
	write(t, ws.Root, "a.go", "one\n")
	first, err := session.Open(ws, false, "")
	if err != nil {
		t.Fatal(err)
	}
	anchor := first.Base()
	first.Close()

	write(t, ws.Root, "a.go", "two\n")
	second, err := session.Open(ws, false, "")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if second.Base() != anchor {
		t.Fatalf("anchor moved from %q to %q", anchor, second.Base())
	}
	if second.Baseline == anchor {
		t.Fatal("the session baseline did not advance with the new session")
	}
}

// Stvena's own excludes bound the first capture of a folder that has no
// .gitignore of its own, so a dependency directory is not hashed every tick.
func TestDefaultExcludesKeepBuildDirectoriesOut(t *testing.T) {
	ws := shadowWorkspace(t)
	if err := os.MkdirAll(filepath.Join(ws.Root, "node_modules", "pkg"), 0700); err != nil {
		t.Fatal(err)
	}
	write(t, ws.Root, filepath.Join("node_modules", "pkg", "index.js"), "module.exports = {}\n")
	write(t, ws.Root, "a.go", "package main\n")
	saved, err := session.Open(ws, false, "")
	if err != nil {
		t.Fatal(err)
	}
	defer saved.Close()
	listed, err := listTree(ws, saved.Baseline)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(listed, "node_modules") {
		t.Fatalf("the capture included a dependency directory: %s", listed)
	}
	if !strings.Contains(listed, "a.go") {
		t.Fatalf("the capture missed the project source: %s", listed)
	}
}

func listTree(ws repo.Workspace, tree string) (string, error) {
	out, err := exec.Command("git", append(ws.Args(), "ls-tree", "-r", "--name-only", tree)...).Output()
	return string(out), err
}
