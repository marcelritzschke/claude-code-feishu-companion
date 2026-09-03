//go:build windows

package session

import "errors"

// interruptSupported reports whether this platform can interrupt a turn.
// Windows has no way to deliver the equivalent of Ctrl+C to another
// console's process without attaching to its console, so the interrupt is
// not offered there rather than offered and broken.
const interruptSupported = false

func interruptProcess(int) error {
	return errors.New("interrupting a turn is not supported on Windows")
}
