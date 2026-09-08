package review

import (
	"github.com/nccapo/stvena/internal/diffview"
	"testing"
)

func treeState() State {
	s := State{Source: "project", Browser: true, FullFile: true}
	snapshot := diffview.Snapshot{Tree: "captured"}
	for _, name := range []string{"README.md", "src/z.go", "src/auth/login.go", "src/auth/session.go", "tests/login_test.go"} {
		snapshot.Files = append(snapshot.Files, diffview.File{Path: name, Scope: diffview.ProjectScope})
	}
	s.Update(snapshot)
	return s
}

func TestProjectTreeFoldersNavigationAndRefresh(t *testing.T) {
	s := treeState()
	if len(s.ProjectRows) != 3 || s.ProjectRows[0].Path != "src" || s.ProjectRows[1].Path != "tests" || s.ProjectRows[2].Path != "README.md" {
		t.Fatalf("folders not grouped first: %+v", s.ProjectRows)
	}
	if s.Current() != nil {
		t.Fatal("folder became a file selection")
	}
	s.Key("right", 10) // expand src
	s.Key("right", 10) // enter src/auth
	if s.ProjectRows[s.TreeIndex].Path != "src/auth" {
		t.Fatal("right did not enter first folder")
	}
	s.Key("enter", 10) // expand auth
	s.Key("down", 10)  // login.go
	s.Key("enter", 10)
	if s.Browser || s.Current().Path != "src/auth/login.go" {
		t.Fatal("file did not open")
	}
	s.Key("n", 10) // selectFile uses the underlying filtered file order
	opened := s.Current().Path
	s.Update(s.Snapshot)
	if s.Current().Path != opened {
		t.Fatal("refresh changed the open file to the tree cursor")
	}
	s.Key("f", 10)
	if !s.Browser || s.ProjectRows[s.TreeIndex].Path != opened {
		t.Fatal("return did not reveal open file")
	}
	s.Key("left", 10)
	if !s.ProjectFolderSelected() {
		t.Fatal("left did not select parent")
	}
	parent := s.ProjectRows[s.TreeIndex].Path
	s.Key("left", 10)
	if s.ExpandedFolders[parent] {
		t.Fatal("left did not collapse folder")
	}
	s.Update(s.Snapshot)
	if s.ProjectRows[s.TreeIndex].Path != parent || s.ExpandedFolders[parent] {
		t.Fatal("refresh lost folder selection or collapse state")
	}
}

func TestProjectTreeSearchRevealsMatchesWithoutOpeningFoldersPermanently(t *testing.T) {
	s := treeState()
	s.Key("/", 10)
	for _, r := range "session.go" {
		s.Key(string(r), 10)
	}
	if s.ProjectFolderSelected() || s.Current() == nil || s.Current().Path != "src/auth/session.go" {
		t.Fatalf("search did not select matching file: %+v", s.ProjectRows)
	}
	if len(s.ExpandedFolders) != 0 {
		t.Fatal("search modified folder expansion state")
	}
	s.Key("enter", 10)
	s.Key("enter", 10)
	if s.Browser || s.Current().Path != "src/auth/session.go" {
		t.Fatal("filtered file did not open")
	}
	s.Key("f", 10)
	s.Key("/", 10)
	for s.Query != "" {
		s.Key("backspace", 10)
	}
	s.Key("enter", 10)
	if s.ProjectRows[s.TreeIndex].Path != "src/auth/session.go" {
		t.Fatal("returning from code failed to reveal selected file")
	}
}
