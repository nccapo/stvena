package app

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/nccapo/stvena/internal/review"
	"github.com/nccapo/stvena/internal/session"
)

type preferences struct {
	Ratio            int
	Wrap, SideBySide bool
	Hotkeys          map[string]string `json:",omitempty"` // Read older repository settings.
}

type hotkeyPreferences struct {
	Hotkeys map[string]string
}

var preferencesConfigDir = os.UserConfigDir

func hotkeysPath() (string, error) {
	dir, err := preferencesConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "stvena", "hotkeys.json"), nil
}

func (s *screenState) loadPreferences() {
	var p preferences
	if dir, err := session.RepoDir(s.root); err == nil {
		if data, err := os.ReadFile(filepath.Join(dir, "layout.json")); err == nil && json.Unmarshal(data, &p) == nil {
			if p.Ratio >= 25 && p.Ratio <= 75 {
				s.ratio = p.Ratio
			}
			s.review.Wrap = p.Wrap
			s.review.SideBySide = p.SideBySide
		} else {
			p = preferences{}
		}
	}
	s.loadHotkeys(p.Hotkeys)
	s.relayout(s.layout.Width, s.layout.Height)
}

func (s *screenState) loadHotkeys(legacy map[string]string) {
	s.review.Hotkeys = nil
	s.review.HotkeysDirty = false
	path, err := hotkeysPath()
	if err != nil {
		s.review.Notice = "Could not load hotkeys: " + err.Error()
		return
	}
	data, err := os.ReadFile(path)
	var p hotkeyPreferences
	switch {
	case os.IsNotExist(err):
		p.Hotkeys = legacy
	case err != nil:
		s.review.Notice = "Could not load hotkeys: " + err.Error()
		return
	default:
		if err := json.Unmarshal(data, &p); err != nil {
			s.review.Notice = "Invalid saved hotkeys; using defaults: " + err.Error()
			return
		}
	}
	if err := review.ValidateHotkeys(p.Hotkeys); err != nil {
		s.review.Notice = "Invalid saved hotkeys; using defaults: " + err.Error()
		return
	}
	s.review.Hotkeys = p.Hotkeys
	// The first opened project with custom bindings seeds the shared settings.
	// An existing shared file, including an explicit reset, always wins.
	if os.IsNotExist(err) && len(legacy) > 0 {
		s.review.HotkeysDirty = true
		if err := s.saveHotkeys(); err != nil {
			s.review.Notice = "Hotkeys active, but could not save: " + err.Error()
		}
	}
}

func (s *screenState) saveHotkeys() error {
	if !s.review.HotkeysDirty {
		return nil
	}
	path, err := hotkeysPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if err := session.AtomicJSON(path, hotkeyPreferences{Hotkeys: s.review.Hotkeys}); err != nil {
		return err
	}
	s.review.HotkeysDirty = false
	return nil
}

func (s *screenState) savePreferences() error {
	// Unedited windows must not overwrite shortcuts saved by another project.
	if err := s.saveHotkeys(); err != nil {
		return err
	}
	dir, err := session.RepoDir(s.root)
	if err != nil {
		return err
	}
	return session.AtomicJSON(filepath.Join(dir, "layout.json"), preferences{
		Ratio: s.ratio, Wrap: s.review.Wrap, SideBySide: s.review.SideBySide,
	})
}
