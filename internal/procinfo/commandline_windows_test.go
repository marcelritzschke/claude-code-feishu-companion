//go:build windows

package procinfo

import (
	"os"
	"testing"
)

// TestCommandLineIsReadableOnWindows is the cross-platform test's twin
// without the skip. TestCommandLineReadsOwnProcess steps aside on a
// platform that cannot answer, which is how this package treated Windows
// for as long as there was no reader here; a skip would now hide the
// reader breaking. It also checks the arguments after the program name,
// because those are the ones a session's channel flag lives in.
func TestCommandLineIsReadableOnWindows(t *testing.T) {
	argv, err := commandLine(os.Getpid())
	if err != nil {
		t.Fatalf("commandLine: %v", err)
	}
	if len(argv) < len(os.Args) {
		t.Errorf("commandLine returned %d arguments, os.Args has %d: %q", len(argv), len(os.Args), argv)
	}
}

// TestCommandLineOfAMissingProcess checks the failure that matters: a pid
// nothing answers for must report an error, because Enabled turns that
// into "unconfirmed" and anything else into a claim about the session.
func TestCommandLineOfAMissingProcess(t *testing.T) {
	// Pid 0 is the idle process: it exists, and nothing may open it.
	if _, err := commandLine(0); err == nil {
		t.Error("commandLine(0) succeeded; want an error")
	}
}
