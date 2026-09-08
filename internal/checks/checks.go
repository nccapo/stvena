// Package checks runs an explicitly requested command against a captured Git tree.
package checks

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

type Result struct {
	ExitCode                      int
	SourceChanged                 bool
	Tree, Command, Status, Output string
	FinishedAt                    time.Time
	Problems                      []Problem
}
type limitedBuffer struct {
	mu sync.Mutex
	bytes.Buffer
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	remaining := (1 << 20) - b.Len()
	if n > remaining {
		p = p[:max(0, remaining)]
		b.truncated = true
	}
	_, err := b.Buffer.Write(p)
	return n, err
}

func Run(ctx context.Context, root, tree, command string) (r Result) {
	r = Result{Tree: tree, Command: command, Status: "Failed", ExitCode: -1, FinishedAt: time.Now()}

	defer func() {
		r.FinishedAt = time.Now()
		if ctx.Err() != nil {
			r.Status = "Cancelled"
		}
	}()
	dir, err := os.MkdirTemp("", "stvena-check-*")
	if err != nil {
		r.Output = err.Error()
		return r
	}
	defer os.RemoveAll(dir)
	index := filepath.Join(dir, "index")
	work := filepath.Join(dir, "work")
	defer func() { r.Problems = ParseProblems(r.Output, work) }()
	if err = os.Mkdir(work, 0700); err != nil {
		r.Output = err.Error()
		return r
	}
	for _, args := range [][]string{{"read-tree", tree}, {"checkout-index", "--all", "--prefix=" + work + string(os.PathSeparator)}} {
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_INDEX_FILE="+index)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
		cmd.WaitDelay = 2 * time.Second
		if out, e := cmd.CombinedOutput(); e != nil {
			r.Output = fmt.Sprintf("Prepare snapshot: %s (%v)", out, e)
			return r
		}
	}
	before, err := sourceHashes(work)
	if err != nil {
		r.Output = "Prepare snapshot: " + err.Error()
		return r
	}
	// Checks receive neither a live worktree nor its .git metadata. Dependencies
	// ignored by Git must be installed by the explicitly supplied command.
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = work
	cmd.Env = append(os.Environ(), "STVENA_SNAPSHOT="+tree, "STVENA_CHECK=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 2 * time.Second
	var output limitedBuffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err = cmd.Run()
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	if cmd.ProcessState != nil {
		r.ExitCode = cmd.ProcessState.ExitCode()
	}
	for path, hash := range before {
		after, e := hashFile(path)
		if e != nil || after != hash {
			r.SourceChanged = true
			break
		}
	}
	r.FinishedAt = time.Now()
	r.Output = output.String()
	if output.truncated {
		r.Output += "\n[Output truncated at 1 MiB]"
	}
	if err == nil {
		r.Status = "Passed"
	} else {
		r.Output += "\n" + err.Error()
		if ctx.Err() != nil {
			r.Status = "Cancelled"
		}
	}
	r.Output = strings.TrimSpace(r.Output)
	return r
}

func sourceHashes(root string) (map[string][32]byte, error) {
	hashes := map[string][32]byte{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			resolved, e := filepath.EvalSymlinks(path)
			if e != nil {
				return fmt.Errorf("cannot capture symlink %s: %w", path, e)
			}
			rel, e := filepath.Rel(root, resolved)
			if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
				return fmt.Errorf("snapshot symlink points outside captured code: %s", path)
			}
		}
		if d.Type().IsRegular() {
			hash, e := hashFile(path)
			if e != nil {
				return e
			}
			hashes[path] = hash
		}
		return nil
	})
	return hashes, err
}

func hashFile(path string) ([32]byte, error) {
	var sum [32]byte
	info, err := os.Lstat(path)
	if err != nil {
		return sum, err
	}
	if !info.Mode().IsRegular() {
		return sum, fmt.Errorf("source type changed: %s", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return sum, err
	}
	defer file.Close()
	h := sha256.New()
	fmt.Fprint(h, info.Mode().String())
	if _, err = io.Copy(h, file); err != nil {
		return sum, err
	}
	copy(sum[:], h.Sum(nil))
	return sum, nil
}
