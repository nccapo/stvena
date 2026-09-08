package diffview

import (
	"fmt"
	"strings"
	"time"
)

// Project lists the captured tree, including unchanged and nonignored new files.
// Blob IDs keep selections stable even if the working file changes during a read.
func Project(root, tree string) Snapshot {
	s := Snapshot{Root: root, Tree: tree, Label: "Project files", UpdatedAt: time.Now()}
	if !validOID(tree) {
		s.Err = fmt.Errorf("project files need a captured Git snapshot")
		return s
	}
	out, err := gitOutput(root, "ls-tree", "-r", "-z", "--full-tree", tree)
	if err != nil {
		s.Err = err
		return s
	}
	for _, entry := range strings.Split(string(out), "\x00") {
		meta, name, ok := strings.Cut(entry, "\t")
		fields := strings.Fields(meta)
		if !ok || len(fields) != 3 || fields[1] != "blob" {
			continue
		}
		s.Files = append(s.Files, File{Path: name, Scope: ProjectScope, AfterOID: fields[2]})
	}
	s.Finish()
	return s
}
