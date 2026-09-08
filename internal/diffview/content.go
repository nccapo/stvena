package diffview

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Content is the complete text on the selected side of a change, up to the
// explicit 16 MiB viewer limit. Deleted files use the pre-deletion version.
type Content struct {
	Lines       []string
	Source      string
	Old, Binary bool
	Err         error
}

func LoadContent(root string, f File) Content {
	result := Content{Source: "working tree"}
	var data []byte
	var err error
	switch {
	case f.ContentRef != "":
		result.Source = "captured working copy"
		data, err = gitOutput(root, "show", f.ContentRef+":"+f.Path)
	case validOID(f.AfterOID):
		result.Source = string(f.Scope) + " · captured version"
		data, err = gitOutput(root, "cat-file", "blob", f.AfterOID)
	case f.Status == "D" && validOID(f.BeforeOID):
		result.Source, result.Old = "captured version · before deletion", true
		data, err = gitOutput(root, "cat-file", "blob", f.BeforeOID)
	case f.Status == "D" && f.Scope == Staged:
		result.Source, result.Old = "HEAD · before deletion", true
		data, err = gitOutput(root, "show", "HEAD:"+f.Path)
	case f.Status == "D" && f.Scope == Unstaged:
		result.Source, result.Old = "index · before deletion", true
		data, err = gitOutput(root, "show", ":0:"+f.Path)
	case f.Scope == Staged && f.Status != "U":
		result.Source = "index · staged version"
		data, err = gitOutput(root, "show", ":0:"+f.Path)
	default:
		data, err = readContentFile(root, f.Path)
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

func readContentFile(root, name string) ([]byte, error) {
	if filepath.IsAbs(name) || name == ".." || strings.HasPrefix(filepath.Clean(name), ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("file path is outside repository")
	}
	path := filepath.Join(root, filepath.FromSlash(name))
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
