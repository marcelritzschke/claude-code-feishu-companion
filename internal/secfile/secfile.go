// Package secfile writes the small files Claude Companion owns or edits - its
// config, its token cache, the user's Claude Code settings - with
// restrictive permissions and an atomic temp-file+rename, so a crash
// mid-write can never leave a truncated file behind and a file holding
// secrets is never briefly world-readable.
package secfile

import (
	"os"
	"path/filepath"
)

// WriteAtomic writes data to p with perm, creating parent dirs with 0700.
func WriteAtomic(p string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after successful rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, p)
}
