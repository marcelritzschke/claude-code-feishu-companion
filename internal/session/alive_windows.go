//go:build windows

package session

import "os"

// processAlive reports whether a pid still names a running process.
// FindProcess opens a handle on Windows and fails when nothing is there,
// which makes it the existence check unix needs a signal for.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	p.Release()
	return true
}
