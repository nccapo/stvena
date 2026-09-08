package app

import (
	"encoding/json"
	"github.com/nccapo/stvena/internal/session"
	"os"
	"path/filepath"
)

type preferences struct {
	Ratio            int
	Wrap, SideBySide bool
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
	s.relayout(s.layout.Width, s.layout.Height)
}
func (s *screenState) savePreferences() error {
	dir, err := session.RepoDir(s.root)
	if err != nil {
		return err
	}
	return session.AtomicJSON(filepath.Join(dir, "layout.json"), preferences{s.ratio, s.review.Wrap, s.review.SideBySide})
}
