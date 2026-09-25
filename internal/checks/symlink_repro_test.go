package checks

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nccapo/stvena/internal/repo"
	"github.com/nccapo/stvena/internal/session"
)

// A relative symlink whose target sits inside the captured tree must be accepted and
// the check must run normally. The snapshot is created under TMPDIR, which on macOS is
// /var/folders/... where /var is itself a symlink to /private/var; the temporary TMPDIR
// below reproduces that shape on any platform so the test does not depend on how the
// ambient TMPDIR happens to be spelled.
func TestChecksAcceptInProjectSourceSymlink(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real-tmp")
	if err := os.Mkdir(real, 0700); err != nil {
		t.Fatal(err)
	}
	linkedTmp := filepath.Join(base, "tmp")
	if err := os.Symlink(real, linkedTmp); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", linkedTmp)

	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init").CombinedOutput(); err != nil {
		t.Fatalf("%s %v", out, err)
	}
	if err := os.WriteFile(filepath.Join(root, "target.txt"), []byte("captured\n"), 0644); err != nil {
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
	if r.Status != "Passed" || r.ExitCode != 0 || !strings.Contains(r.Output, "captured") {
		t.Fatalf("in-project symlink rejected: status=%q exit=%d output=%q", r.Status, r.ExitCode, r.Output)
	}
}
