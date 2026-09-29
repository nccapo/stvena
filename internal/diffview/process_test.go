package diffview

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/nccapo/stvena/internal/repo"
)

// pgrpEnv makes this test binary print its process group and exit, so the
// Git stand-in below can report one without ps (sandboxes may forbid ps).
const pgrpEnv = "STVENA_TEST_PRINT_PGRP"

func init() {
	if os.Getenv(pgrpEnv) == "1" {
		fmt.Println(syscall.Getpgrp())
		os.Exit(0)
	}
}

func TestRefreshGitRunsOutsideTerminalForegroundGroup(t *testing.T) {
	bin := t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// The stand-in reports the process group inherited by a Git invocation;
	// exec keeps the group.
	script := fmt.Sprintf("#!/bin/sh\n%s=1 exec '%s'\n", pgrpEnv, strings.ReplaceAll(self, "'", `'\''`))
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := gitOutput(repo.Git(t.TempDir()), "status")
	if err != nil {
		t.Fatal(err)
	}
	group, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatal(err)
	}
	if group == syscall.Getpgrp() {
		t.Fatal("refresh Git shares stvena's terminal process group")
	}
}
