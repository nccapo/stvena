// Package session captures working copies without changing the user's index or
// branch. Private Git refs retain the objects used by saved review sessions.
package session

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

type Session struct {
	ID, Root, Baseline, LastTree string
	CreatedAt                    time.Time
	Batches                      []Batch `json:",omitempty"`
	Dir                          string  `json:"-"`
	index                        string
	mu                           sync.Mutex
}

// Batch is one stable working-tree transition observed by Stvena. It does not
// claim that a particular agent or conversation turn authored the change.
type Batch struct {
	ID                        uint64
	Before, After             string
	ObservedAt                time.Time
	FileCount, Added, Deleted int
}

const maxBatches = 100

func RepoDir(root string) (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(root))
	dir := filepath.Join(cache, "stvena", hex.EncodeToString(sum[:12]))
	return dir, os.MkdirAll(dir, 0700)
}

func Open(root string, resume bool, id string) (*Session, error) {
	dir, err := RepoDir(root)
	if err != nil {
		return nil, err
	}
	s := &Session{Root: root, Dir: dir}
	if resume {
		name := "latest.json"
		if id != "" {
			if filepath.Base(id) != id || strings.ContainsAny(id, "/\\") {
				return nil, fmt.Errorf("invalid session ID")
			}
			name = id + ".json"
		}
		data, readErr := os.ReadFile(filepath.Join(dir, name))
		if readErr == nil {
			if err = json.Unmarshal(data, s); err != nil {
				return nil, err
			}
			s.Dir = dir
			if s.Root != root {
				return nil, fmt.Errorf("session belongs to another repository")
			}
		} else if !os.IsNotExist(readErr) || id != "" {
			return nil, readErr
		}
	}
	gitDir, err := git(root, nil, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return nil, err
	}
	index, err := os.CreateTemp(strings.TrimSpace(gitDir), "stvena-index-*")
	if err != nil {
		return nil, err
	}
	s.index = index.Name()
	_ = index.Close()
	_ = os.Remove(s.index)
	if s.ID == "" {
		s.CreatedAt = time.Now()
		s.ID = s.CreatedAt.UTC().Format("20060102T150405.000000000")
		tree, captureErr := s.Capture()
		if captureErr != nil {
			s.Close()
			return nil, captureErr
		}
		s.Baseline = tree
		if _, err = git(root, nil, "update-ref", s.ref("baseline"), tree); err != nil {
			s.Close()
			return nil, err
		}
		if err = s.Save(); err != nil {
			s.Close()
			return nil, err
		}
	}
	return s, nil
}
func (s *Session) ref(kind string) string { return "refs/stvena/sessions/" + s.ID + "/" + kind }

func (s *Session) Capture() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Copy Git's atomically replaced index so staged ignored paths and conflict
	// entries are included. The following add operates only on the private copy.
	actual, err := git(s.Root, nil, "rev-parse", "--git-path", "index")
	if err != nil {
		return "", err
	}
	actual = strings.TrimSpace(actual)
	if !filepath.IsAbs(actual) {
		actual = filepath.Join(s.Root, actual)
	}
	source, err := os.Open(actual)
	if err == nil {
		// Read metadata and bytes from the same open index: Git atomically replaces
		// it. Preserve its timestamp so copying cannot hide a racy same-size edit.
		var info os.FileInfo
		info, err = source.Stat()
		var data []byte
		if err == nil {
			data, err = io.ReadAll(source)
		}
		_ = source.Close()
		if err == nil {
			err = os.WriteFile(s.index, data, 0600)
		}
		if err == nil {
			err = os.Chtimes(s.index, info.ModTime(), info.ModTime())
		}
	} else if os.IsNotExist(err) {
		_ = os.Remove(s.index)
		err = nil
	}
	if err != nil {
		return "", err
	}
	env := []string{"GIT_INDEX_FILE=" + s.index}
	if _, err = git(s.Root, env, "add", "--all", "--", "."); err != nil {
		return "", err
	}
	tree, err := git(s.Root, env, "write-tree")
	if err != nil {
		return "", err
	}
	tree = strings.TrimSpace(tree)
	if tree != s.LastTree {
		if _, err = git(s.Root, nil, "update-ref", s.ref("latest"), tree); err != nil {
			return "", err
		}
		s.LastTree = tree
		if err = s.Save(); err != nil {
			return "", err
		}
	}
	return tree, nil
}
func (s *Session) Save() error {
	return AtomicJSON(filepath.Join(s.Dir, s.ID+".json"), s, filepath.Join(s.Dir, "latest.json"))
}

