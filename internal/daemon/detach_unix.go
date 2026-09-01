//go:build !windows

package daemon

import (
	"os/exec"
	"syscall"
)

// detach puts the daemon in its own session, so closing the terminal that
// happened to start it - or the Claude Code session whose hook did - does
// not take it down.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
