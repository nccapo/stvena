package editorlsp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/nccapo/stvena/internal/editor"
	"github.com/nccapo/stvena/internal/repo"
	"github.com/nccapo/stvena/internal/session"
)

// Descriptor limits and lifetimes, kept identical to the VS Code extension so
// both consumers behave the same way against the same files.
const (
	maxStateBytes  = 8 * 1024 * 1024
	maxReviewBytes = 64 * 1024
	// A heartbeat older than this means the writer is gone.
	liveWindow = 30 * time.Second
	// A reported read stops being current after this.
	activityLifetime = 15 * time.Second
	// How often this server republishes its presence descriptor. Stvena treats
	// anything older than 30 s as disconnected, so this leaves room for a slow
	// poll without the editor appearing to drop out.
	presenceInterval = 20 * time.Second
	// Poll cadence, matching the VS Code extension and Stvena's own capture.
	pollInterval = 700 * time.Millisecond
)

var (
	oidPattern    = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
	zeroOID       = regexp.MustCompile(`^0+$`)
	hunkPattern   = regexp.MustCompile(`^[0-9a-f]{64}$`)
	statusPattern = regexp.MustCompile(`^[AMDRCTU?]$`)
)

// bridge is the set of descriptor paths for one reviewed root. Linked worktrees
// have their own Git directory, so .git is resolved rather than assumed, and a
// folder Stvena reviews without initializing it keeps its descriptors in
// Stvena's cache instead.
type bridge struct {
	root         string
	descriptors  string
	statePath    string
	reviewPath   string
	requestPath  string
	presencePath string
}

// discover resolves a folder to the root under review and the directory holding
// its descriptors. Git answers first, and Stvena's bridge registry answers for
// a folder that is not a repository — the same order every editor consumer
// follows, so the fallback path stays exercised rather than only running in the
// case nobody tests.
func discover(dir string) (*bridge, error) {
	if root, err := gitOutput(dir, "rev-parse", "--show-toplevel"); err == nil {
		gitDir, gitErr := gitOutput(root, "rev-parse", "--absolute-git-dir")
		if gitErr != nil {
			return nil, fmt.Errorf("resolve Git directory: %w", gitErr)
		}
		return newBridge(root, gitDir), nil
	}
	if entry, ok := repo.Lookup(dir); ok {
		return newBridge(entry.Root, entry.Dir), nil
	}
	return nil, fmt.Errorf("%s is neither a Git repository nor a folder Stvena has reviewed", dir)
}

func newBridge(root, descriptors string) *bridge {
	return &bridge{
		root: root, descriptors: descriptors,
		statePath:    filepath.Join(descriptors, "stvena-live.json"),
		reviewPath:   filepath.Join(descriptors, "stvena-review.json"),
		requestPath:  filepath.Join(descriptors, "stvena-request.json"),
		presencePath: filepath.Join(descriptors, "stvena-ide.json"),
	}
}

func gitOutput(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", append([]string{"--no-optional-locks", "-C", dir}, args...)...).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// readMetadata rejects oversized descriptors before parsing them. A missing
// descriptor is not an error: it only means Stvena is not running here.
func readMetadata(path string, limit int64, parse func([]byte) error) error {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return errAbsent
	}
	if err != nil {
		return err
	}
	if info.Size() > limit {
		return fmt.Errorf("Stvena metadata exceeds its size limit")
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return errAbsent
	}
	if err != nil {
		return err
	}
	return parse(data)
}

var errAbsent = errors.New("descriptor absent")

// readState loads stvena-live.json. A nil state with a nil error means the
// descriptor is not there.
func readState(r *bridge) (*editor.State, error) {
	var state editor.State
	err := readMetadata(r.statePath, maxStateBytes, func(data []byte) error {
		if err := json.Unmarshal(data, &state); err != nil {
			return fmt.Errorf("read Stvena live state: %w", err)
		}
		return validateState(&state)
	})
	if errors.Is(err, errAbsent) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &state, nil
}

