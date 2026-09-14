package editor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

// ReviewHunk is one change block located in the ordinary working file. It
// carries no patch text; the TUI remains the authoritative diff surface.
type ReviewHunk struct {
	ID       string `json:"id"`
	Start    int    `json:"start"`
	End      int    `json:"end"`
	Reviewed bool   `json:"reviewed,omitempty"`
	Rejected bool   `json:"rejected,omitempty"`
}

type ReviewFile struct {
	Path     string       `json:"path"`
	OldPath  string       `json:"oldPath,omitempty"`
	Status   string       `json:"status"`
	Reviewed bool         `json:"reviewed,omitempty"`
	Rejected bool         `json:"rejected,omitempty"`
	Binary   bool         `json:"binary,omitempty"`
	Hunks    []ReviewHunk `json:"hunks,omitempty"`
}

// PendingRejections describes work the user has refused that has not reached
// the working tree yet. AppliesAt is "now", "turn-end" or "manual".
type PendingRejections struct {
	Count     int    `json:"count"`
	AppliesAt string `json:"appliesAt"`
	Reason    string `json:"reason,omitempty"`
}

// RequestResult acknowledges the editor request Stvena last acted on. Without
// it an extension can only claim it sent a request, never that anything
// happened; Status is "applied", "queued" or "refused".
type RequestResult struct {
	ID      string    `json:"id"`
	Action  string    `json:"action"`
	Status  string    `json:"status"`
	Message string    `json:"message,omitempty"`
	At      time.Time `json:"at"`
}

// ReviewState is a small, source-only companion protocol. It intentionally
// contains no patch text: the TUI remains the authoritative review surface.
// Fields added after version 1 are optional, so an older extension keeps
// working against a newer Stvena and vice versa. Features names what this
// Stvena understands, so an extension can hide UI it cannot drive.
type ReviewState struct {
	Version         int                `json:"version"`
	Session         string             `json:"session"`
	Sequence        uint64             `json:"sequence"`
	Active          bool               `json:"active"`
	UpdatedAt       time.Time          `json:"updatedAt"`
	ChangedAt       time.Time          `json:"changedAt,omitempty"`
	Focus           *ReviewFocus       `json:"focus,omitempty"`
	UnreviewedFiles int                `json:"unreviewedFiles"`
	UnreviewedHunks int                `json:"unreviewedHunks"`
	NewerBatches    int                `json:"newerBatches"`
	Features        []string           `json:"features,omitempty"`
	Tree            string             `json:"tree,omitempty"`
	Files           []ReviewFile       `json:"files,omitempty"`
	Truncated       bool               `json:"truncated,omitempty"`
	Pending         *PendingRejections `json:"pendingRejections,omitempty"`
	LastRequest     *RequestResult     `json:"lastRequest,omitempty"`
}

// ReviewUpdate is one published snapshot of review state.
type ReviewUpdate struct {
	Focus                       *ReviewFocus
	Tree                        string
	Files                       []ReviewFile
	Truncated                   bool
	Unreviewed, UnreviewedHunks int
	NewerBatches                int
	Pending                     *PendingRejections
	LastRequest                 *RequestResult
}

// Features lists the request actions this build accepts.
var Features = []string{"review", "context", "accept", "reject", "undo-reject", "apply-rejections", "next-unreviewed"}

// Protocol limits. A very large change set is reported truncated rather than
// written in full: the descriptor is polled, not streamed.
const (
	MaxReviewFiles = 500
	MaxReviewHunks = 5000
)

// HunkRef turns an internal hunk identity into an opaque token. The internal id
// embeds a NUL separator and a repository path; neither belongs in a protocol
// an editor echoes back. Because the identity is content-addressed, a token for
// a hunk that has since changed simply stops matching.
func HunkRef(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:])
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

func (p *ReviewPublisher) Publish(u ReviewUpdate) error {
	next := p.state
	next.Features = Features
	if u.Focus != nil {
		focus := *u.Focus
		next.Focus = &focus
	} else {
		next.Focus = nil
	}
	next.Tree, next.Files, next.Truncated = u.Tree, u.Files, u.Truncated
	next.UnreviewedFiles, next.UnreviewedHunks, next.NewerBatches = u.Unreviewed, u.UnreviewedHunks, u.NewerBatches
	next.Pending, next.LastRequest = u.Pending, u.LastRequest
	// Heartbeats must not advance the sequence, so compare everything the
	// consumer reacts to and ignore the timestamps that always move.
	settled := func(s ReviewState) ReviewState {
		s.Sequence, s.UpdatedAt, s.ChangedAt = 0, time.Time{}, time.Time{}
		return s
	}
	changed := !reflect.DeepEqual(settled(next), settled(p.state))
	if !changed && !p.writtenAt.IsZero() && time.Since(p.writtenAt) < 5*time.Second {
		return nil
	}
	if changed {
		next.Sequence++
		next.ChangedAt = time.Now().UTC()
	}
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

// Request is a user-initiated editor action. Path, Line and EndLine locate the
// change; HunkID names one change block and is empty for a whole file. Text
// carries an optional reason for a rejection.
type Request struct {
	Version   int       `json:"version"`
	Session   string    `json:"session"`
	ID        string    `json:"id"`
	Action    string    `json:"action"`
	Path      string    `json:"path"`
	Line      int       `json:"line"`
	EndLine   int       `json:"endLine"`
	HunkID    string    `json:"hunkId,omitempty"`
	Text      string    `json:"text,omitempty"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// MaxRequestText bounds a rejection reason. It reaches the agent as text, so it
// is kept short enough to stay readable in a handoff message.
const MaxRequestText = 4096

// pathlessActions operate on the whole queue rather than one change.
var pathlessActions = map[string]bool{"apply-rejections": true, "next-unreviewed": true}

func validAction(action string) bool {
	for _, known := range Features {
		if action == known {
			return true
		}
	}
	return false
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
	if request.Version != 1 || request.ID == "" || !validAction(request.Action) ||
		len(request.Text) > MaxRequestText || !validHunkID(request.HunkID) ||
		request.UpdatedAt.IsZero() || time.Since(request.UpdatedAt) > time.Minute || time.Since(request.UpdatedAt) < -time.Minute {
		return nil, fmt.Errorf("invalid or expired editor request")
	}
	// Queue-wide actions carry no location; everything else must name one.
	if !pathlessActions[request.Action] &&
		(!validRequestPath(request.Path) || request.Line < 1 || request.EndLine < request.Line) {
		return nil, fmt.Errorf("invalid or expired editor request")
	}
	return &request, nil
}

// validHunkID accepts the opaque token HunkRef produces, or none at all.
func validHunkID(id string) bool {
	if id == "" {
		return true
	}
	if len(id) != 64 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
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
