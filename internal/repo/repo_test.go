package repo

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// isolate redirects everything Stvena writes outside the project, so a test
// never touches the developer's real cache, home or bridge registry.
func isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	t.Setenv("STVENA_HOME", filepath.Join(home, "stvena"))
	return home
}

func gitInit(t *testing.T, root string) {
	t.Helper()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %s %v", out, err)
	}
}

// A Git repository must see exactly the command line Stvena has always sent.
// Every diff, patch and capture is composed from this prefix, so an extra or
// reordered argument changes pathspec resolution and locking in ways that only
// show up under load.
func TestGitModeArgumentsAreUnchanged(t *testing.T) {
	ws := Workspace{Root: "/tmp/project", GitDir: "/tmp/project/.git", Mode: ModeGit}
	if got := ws.Args(); !reflect.DeepEqual(got, []string{"-C", "/tmp/project"}) {
		t.Fatalf("Git mode argv changed: %q", got)
	}
	if !ws.Git() {
		t.Fatal("Git mode did not report itself as Git backed")
	}
	if ws.DescriptorDir() != "/tmp/project/.git" {
		t.Fatalf("descriptors moved out of the Git directory: %s", ws.DescriptorDir())
	}
}

func TestShadowModeDrivesTheWorkingTreeFromThePrivateStore(t *testing.T) {
	ws := Workspace{Root: "/tmp/project", GitDir: "/cache/shadow.git", Dir: "/cache", Mode: ModeShadow}
	want := []string{"-C", "/tmp/project", "--git-dir", "/cache/shadow.git", "--work-tree", "/tmp/project"}
	if got := ws.Args(); !reflect.DeepEqual(got, want) {
		t.Fatalf("shadow argv: %q", got)
	}
	if ws.Git() {
		t.Fatal("shadow workspace claimed to be Git backed")
	}
	if ws.DescriptorDir() != "/cache" {
		t.Fatalf("shadow descriptors: %s", ws.DescriptorDir())
	}
}

func TestDiscoverDescribesARepositoryAsBefore(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	gitInit(t, root)
	ws, _, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if !ws.Git() {
		t.Fatal("a Git repository was not recognized")
	}
	real, _ := filepath.EvalSymlinks(root)
	if got, _ := filepath.EvalSymlinks(ws.Root); got != real {
		t.Fatalf("root %s is not %s", ws.Root, real)
	}
	if filepath.Base(ws.GitDir) != ".git" || ws.DescriptorDir() != ws.GitDir {
		t.Fatalf("descriptors did not stay in the Git directory: %+v", ws)
	}
}

func TestDiscoverSnapshotsAFolderThatIsNotARepository(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	ws, notice, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if ws.Git() || notice != "" {
		t.Fatalf("expected a quiet shadow workspace: %+v %q", ws, notice)
	}
	// Stvena must never turn the user's folder into a repository.
	if _, err := os.Stat(filepath.Join(root, ".git")); !os.IsNotExist(err) {
		t.Fatal("Discover created a Git repository in the project")
	}
	if !strings.HasPrefix(ws.GitDir, ws.Dir) || filepath.Base(ws.GitDir) != "shadow.git" {
		t.Fatalf("snapshot store is not in the cache: %+v", ws)
	}
	if ws.DescriptorDir() != ws.Dir {
		t.Fatalf("descriptors are not in the cache: %s", ws.DescriptorDir())
	}
	if _, err := os.Stat(filepath.Join(ws.GitDir, "info", "exclude")); err != nil {
		t.Fatalf("default excludes were not written: %v", err)
	}
	hooks, err := exec.Command("git", "--git-dir", ws.GitDir, "config", "core.hooksPath").Output()
	if err != nil || !strings.Contains(string(hooks), "no-hooks") {
		t.Fatalf("hooks were not disabled: %s %v", hooks, err)
	}
	again, _, err := Discover(root)
	if err != nil || again.GitDir != ws.GitDir {
		t.Fatalf("second discovery rebuilt the store: %+v %v", again, err)
	}
}

func TestDiscoverRebuildsAnUnreadableStore(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	ws, _, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(ws.GitDir, "HEAD"), []byte("garbage"), 0600); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(ws.Dir, "latest.json")
	if err = os.WriteFile(stale, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	rebuilt, notice, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if notice == "" {
		t.Fatal("rebuilding the store was silent")
	}
	if err = probeShadow(rebuilt.GitDir); err != nil {
		t.Fatalf("rebuilt store is still unusable: %v", err)
	}
	if _, err = os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("sessions naming vanished captures survived the rebuild")
	}
}

func TestDiscoverRefusesWhatItShouldNotSnapshot(t *testing.T) {
	home := isolate(t)
	if _, _, err := Discover(home); err == nil {
		t.Fatal("the home directory was accepted as a project")
	}
	if _, _, err := Discover(string(filepath.Separator)); err == nil {
		t.Fatal("the filesystem root was accepted as a project")
	}
	broken := t.TempDir()
	if err := os.WriteFile(filepath.Join(broken, ".git"), []byte("gitdir: /nowhere\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Discover(broken); err == nil {
		t.Fatal("a repository Git cannot read was quietly shadowed")
	}
}

// A Git that fails for any other reason must not be read as "this is not a
// repository". Shadowing a real project would detach every connected editor
// from it and start a second, private history.
func TestDiscoverDoesNotShadowWhenGitMerelyFails(t *testing.T) {
	isolate(t)
	bin := t.TempDir()
	script := "#!/bin/sh\necho 'fatal: unable to read index' 1>&2\nexit 128\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if _, _, err := Discover(t.TempDir()); err == nil {
		t.Fatal("a failing Git was treated as a folder without one")
	}
}
