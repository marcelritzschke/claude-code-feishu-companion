//go:build !windows

package update

import "os"

// replace swaps the new binary in with a rename. A process already running
// the old program keeps the inode it opened and is undisturbed, which is
// why the daemon is restarted deliberately rather than as a side effect of
// installing.
func replace(target, staged string) error {
	return os.Rename(staged, target)
}
