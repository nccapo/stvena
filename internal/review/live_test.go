package review

import (
	"testing"

	"github.com/nccapo/stvena/internal/diffview"
)

func TestClearingSelectionResumesLiveChanges(t *testing.T) {
	for _, mouse := range []bool{false, true} {
		for _, key := range []string{"V", "P", "f", "esc", "backspace", "v", "view-diff"} {
			s := State{PatchFocused: true, FullFile: true}
			s.Update(diffview.Snapshot{Tree: "old", Files: []diffview.File{{Path: "file.go"}}})
			if mouse {
				s.SelectWithMouse(0, 0, false)
			} else {
				s.Key("V", 10)
			}
			s.Update(diffview.Snapshot{Tree: "new", Files: []diffview.File{{Path: "file.go"}, {Path: "added.go"}}})
			if !s.Pinned || s.Snapshot.Tree != "old" {
				t.Fatal("selection did not retain its captured code")
			}
			s.Key(key, 10)
			if s.Pinned || s.Selecting || s.Snapshot.Tree != "new" || len(s.Indices) != 2 {
				t.Errorf("mouse=%t key=%s: clearing selection left Changes frozen: pinned=%t selecting=%t tree=%s", mouse, key, s.Pinned, s.Selecting, s.Snapshot.Tree)
			}
		}
	}
}

func TestClearingSelectionPreservesExplicitPin(t *testing.T) {
	s := State{PatchFocused: true}
	s.Update(diffview.Snapshot{Tree: "old", Files: []diffview.File{{Path: "file.go"}}})
	s.Key("P", 10)
	s.SelectWithMouse(0, 0, false)
	s.Update(diffview.Snapshot{Tree: "new"})
	s.Key("V", 10)
	if !s.Pinned || s.Snapshot.Tree != "old" || s.Selecting {
		t.Fatal("clearing selection released an explicit pin")
	}
}
