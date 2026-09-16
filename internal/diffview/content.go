package diffview

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/nccapo/stvena/internal/repo"
)

// Content is the complete text on the selected side of a change, up to the
// explicit 16 MiB viewer limit. Deleted files use the pre-deletion version.
type Content struct {
	Lines       []string
	Source      string
	Old, Binary bool
	Err         error
}

func LoadContent(ws repo.Workspace, f File) Content {
	result := Content{Source: "working tree"}
	if ws.Root == "" {
		result.Err = fmt.Errorf("no project is open")
		return result
	}
	var data []byte
	var err error
	switch {
	case f.ContentRef != "":
		result.Source = "captured working copy"
		data, err = gitOutput(ws, "show", f.ContentRef+":"+f.Path)
	case validOID(f.AfterOID):
		result.Source = string(f.Scope) + " · captured version"
		data, err = gitOutput(ws, "cat-file", "blob", f.AfterOID)
	case f.Status == "D" && validOID(f.BeforeOID):
		result.Source, result.Old = "captured version · before deletion", true
		data, err = gitOutput(ws, "cat-file", "blob", f.BeforeOID)
	case !ws.Git():
		// Every capture in a project without Git carries its own object IDs, so
		// the cases below are unreachable; say so plainly if that ever changes.
		result.Err = fmt.Errorf("this project has no index or HEAD to read %s from", f.Path)
		return result
	case f.Status == "D" && f.Scope == Staged:
		result.Source, result.Old = "HEAD · before deletion", true
		data, err = gitOutput(ws, "show", "HEAD:"+f.Path)
	case f.Status == "D" && f.Scope == Unstaged:
		result.Source, result.Old = "index · before deletion", true
		data, err = gitOutput(ws, "show", ":0:"+f.Path)
	case f.Scope == Staged && f.Status != "U":
		result.Source = "index · staged version"
		data, err = gitOutput(ws, "show", ":0:"+f.Path)
	default:
		data, err = readContentFile(ws, f.Path)
	}
	if err != nil {
		result.Err = err
		return result
	}
	result.Binary = bytes.IndexByte(data, 0) >= 0
	if !result.Binary && len(data) > 0 {
		result.Lines = strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	}
	return result
}

func readContentFile(ws repo.Workspace, name string) ([]byte, error) {
	if filepath.IsAbs(name) || name == ".." || strings.HasPrefix(filepath.Clean(name), ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("file path is outside repository")
	}
	path := filepath.Join(ws.Root, filepath.FromSlash(name))
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(path)
		return []byte(target), err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("no regular text file at this path")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxGitBytes+1))
	if len(data) > maxGitBytes {
		return nil, fmt.Errorf("file exceeds 16 MiB viewer limit")
	}
	return data, err
}
