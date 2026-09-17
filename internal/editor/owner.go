package editor

import (
	"encoding/json"
	"errors"
	"os"
	"time"
)

// ErrNotOwner means another Stvena in the same project is publishing to the
// editor, and this one has stepped back so the two do not take turns.
var ErrNotOwner = errors.New("another Stvena in this project is connected to the editor")

// ownerLease is how long a descriptor keeps its owner without a write. Both
// descriptors are rewritten at least every five seconds while their session
// runs, so a lapsed lease means that process has gone without closing.
const ownerLease = 15 * time.Second

// ownedByNewer reports whether the descriptor at path belongs to a different,
// still-running Stvena that started after this one. Two sessions in one
// project would otherwise overwrite each other every few seconds, and the
// editor would flip between their review states. The newest process wins: it
// is the one the user most likely just opened. The older one resumes when the
// newer one exits (it marks the descriptor inactive) or its lease lapses.
func ownedByNewer(path, session string, started time.Time) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var current struct {
		Session   string    `json:"session"`
		Active    bool      `json:"active"`
		UpdatedAt time.Time `json:"updatedAt"`
		StartedAt time.Time `json:"startedAt"`
	}
	if json.Unmarshal(data, &current) != nil || current.Session == session || !current.Active {
		return false
	}
	if age := time.Since(current.UpdatedAt); age < 0 || age > ownerLease {
		return false
	}
	// A build without the field started before this one, as far as anyone can
	// tell; the newer build takes over.
	return current.StartedAt.After(started)
}
