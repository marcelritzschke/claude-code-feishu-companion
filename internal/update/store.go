package update

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/marcelritzschke/claude-code-feishu-companion/internal/flock"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/paths"
)

// cacheFileName holds the version a check last found and the version
// already announced, so a passive display never has to hit the network
// and a notification is never repeated for a version already reported.
const cacheFileName = "update.json"

// cached is the cache file's shape.
type cached struct {
	Latest   string `json:"latest_version"`
	Notified string `json:"notified_version"`
}

// Store is the update cache in Claude Companion's private directory.
type Store struct {
	dir string
}

// OpenStore returns the store rooted at Claude Companion's private
// directory.
func OpenStore() (*Store, error) {
	dir, err := paths.Dir()
	if err != nil {
		return nil, err
	}
	return &Store{dir: dir}, nil
}

// Cached returns the version a check last found on GitHub and the version
// already announced, without making a network call. Either is empty if no
// check has completed, or none was ever newer than the running version.
func (s *Store) Cached() (latest, notified string, err error) {
	c, err := s.load()
	if err != nil {
		return "", "", err
	}
	return c.Latest, c.Notified, nil
}

// RecordLatest saves the version a check most recently found on GitHub.
func (s *Store) RecordLatest(version string) error {
	return s.mutate(func(c *cached) { c.Latest = version })
}

// RecordNotified saves that version has been announced - in Feishu, or
// printed by an on-demand check - so a later check never announces it
// again.
func (s *Store) RecordNotified(version string) error {
	return s.mutate(func(c *cached) { c.Notified = version })
}

// mutate takes the store's lock, applies fn to the current cache, and
// persists the result. The lock keeps a concurrent daemon check and a
// manual "update" command from tearing each other's write.
func (s *Store) mutate(fn func(*cached)) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(s.dir, cacheFileName+".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := flock.Lock(lock); err != nil {
		return err
	}
	defer flock.Unlock(lock)

	c, err := s.load()
	if err != nil {
		return err
	}
	fn(&c)
	return s.save(c)
}

// load reads the cache; a missing or corrupt file starts fresh.
func (s *Store) load() (cached, error) {
	data, err := os.ReadFile(filepath.Join(s.dir, cacheFileName))
	if os.IsNotExist(err) {
		return cached{}, nil
	}
	if err != nil {
		return cached{}, err
	}
	var c cached
	if err := json.Unmarshal(data, &c); err != nil {
		return cached{}, nil
	}
	return c, nil
}

func (s *Store) save(c cached) error {
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.dir, cacheFileName), data, 0o600)
}
