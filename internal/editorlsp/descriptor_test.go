package editorlsp

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nccapo/stvena/internal/editor"
	"github.com/nccapo/stvena/internal/repo"
)

func liveFixture(t *testing.T) map[string]any {
	t.Helper()
	return map[string]any{
		"version": 1, "session": "test-session", "sequence": 1, "active": true,
		"updatedAt": time.Now().UTC().Format(time.RFC3339Nano),
		"files": []any{map[string]any{
			"path": "nested/file.txt", "status": "M",
			"before": strings.Repeat("1", 40), "after": strings.Repeat("2", 40),
			"line": 4, "added": 1, "deleted": 1, "binary": false, "truncated": false,
		}},
	}
}

func parseStateBytes(t *testing.T, value map[string]any) (*editor.State, error) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	var state editor.State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, validateState(&state)
}

func TestStateValidationAndHeartbeatExpiry(t *testing.T) {
	value := liveFixture(t)
	state, err := parseStateBytes(t, value)
	if err != nil {
		t.Fatalf("valid state rejected: %v", err)
	}
	now := state.UpdatedAt
	if !liveState(state, now) {
		t.Fatal("a fresh descriptor should be live")
	}
	if liveState(state, now.Add(30*time.Second+time.Millisecond)) {
		t.Fatal("a descriptor older than the heartbeat window should not be live")
	}
	stale := *state
	stale.Active = false
	if liveState(&stale, now) {
		t.Fatal("an inactive descriptor should not be live")
	}

	for name, change := range map[string]map[string]any{
		"wrong version": {"version": 2},
		"no session":    {"session": ""},
		"bad timestamp": {"updatedAt": "invalid"},
		"bad sequence":  {"sequence": -1},
	} {
		t.Run(name, func(t *testing.T) {
			broken := liveFixture(t)
			for k, v := range change {
				broken[k] = v
			}
			if _, err := parseStateBytes(t, broken); err == nil {
				t.Fatalf("accepted %s", name)
			}
		})
	}

	for name, change := range map[string]map[string]any{
		"parent escape":  {"path": "../outside"},
		"absolute path":  {"path": "/outside"},
		"windows path":   {"path": `C:\outside`},
		"old path":       {"oldPath": "../outside"},
		"oid is a flag":  {"before": "--help"},
		"zero line":      {"line": 0},
		"unknown status": {"status": "Z"},
		"negative added": {"added": -1},
	} {
		t.Run(name, func(t *testing.T) {
			broken := liveFixture(t)
			file := broken["files"].([]any)[0].(map[string]any)
			for k, v := range change {
				file[k] = v
			}
			if _, err := parseStateBytes(t, broken); err == nil {
				t.Fatalf("accepted %s", name)
			}
		})
	}
}

func TestActivityMustBelongToTheSession(t *testing.T) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for name, activity := range map[string]map[string]any{
		"other session": {"session": "someone-else", "id": "a1", "agent": "claude", "path": "a.go", "line": 2, "updatedAt": now},
		"escaping path": {"session": "test-session", "id": "a1", "agent": "claude", "path": "../a.go", "line": 2, "updatedAt": now},
		"zero line":     {"session": "test-session", "id": "a1", "agent": "claude", "path": "a.go", "line": 0, "updatedAt": now},
		"backwards end": {"session": "test-session", "id": "a1", "agent": "claude", "path": "a.go", "line": 9, "endLine": 2, "updatedAt": now},
		"no id":         {"session": "test-session", "id": "", "agent": "claude", "path": "a.go", "line": 2, "updatedAt": now},
	} {
		t.Run(name, func(t *testing.T) {
			value := liveFixture(t)
			value["activity"] = activity
			if _, err := parseStateBytes(t, value); err == nil {
				t.Fatalf("accepted %s", name)
			}
		})
	}
	value := liveFixture(t)
	value["activity"] = map[string]any{"session": "test-session", "id": "a1", "agent": "claude",
		"path": "a.go", "line": 2, "endLine": 9, "updatedAt": now}
	if _, err := parseStateBytes(t, value); err != nil {
		t.Fatalf("valid activity rejected: %v", err)
	}
}

func parseReviewBytes(t *testing.T, value map[string]any) (*editor.ReviewState, error) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	var state editor.ReviewState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, validateReview(&state)
}

func reviewFixture() map[string]any {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	return map[string]any{
		"version": 1, "session": "review-session", "sequence": 3, "active": true,
		"updatedAt": now, "changedAt": now,
		"unreviewedFiles": 2, "unreviewedHunks": 4, "newerBatches": 1,
		"focus": map[string]any{"tree": strings.Repeat("1", 40), "source": "session",
			"path": "nested/file.txt", "line": 4, "endLine": 7},
	}
}

