//go:build !windows

package flock

import (
	"errors"
	"os"
	"syscall"
)

// ErrLocked reports that another process holds the lock.
var ErrLocked = errors.New("already locked by another process")

func lockFile(f *os.File, wait bool) error {
	how := syscall.LOCK_EX
	if !wait {
		how |= syscall.LOCK_NB
	}
	err := syscall.Flock(int(f.Fd()), how)
	if !wait && errors.Is(err, syscall.EWOULDBLOCK) {
		return ErrLocked
	}
	return err
}

func unlockFile(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
