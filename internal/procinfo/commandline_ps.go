//go:build darwin || freebsd || netbsd || openbsd

package procinfo

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// commandLine asks ps for the process argv. ps joins the arguments with
// spaces, so an argument containing a space cannot be recovered - which
// costs nothing here, because the flags and entries this package looks for
// never contain one.
func commandLine(pid int) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ps", "-o", "args=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return nil, err
	}
	return strings.Fields(string(out)), nil
}
