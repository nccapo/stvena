package diffview

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

func TestRefreshGitRunsOutsideTerminalForegroundGroup(t *testing.T) {
	bin := t.TempDir()
	// The stand-in reports the process group inherited by a Git invocation.
	err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\nps -o pgid= -p $$\n"), 0700)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := gitOutput(t.TempDir(), "status")
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
