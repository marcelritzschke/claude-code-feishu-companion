//go:build !linux && !darwin && !freebsd && !netbsd && !openbsd && !windows

package procinfo

import "errors"

// commandLine has no implementation on the platforms left over here.
// Callers turn the error into "unconfirmed", never into "not available".
func commandLine(int) ([]string, error) {
	return nil, errors.New("reading another process's command line is not supported on this platform")
}
