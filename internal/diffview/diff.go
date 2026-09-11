package diffview

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	maxUntrackedFiles = 100
	maxUntrackedLines = 400
	maxUntrackedBytes = 256 << 10
	maxGitBytes       = 16 << 20
)

type Scope string

const (
	Staged       Scope = "staged"
	Unstaged     Scope = "unstaged"
	Untracked    Scope = "untracked"
	Session      Scope = "session"
	BranchScope  Scope = "branch"
	ProjectScope Scope = "project"
)

// File is one change relative to the index (unstaged) or HEAD (staged).
// A path can appear in both scopes; their patches must not be combined.
type File struct {
	Path, OldPath       string
	Scope               Scope
	Status              string
	Added, Deleted      int
	Binary, Truncated   bool
	Lines               []string
	ContentHash         [32]byte
	BeforeOID, AfterOID string
	ContentRef          string
}

func (f File) Key() string { return string(f.Scope) + "\x00" + f.Path }

type Snapshot struct {
	Root                 string
	Tree, Version, Label string
	Files                []File
	FileCount            int
	Added, Deleted       int
	Approximate          bool
	Branch               string
	UpdatedAt            time.Time
	Err                  error
}

func GitRoot(dir string) (string, error) {
	out, err := gitOutput(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("not inside a Git repository: %w", err)
	}
	return strings.TrimSuffix(string(out), "\n"), nil
}

// Collect retains separate index and worktree changes, including unborn HEADs.
func Collect(root string) Snapshot {
	s := Snapshot{Root: root, UpdatedAt: time.Now()}
	if out, err := gitOutput(root, "symbolic-ref", "--quiet", "--short", "HEAD"); err == nil {
		s.Branch = strings.TrimSpace(string(out))
	} else if out, err := gitOutput(root, "rev-parse", "--short", "HEAD"); err == nil {
		s.Branch = strings.TrimSpace(string(out)) + " (detached)"
	}
	for _, scope := range []Scope{Staged, Unstaged} {
		files, err := trackedFiles(root, scope)
		s.Files = append(s.Files, files...)
		if err != nil {
			s.Err = errors.Join(s.Err, err)
		}
	}
	names, err := gitOutput(root, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		s.Err = errors.Join(s.Err, err)
	} else {
		for i, name := range splitNUL(names) {
			f := File{Path: name, Scope: Untracked, Status: "?"}
			if i < maxUntrackedFiles {
				readUntracked(root, &f)
			} else {
				f.Truncated = true
				f.Lines = []string{"Preview omitted: first 100 untracked files are loaded."}
			}
			s.Files = append(s.Files, f)
		}
	}
	paths := make(map[string]bool)
	for _, f := range s.Files {
		paths[f.Path] = true
		s.Added += f.Added
		s.Deleted += f.Deleted
		s.Approximate = s.Approximate || f.Truncated || f.Status == "U"
	}
	s.Approximate = s.Approximate || s.Err != nil
	s.FileCount = len(paths)
	s.Finish()
	return s
}

func trackedFiles(root string, scope Scope) ([]File, error) { return collectDiff(root, scope, nil) }