// RecordBatch retains a bounded, replayable history of successfully compared
// captured trees. The newest tree remains protected by the session latest ref;
// individual refs keep intermediate snapshots available for timeline review.
func (s *Session) RecordBatch(before, after string, observedAt time.Time, files, added, deleted int) error {
	if before == "" || after == "" || before == after {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.Batches) > 0 && s.Batches[len(s.Batches)-1].After == after {
		return nil
	}
	id := uint64(1)
	if len(s.Batches) > 0 {
		id = s.Batches[len(s.Batches)-1].ID + 1
	}
	ref := s.ref(fmt.Sprintf("batches/%06d", id))
	if _, err := git(s.Root, nil, "update-ref", ref, after); err != nil {
		return err
	}
	previous := append([]Batch(nil), s.Batches...)
	s.Batches = append(s.Batches, Batch{ID: id, Before: before, After: after, ObservedAt: observedAt.UTC(), FileCount: files, Added: added, Deleted: deleted})
	var dropped []Batch
	if len(s.Batches) > maxBatches {
		dropped = append(dropped, s.Batches[:len(s.Batches)-maxBatches]...)
		s.Batches = append([]Batch(nil), s.Batches[len(s.Batches)-maxBatches:]...)
		if _, err := git(s.Root, nil, "update-ref", s.ref("batch-base"), s.Batches[0].Before); err != nil {
			s.Batches = previous
			_, _ = git(s.Root, nil, "update-ref", "-d", ref)
			return err
		}
	}
	if err := s.Save(); err != nil {
		s.Batches = previous
		_, _ = git(s.Root, nil, "update-ref", "-d", ref)
		return err
	}
	for _, batch := range dropped {
		_, _ = git(s.Root, nil, "update-ref", "-d", s.ref(fmt.Sprintf("batches/%06d", batch.ID)))
	}
	return nil
}
func (s *Session) Close() { _ = os.Remove(s.index); _ = os.Remove(s.index + ".lock") }

func AtomicJSON(path string, value any, aliases ...string) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	for _, target := range append([]string{path}, aliases...) {
		file, err := os.CreateTemp(filepath.Dir(target), ".save-*")
		if err != nil {
			return err
		}
		name := file.Name()
		if _, err = file.Write(data); err == nil {
			err = file.Sync()
		}
		closeErr := file.Close()
		if err == nil {
			err = closeErr
		}
		if err == nil {
			err = os.Rename(name, target)
		}
		_ = os.Remove(name)
		if err != nil {
			return err
		}
	}
	return nil
}
func git(root string, env []string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"--no-optional-locks", "-C", root}, args...)...)
	cmd.Env = append(os.Environ(), env...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("snapshot: %s (%v)", strings.TrimSpace(string(out)), err)
	}
	return strings.TrimSuffix(string(out), "\n"), nil
}

// Retain protects a version referenced by review feedback or check results from
// Git garbage collection after the live session advances.
func Retain(root, tree string) error {
	if len(tree) != 40 && len(tree) != 64 {
		return fmt.Errorf("invalid captured tree")
	}
	if _, err := hex.DecodeString(tree); err != nil {
		return err
	}
	_, err := git(root, nil, "update-ref", "refs/stvena/reviews/"+tree, tree)
	return err
}

// List returns local saved sessions in filename (creation time) order.
func List(root string) ([]*Session, error) {
	dir, err := RepoDir(root)
	if err != nil {
		return nil, err
	}
	names, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	var result []*Session
	for _, name := range names {
		if filepath.Base(name) == "latest.json" {
			continue
		}
		data, e := os.ReadFile(name)
		if e != nil {
			continue
		}
		s := &Session{}
		if json.Unmarshal(data, s) == nil && s.ID != "" && s.Root == root {
			result = append(result, s)
		}
	}
	return result, nil
}
