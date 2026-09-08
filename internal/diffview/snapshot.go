package diffview

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

func validOID(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil && strings.Trim(s, "0") != ""
}

func Revision(f File) string {
	hash := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%x\x00%s", f.Status, f.OldPath, f.BeforeOID, f.AfterOID, f.ContentHash, strings.Join(f.Lines, "\n"))))
	return hex.EncodeToString(hash[:])
}

func (s *Snapshot) Finish() {
	paths := map[string]bool{}
	s.Added, s.Deleted = 0, 0
	var identity strings.Builder
	identity.WriteString(s.Tree)
	for _, f := range s.Files {
		paths[f.Path] = true
		s.Added += f.Added
		s.Deleted += f.Deleted
		s.Approximate = s.Approximate || f.Truncated || f.Status == "U"
		identity.WriteString(f.Key() + Revision(f))
	}
	s.FileCount = len(paths)
	hash := sha256.Sum256([]byte(identity.String()))
	s.Version = hex.EncodeToString(hash[:])
}

// CompareTrees includes committed and uncommitted changes relative to a saved
// working tree. Neither tree is the user's staging index.
func CompareTrees(root, before, after string) Snapshot {
	s := Snapshot{Root: root, Tree: after, Label: "This session", UpdatedAt: time.Now()}
	if !validOID(before) || !validOID(after) {
		s.Err = fmt.Errorf("session snapshot unavailable")
		return s
	}
	s.Files, s.Err = collectDiff(root, Session, []string{before, after})
	s.Finish()
	return s
}

func CompareFileVersions(root string, previous, current File) (File, error) {
	resolve := func(f File) string {
		if validOID(f.AfterOID) {
			return f.AfterOID
		}
		if f.ContentRef != "" {
			if out, err := gitOutput(root, "rev-parse", f.ContentRef+":"+f.Path); err == nil {
				return strings.TrimSpace(string(out))
			}
		}
		return ""
	}
	before, after := resolve(previous), resolve(current)
	if !validOID(before) || !validOID(after) {
		return File{}, fmt.Errorf("comparison needs two existing captured file versions")
	}
	out, err := gitOutput(root, "diff", "--no-ext-diff", "--no-textconv", "--no-color", "--patch", "--unified=3", before, after)
	if err != nil {
		return File{}, err
	}
	f := File{Path: current.Path, Scope: current.Scope, Status: "M", BeforeOID: before, AfterOID: after}
	if len(out) == 0 {
		f.Lines = []string{"No changes since your last review."}
		return f, nil
	}
	f.Lines = []string{"--- previous review: " + current.Path, "+++ captured version: " + current.Path}
	inHunk := false
	for _, line := range strings.Split(strings.TrimSuffix(string(out), "\n"), "\n") {
		if strings.HasPrefix(line, "@@ ") {
			inHunk = true
		}
		if strings.HasPrefix(line, "Binary files ") {
			f.Binary = true
			f.Lines = append(f.Lines, "Binary content changed.")
		}
		if inHunk {
			f.Lines = append(f.Lines, line)
			if strings.HasPrefix(line, "+") {
				f.Added++
			}
			if strings.HasPrefix(line, "-") {
				f.Deleted++
			}
		}
	}
	return f, nil
}

// FreezeContent associates worktree file previews with the same captured tree.
func (s *Snapshot) FreezeContent(tree string) {
	s.Tree = tree
	for i := range s.Files {
		f := &s.Files[i]
		if f.Scope != Staged && f.Status != "D" {
			f.ContentRef = tree
		}
	}
	s.Finish()
}
