//go:build linux

package procinfo

import (
	"os"
	"strconv"
	"strings"
)

// commandLine reads the process argv from procfs, where it is stored as
// NUL-separated arguments. This is the path WSL takes too.
func commandLine(pid int) ([]string, error) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
	if err != nil {
		return nil, err
	}
	args := strings.Split(strings.TrimRight(string(data), "\x00"), "\x00")
	return args, nil
}