func TestReviewValidation(t *testing.T) {
	if _, err := parseReviewBytes(t, reviewFixture()); err != nil {
		t.Fatalf("valid review state rejected: %v", err)
	}
	focus := func(change map[string]any) map[string]any {
		value := reviewFixture()
		base := value["focus"].(map[string]any)
		for k, v := range change {
			base[k] = v
		}
		return value
	}
	cases := map[string]map[string]any{
		"negative unreviewed": {"unreviewedFiles": -1},
		"fractional batches":  {"newerBatches": 1.5},
		"bad changedAt":       {"changedAt": "bad"},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			value := reviewFixture()
			for k, v := range change {
				value[k] = v
			}
			if _, err := parseReviewBytes(t, value); err == nil {
				t.Fatalf("accepted %s", name)
			}
		})
	}
	for name, change := range map[string]map[string]any{
		"escaping focus path": {"path": "../outside"},
		"backwards focus":     {"endLine": 3},
		"unknown source":      {"source": "unknown"},
		"zero tree":           {"tree": strings.Repeat("0", 40)},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseReviewBytes(t, focus(change)); err == nil {
				t.Fatalf("accepted %s", name)
			}
		})
	}
}

// Fields added after version 1 are optional: an older Stvena omits them and a
// consumer must treat absence as "this build cannot do that".
func TestOlderReviewDescriptorsStillParse(t *testing.T) {
	base := map[string]any{"version": 1, "session": "review-session", "sequence": 3, "active": true,
		"updatedAt":       time.Now().UTC().Format(time.RFC3339Nano),
		"unreviewedFiles": 2, "unreviewedHunks": 4, "newerBatches": 0}
	state, err := parseReviewBytes(t, base)
	if err != nil {
		t.Fatalf("older descriptor rejected: %v", err)
	}
	if supports(state, "reject") || supports(state, "accept") {
		t.Fatal("a descriptor without features must not advertise accept or reject")
	}
	if !supports(state, "review") || !supports(state, "context") {
		t.Fatal("review and context are the two actions every build accepts")
	}

	hunk := map[string]any{"id": strings.Repeat("a", 64), "start": 4, "end": 9, "reviewed": true}
	full := base
	full["features"] = []any{"review", "accept", "reject"}
	full["files"] = []any{map[string]any{"path": "a.go", "status": "M", "hunks": []any{hunk}}}
	full["pendingRejections"] = map[string]any{"count": 2, "appliesAt": "turn-end", "reason": "claude 1 is running"}
	full["lastRequest"] = map[string]any{"id": "r1", "action": "reject", "status": "queued",
		"at": time.Now().UTC().Format(time.RFC3339Nano)}
	state, err = parseReviewBytes(t, full)
	if err != nil {
		t.Fatalf("full descriptor rejected: %v", err)
	}
	if !supports(state, "accept") || supports(state, "prompt") {
		t.Fatal("features must gate exactly what the descriptor advertises")
	}

	for name, change := range map[string]any{
		"escaping file":  []any{map[string]any{"path": "../outside", "status": "M"}},
		"unknown status": []any{map[string]any{"path": "a.go", "status": "Z"}},
		"short hunk id":  []any{map[string]any{"path": "a.go", "status": "M", "hunks": []any{map[string]any{"id": "short", "start": 4, "end": 9}}}},
		"backwards hunk": []any{map[string]any{"path": "a.go", "status": "M", "hunks": []any{map[string]any{"id": strings.Repeat("a", 64), "start": 4, "end": 1}}}},
		"zero start":     []any{map[string]any{"path": "a.go", "status": "M", "hunks": []any{map[string]any{"id": strings.Repeat("a", 64), "start": 0, "end": 9}}}},
	} {
		t.Run(name, func(t *testing.T) {
			value := reviewFixture()
			value["files"] = change
			if _, err := parseReviewBytes(t, value); err == nil {
				t.Fatalf("accepted %s", name)
			}
		})
	}
	for name, change := range map[string]any{
		"unknown appliesAt": map[string]any{"count": 1, "appliesAt": "whenever"},
		"negative count":    map[string]any{"count": -1, "appliesAt": "now"},
	} {
		t.Run(name, func(t *testing.T) {
			value := reviewFixture()
			value["pendingRejections"] = change
			if _, err := parseReviewBytes(t, value); err == nil {
				t.Fatalf("accepted %s", name)
			}
		})
	}
	value := reviewFixture()
	value["lastRequest"] = map[string]any{"id": "r1", "action": "reject", "status": "maybe",
		"at": time.Now().UTC().Format(time.RFC3339Nano)}
	if _, err := parseReviewBytes(t, value); err == nil {
		t.Fatal("accepted an unknown request status")
	}
}

