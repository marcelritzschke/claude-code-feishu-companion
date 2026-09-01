//go:build !windows

package session

import (
	"os"
	"syscall"
)

// processAlive reports whether a pid still names a running process. Signal
// 0 performs the permission and existence checks without delivering
// anything, which is exactly the question being asked.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid) // never fails on unix
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	return err == nil || err == os.ErrPermission
}
