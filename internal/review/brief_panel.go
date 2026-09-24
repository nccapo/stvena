package review

func (s *State) selectedBriefRow() (BriefRow, bool) {
	rows := s.BriefRows()
	if len(rows) == 0 {
		return BriefRow{}, false
	}
	s.BriefIndex = min(max(0, s.BriefIndex), len(rows)-1)
	return rows[s.BriefIndex], true
}

func (s *State) briefKey(key string, visible int) bool {
	if s.Panel == "Brief comments" {
		switch key {
		case "esc":
			s.Panel = "Brief"
		case "up", "k":
			s.BriefCommentIndex = max(0, s.BriefCommentIndex-1)
		case "down", "j":
			s.BriefCommentIndex = min(max(0, len(s.Comments)-1), s.BriefCommentIndex+1)
		case "pageup":
			s.BriefCommentIndex = max(0, s.BriefCommentIndex-max(1, visible/2))
		case "pagedown":
			s.BriefCommentIndex = min(max(0, len(s.Comments)-1), s.BriefCommentIndex+max(1, visible/2))
		case "enter":
			if len(s.Comments) == 0 {
				s.Notice = "No saved comments to attach"
				break
			}
			if row, ok := s.selectedBriefRow(); ok {
				if err := s.AttachCommentEvidence(row.Item, s.Comments[s.BriefCommentIndex].ID); err != nil {
					s.Notice = err.Error()
				} else {
					s.Panel = "Brief"
				}
			}
		}
		return true
	}
	if s.Panel != "Brief" {
		return false
	}
	row, ok := s.selectedBriefRow()
	switch key {
	case "esc", "J":
		s.Panel = ""
		s.PanelScroll = 0
	case "up", "k":
		s.BriefIndex = max(0, s.BriefIndex-1)
	case "down", "j":
		s.BriefIndex = min(max(0, len(s.BriefRows())-1), s.BriefIndex+1)
	case "pageup":
		s.BriefIndex = max(0, s.BriefIndex-max(1, visible/2))
	case "pagedown":
		s.BriefIndex = min(max(0, len(s.BriefRows())-1), s.BriefIndex+max(1, visible/2))
	case "a":
		s.Prompt, s.Input = "Brief item", ""
	case "i":
		if ok {
			s.Prompt, s.Input = "Edit brief item", s.Brief[row.Item].Text
		}
	case "d", "delete", "backspace":
		if ok {
			if row.Evidence >= 0 {
				s.RemoveBriefEvidence(row.Item, row.Evidence)
			} else {
				s.DeleteBriefItem(row.Item)
			}
			s.BriefIndex = min(s.BriefIndex, max(0, len(s.BriefRows())-1))
		}
	case "[", "]":
		if ok {
			delta := -1
			if key == "]" {
				delta = 1
			}
			if to := row.Item + delta; to >= 0 && to < len(s.Brief) {
				s.MoveBriefItem(row.Item, delta)
				for i, r := range s.BriefRows() {
					if r.Item == to && r.Evidence == row.Evidence {
						s.BriefIndex = i
						break
					}
				}
			}
		}
	case " ":
		if ok {
			s.ToggleBriefItemDone(row.Item)
			s.Notice = "Marked by hand"
		}
	case "x":
		if ok {
			if err := s.AttachSelectionEvidence(row.Item); err != nil {
				s.Notice = err.Error()
			}
		} else {
			s.Notice = "Add a brief item first"
		}
	case "t":
		if ok {
			if err := s.AttachCheckEvidence(row.Item); err != nil {
				s.Notice = err.Error()
			}
		} else {
			s.Notice = "Add a brief item first"
		}
	case "c":
		if ok {
			if len(s.Comments) == 0 {
				s.Notice = "No saved comments to attach"
			} else {
				s.Panel, s.BriefCommentIndex = "Brief comments", 0
			}
		} else {
			s.Notice = "Add a brief item first"
		}
	case "enter":
		if ok {
			if row.Evidence < 0 && len(s.Brief[row.Item].Evidence) == 0 {
				s.Notice = "This item has no evidence yet"
			} else {
				s.Request = "brief-open"
			}
		}
	}
	return true
}
