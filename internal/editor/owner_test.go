package editor

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func reviewPublisherAt(path, id string, started time.Time) *ReviewPublisher {
	return &ReviewPublisher{path: path, session: id,
		state: ReviewState{Version: 1, Session: id, Active: true, StartedAt: started}}
}

func publishedSession(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var head struct {
		Session string `json:"session"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		t.Fatal(err)
	}
	return head.Session
}

// Two Stvenas in one project used to overwrite each other's review state every
// few seconds, so an accepted change flipped back to Accept / Reject.
func TestOlderSessionStepsBackForANewerOne(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stvena-review.json")
	now := time.Now().UTC()
	older := reviewPublisherAt(path, "older", now.Add(-time.Minute))
	newer := reviewPublisherAt(path, "newer", now)
	accepted := ReviewUpdate{Files: []ReviewFile{{Path: "a.go", Status: "M",
		Hunks: []ReviewHunk{{ID: HunkRef("a"), Start: 1, End: 2, Reviewed: true}}}}}

	if err := older.Publish(ReviewUpdate{}); err != nil {
		t.Fatal(err)
	}
	// The newer session takes over from a live older one.
	if err := newer.Publish(accepted); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := older.Publish(ReviewUpdate{}); !errors.Is(err, ErrNotOwner) {
			t.Fatalf("older session published over a newer one: %v", err)
		}
		if err := newer.Publish(accepted); err != nil {
			t.Fatal(err)
		}
		if got := publishedSession(t, path); got != "newer" {
			t.Fatalf("descriptor owned by %q, want newer", got)
		}
	}

	// When the newer session exits, the older one resumes at once.
	newer.Close()
	if err := older.Publish(ReviewUpdate{}); err != nil {
		t.Fatalf("older session did not resume after the newer one closed: %v", err)
	}
	if got := publishedSession(t, path); got != "older" {
		t.Fatalf("descriptor owned by %q after resume, want older", got)
	}
}

func TestOwnershipLapsesAndPredatesTheField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stvena-review.json")
	now := time.Now().UTC()
	write := func(value map[string]any) {
		data, _ := json.Marshal(value)
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	mine := reviewPublisherAt(path, "mine", now)

	// A newer process that stopped writing without closing (a crash).
	write(map[string]any{"version": 1, "session": "crashed", "active": true,
		"updatedAt": now.Add(-ownerLease - time.Second), "startedAt": now.Add(time.Second)})
	if err := mine.Publish(ReviewUpdate{}); err != nil {
		t.Fatalf("a lapsed owner kept the descriptor: %v", err)
	}

	// A build that does not write startedAt is treated as the older one.
	write(map[string]any{"version": 1, "session": "old-build", "active": true, "updatedAt": now})
	if err := mine.Publish(ReviewUpdate{}); err != nil {
		t.Fatalf("an older build kept the descriptor: %v", err)
	}

	// The live descriptor follows the same rule.
	livePath := filepath.Join(dir, "stvena-live.json")
	live := func(id string, started time.Time) *Publisher {
		return &Publisher{path: livePath, activityPath: filepath.Join(dir, "none"),
			state: State{Version: 1, Session: id, Active: true, StartedAt: started, Files: []Change{}}}
	}
	older, newer := live("older", now.Add(-time.Minute)), live("newer", now)
	if err := newer.Publish("", nil); err != nil {
		t.Fatal(err)
	}
	if err := older.Publish("", nil); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("older live publisher wrote over a newer one: %v", err)
	}
	if got := publishedSession(t, livePath); got != "newer" {
		t.Fatalf("live descriptor owned by %q, want newer", got)
	}
}