func TestValidPathRejectsEscapes(t *testing.T) {
	for _, good := range []string{"a.go", "nested/file.txt", "a b/c.go", "dir/..file"} {
		if !validPath(good) {
			t.Errorf("validPath(%q) = false, want true", good)
		}
	}
	for _, bad := range []string{"", "/etc/passwd", "../outside", "nested/../../outside",
		`C:\outside`, `\\server\share`, "with\x00nul", `nested\..\..\outside`} {
		if validPath(bad) {
			t.Errorf("validPath(%q) = true, want false", bad)
		}
	}
}

// testRepo builds a real Git repository, because the Git directory of a linked
// worktree is not .git and must be resolved rather than assumed.
func testRepo(t *testing.T) *bridge {
	t.Helper()
	root := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.name", "Stvena Test"},
		{"config", "user.email", "test@example.invalid"},
	} {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	found, err := discover(root)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	return found
}

func TestDiscoverResolvesNestedFoldersAndWorktrees(t *testing.T) {
	r := testRepo(t)
	if err := os.MkdirAll(filepath.Join(r.root, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	nested, err := discover(filepath.Join(r.root, "nested"))
	if err != nil {
		t.Fatalf("discover nested: %v", err)
	}
	if nested.root != r.root || nested.statePath != r.statePath {
		t.Fatalf("nested folder resolved to %s, want %s", nested.root, r.root)
	}
	// An absent descriptor is not an error: Stvena simply is not running here.
	if state, err := readState(r); err != nil || state != nil {
		t.Fatalf("readState with no descriptor = %v, %v; want nil, nil", state, err)
	}
	if review, err := readReview(r); err != nil || review != nil {
		t.Fatalf("readReview with no descriptor = %v, %v; want nil, nil", review, err)
	}

	write(t, filepath.Join(r.root, "a.txt"), "hello\n")
	run(t, r.root, "add", ".")
	run(t, r.root, "commit", "-qm", "baseline")
	run(t, r.root, "worktree", "add", "-qb", "linked", filepath.Join(r.root, "linked"))
	linked, err := discover(filepath.Join(r.root, "linked"))
	if err != nil {
		t.Fatalf("discover worktree: %v", err)
	}
	if linked.statePath == r.statePath {
		t.Fatal("a linked worktree must have its own descriptor path")
	}
}

func TestReadStateRejectsOversizedDescriptors(t *testing.T) {
	r := testRepo(t)
	write(t, r.reviewPath, strings.Repeat("x", maxReviewBytes+1))
	if _, err := readReview(r); err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Fatalf("err = %v, want a size-limit error", err)
	}
}

func TestWriteRequestIsSessionBoundAndGated(t *testing.T) {
	r := testRepo(t)
	now := time.Now()
	live := &editor.ReviewState{Version: 1, Session: "active-session", Active: true, UpdatedAt: now}

	// An older Stvena advertises no features: only the original actions are safe.
	if _, err := writeRequest(r, live, editor.Request{Action: "reject", Path: "a.go", Line: 1, EndLine: 1}, now); err == nil ||
		!strings.Contains(err.Error(), "does not support") {
		t.Fatalf("err = %v, want a refusal naming the missing feature", err)
	}
	written, err := writeRequest(r, live, editor.Request{Action: "review", Path: "a.go", Line: 2, EndLine: 5}, now)
	if err != nil {
		t.Fatalf("write review request: %v", err)
	}
	var stored editor.Request
	data, err := os.ReadFile(r.requestPath)
	if err != nil {
		t.Fatalf("read request: %v", err)
	}
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatalf("stored request is not JSON: %v", err)
	}
	if stored.Session != "active-session" || stored.ID == "" || stored.ID != written.ID || stored.Version != 1 {
		t.Fatalf("stored = %+v, want a session-bound version 1 request with an id", stored)
	}
	if info, err := os.Stat(r.requestPath); err != nil || info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("request file mode = %v (err %v), want owner-only", info.Mode().Perm(), err)
	}

	live.Features = editor.Features
	rejection, err := writeRequest(r, live, editor.Request{Action: "reject", Path: "a.go", Line: 4, EndLine: 9,
		HunkID: strings.Repeat("b", 64), Text: "breaks the contract"}, now)
	if err != nil {
		t.Fatalf("write rejection: %v", err)
	}
	if rejection.HunkID != strings.Repeat("b", 64) || rejection.Text != "breaks the contract" {
		t.Fatalf("rejection = %+v, want the hunk id and reason echoed", rejection)
	}
	// Queue-wide actions carry no location.
	queue, err := writeRequest(r, live, editor.Request{Action: "apply-rejections", Path: "ignored", Line: 7, EndLine: 9}, now)
	if err != nil {
		t.Fatalf("write queue action: %v", err)
	}
	if queue.Path != "" || queue.Line != 0 || queue.EndLine != 0 {
		t.Fatalf("queue action = %+v, want no location", queue)
	}

	for name, request := range map[string]editor.Request{
		"escaping path": {Action: "context", Path: "../outside", Line: 1, EndLine: 1},
		"zero line":     {Action: "context", Path: "a.go", Line: 0, EndLine: 1},
		"backwards":     {Action: "context", Path: "a.go", Line: 9, EndLine: 2},
		"bad hunk":      {Action: "reject", Path: "a.go", Line: 1, EndLine: 1, HunkID: "short"},
		"long reason":   {Action: "reject", Path: "a.go", Line: 1, EndLine: 1, Text: strings.Repeat("x", editor.MaxRequestText+1)},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := writeRequest(r, live, request, now); err == nil {
				t.Fatalf("accepted %s", name)
			}
		})
	}

	dead := *live
	dead.Active = false
	if _, err := writeRequest(r, &dead, editor.Request{Action: "review", Path: "a.go", Line: 1, EndLine: 1}, now); err == nil ||
		!strings.Contains(err.Error(), "not active") {
		t.Fatalf("err = %v, want a refusal because review is not active", err)
	}
}

