package repo

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Entry maps a reviewed root to the directory holding its editor descriptors.
// An editor extension resolves a workspace folder with Git first and falls back
// to these entries, so a folder that is not a repository can still be found.
// The entry is a map of paths and nothing else: a consumer reads Dir and GitDir
// as locations, and never derives a command from them.
type Entry struct {
	Version   int       `json:"version"`
	Root      string    `json:"root"`
	RealRoot  string    `json:"realRoot"`
	Mode      Mode      `json:"mode"`
	Dir       string    `json:"dir"`
	GitDir    string    `json:"gitDir"`
	UpdatedAt time.Time `json:"updatedAt"`
}

const (
	entryVersion = 1
	entryMaxAge  = 30 * 24 * time.Hour
	entryScanMax = 1000
)

// Home is Stvena's own directory, overridable so tests and editor host suites
// never write into a real home.
func Home() string {
	if home := os.Getenv("STVENA_HOME"); home != "" {
		return home
	}
	dir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, ".stvena")
}

// BridgesDir holds one entry per reviewed root.
func BridgesDir() string {
	home := Home()
	if home == "" {
		return ""
	}
	return filepath.Join(home, "bridges")
}

func (w Workspace) entry() Entry {
	real, err := filepath.EvalSymlinks(w.Root)
	if err != nil {
		real = w.Root
	}
	return Entry{Version: entryVersion, Root: w.Root, RealRoot: real, Mode: w.Mode,
		Dir: w.DescriptorDir(), GitDir: w.GitDir, UpdatedAt: time.Now().UTC()}
}

// Publish records where this workspace's descriptors are. It is written in both
// modes: in a Git repository the entry is redundant with rev-parse, and that is
// the point, because the fallback path stays exercised rather than only running
// in the case nobody tests.
func Publish(ws Workspace) error {
	dir := BridgesDir()
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	return atomicJSON(filepath.Join(dir, Key(ws.Root)+".json"), ws.entry())
}

// Prune drops entries for roots that no longer exist and entries nothing has
// refreshed in a month. Entries are never removed on exit: they are a map, not
// a liveness signal, and liveness already comes from the descriptor heartbeat.
func Prune() {
	dir := BridgesDir()
	if dir == "" {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) > entryScanMax {
		return
	}
	for _, file := range entries {
		path := filepath.Join(dir, file.Name())
		entry, readErr := readEntry(path)
		if readErr != nil {
			continue
		}
		if _, statErr := os.Stat(entry.Root); os.IsNotExist(statErr) ||
			time.Since(entry.UpdatedAt) > entryMaxAge {
			_ = os.Remove(path)
		}
	}
}

// Lookup resolves a folder to the entry whose root contains it. The longest
// matching root wins, which answers both "the folder is a subdirectory of the
// reviewed root" and "one reviewed root sits inside another" with one rule. An
// entry whose root does not contain the folder is discarded, so a stray file
// can never point a consumer at an unrelated tree.
func Lookup(folder string) (Entry, bool) {
	dir := BridgesDir()
	if dir == "" {
		return Entry{}, false
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) > entryScanMax {
		return Entry{}, false
	}
	target := folder
	if real, evalErr := filepath.EvalSymlinks(folder); evalErr == nil {
		target = real
	}
	var matches []Entry
	for _, file := range files {
		entry, readErr := readEntry(filepath.Join(dir, file.Name()))
		if readErr != nil {
			continue
		}
		if contains(entry.RealRoot, target) || contains(entry.Root, folder) {
			matches = append(matches, entry)
		}
	}
	if len(matches) == 0 {
		return Entry{}, false
	}
	sort.Slice(matches, func(i, j int) bool { return len(matches[i].RealRoot) > len(matches[j].RealRoot) })
	return matches[0], true
}

func contains(root, path string) bool {
	if root == "" || path == "" {
		return false
	}
	root, path = filepath.Clean(root), filepath.Clean(path)
	return root == path || strings.HasPrefix(path, root+string(filepath.Separator))
}

func readEntry(path string) (Entry, error) {
	if !strings.HasSuffix(path, ".json") {
		return Entry{}, os.ErrInvalid
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) > 8*1024 {
		if err == nil {
			err = os.ErrInvalid
		}
		return Entry{}, err
	}
	var entry Entry
	if err = json.Unmarshal(data, &entry); err != nil {
		return Entry{}, err
	}
	if entry.Version != entryVersion || (entry.Mode != ModeGit && entry.Mode != ModeShadow) ||
		entry.UpdatedAt.IsZero() || !absolute(entry.Root) || !absolute(entry.RealRoot) ||
		!absolute(entry.Dir) || !absolute(entry.GitDir) {
		return Entry{}, os.ErrInvalid
	}
	return entry, nil
}

func absolute(path string) bool {
	return path != "" && !strings.ContainsRune(path, 0) && filepath.IsAbs(path)
}
