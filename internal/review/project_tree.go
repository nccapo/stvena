package review

import (
	"path"
	"sort"
)

type ProjectEntry struct {
	Path             string
	Directory        bool
	Depth, FileIndex int
}

// Folder state is separate from file selection so opening/collapsing a folder
// never makes it a source file, a staging target or a context attachment.
func (s *State) rebuildProjectTree(preferred string) {
	selected := s.Selected
	defer func() {
		if !s.Browser {
			s.Selected = selected
		}
	}()
	if preferred == "" && s.TreeIndex >= 0 && s.TreeIndex < len(s.ProjectRows) {
		row := s.ProjectRows[s.TreeIndex]
		if s.Query == "" || !row.Directory {
			preferred = row.Path
		}
	}
	entries := map[string]ProjectEntry{}
	children := map[string][]string{}
	for index, fileIndex := range s.Indices {
		name := s.Snapshot.Files[fileIndex].Path
		entries[name] = ProjectEntry{Path: name, FileIndex: index}
		parent := path.Dir(name)
		if parent == "." {
			parent = ""
		}
		children[parent] = append(children[parent], name)
		for parent != "" {
			if _, exists := entries[parent]; exists {
				break
			}
			entries[parent] = ProjectEntry{Path: parent, Directory: true, FileIndex: -1}
			upper := path.Dir(parent)
			if upper == "." {
				upper = ""
			}
			children[upper] = append(children[upper], parent)
			parent = upper
		}
	}
	for parent := range children {
		sort.Slice(children[parent], func(i, j int) bool {
			a, b := entries[children[parent][i]], entries[children[parent][j]]
			if a.Directory != b.Directory {
				return a.Directory
			}
			return a.Path < b.Path
		})
	}
	s.ProjectRows = nil
	var visit func(string, int)
	visit = func(parent string, depth int) {
		for _, name := range children[parent] {
			row := entries[name]
			row.Depth = depth
			s.ProjectRows = append(s.ProjectRows, row)
			if row.Directory && (s.ExpandedFolders[name] || s.Query != "") {
				visit(name, depth+1)
			}
		}
	}
	visit("", 0)
	s.TreeIndex = 0
	if s.Query != "" {
		for i, row := range s.ProjectRows {
			if !row.Directory && row.Path == preferred {
				s.selectTreeRow(i)
				return
			}
		}
		for i, row := range s.ProjectRows {
			if !row.Directory {
				s.selectTreeRow(i)
				return
			}
		}
	} else {
		// If a selected entry disappears, retain the closest visible ancestor.
		for candidate := preferred; candidate != "" && candidate != "."; candidate = path.Dir(candidate) {
			for i, row := range s.ProjectRows {
				if row.Path == candidate {
					s.selectTreeRow(i)
					return
				}
			}
		}
	}
	s.selectTreeRow(0)
}

func (s *State) selectTreeRow(index int) {
	s.TreeIndex = min(max(0, len(s.ProjectRows)-1), max(0, index))
	if len(s.ProjectRows) > 0 && !s.ProjectRows[s.TreeIndex].Directory {
		s.Selected = s.ProjectRows[s.TreeIndex].FileIndex
	}
}

func (s *State) RevealProjectFile() {
	if s.Source != "project" || s.Selected < 0 || s.Selected >= len(s.Indices) {
		return
	}
	name := s.Snapshot.Files[s.Indices[s.Selected]].Path
	if s.ExpandedFolders == nil {
		s.ExpandedFolders = map[string]bool{}
	}
	for parent := path.Dir(name); parent != "." && parent != ""; parent = path.Dir(parent) {
		s.ExpandedFolders[parent] = true
	}
	s.rebuildProjectTree(name)
}

func (s *State) ActivateProjectRow(index int) {
	if index < 0 || index >= len(s.ProjectRows) {
		return
	}
	s.selectTreeRow(index)
	row := s.ProjectRows[index]
	if row.Directory {
		if s.Query != "" {
			s.Notice = "Folders stay expanded while filtering · Esc: clear filter"
			return
		}
		if s.ExpandedFolders == nil {
			s.ExpandedFolders = map[string]bool{}
		}
		s.ExpandedFolders[row.Path] = !s.ExpandedFolders[row.Path]
		s.rebuildProjectTree(row.Path)
		return
	}
	s.Scroll, s.Horizontal, s.TargetLine = 0, 0, 0
	s.ClearSelection()
	s.Browser, s.PatchFocused, s.FullFile = false, true, true
}

func (s *State) projectTreeKey(key string, visible int) bool {
	if s.Source != "project" || !s.Browser || s.Menu || s.Panel != "" || s.Prompt != "" || s.ConfirmAction != "" {
		return false
	}
	switch key {
	case "up", "k":
		s.selectTreeRow(s.TreeIndex - 1)
	case "down", "j":
		s.selectTreeRow(s.TreeIndex + 1)
	case "pageup", "u":
		s.selectTreeRow(s.TreeIndex - max(1, visible/2))
	case "pagedown", "d":
		s.selectTreeRow(s.TreeIndex + max(1, visible/2))
	case "home", "g":
		s.selectTreeRow(0)
	case "end", "G":
		s.selectTreeRow(len(s.ProjectRows) - 1)
	case "enter", " ":
		s.ActivateProjectRow(s.TreeIndex)
	case "right", "l":
		if len(s.ProjectRows) == 0 {
			return true
		}
		row := s.ProjectRows[s.TreeIndex]
		if row.Directory && (s.ExpandedFolders[row.Path] || s.Query != "") {
			s.selectTreeRow(s.TreeIndex + 1)
		} else {
			s.ActivateProjectRow(s.TreeIndex)
		}
	case "left", "h", "backspace":
		if len(s.ProjectRows) == 0 {
			return true
		}
		row := s.ProjectRows[s.TreeIndex]
		if row.Directory && s.ExpandedFolders[row.Path] && s.Query == "" {
			s.ActivateProjectRow(s.TreeIndex)
		} else {
			parent := path.Dir(row.Path)
			for i, entry := range s.ProjectRows {
				if entry.Directory && entry.Path == parent {
					s.selectTreeRow(i)
					break
				}
			}
		}
	case "n", "p":
		step := 1
		if key == "p" {
			step = -1
		}
		for i := s.TreeIndex + step; i >= 0 && i < len(s.ProjectRows); i += step {
			if !s.ProjectRows[i].Directory {
				s.selectTreeRow(i)
				break
			}
		}
	default:
		return false
	}
	return true
}

func (s *State) ProjectFolderSelected() bool {
	return s.Source == "project" && s.Browser && s.TreeIndex >= 0 && s.TreeIndex < len(s.ProjectRows) && s.ProjectRows[s.TreeIndex].Directory
}

// Search temporarily reveals ancestor folders without changing saved expansion.
func (s *State) ProjectFolderExpanded(name string) bool {
	return s.ExpandedFolders[name] || s.Query != ""
}

func (s *State) FileListCount() int {
	if s.Source == "project" {
		return len(s.ProjectRows)
	}
	return len(s.Indices)
}
