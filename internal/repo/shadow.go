package repo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// BaseRef anchors the workspace view of a shadow workspace. It is written once,
// from the first capture, and never re-anchored automatically: "everything that
// changed since Stvena first saw this folder" is the closest honest analogue of
// comparing against HEAD, and it survives restarts the way HEAD does.
const BaseRef = "refs/stvena/shadow/base"

// descriptors are the editor bridge files. They live in the Git directory of a
// real repository and in the cache directory of a shadow workspace.
var descriptors = []string{"stvena-live.json", "stvena-review.json", "stvena-request.json", "stvena-ide.json"}

// defaultExclude bounds the first capture. A Git repository is bounded by its
// own .gitignore; a folder that was never initialized usually has none, so
// without this the very first capture hashes node_modules and build output, and
// then does it again every tick.
const defaultExclude = `# Stvena ignores these in a folder that is not a Git repository.
# Edit this file to change what your captures include.
.git/
.stvena/
node_modules/
.venv/
venv/
__pycache__/
target/
build/
dist/
.next/
.gradle/
vendor/
.terraform/
*.log
.DS_Store
`

// openShadow resolves a directory that is not a Git repository, creating the
// private snapshot store on first use.
func openShadow(cwd string) (Workspace, Notice, error) {
	root, err := filepath.Abs(cwd)
	if err != nil {
		return Workspace{}, "", err
	}
	root = filepath.Clean(root)
	if err = reviewable(root); err != nil {
		return Workspace{}, "", err
	}
	// A .git that exists but does not answer is a repository to repair, not a
	// folder to shadow. Shadowing it would hide the real problem.
	if _, statErr := os.Lstat(filepath.Join(root, ".git")); statErr == nil {
		return Workspace{}, "", fmt.Errorf("%s has a .git that Git does not recognize; repair or remove it", root)
	}
	dir, err := CacheDir(root)
	if err != nil {
		return Workspace{}, "", err
	}
	shadow := ShadowPath(dir)
	notice := Notice("")
	if _, err = os.Stat(shadow); err != nil {
		if !os.IsNotExist(err) {
			return Workspace{}, "", err
		}
		if err = initShadow(shadow); err != nil {
			return Workspace{}, "", err
		}
	} else if err = probeShadow(shadow); err != nil {
		if err = rebuildShadow(dir, shadow); err != nil {
			return Workspace{}, "", err
		}
		notice = "Stvena's snapshot store for this folder was unreadable and has been rebuilt; earlier sessions here are gone."
	}
	return Workspace{Root: root, GitDir: shadow, Dir: dir, Mode: ModeShadow}, notice, nil
}

// reviewable refuses roots whose capture would be meaningless or enormous.
func reviewable(root string) error {
	if root == string(filepath.Separator) || filepath.Dir(root) == root {
		return errors.New("stvena will not review a filesystem root; run it inside a project directory")
	}
	if home, err := os.UserHomeDir(); err == nil && filepath.Clean(home) == root {
		return errors.New("stvena will not review your home directory; run it inside a project directory")
	}
	return nil
}

func initShadow(shadow string) error {
	// An empty template directory keeps a global init.templateDir from copying
	// the user's hooks into a repository whose plumbing Stvena drives every
	// tick, against a working tree those hooks were never written for.
	if _, err := output("-c", "init.templateDir=", "init", "--bare", "--quiet", shadow); err != nil {
		return fmt.Errorf("create snapshot store: %w", err)
	}
	settings := [][2]string{
		// Same reason as init.templateDir, for a global core.hooksPath.
		{"core.hooksPath", filepath.Join(shadow, "no-hooks")},
		// Captures must be byte-exact: a global autocrlf would rewrite the tree
		// and make reverting a change fail against the real file.
		{"core.autocrlf", "false"},
		{"core.safecrlf", "false"},
		// Neither cache belongs to a working tree this repository does not own.
		{"core.fsmonitor", "false"},
		{"core.untrackedCache", "false"},
		// Reflogs would pin every superseded capture forever.
		{"core.logAllRefUpdates", "false"},
		{"gc.auto", "512"},
		{"gc.autoDetach", "true"},
		{"gc.pruneExpire", "2.weeks.ago"},
	}
	for _, setting := range settings {
		if _, err := shadowOutput(shadow, "config", setting[0], setting[1]); err != nil {
			return fmt.Errorf("configure snapshot store: %w", err)
		}
	}
	if err := os.MkdirAll(filepath.Join(shadow, "info"), 0700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(shadow, "info", "exclude"), []byte(defaultExclude), 0600)
}

// probeShadow checks that the store still answers and can still be written to.
// Writing the empty tree exercises the object database, the configuration and
// the directory permissions in one command.
func probeShadow(shadow string) error {
	out, err := shadowOutput(shadow, "rev-parse", "--is-bare-repository")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(out)) != "true" {
		return fmt.Errorf("snapshot store at %s is not a bare repository", shadow)
	}
	_, err = shadowInput(shadow, nil, "hash-object", "-w", "-t", "tree", "--stdin")
	return err
}

// rebuildShadow replaces an unusable store. The saved sessions beside it name
// trees that no longer exist, so they go too rather than failing one by one.
func rebuildShadow(dir, shadow string) error {
	if err := os.RemoveAll(shadow); err != nil {
		return err
	}
	if err := initShadow(shadow); err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") || name == "preferences.json" {
			continue
		}
		_ = os.Remove(filepath.Join(dir, name))
	}
	return nil
}

// clearShadow runs when a folder that Stvena once shadowed has become a real
// repository. The descriptors move to the Git directory, so the ones left in
// the cache must go: an editor that found the stale pair would follow a session
// that ended, and never say why.
func clearShadow(ws Workspace) Notice {
	if ws.Dir == "" {
		return ""
	}
	if _, err := os.Stat(ShadowPath(ws.Dir)); err != nil {
		return ""
	}
	for _, name := range descriptors {
		_ = os.Remove(filepath.Join(ws.Dir, name))
	}
	return "This folder is now a Git repository; Stvena is reviewing it with Git."
}

// Collect runs the store's garbage collection. Stvena writes a tree every tick
// and none of its commands trigger Git's automatic gc, so in a shadow workspace
// nothing else ever collects the superseded objects.
func Collect(ws Workspace) error {
	if ws.Git() {
		return nil
	}
	_, err := shadowOutput(ws.GitDir, "gc", "--auto", "--quiet")
	return err
}

func shadowOutput(shadow string, args ...string) ([]byte, error) {
	return shadowInput(shadow, nil, args...)
}

func shadowInput(shadow string, stdin []byte, args ...string) ([]byte, error) {
	return outputInput(stdin, append([]string{"--git-dir", shadow}, args...)...)
}

func output(args ...string) ([]byte, error) { return outputInput(nil, args...) }

func outputInput(stdin []byte, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"--no-optional-locks"}, args...)...)
	cmd.Stdin = bytes.NewReader(stdin)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	return cmd.Output()
}
