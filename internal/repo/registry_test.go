package repo

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func publish(t *testing.T, root string, mode Mode) Workspace {
	t.Helper()
	dir, err := CacheDir(root)
	if err != nil {
		t.Fatal(err)
	}
	ws := Workspace{Root: root, GitDir: filepath.Join(root, ".git"), Dir: dir, Mode: mode}
	if mode == ModeShadow {
		ws.GitDir = ShadowPath(dir)
	}
	if err := Publish(ws); err != nil {
		t.Fatal(err)
	}
	return ws
}

func TestLookupFindsTheProjectAFolderBelongsTo(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	ws := publish(t, root, ModeShadow)
	nested := filepath.Join(root, "pkg", "inner")
	if err := os.MkdirAll(nested, 0700); err != nil {
		t.Fatal(err)
	}
	for _, folder := range []string{root, nested} {
		entry, ok := Lookup(folder)
		if !ok {
			t.Fatalf("no entry for %s", folder)
		}
		if entry.Dir != ws.DescriptorDir() || entry.Mode != ModeShadow {
			t.Fatalf("wrong entry for %s: %+v", folder, entry)
		}
	}
}

// Two reviewed projects, one inside the other: the folder belongs to the
// nearest one, not to whichever entry happens to be read first.
func TestLookupPrefersTheNearestProject(t *testing.T) {
	isolate(t)
	outer := t.TempDir()
	inner := filepath.Join(outer, "service")
	if err := os.MkdirAll(inner, 0700); err != nil {
		t.Fatal(err)
	}
	publish(t, outer, ModeShadow)
	want := publish(t, inner, ModeShadow)
	entry, ok := Lookup(filepath.Join(inner, "cmd"))
	if !ok {
		t.Fatal("no entry for a folder inside the nested project")
	}
	if entry.Dir != want.DescriptorDir() {
		t.Fatalf("outer project claimed a nested folder: %+v", entry)
	}
}

func TestLookupIgnoresEntriesThatDoNotContainTheFolder(t *testing.T) {
	isolate(t)
	publish(t, t.TempDir(), ModeShadow)
	if entry, ok := Lookup(t.TempDir()); ok {
		t.Fatalf("an unrelated project was offered: %+v", entry)
	}
}

func TestLookupRejectsUnusableEntries(t *testing.T) {
	isolate(t)
	dir := BridgesDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for name, body := range map[string]string{
		"broken.json":   "{",
		"version.json":  `{"version":2,"root":"` + root + `","realRoot":"` + root + `","mode":"shadow","dir":"/tmp","gitDir":"/tmp","updatedAt":"2026-01-01T00:00:00Z"}`,
		"mode.json":     `{"version":1,"root":"` + root + `","realRoot":"` + root + `","mode":"other","dir":"/tmp","gitDir":"/tmp","updatedAt":"2026-01-01T00:00:00Z"}`,
		"relative.json": `{"version":1,"root":"` + root + `","realRoot":"` + root + `","mode":"shadow","dir":"cache","gitDir":"cache","updatedAt":"2026-01-01T00:00:00Z"}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if entry, ok := Lookup(root); ok {
		t.Fatalf("an invalid entry was accepted: %+v", entry)
	}
}

func TestPublishReplacesTheEntryWhenAProjectGainsGit(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	publish(t, root, ModeShadow)
	want := publish(t, root, ModeGit)
	entry, ok := Lookup(root)
	if !ok {
		t.Fatal("no entry after republishing")
	}
	if entry.Mode != ModeGit || entry.Dir != want.GitDir {
		t.Fatalf("stale shadow entry survived: %+v", entry)
	}
	files, err := os.ReadDir(BridgesDir())
	if err != nil || len(files) != 1 {
		t.Fatalf("a project should keep exactly one entry: %v %v", files, err)
	}
}

func TestPruneDropsProjectsThatAreGone(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	publish(t, root, ModeShadow)
	stale := filepath.Join(BridgesDir(), Key("/vanished")+".json")
	if err := atomicJSON(stale, Entry{Version: 1, Root: "/vanished", RealRoot: "/vanished",
		Mode: ModeShadow, Dir: "/tmp", GitDir: "/tmp", UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	Prune()
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("an entry for a deleted project survived")
	}
	if _, ok := Lookup(root); !ok {
		t.Fatal("pruning dropped a live project")
	}
}

// Nothing is deleted on exit: the registry is a map, not a heartbeat. Liveness
// is the descriptor's own timestamp, so a project stays findable between runs.
func TestEntriesSurviveBetweenRuns(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	publish(t, root, ModeShadow)
	Prune()
	if _, ok := Lookup(root); !ok {
		t.Fatal("the project was forgotten between runs")
	}
}
