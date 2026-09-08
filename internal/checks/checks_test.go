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
)

func TestChecksUseCapturedCodeAndKeepLiveFiles(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init").CombinedOutput(); err != nil {
		t.Fatalf("%s %v", out, err)
	}
	path := filepath.Join(root, "version")
	os.WriteFile(path, []byte("captured\n"), 0644)
	s, err := session.Open(root, false, "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	os.WriteFile(path, []byte("live\n"), 0644)
	r := Run(context.Background(), root, s.Baseline, "cat version; echo check-edit > version; test ! -e .git")
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
	r = Run(ctx, root, s.Baseline, "sleep 30")
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
	s, err := session.Open(root, false, "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r := Run(context.Background(), root, s.Baseline, "echo changed > link")
	if r.Status != "Failed" || r.ExitCode != -1 {
		t.Fatalf("external source was used: %+v", r)
	}
	data, _ := os.ReadFile(outside)
	if string(data) != "keep" {
		t.Fatal("external source modified")
	}
}

func TestCancelledBeforeSnapshotPreparation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := Run(ctx, t.TempDir(), strings.Repeat("a", 40), "echo should-not-run")
	if r.Status != "Cancelled" || r.ExitCode != -1 {
		t.Fatalf("preparation cancellation mislabeled: %+v", r)
	}
}
