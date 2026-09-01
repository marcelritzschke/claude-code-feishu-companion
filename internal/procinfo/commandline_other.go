//go:build !linux && !darwin && !freebsd && !netbsd && !openbsd

package procinfo

import "errors"

// commandLine has no portable implementation here (Windows above all).
// Callers turn the error into "unconfirmed", never into "not available".
func commandLine(int) ([]string, error) {
	return nil, errors.New("reading another process's command line is not supported on this platform")
}
