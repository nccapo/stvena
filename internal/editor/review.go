package editor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/nccapo/stvena/internal/session"
)

type ReviewFocus struct {
	Tree    string `json:"tree"`
	Source  string `json:"source"`
	Path    string `json:"path"`
	Line    int    `json:"line"`
	EndLine int    `json:"endLine"`
}

// ReviewState is a small, source-only companion protocol. It intentionally
// contains no patch text: the TUI remains the authoritative review surface.
type ReviewState struct {
	Version         int          `json:"version"`
	Session         string       `json:"session"`
	Sequence        uint64       `json:"sequence"`
	Active          bool         `json:"active"`
	UpdatedAt       time.Time    `json:"updatedAt"`
	ChangedAt       time.Time    `json:"changedAt,omitempty"`
	Focus           *ReviewFocus `json:"focus,omitempty"`
	UnreviewedFiles int          `json:"unreviewedFiles"`
	UnreviewedHunks int          `json:"unreviewedHunks"`
	NewerBatches    int          `json:"newerBatches"`
}

type ReviewPublisher struct {
	path, session string
	state         ReviewState
	writtenAt     time.Time
}

func OpenReview(saved *session.Session) (*ReviewPublisher, error) {
	dir, err := repositoryGitDir(saved.Root)
	if err != nil {
		return nil, err
	}
	return &ReviewPublisher{path: filepath.Join(dir, "stvena-review.json"), session: saved.ID,
		state: ReviewState{Version: 1, Session: saved.ID, Active: true}}, nil
}

func (p *ReviewPublisher) Publish(focus *ReviewFocus, files, hunks, newer int) error {
	next := p.state
	changed := !reflect.DeepEqual(next.Focus, focus) || next.UnreviewedFiles != files ||
		next.UnreviewedHunks != hunks || next.NewerBatches != newer
	if !changed && !p.writtenAt.IsZero() && time.Since(p.writtenAt) < 5*time.Second {
		return nil
	}
	if focus != nil {
		copy := *focus
		next.Focus = &copy
	} else {
		next.Focus = nil
	}
	if changed {
		next.Sequence++
		next.ChangedAt = time.Now().UTC()
	}
	next.UnreviewedFiles, next.UnreviewedHunks, next.NewerBatches = files, hunks, newer
	next.UpdatedAt = time.Now().UTC()
	if err := session.AtomicJSON(p.path, next); err != nil {
		return err
	}
	p.state, p.writtenAt = next, time.Now()
	return nil
}

func (p *ReviewPublisher) Close() {
	data, err := os.ReadFile(p.path)
	var current ReviewState
	if err != nil || json.Unmarshal(data, &current) != nil || current.Session != p.session {
		return
	}
	p.state.Active = false
	p.state.UpdatedAt = time.Now().UTC()
	_ = session.AtomicJSON(p.path, p.state)
}

type Request struct {
	Version   int       `json:"version"`
	Session   string    `json:"session"`
	ID        string    `json:"id"`
	Action    string    `json:"action"`
	Path      string    `json:"path"`
	Line      int       `json:"line"`
	EndLine   int       `json:"endLine"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type RequestReader struct {
	path, session, lastID string
}

func OpenRequests(saved *session.Session) (*RequestReader, error) {
	dir, err := repositoryGitDir(saved.Root)
	if err != nil {
		return nil, err
	}
	reader := &RequestReader{path: filepath.Join(dir, "stvena-request.json"), session: saved.ID}
	// A saved session can be reopened. Never replay a request left for the
	// previous process; only descriptors replaced after this reader opens count.
	if data, readErr := os.ReadFile(reader.path); readErr == nil {
		var previous Request
		if json.Unmarshal(data, &previous) == nil {
			reader.lastID = previous.ID
		}
	}
	return reader, nil
}

func (r *RequestReader) Poll() (*Request, error) {
	data, err := os.ReadFile(r.path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(data) > 64*1024 {
		return nil, fmt.Errorf("editor request exceeds 64 KiB")
	}
	var request Request
	if err := json.Unmarshal(data, &request); err != nil {
		return nil, fmt.Errorf("read editor request: %w", err)
	}
	if request.ID == r.lastID || request.Session != r.session {
		return nil, nil
	}
	r.lastID = request.ID
	if request.Version != 1 || request.ID == "" || request.Action != "review" && request.Action != "context" ||
		!validRequestPath(request.Path) || request.Line < 1 || request.EndLine < request.Line ||
		request.UpdatedAt.IsZero() || time.Since(request.UpdatedAt) > time.Minute || time.Since(request.UpdatedAt) < -time.Minute {
		return nil, fmt.Errorf("invalid or expired editor request")
	}
	return &request, nil
}

func validRequestPath(path string) bool {
	if path == "" || strings.ContainsRune(path, 0) || filepath.IsAbs(path) {
		return false
	}
	clean := filepath.Clean(filepath.FromSlash(path))
	return clean != "." && clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator))
}

func repositoryGitDir(root string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "--absolute-git-dir").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
