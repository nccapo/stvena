package checks

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nccapo/stvena/internal/session"

	"github.com/nccapo/stvena/internal/repo"
)

func TestChecksUseCapturedCodeAndKeepLiveFiles(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init").CombinedOutput(); err != nil {
		t.Fatalf("%s %v", out, err)
	}
	path := filepath.Join(root, "version")
	os.WriteFile(path, []byte("captured\n"), 0644)
	s, err := session.Open(repo.Git(root), false, "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	os.WriteFile(path, []byte("live\n"), 0644)
	r := Run(context.Background(), repo.Git(root), s.Baseline, "cat version; echo check-edit > version; test ! -e .git")
	if !r.SourceChanged || r.ExitCode != 0 || r.Status != "Passed" || !strings.Contains(r.Output, "captured") || r.Tree != s.Baseline {
		t.Fatalf("wrong result: %+v", r)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "live\n" {
		t.Fatal("check modified live file")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	r = Run(ctx, repo.Git(root), s.Baseline, "sleep 30")
	if r.Status != "Cancelled" || time.Since(start) > 3*time.Second {
		t.Fatalf("check cancellation failed: %+v", r)
	}
}

func TestChecksRejectExternalSourceSymlink(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init").CombinedOutput(); err != nil {
		t.Fatalf("%s %v", out, err)
	}
	outside := filepath.Join(t.TempDir(), "live")
	os.WriteFile(outside, []byte("keep"), 0644)
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	s, err := session.Open(repo.Git(root), false, "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r := Run(context.Background(), repo.Git(root), s.Baseline, "echo changed > link")
	if r.Status != "Failed" || r.ExitCode != -1 {
		t.Fatalf("external source was used: %+v", r)
	}
	data, _ := os.ReadFile(outside)
	if string(data) != "keep" {
		t.Fatal("external source modified")
	}
}

func TestChecksRejectAbsoluteExternalSymlinkReportsPath(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init").CombinedOutput(); err != nil {
		t.Fatalf("%s %v", out, err)
	}
	outside := filepath.Join(t.TempDir(), "live")
	if err := os.WriteFile(outside, []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(nested, "link")); err != nil {
		t.Fatal(err)
	}
	s, err := session.Open(repo.Git(root), false, "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r := Run(context.Background(), repo.Git(root), s.Baseline, "echo changed > nested/link")
	if r.Status != "Failed" || r.ExitCode != -1 || !strings.Contains(r.Output, "snapshot symlink points outside captured code:") || !strings.Contains(r.Output, "nested/link") {
		t.Fatalf("absolute external symlink was not reported: %+v", r)
	}
	data, err := os.ReadFile(outside)
	if err != nil || string(data) != "keep" {
		t.Fatalf("outside file changed: %q, %v", data, err)
	}
}

func TestChecksAcceptInProjectSymlink(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init").CombinedOutput(); err != nil {
		t.Fatalf("%s %v", out, err)
	}
	if err := os.WriteFile(filepath.Join(root, "target.txt"), []byte("captured content\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target.txt", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	s, err := session.Open(repo.Git(root), false, "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r := Run(context.Background(), repo.Git(root), s.Baseline, "cat link")
	if r.Status != "Passed" || r.ExitCode != 0 || !strings.Contains(r.Output, "captured content") || strings.Contains(r.Output, "Prepare snapshot:") || strings.Contains(r.Output, "points outside captured code") {
		t.Fatalf("in-project symlink rejected: %+v", r)
	}
}

func TestChecksAcceptAbsoluteInProjectSymlinkThroughSymlinkedTempRoot(t *testing.T) {
	dir, err := os.MkdirTemp("", "stvena-symlink-root-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	alias := dir + "-alias"
	if err := os.Symlink(dir, alias); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(alias) })
	root := filepath.Join(alias, "work")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "target.txt")
	if err := os.WriteFile(target, []byte("captured\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := sourceHashes(root); err != nil {
		t.Fatalf("absolute in-project symlink rejected: %v", err)
	}
}

func TestChecksRejectRelativeEscapingSymlink(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init").CombinedOutput(); err != nil {
		t.Fatalf("%s %v", out, err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0755); err != nil {
		t.Fatal(err)
	}
	target, err := filepath.Rel(nested, outside)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(nested, "link")); err != nil {
		t.Fatal(err)
	}
	s, err := session.Open(repo.Git(root), false, "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r := Run(context.Background(), repo.Git(root), s.Baseline, "echo changed > nested/link")
	if r.Status != "Failed" || r.ExitCode != -1 || !strings.Contains(r.Output, "snapshot symlink points outside captured code:") || !strings.Contains(r.Output, "nested/link") {
		t.Fatalf("relative external symlink accepted: %+v", r)
	}
	data, err := os.ReadFile(outside)
	if err != nil || string(data) != "keep" {
		t.Fatalf("outside file changed: %q, %v", data, err)
	}
}

func TestChecksAcceptSymlinkChainAndDirectory(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init").CombinedOutput(); err != nil {
		t.Fatalf("%s %v", out, err)
	}
	if err := os.Mkdir(filepath.Join(root, "dir"), 0755); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{"file.txt": "chain content\n", "dir/file.txt": "directory content\n"} {
		if err := os.WriteFile(filepath.Join(root, path), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	for link, target := range map[string]string{"dirlink": "dir", "a": "b", "b": "file.txt"} {
		if err := os.Symlink(target, filepath.Join(root, link)); err != nil {
			t.Fatal(err)
		}
	}
	s, err := session.Open(repo.Git(root), false, "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r := Run(context.Background(), repo.Git(root), s.Baseline, "cat dirlink/file.txt a; echo changed > dirlink/file.txt")
	if r.Status != "Passed" || r.ExitCode != 0 || !r.SourceChanged || !strings.Contains(r.Output, "directory content") || !strings.Contains(r.Output, "chain content") {
		t.Fatalf("symlink chain or directory rejected: %+v", r)
	}
}

func TestChecksKeepSourceUnchangedForUntouchedSymlinkTree(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init").CombinedOutput(); err != nil {
		t.Fatalf("%s %v", out, err)
	}
	if err := os.Mkdir(filepath.Join(root, "dir"), 0755); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{"file.txt": "chain content\n", "dir/file.txt": "directory content\n"} {
		if err := os.WriteFile(filepath.Join(root, path), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	for link, target := range map[string]string{"dirlink": "dir", "a": "b", "b": "file.txt", "gone": "missing.txt"} {
		if err := os.Symlink(target, filepath.Join(root, link)); err != nil {
			t.Fatal(err)
		}
	}
	s, err := session.Open(repo.Git(root), false, "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r := Run(context.Background(), repo.Git(root), s.Baseline, "cat a dirlink/file.txt")
	if r.Status != "Passed" || r.ExitCode != 0 || r.SourceChanged || !strings.Contains(r.Output, "chain content") || !strings.Contains(r.Output, "directory content") {
		t.Fatalf("read-only symlink check changed source: %+v", r)
	}
}

func TestChecksAcceptDanglingInProjectSymlink(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init").CombinedOutput(); err != nil {
		t.Fatalf("%s %v", out, err)
	}
	if err := os.Symlink("missing.txt", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	s, err := session.Open(repo.Git(root), false, "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r := Run(context.Background(), repo.Git(root), s.Baseline, "echo ok")
	if r.Status != "Passed" || r.ExitCode != 0 || !strings.Contains(r.Output, "ok") || strings.Contains(r.Output, "Prepare snapshot:") {
		t.Fatalf("dangling in-project symlink rejected: %+v", r)
	}
}

func TestChecksRejectDanglingExternalSymlink(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init").CombinedOutput(); err != nil {
		t.Fatalf("%s %v", out, err)
	}
	if err := os.Symlink("/nonexistent-outside/file", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	s, err := session.Open(repo.Git(root), false, "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r := Run(context.Background(), repo.Git(root), s.Baseline, "echo should-not-run")
	if r.Status != "Failed" || r.ExitCode != -1 || !strings.Contains(r.Output, "snapshot symlink points outside captured code:") {
		t.Fatalf("dangling external symlink accepted: %+v", r)
	}
}

func TestChecksRejectSymlinkLoop(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init").CombinedOutput(); err != nil {
		t.Fatalf("%s %v", out, err)
	}
	if err := os.Symlink("loop2", filepath.Join(root, "loop")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("loop", filepath.Join(root, "loop2")); err != nil {
		t.Fatal(err)
	}
	s, err := session.Open(repo.Git(root), false, "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	r := Run(ctx, repo.Git(root), s.Baseline, "echo should-not-run")
	if r.Status != "Failed" || r.ExitCode != -1 || !strings.Contains(r.Output, "cannot capture symlink") {
		t.Fatalf("symlink loop was not rejected: %+v", r)
	}
}

func TestCancelledBeforeSnapshotPreparation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := Run(ctx, repo.Git(t.TempDir()), strings.Repeat("a", 40), "echo should-not-run")
	if r.Status != "Cancelled" || r.ExitCode != -1 {
		t.Fatalf("preparation cancellation mislabeled: %+v", r)
	}
}

func TestChecksAcceptDanglingLinkThroughInProjectFile(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init").CombinedOutput(); err != nil {
		t.Fatalf("%s %v", out, err)
	}
	if err := os.WriteFile(filepath.Join(root, "target.txt"), []byte("captured\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target.txt/sub", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	s, err := session.Open(repo.Git(root), false, "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r := Run(context.Background(), repo.Git(root), s.Baseline, "echo ok")
	if r.Status != "Passed" || r.ExitCode != 0 || !strings.Contains(r.Output, "ok") || strings.Contains(r.Output, "Prepare snapshot:") || strings.Contains(r.Output, "cannot capture symlink") {
		t.Fatalf("in-project dangling link rejected: %+v", r)
	}
}

func TestChecksRejectDanglingLinkThroughExternalFile(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init").CombinedOutput(); err != nil {
		t.Fatalf("%s %v", out, err)
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("keep\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "sub"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	s, err := session.Open(repo.Git(root), false, "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r := Run(context.Background(), repo.Git(root), s.Baseline, "echo should-not-run")
	if r.Status != "Failed" || r.ExitCode != -1 || !strings.Contains(r.Output, "snapshot symlink points outside captured code:") {
		t.Fatalf("external dangling link accepted: %+v", r)
	}
}
