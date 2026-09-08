package diffview

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContentSelectsIndexWorktreeAndDeletedVersions(t *testing.T) {
	root := repo(t)
	name := "path with\ttab.txt"
	writeFile(t, root, name, "unchanged first\noriginal\nunchanged last\n")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-qm", "initial")
	writeFile(t, root, name, "unchanged first\nstaged\nunchanged last\n")
	runGit(t, root, "add", ".")
	writeFile(t, root, name, "unchanged first\nworking\nunchanged last\n")
	for _, tc := range []struct {
		scope Scope
		want  string
	}{{Staged, "staged"}, {Unstaged, "working"}} {
		content := LoadContent(root, File{Path: name, Scope: tc.scope, Status: "M"})
		if content.Err != nil || len(content.Lines) != 3 || content.Lines[1] != tc.want {
			t.Fatalf("wrong full version: %+v", content)
		}
	}
	if err := os.Remove(filepath.Join(root, name)); err != nil {
		t.Fatal(err)
	}
	content := LoadContent(root, File{Path: name, Scope: Unstaged, Status: "D"})
	if content.Err != nil || !content.Old || content.Lines[1] != "staged" {
		t.Fatalf("wrong unstaged deletion: %+v", content)
	}
	runGit(t, root, "add", ".")
	content = LoadContent(root, File{Path: name, Scope: Staged, Status: "D"})
	if content.Err != nil || !content.Old || content.Lines[1] != "original" {
		t.Fatalf("wrong staged deletion: %+v", content)
	}
}
func TestFullUntrackedFileBeyondPatchPreview(t *testing.T) {
	root := repo(t)
	writeFile(t, root, "large.txt", strings.Repeat("line\n", 600)+"last line\n")
	content := LoadContent(root, File{Path: "large.txt", Scope: Untracked})
	if content.Err != nil || len(content.Lines) != 601 || content.Lines[600] != "last line" {
		t.Fatalf("full file truncated: %+v", content)
	}
	writeFile(t, root, "binary", "\x00\x01")
	if !LoadContent(root, File{Path: "binary", Scope: Untracked}).Binary {
		t.Fatal("binary not detected")
	}
	writeFile(t, root, "empty", "")
	content = LoadContent(root, File{Path: "empty", Scope: Untracked})
	if content.Err != nil || len(content.Lines) != 0 {
		t.Fatal("empty file misread")
	}
	if LoadContent(root, File{Path: "missing", Scope: Untracked}).Err == nil {
		t.Fatal("missing file not reported")
	}
}
func TestFullFileSymlinkAndLimit(t *testing.T) {
	root := repo(t)
	if err := os.Symlink("missing-target", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	content := LoadContent(root, File{Path: "link", Scope: Untracked})
	if content.Err != nil || len(content.Lines) != 1 || content.Lines[0] != "missing-target" {
		t.Fatalf("symlink followed: %+v", content)
	}
	writeFile(t, root, "too-big", strings.Repeat("x", maxGitBytes+1))
	if LoadContent(root, File{Path: "too-big", Scope: Untracked}).Err == nil {
		t.Fatal("oversized full file not reported")
	}
}
