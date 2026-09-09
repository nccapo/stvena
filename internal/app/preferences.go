package app

import (
	"encoding/json"
	"github.com/nccapo/stvena/internal/review"
	"github.com/nccapo/stvena/internal/session"
	"os"
	"path/filepath"
)

type preferences struct {
	Ratio            int
	Wrap, SideBySide bool
	Hotkeys          map[string]string `json:",omitempty"`
}

func (s *screenState) loadPreferences() {
	dir, err := session.RepoDir(s.root)
	if err != nil {
		return
	}
	data, err := os.ReadFile(filepath.Join(dir, "layout.json"))
	if err != nil {
		return
	}
	var p preferences
	if json.Unmarshal(data, &p) != nil {
		return
	}
	if p.Ratio >= 25 && p.Ratio <= 75 {
		s.ratio = p.Ratio
	}
	s.review.Wrap = p.Wrap
	s.review.SideBySide = p.SideBySide
	s.review.Hotkeys = nil
	if err := review.ValidateHotkeys(p.Hotkeys); err != nil {
		s.review.Notice = "Invalid saved hotkeys; using defaults: " + err.Error()
	} else {
		s.review.Hotkeys = p.Hotkeys
	}
	s.relayout(s.layout.Width, s.layout.Height)
}
func (s *screenState) savePreferences() error {
	dir, err := session.RepoDir(s.root)
	if err != nil {
		return err
	}
	return session.AtomicJSON(filepath.Join(dir, "layout.json"), preferences{
		Ratio: s.ratio, Wrap: s.review.Wrap, SideBySide: s.review.SideBySide, Hotkeys: s.review.Hotkeys,
	})
}