func readReview(r *bridge) (*editor.ReviewState, error) {
	var state editor.ReviewState
	err := readMetadata(r.reviewPath, maxReviewBytes, func(data []byte) error {
		if err := json.Unmarshal(data, &state); err != nil {
			return fmt.Errorf("read Stvena review state: %w", err)
		}
		return validateReview(&state)
	})
	if errors.Is(err, errAbsent) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &state, nil
}

// validateState applies the checks the VS Code bridge applies. Decoding into
// typed structs already rejects wrong types; what remains is the value-level
// contract. An absent boolean decodes to false, which is the conservative
// reading in every case here (not active, not binary, not truncated).
func validateState(state *editor.State) error {
	if state.Version != 1 || state.Session == "" || state.UpdatedAt.IsZero() {
		return errors.New("unsupported or invalid Stvena live state; update Stvena and the extension together")
	}
	for i := range state.Files {
		file := &state.Files[i]
		if !validPath(file.Path) || (file.OldPath != "" && !validPath(file.OldPath)) ||
			!oidPattern.MatchString(file.Before) || !oidPattern.MatchString(file.After) ||
			file.Line < 1 || !statusPattern.MatchString(file.Status) ||
			file.Added < 0 || file.Deleted < 0 {
			return errors.New("invalid captured edit in Stvena live state")
		}
		for _, span := range file.Ranges {
			if span.Start < 1 || span.End < span.Start {
				return errors.New("invalid changed line ranges")
			}
		}
	}
	if activity := state.Activity; activity != nil {
		if activity.Session != state.Session || activity.ID == "" || !validPath(activity.Path) ||
			activity.Line < 1 || (activity.EndLine != 0 && activity.EndLine < activity.Line) ||
			activity.UpdatedAt.IsZero() {
			return errors.New("invalid agent read activity")
		}
	}
	return nil
}

var reviewSources = map[string]bool{"session": true, "workspace": true, "project": true, "branch": true}

func validateReview(state *editor.ReviewState) error {
	if state.Version != 1 || state.Session == "" || state.UpdatedAt.IsZero() ||
		state.UnreviewedFiles < 0 || state.UnreviewedHunks < 0 || state.NewerBatches < 0 {
		return errors.New("unsupported or invalid Stvena review state; update Stvena and the extension together")
	}
	if focus := state.Focus; focus != nil {
		if !reviewSources[focus.Source] || !oidPattern.MatchString(focus.Tree) || zeroOID.MatchString(focus.Tree) ||
			!validPath(focus.Path) || focus.Line < 1 || focus.EndLine < focus.Line {
			return errors.New("invalid Stvena review focus")
		}
	}
	// Everything from Features onward was added after version 1. An older
	// Stvena omits it, and absence means "this build cannot do that".
	for i := range state.Files {
		file := &state.Files[i]
		if !validPath(file.Path) || (file.OldPath != "" && !validPath(file.OldPath)) ||
			!statusPattern.MatchString(file.Status) {
			return errors.New("invalid Stvena review file")
		}
		for _, hunk := range file.Hunks {
			if !hunkPattern.MatchString(hunk.ID) || hunk.Start < 1 || hunk.End < hunk.Start {
				return errors.New("invalid Stvena review hunk")
			}
		}
	}
	if pending := state.Pending; pending != nil {
		if pending.Count < 0 || (pending.AppliesAt != "now" && pending.AppliesAt != "turn-end" && pending.AppliesAt != "manual") {
			return errors.New("invalid Stvena pending rejections")
		}
	}
	if last := state.LastRequest; last != nil {
		if last.ID == "" || last.Action == "" || last.At.IsZero() ||
			(last.Status != "applied" && last.Status != "queued" && last.Status != "refused") {
			return errors.New("invalid Stvena request result")
		}
	}
	return nil
}

