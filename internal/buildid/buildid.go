// Package buildid identifies the program image a Claude Companion process
// is running, so that the one long-lived process can tell it has been
// replaced.
//
// Everything else here is short-lived: a hook runs for one event, a
// channel for one session. The daemon runs for days, and an install puts a
// new program at the same path underneath it. Nothing in the process
// notices - it keeps its own image open, keeps answering, and keeps
// serving the code the user has just replaced. The symptom is the worst
// kind: the install worked, the version command reports the new build, and
// the product behaves as it did before.
package buildid

import (
	"fmt"
	"os"
)

// The identity is taken at init, before main has run and long before
// anything can replace the file. Asking later would read whatever is at
// the path now, which for the process that needs this answer is precisely
// the wrong file.
var (
	path  string
	stamp string
)

func init() {
	path, _ = os.Executable()
	stamp = StampOf(path)
}

// Path is the executable this process was started from, as it was named at
// startup.
//
// It is the install location rather than this process's image, and after a
// replacement those are different things: on Linux a running program's own
// path reads back as "/usr/local/bin/thing (deleted)", which names nothing
// that can be started. Whoever has to start a fresh copy wants the path -
// what is installed there now is the build that should be running.
func Path() string { return path }

// Stamp identifies the image this process is running. It is empty when the
// executable could not be read, which callers must treat as "unknown" and
// never as "different": retiring a working daemon over a failed stat would
// trade a real service for a guess.
func Stamp() string { return stamp }

// StampOf identifies the executable at p as it is right now.
//
// Size and modification time, because they are what every platform agrees
// on and what an install always changes: a release carries its own build
// time through the archive, so two installs of one release stamp the same
// and two different builds never do. Hashing fifteen megabytes on a timer
// would buy nothing over that.
func StampOf(p string) string {
	if p == "" {
		return ""
	}
	info, err := os.Stat(p)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%d-%d", info.Size(), info.ModTime().UnixNano())
}

// Replaced reports that the program installed where this process came from
// is no longer the program this process is running.
//
// Either side being unknown means no: this is read by a daemon deciding
// whether to take itself away from the sessions attached to it, and that
// decision is only ever made on two answers that are both certain and
// different.
func Replaced() bool {
	now := StampOf(path)
	return stamp != "" && now != "" && now != stamp
}
