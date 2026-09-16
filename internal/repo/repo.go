// Package repo names the directory under review and the Git directory Stvena
// drives it with. In a Git repository that is the user's own Git directory. In
// a folder that was never initialized, it is a private bare repository in
// Stvena's cache: the working tree is reviewed without writing anything into
// the user's source tree, and without the folder becoming a repository.
package repo

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Mode string

const (
	ModeGit    Mode = "git"
	ModeShadow Mode = "shadow"
)

// Notice is a user-facing message a caller should show once. An empty Notice
// means there is nothing to say.
type Notice string

// Workspace is the reviewed working tree plus the Git directory that describes
// it. Root is always the worktree root; GitDir is the user's own Git directory
// in ModeGit and Stvena's private shadow repository in ModeShadow.
type Workspace struct {
	Root   string
	GitDir string
	Dir    string // Stvena's per-root cache directory
	Mode   Mode
}

// Git reports whether this workspace is backed by the user's own repository.
// Branch comparison and staging exist only there.
func (w Workspace) Git() bool { return w.Mode != ModeShadow }

// Args is the leading argv that selects this workspace, inserted by each caller
// after its own flags and before the subcommand. In ModeGit it is exactly what
// Stvena has always passed, so a Git repository sees byte-identical commands.
func (w Workspace) Args() []string {
	if w.Mode == ModeShadow {
		return []string{"-C", w.Root, "--git-dir", w.GitDir, "--work-tree", w.Root}
	}
	return []string{"-C", w.Root}
}

// DescriptorDir holds the stvena-*.json editor descriptors. It has no fallback
// on purpose: a Git workspace that answered with the cache directory would
// leave every running editor polling .git forever with no error anywhere.
func (w Workspace) DescriptorDir() string {
	if w.Mode == ModeShadow {
		return w.Dir
	}
	return w.GitDir
}

// ShadowPath is where a root's private repository lives, whether or not it exists.
func ShadowPath(dir string) string { return filepath.Join(dir, "shadow.git") }

// CacheDir is Stvena's private per-root directory. Sessions, preferences and
// review state have always lived here; in ModeShadow the snapshot store and the
// editor descriptors join them.
func CacheDir(root string) (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(root))
	dir := filepath.Join(cache, "stvena", hex.EncodeToString(sum[:12]))
	return dir, os.MkdirAll(dir, 0700)
}

// Key is the per-root identifier shared by the cache directory and the bridge
// registry, so a root has exactly one of each.
func Key(root string) string {
	sum := sha256.Sum256([]byte(root))
	return hex.EncodeToString(sum[:12])
}

// Git builds a Git-mode workspace for a root whose Git directory is the
// conventional one. Tests and callers that already know they hold a repository
// use it; Discover is what decides the mode for real.
func Git(root string) Workspace {
	dir, _ := CacheDir(root)
	return Workspace{Root: root, GitDir: filepath.Join(root, ".git"), Dir: dir, Mode: ModeGit}
}

// FromEnv rebuilds a workspace inside a child process, from the environment
// Stvena exports to the agents it launches. An empty gitDir is the ordinary
// Git case, where naming the working tree is enough.
func FromEnv(root, gitDir string) Workspace {
	if gitDir == "" {
		return Workspace{Root: root, GitDir: filepath.Join(root, ".git"), Mode: ModeGit}
	}
	return Workspace{Root: root, GitDir: gitDir, Dir: filepath.Dir(gitDir), Mode: ModeShadow}
}

// Root resolves the worktree root of a Git repository containing dir.
func Root(dir string) (string, error) {
	out, err := run(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("not inside a Git repository: %w", err)
	}
	return strings.TrimSuffix(string(out), "\n"), nil
}

// Discover resolves the workspace for a directory. A Git repository is
// described exactly as before. Anything else is only treated as a shadow
// workspace when Git positively reported that it is not a repository: a
// timeout, a permission error or a missing git binary is returned to the
// caller, because silently shadowing a real project would detach every
// connected editor from it.
func Discover(cwd string) (Workspace, Notice, error) {
	root, err := Root(cwd)
	if err == nil {
		gitDir, dirErr := run(root, "rev-parse", "--absolute-git-dir")
		if dirErr != nil {
			// The toplevel resolved, so this is a repository. Never downgrade.
			return Workspace{}, "", fmt.Errorf("resolve Git directory: %w", dirErr)
		}
		dir, cacheErr := CacheDir(root)
		if cacheErr != nil {
			return Workspace{}, "", cacheErr
		}
		ws := Workspace{Root: root, GitDir: strings.TrimSpace(string(gitDir)), Dir: dir, Mode: ModeGit}
		return ws, clearShadow(ws), nil
	}
	if !notARepository(err) {
		return Workspace{}, "", err
	}
	return openShadow(cwd)
}

// notARepository reports whether Git answered that the directory is not a
// repository, as opposed to failing to answer at all.
func notARepository(err error) bool {
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 128 {
		return false
	}
	return strings.Contains(strings.ToLower(string(exit.Stderr)), "not a git repository")
}

func run(dir string, args ...string) ([]byte, error) {
	return output(append([]string{"-C", dir}, args...)...)
}

func atomicJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".save-*")
	if err != nil {
		return err
	}
	name := file.Name()
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(name, path)
	}
	_ = os.Remove(name)
	return err
}