// validPath mirrors the extension's check: repository-relative, no NUL, no
// absolute path in either syntax, no parent-directory escape.
func validPath(value string) bool {
	if value == "" || strings.ContainsRune(value, 0) || strings.HasPrefix(value, "/") || filepath.IsAbs(value) {
		return false
	}
	// A Windows-style absolute path must be rejected even on Unix, because the
	// descriptor may have been written on another platform.
	if len(value) >= 2 && value[1] == ':' {
		return false
	}
	if strings.HasPrefix(value, `\`) {
		return false
	}
	for _, segment := range strings.FieldsFunc(value, func(r rune) bool { return r == '/' || r == '\\' }) {
		if segment == ".." {
			return false
		}
	}
	return true
}

// liveState reports whether a descriptor's writer is still there.
func liveState(state *editor.State, now time.Time) bool {
	return state != nil && state.Active && now.Sub(state.UpdatedAt) < liveWindow
}

func liveReview(state *editor.ReviewState, now time.Time) bool {
	return state != nil && state.Active && now.Sub(state.UpdatedAt) < liveWindow
}

func fresh(at time.Time, now time.Time) bool {
	age := now.Sub(at)
	return age >= 0 && age < activityLifetime
}

// supports reports whether the running Stvena accepts a request action. An
// older build advertises nothing, so only the two original actions are assumed.
func supports(review *editor.ReviewState, action string) bool {
	if review == nil {
		return false
	}
	if review.Features == nil {
		return action == "review" || action == "context"
	}
	for _, name := range review.Features {
		if name == action {
			return true
		}
	}
	return false
}

// pathless actions operate on the whole queue rather than one located change.
var pathlessActions = map[string]bool{"apply-rejections": true, "next-unreviewed": true}

// writeRequest atomically replaces stvena-request.json. This and the presence
// descriptor are the only files this server ever writes; it never touches the
// working tree.
func writeRequest(r *bridge, review *editor.ReviewState, request editor.Request, now time.Time) (editor.Request, error) {
	if !liveReview(review, now) {
		return request, errors.New("Stvena review is not active in this repository")
	}
	if !supports(review, request.Action) {
		return request, fmt.Errorf("this Stvena version does not support %q; update the stvena binary", request.Action)
	}
	located := !pathlessActions[request.Action]
	if located && (!validPath(request.Path) || request.Line < 1 || request.EndLine < request.Line) {
		return request, errors.New("invalid Stvena editor request")
	}
	if request.HunkID != "" && !hunkPattern.MatchString(request.HunkID) {
		return request, errors.New("invalid Stvena hunk reference")
	}
	if len(request.Text) > editor.MaxRequestText {
		return request, errors.New("rejection reason is too long")
	}
	id, err := requestID()
	if err != nil {
		return request, err
	}
	request.Version = 1
	request.Session = review.Session
	request.ID = id
	request.UpdatedAt = now.UTC()
	if !located {
		request.Path, request.Line, request.EndLine = "", 0, 0
	}
	if err := session.AtomicJSON(r.requestPath, request); err != nil {
		return request, err
	}
	return request, nil
}

// announce tells Stvena an editor is actually watching this repository. Several
// editors report themselves as VS Code to the terminal and the extension may
// not be installed in the one running Stvena, so Stvena waits for this rather
// than trusting the environment.
func announce(r *bridge, ide, version string, now time.Time) error {
	return session.AtomicJSON(r.presencePath, editor.Presence{
		Version: 1, IDE: ide, Extension: version, UpdatedAt: now.UTC(),
	})
}

// withdraw marks this editor disconnected on shutdown, so Stvena stops offering
// IDE mode immediately instead of waiting for the heartbeat to age out. A
// descriptor another editor has taken over is left alone.
func withdraw(r *bridge, ide string) {
	data, err := os.ReadFile(r.presencePath)
	if err != nil {
		return
	}
	var presence editor.Presence
	if json.Unmarshal(data, &presence) != nil || presence.IDE != ide {
		return
	}
	_ = os.Remove(r.presencePath)
}

func requestID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
