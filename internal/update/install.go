package update

import (
	"fmt"
	"os"
	"path/filepath"
)

// Install replaces the program at target with the verified binary at
// staged, which Download wrote into target's own directory.
//
// The replacement is a rename, so the binary is never half-written: a
// reader either opens the old program or the new one. What that means for
// a running process differs by platform, which is what replace/1 handles.
func Install(target, staged string) error {
	if err := replace(target, staged); err != nil {
		os.Remove(staged)
		return fmt.Errorf("install %s: %w", target, err)
	}
	return nil
}

// TargetPath resolves where the running program actually lives, following
// symlinks so that an update replaces the binary rather than a link to it.
func TargetPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve this program's path: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return exe, nil
}

// Writable reports whether the program's own directory can be written,
// which is what an install needs: the new binary is staged there and
// renamed into place. It is checked before anything is downloaded or
// stopped, so a binary owned by root fails while everything still works.
func Writable(target string) error {
	dir := filepath.Dir(target)
	f, err := os.CreateTemp(dir, ".claude-companion-check-*")
	if err != nil {
		return fmt.Errorf("%s is not writable by this user", dir)
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return nil
}
