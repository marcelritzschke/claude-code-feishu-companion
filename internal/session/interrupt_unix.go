//go:build !windows

package session

import (
	"errors"
	"os"
	"syscall"
)

// interruptSupported reports whether this platform can interrupt a turn.
const interruptSupported = true

// interruptProcess sends the session's process the same SIGINT its own
// terminal would on Ctrl+C. Claude Code answers a single SIGINT during a
// turn by stopping the work and returning to its prompt; it takes a second
// one, which is never sent from here, to exit.
func interruptProcess(pid int) error {
	if pid <= 0 {
		return errors.New("no process to interrupt")
	}
	p, err := os.FindProcess(pid) // never fails on unix
	if err != nil {
		return err
	}
	return p.Signal(syscall.SIGINT)
}
