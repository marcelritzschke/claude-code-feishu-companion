// Package paths locates the files the Wirelark roles share. Every one of
// them lives under a single private directory, so a hook, the daemon, and a
// channel all agree on where the state, the socket, and the logs are
// without passing paths to each other.
package paths

import (
	"fmt"
	"os"
	"path/filepath"
)

// EnvVar points every Wirelark role at a different private directory,
// which is what lets a test exercise the real files without touching the
// user's own daemon.
const EnvVar = "WIRELARK_STATE_DIR"

// Dir returns the private directory, creating it if needed. It is 0700: it
// holds the daemon socket and the cached tenant token.
func Dir() (string, error) {
	dir := os.Getenv(EnvVar)
	if dir == "" {
		cache, err := os.UserCacheDir()
		if err != nil {
			return "", fmt.Errorf("resolve user cache dir: %w", err)
		}
		dir = filepath.Join(cache, "wirelark")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create %s: %w", dir, err)
	}
	return dir, nil
}

// File returns the path of one file in the private directory.
func File(name string) (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}
