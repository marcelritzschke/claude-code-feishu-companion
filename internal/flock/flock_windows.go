//go:build windows

package flock

import (
	"errors"
	"os"
	"syscall"
	"unsafe"
)

// ErrLocked reports that another process holds the lock.
var ErrLocked = errors.New("already locked by another process")

// LockFileEx is not exposed by the syscall package, so it is called
// directly from kernel32. The lock is an exclusive lock on the first byte
// of the lock file.
var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx   = kernel32.NewProc("LockFileEx")
	procUnlockFileEx = kernel32.NewProc("UnlockFileEx")
)

const (
	lockRange               = 1
	lockfileExclusiveLock   = 0x00000002
	lockfileFailImmediately = 0x00000001
	errorLockViolation      = syscall.Errno(33)
	errorIOPending          = syscall.Errno(997)
)

func lockFile(f *os.File, wait bool) error {
	flags := uintptr(lockfileExclusiveLock)
	if !wait {
		flags |= lockfileFailImmediately
	}
	ol := new(syscall.Overlapped)
	r1, _, err := procLockFileEx.Call(
		uintptr(f.Fd()),
		flags,
		0,
		lockRange, 0,
		uintptr(unsafe.Pointer(ol)),
	)
	if r1 != 0 {
		return nil
	}
	if !wait && (errors.Is(err, errorLockViolation) || errors.Is(err, errorIOPending)) {
		return ErrLocked
	}
	return err
}

func unlockFile(f *os.File) error {
	ol := new(syscall.Overlapped)
	r1, _, err := procUnlockFileEx.Call(uintptr(f.Fd()), 0, lockRange, 0, uintptr(unsafe.Pointer(ol)))
	if r1 == 0 {
		return err
	}
	return nil
}
