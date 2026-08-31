// Package state remembers which Feishu message carries the live progress
// card for each Claude Code session, so one turn updates one message in
// place instead of piling up notifications. Access is serialized with a
// file lock: Claude Code runs hooks concurrently for parallel tool calls.
package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Entry is the progress-card bookkeeping for one session.
type Entry struct {
	PromptID  string    `json:"prompt_id"` // turn the card belongs to
	MessageID string    `json:"message_id"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Store is the state file in the user's cache dir.
type Store struct {
	dir string
}

// Open returns the store rooted at <user cache dir>/wirelark.
func Open() (*Store, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	return &Store{dir: filepath.Join(dir, "wirelark")}, nil
}

// Mutate runs fn with the current entries while holding the store's
// exclusive lock, then persists whatever fn left in the map. The lock is
// held across fn on purpose: fn sends or updates the Feishu card, and
// without the lock two concurrent hooks could both decide to send one.
// An error means fn never ran; a failure to persist after fn ran is
// swallowed, because the notification already went out and callers must
// not retry it.
func (s *Store) Mutate(fn func(entries map[string]Entry)) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(s.dir, "state.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := lockFile(lock); err != nil {
		return err
	}
	defer unlockFile(lock)

	entries := s.load()
	fn(entries)
	_ = s.save(entries)
	return nil
}

// load reads the entries; a missing or corrupt file starts fresh.
func (s *Store) load() map[string]Entry {
	entries := map[string]Entry{}
	data, err := os.ReadFile(filepath.Join(s.dir, "state.json"))
	if err != nil {
		return entries
	}
	_ = json.Unmarshal(data, &entries)
	return entries
}

// save writes the entries, dropping sessions whose last update is older
// than maxAge.
func (s *Store) save(entries map[string]Entry) error {
	cutoff := time.Now().Add(-maxAge)
	for id, e := range entries {
		if e.UpdatedAt.Before(cutoff) {
			delete(entries, id)
		}
	}
	data, err := json.Marshal(entries)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.dir, "state.json"), data, 0o600)
}

// maxAge bounds how long a stale entry can linger after its session ended.
const maxAge = 24 * time.Hour
