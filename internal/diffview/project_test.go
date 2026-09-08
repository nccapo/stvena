package diffview

import (
	"strings"
	"testing"
)

func TestProjectIncludesUnchangedFilesAndReadsCapturedVersion(t *testing.T) {
	root := repo(t)
	writeFile(t, root, "unchanged.go", "package main\nfunc old() {}\n")
	writeFile(t, root, "目录.go", "package main\n")
	runGit(t, root, "add", ".")
	out, err := gitOutput(root, "write-tree")
	if err != nil {
		t.Fatal(err)
	}
	tree := strings.TrimSpace(string(out))
	project := Project(root, tree)
	if project.Err != nil || project.FileCount != 2 {
		t.Fatalf("project: %+v", project)
	}
	writeFile(t, root, "unchanged.go", "package main\nfunc newer() {}\n")
	f := findFile(t, project, "unchanged.go", ProjectScope)
	content := LoadContent(root, f)
	if content.Err != nil || !strings.Contains(strings.Join(content.Lines, "\n"), "old()") {
		t.Fatalf("read live source: %+v", content)
	}
	if f.Status != "" || project.Added != 0 || project.Deleted != 0 {
		t.Fatal("unchanged files became changes")
	}
	if Project(root, "--all").Err == nil {
		t.Fatal("accepted a revision option as a tree")
	}
}
