//go:build windows

package update

import "os"

// replace swaps the new binary in the only way Windows allows: a running
// executable cannot be overwritten, and the program doing the updating is
// itself that executable. So the old one is moved aside first - renaming a
// running image is permitted - and the new one takes its place.
//
// The sidecar is deleted if nothing holds it any more. Anything still
// running the old program keeps it open, so a failure here is ordinary:
// the file is left for the next update to clear away.
func replace(target, staged string) error {
	old := target + ".old"
	os.Remove(old) // whatever a previous update had to leave behind
	if err := os.Rename(target, old); err != nil {
		return err
	}
	if err := os.Rename(staged, target); err != nil {
		os.Rename(old, target) // put the working program back
		return err
	}
	os.Remove(old)
	return nil
}
