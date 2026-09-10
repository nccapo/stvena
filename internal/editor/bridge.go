// Package editor publishes captured edits for local editor integrations.
package editor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/nccapo/stvena/internal/diffview"
	"github.com/nccapo/stvena/internal/session"
)

// State is protocol v1. Files describes the most recent capture that changed,
// not the cumulative session diff. Heartbeats keep idle sessions discoverable.
type State struct {
	Version   int       `json:"version"`
	Session   string    `json:"session"`
	Sequence  uint64    `json:"sequence"`
	Active    bool      `json:"active"`
	UpdatedAt time.Time `json:"updatedAt"`
	Error     string    `json:"error,omitempty"`
	Files     []Change  `json:"files"`
	Activity  *Activity `json:"activity,omitempty"`
	ChangedAt time.Time `json:"changedAt,omitempty"`
}

type Change struct {
	Path      string      `json:"path"`
	OldPath   string      `json:"oldPath,omitempty"`
	Status    string      `json:"status"`
	Before    string      `json:"before"`
	After     string      `json:"after"`
	Line      int         `json:"line"`
	Added     int         `json:"added"`
	Deleted   int         `json:"deleted"`
	Binary    bool        `json:"binary"`
	Truncated bool        `json:"truncated"`
	Ranges    []LineRange `json:"ranges,omitempty"`
}

type Publisher struct {
	path, root, previous string
	state                State
	activityPath         string
}

func Open(saved *session.Session) (*Publisher, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", saved.Root, "rev-parse", "--absolute-git-dir").Output()
	if err != nil {
		return nil, err
	}
	return &Publisher{
		path: filepath.Join(strings.TrimSuffix(string(out), "\n"), "stvena-live.json"),
		root: saved.Root, previous: saved.Baseline,
		activityPath: ActivityPath(saved),
		state:        State{Version: 1, Session: saved.ID, Active: true, Files: []Change{}},
	}, nil
}

func (p *Publisher) Publish(tree string, captureErr error) error {
	next := p.state
	previous := p.previous
	next.Error = ""
	if captureErr == nil && tree != p.previous {
		delta := diffview.CompareTrees(p.root, p.previous, tree)
		captureErr = delta.Err
		if captureErr == nil {
			files := make([]Change, 0, len(delta.Files))
			for _, f := range delta.Files {
				files = append(files, Change{
					Path: f.Path, OldPath: f.OldPath, Status: f.Status,
					Before: f.BeforeOID, After: f.AfterOID, Line: changedLine(f.Lines),
					Added: f.Added, Deleted: f.Deleted, Binary: f.Binary, Truncated: f.Truncated,
					Ranges: changedRanges(f.Lines),
				})
			}
			next.Files = files
			next.Sequence++
			next.ChangedAt = time.Now().UTC()
			previous = tree
		}
	}
	if captureErr != nil {
		next.Error = captureErr.Error()
	}
	next.UpdatedAt = time.Now().UTC()
	next.Activity = nil
	if data, err := os.ReadFile(p.activityPath); err == nil && len(data) <= 64*1024 {
		var activity Activity
		if json.Unmarshal(data, &activity) == nil && activity.Session == next.Session &&
			time.Since(activity.UpdatedAt) < 15*time.Second && time.Since(activity.UpdatedAt) >= 0 {
			next.Activity = &activity
		}
	}
	if err := session.AtomicJSON(p.path, next); err != nil {
		return err
	}
	p.state, p.previous = next, previous
	return nil
}

func (p *Publisher) Close() {
	// Do not overwrite another process's descriptor after it has taken over.
	data, err := os.ReadFile(p.path)
	var current State
	if err != nil || json.Unmarshal(data, &current) != nil || current.Session != p.state.Session {
		return
	}
	p.state.Active = false
	p.state.UpdatedAt = time.Now().UTC()
	_ = session.AtomicJSON(p.path, p.state)
}

// Report the first changed line in the new file, skipping hunk context. A
// deletion points at its surviving neighbor, clamped by the editor at EOF.
func changedLine(lines []string) int {
	line, inHunk := 1, false
	for _, text := range lines {
		if strings.HasPrefix(text, "@@ ") {
			fields := strings.Fields(text)
			if len(fields) >= 3 {
				_, _ = fmt.Sscanf(strings.Split(fields[2], ",")[0], "+%d", &line)
				inHunk = true
			}
			continue
		}
		if !inHunk || text == "" {
			continue
		}
		switch text[0] {
		case '+', '-':
			return max(1, line)
		case ' ':
			line++
		}
	}
	return 1
}