func TestPresenceAnnounceAndWithdraw(t *testing.T) {
	r := testRepo(t)
	if err := announce(r, "zed", "0.1.0", time.Now()); err != nil {
		t.Fatalf("announce: %v", err)
	}
	if name := editor.ReadPresence(r.presencePath); name != "zed" {
		t.Fatalf("presence = %q, want zed", name)
	}
	// Another editor's descriptor must survive this server's shutdown.
	if err := announce(r, "vscode", "0.3.1", time.Now()); err != nil {
		t.Fatalf("announce other: %v", err)
	}
	withdraw(r, "zed")
	if name := editor.ReadPresence(r.presencePath); name != "vscode" {
		t.Fatalf("presence = %q, want the other editor's descriptor left alone", name)
	}
	withdraw(r, "vscode")
	if name := editor.ReadPresence(r.presencePath); name != "" {
		t.Fatalf("presence = %q, want it gone after withdrawing", name)
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func run(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

// A folder Stvena reviews without initializing it as a repository keeps its
// descriptors in Stvena's cache. Git cannot answer for it, so the consumer must
// fall back to the bridge registry, exactly as the extension does.
func TestDiscoverFallsBackToTheBridgeRegistry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("STVENA_HOME", home)
	root := t.TempDir()
	descriptors := filepath.Join(t.TempDir(), "cache")
	if err := os.MkdirAll(descriptors, 0o700); err != nil {
		t.Fatal(err)
	}

	if _, err := discover(root); err == nil {
		t.Fatal("a folder with no repository and no registry entry must not resolve")
	}
	if err := repo.Publish(repo.Workspace{Root: root, GitDir: repo.ShadowPath(descriptors),
		Dir: descriptors, Mode: repo.ModeShadow}); err != nil {
		t.Fatalf("publish registry entry: %v", err)
	}

	found, err := discover(root)
	if err != nil {
		t.Fatalf("discover after publishing: %v", err)
	}
	if found.root != root || found.statePath != filepath.Join(descriptors, "stvena-live.json") {
		t.Fatalf("resolved to %s / %s, want the registered cache directory", found.root, found.statePath)
	}
	// A subdirectory of a reviewed root resolves to the same entry.
	nested := filepath.Join(root, "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if found, err = discover(nested); err != nil || found.requestPath != filepath.Join(descriptors, "stvena-request.json") {
		t.Fatalf("nested folder resolved to %v (err %v), want the same entry", found, err)
	}
}

// A real repository must never be answered by a stale registry entry pointing
// somewhere else: Git comes first.
func TestGitAnswersBeforeTheRegistry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("STVENA_HOME", home)
	r := testRepo(t)
	elsewhere := t.TempDir()
	if err := repo.Publish(repo.Workspace{Root: r.root, GitDir: repo.ShadowPath(elsewhere),
		Dir: elsewhere, Mode: repo.ModeShadow}); err != nil {
		t.Fatalf("publish registry entry: %v", err)
	}
	found, err := discover(r.root)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if found.statePath != r.statePath {
		t.Fatalf("resolved to %s, want the repository's own Git directory %s", found.statePath, r.statePath)
	}
}
