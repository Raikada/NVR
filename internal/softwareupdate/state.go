package softwareupdate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// stateFileName is the recorder-local JSON file tracking lifecycle
// state across restarts. Lives alongside the identity directory so
// it survives binary swaps (the file's location is stable; the
// running process points to it via the identity dir).
const stateFileName = "software_update_state.json"

// State is the persisted shape recorded across restarts. The file
// lives at <identityDir>/software_update_state.json.
type State struct {
	// CurrentVersion is the version the recorder believes it's
	// currently running. Refreshed on a successful apply.
	CurrentVersion string `json:"current_version"`
	// PendingApprovedLifecycleID is set when the MS has pushed an
	// apply request that the recorder has not yet applied. Cleared
	// on apply.
	PendingApprovedLifecycleID string `json:"pending_approved_lifecycle_id,omitempty"`
	// PendingTargetVersion is the version of the pending update.
	PendingTargetVersion string `json:"pending_target_version,omitempty"`
	// LastAppliedAt is the time the most recent successful apply
	// completed.
	LastAppliedAt time.Time `json:"last_applied_at,omitempty"`
	// LastAppliedLifecycleID is the lifecycle row the recorder
	// applied last. Used by the post-restart health check to know
	// which row to MarkApplied / MarkFailed.
	LastAppliedLifecycleID string `json:"last_applied_lifecycle_id,omitempty"`
	// PreviousVersion is the version that was running before the
	// most recent apply. Carried forward so a rollback can restore.
	PreviousVersion string `json:"previous_version,omitempty"`
	// LastFailureReason is set when the most recent apply or
	// rollback failed; cleared on the next success.
	LastFailureReason string `json:"last_failure_reason,omitempty"`
}

// StateStore persists State to disk under a mutex.
type StateStore struct {
	mu  sync.Mutex
	dir string
}

// NewStateStore builds a store rooted at dir. The directory must
// exist; ApplyDir / IdentityDir is the typical pick.
func NewStateStore(dir string) (*StateStore, error) {
	if dir == "" {
		return nil, errors.New("software_update: empty state dir")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("software_update: mkdir %q: %w", dir, err)
	}
	return &StateStore{dir: dir}, nil
}

// Load reads the persisted state. Returns a zero-value State if the
// file doesn't exist.
func (s *StateStore) Load() (*State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(filepath.Join(s.dir, stateFileName))
	if errors.Is(err, os.ErrNotExist) {
		return &State{}, nil
	}
	if err != nil {
		return nil, err
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("software_update: parse state: %w", err)
	}
	return &st, nil
}

// Save writes the state to disk via temp-file + atomic rename.
func (s *StateStore) Save(st *State) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("software_update: marshal state: %w", err)
	}
	path := filepath.Join(s.dir, stateFileName)
	tmp, err := os.CreateTemp(s.dir, ".su-state-")
	if err != nil {
		return fmt.Errorf("software_update: open tmp: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