func collectDiff(root string, scope Scope, revisions []string) ([]File, error) {
	args := []string{"diff", "--no-abbrev", "--no-ext-diff", "--no-textconv", "--no-color", "--no-relative", "--find-renames", "--ignore-submodules=none", "--raw", "--numstat", "-z", "--unified=3", "--src-prefix=a/", "--dst-prefix=b/", "--submodule=short"}
	if scope == Staged && len(revisions) == 0 {
		args = append(args, "--cached")
	}
	args = append(args, revisions...)
	// Obtain names, statistics, and patch ordering from the same Git invocation.
	// Separate commands can disagree while an agent is adding/removing files.
	out, err := gitOutput(root, append(append([]string{}, args...), "--patch", "--", ".")...)
	var previewErr error
	if err != nil && strings.Contains(err.Error(), "16 MiB") {
		previewErr = err
		out, err = gitOutput(root, append(args, "--", ".")...)
	}
	if err != nil {
		return nil, err
	}
	metadata, patch, _ := bytes.Cut(out, []byte{0, 0})
	parts := splitNUL(metadata)
	var files []File
	byPath := make(map[string]int)
	i := 0
	for i < len(parts) && strings.HasPrefix(parts[i], ":") {
		fields := strings.Fields(parts[i])
		i++
		if len(fields) < 5 || i >= len(parts) {
			return files, fmt.Errorf("incomplete Git file status")
		}
		status := fields[len(fields)-1]
		// Combined raw records represent unmerged files with multiple parents.
		if strings.HasPrefix(fields[0], "::") {
			status = "U"
		}
		f := File{Scope: scope, Status: status[:1], Path: parts[i]}
		if len(fields) == 5 {
			f.BeforeOID, f.AfterOID = fields[2], fields[3]
		}
		i++
		if f.Status == "R" || f.Status == "C" {
			if i >= len(parts) {
				return files, fmt.Errorf("incomplete Git rename")
			}
			f.OldPath, f.Path = f.Path, parts[i]
			i++
		}
		byPath[f.Path] = len(files)
		files = append(files, f)
	}
	var patchOrder []int
	for ; i < len(parts); i++ {
		fields := strings.SplitN(parts[i], "\t", 3)
		if len(fields) != 3 {
			return files, fmt.Errorf("incomplete Git line statistics")
		}
		path := fields[2]
		if path == "" {
			if i+2 >= len(parts) {
				return files, fmt.Errorf("incomplete Git rename statistics")
			}
			path = parts[i+2]
			i += 2
		}
		if index, ok := byPath[path]; ok {
			files[index].Added, _ = strconv.Atoi(fields[0])
			files[index].Deleted, _ = strconv.Atoi(fields[1])
			files[index].Binary = fields[0] == "-"
			if files[index].Status != "U" {
				patchOrder = append(patchOrder, index)
			}
		}
	}
	index := -1
	for _, line := range strings.Split(strings.TrimSuffix(string(patch), "\n"), "\n") {
		if strings.HasPrefix(line, "diff --git ") {
			index++
		}
		if index >= 0 && index < len(patchOrder) {
			n := patchOrder[index]
			files[n].Lines = append(files[n].Lines, line)
		}
	}
	for i := range files {
		if files[i].Status == "U" {
			files[i].Lines = []string{"Unmerged file: resolve conflicts before reviewing a regular patch."}
		}
		if previewErr != nil {
			files[i].Truncated = true
			files[i].Lines = []string{"Patch unavailable: " + previewErr.Error()}
		}
		if len(files[i].Lines) == 0 {
			files[i].Lines = []string{"No text patch available (metadata-only change)."}
		}
	}
	return files, previewErr
}

func gitOutput(root string, args ...string) ([]byte, error) { return gitEnvOutput(root, nil, args...) }

func gitEnvOutput(root string, env []string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"--no-optional-locks", "--literal-pathspecs", "-c", "diff.orderFile=" + os.DevNull, "-C", root}, args...)...)
	// Refreshes are background work. Sharing the foreground terminal group
	// makes terminals that display the active process keep switching titles.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	out, readErr := io.ReadAll(io.LimitReader(pipe, maxGitBytes+1))
	if len(out) > maxGitBytes || readErr != nil {
		_ = cmd.Cancel()
	}
	err = cmd.Wait()
	if len(out) > maxGitBytes {
		return nil, fmt.Errorf("Git output exceeds 16 MiB preview limit")
	}
	if ctx.Err() != nil {
		return nil, fmt.Errorf("Git refresh timed out")
	}
	if readErr != nil {
		return nil, readErr
	}
	if err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return nil, fmt.Errorf("git: %s", message)
	}
	return out, nil
}

func splitNUL(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	return strings.Split(strings.TrimSuffix(string(data), "\x00"), "\x00")
}

func readUntracked(root string, f *File) {
	path := filepath.Join(root, filepath.FromSlash(f.Path))
	info, err := os.Lstat(path)
	var data []byte
	if err == nil && info.Mode()&os.ModeSymlink != 0 {
		var target string
		target, err = os.Readlink(path)
		data = []byte(target)
	} else if err == nil && info.Mode().IsRegular() {
		var file *os.File
		file, err = os.Open(path)
		if err == nil {
			data, err = io.ReadAll(io.LimitReader(file, maxUntrackedBytes+1))
			_ = file.Close()
		}
	} else if err == nil {
		err = fmt.Errorf("not a regular file")
	}
	if err != nil {
		f.Truncated = true
		f.Lines = []string{"Unable to preview: " + err.Error()}
		return
	}
	f.ContentHash = sha256.Sum256(data)
	f.Truncated = len(data) > maxUntrackedBytes
	if f.Truncated {
		data = data[:maxUntrackedBytes]
	}
	f.Lines = []string{"diff --git a/" + f.Path + " b/" + f.Path, "new file", "--- /dev/null", "+++ b/" + f.Path}
	if bytes.IndexByte(data, 0) >= 0 {
		f.Binary = true
		f.Lines = append(f.Lines, "Binary file")
		return
	}
	var content []string
	if len(data) > 0 {
		content = strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	}
	f.Added = len(content)
	f.Lines = append(f.Lines, fmt.Sprintf("@@ -0,0 +1,%d @@", f.Added))
	for i, line := range content {
		if i == maxUntrackedLines {
			f.Truncated = true
			break
		}
		f.Lines = append(f.Lines, "+"+line)
	}
	if f.Truncated {
		f.Lines = append(f.Lines, "… preview truncated; line count may be a lower bound")
	}
}
