//go:build windows

package daemon

import (
	"os/exec"
	"syscall"
)

// createNewProcessGroup keeps the daemon out of the starter's console
// group, so a Ctrl+C in the terminal that started it does not stop it.
const createNewProcessGroup = 0x00000200

func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: createNewProcessGroup,
		HideWindow:    true,
	}
}
