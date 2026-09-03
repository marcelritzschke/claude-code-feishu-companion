// Package flock is Claude Companion's advisory file locking, in the two flavours it
// needs: a blocking lock to serialize the state file between concurrent
// hook processes, and a non-blocking one that lets exactly one daemon own
// the machine.
package flock

import "os"

// Lock takes an exclusive lock on f, waiting for whoever holds it.
func Lock(f *os.File) error { return lockFile(f, true) }

// TryLock takes an exclusive lock on f, returning ErrLocked immediately if
// another process holds it. That failure is the answer, not a fault: it is
// how a second daemon learns the first one is already running.
func TryLock(f *os.File) error { return lockFile(f, false) }

// Unlock releases a lock taken by Lock or TryLock.
func Unlock(f *os.File) error { return unlockFile(f) }
