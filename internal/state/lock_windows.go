//go:build windows

package state

import (
	"os"
	"syscall"
	"unsafe"
)

// LockFileEx is not exposed by the syscall package, so it is called
// directly from kernel32. The lock is a blocking exclusive lock on the
// first byte of the lock file.
var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx   = kernel32.NewProc("LockFileEx")
	procUnlockFileEx = kernel32.NewProc("UnlockFileEx")
)

const (
	lockRange             = 1
	lockfileExclusiveLock = 0x00000002
)

func lockFile(f *os.File) error {
	ol := new(syscall.Overlapped)
	r1, _, err := procLockFileEx.Call(
		uintptr(f.Fd()),
		lockfileExclusiveLock,
		0,
		lockRange, 0,
		uintptr(unsafe.Pointer(ol)),
	)
	if r1 == 0 {
		return err
	}
	return nil
}

func unlockFile(f *os.File) error {
	ol := new(syscall.Overlapped)
	r1, _, err := procUnlockFileEx.Call(uintptr(f.Fd()), 0, lockRange, 0, uintptr(unsafe.Pointer(ol)))
	if r1 == 0 {
		return err
	}
	return nil
}
