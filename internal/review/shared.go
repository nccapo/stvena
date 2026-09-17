package review

import (
	"encoding/json"
	"maps"
	"os"
	"syscall"
	"time"
)

// marks are the review marks every Stvena in a project shares through
// reviews.json. Accepting a change in one session accepts it in the others,
// wherever they show the same change.
type marks struct {
	reviewed map[string][32]byte
	hunks    map[string]bool
}

func (m marks) clone() marks {
	return marks{reviewed: maps.Clone(m.reviewed), hunks: maps.Clone(m.hunks)}
}

// fileStamp identifies one version of reviews.json without reading it.
type fileStamp struct {
	mod  time.Time
	size int64
}

func stampOf(path string) fileStamp {
	info, err := os.Stat(path)
	if err != nil {
		return fileStamp{}
	}
	return fileStamp{mod: info.ModTime(), size: info.Size()}
}

// rebase applies what this session changed since base on top of theirs. Only
// this session's own changes travel: a mark another session added or removed
// in the meantime is kept as that session left it.
func rebase[K comparable, V comparable](base, mine, theirs map[K]V) map[K]V {
	out := maps.Clone(theirs)
	if out == nil {
		out = map[K]V{}
	}
	for key, value := range mine {
		if old, ok := base[key]; !ok || old != value {
			out[key] = value
		}
	}
	for key := range base {
		if _, ok := mine[key]; !ok {
			delete(out, key)
		}
	}
	return out
}

// mergeMarks folds the marks on disk into this session's and returns whether
// anything this session shows changed.
func (s *State) mergeMarks(disk marks) bool {
	reviewed := rebase(s.marksBase.reviewed, s.reviewed, disk.reviewed)
	hunks := rebase(s.marksBase.hunks, s.Hunks, disk.hunks)
	changed := !maps.Equal(reviewed, s.reviewed) || !maps.Equal(hunks, s.Hunks)
	s.reviewed, s.Hunks = reviewed, hunks
	s.marksBase = disk.clone()
	return changed
}

// mergeHistory keeps the most recent record of each file from either session.
func mergeHistory(mine, theirs map[string]Record) map[string]Record {
	out := maps.Clone(mine)
	if out == nil {
		out = map[string]Record{}
	}
	for key, record := range theirs {
		if current, ok := out[key]; !ok || record.At.After(current.At) {
			out[key] = record
		}
	}
	return out
}

func readSaved(path string) (Saved, bool) {
	var saved Saved
	data, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(data, &saved) != nil {
		return Saved{}, false
	}
	return saved, true
}

// SyncMarks picks up review marks another Stvena in the project saved. It only
// reads the file when it has changed, so it is cheap to call on every tick.
// It reports whether this session's marks changed.
func (s *State) SyncMarks() bool {
	if s.savePath == "" {
		return false
	}
	stamp := stampOf(s.savePath)
	if stamp == s.marksStamp {
		return false
	}
	saved, ok := readSaved(s.savePath)
	if !ok {
		// Mid-write or unreadable: keep what this session has and look again.
		return false
	}
	s.marksStamp = stamp
	s.History = mergeHistory(s.History, saved.History)
	return s.mergeMarks(marks{reviewed: saved.Reviewed, hunks: saved.Hunks})
}

// lockFile holds an exclusive lock on path until the returned function runs.
// Saving still works without it; the lock only closes the window in which two
// sessions saving at once could drop one's change.
func lockFile(path string) func() {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return func() {}
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		file.Close()
		return func() {}
	}
	return func() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		file.Close()
	}
}
