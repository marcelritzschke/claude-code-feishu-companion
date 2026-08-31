package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const debugEnv = "WIRELARK_DEBUG"

// debugLog appends to the debug log only when WIRELARK_DEBUG=1.
// It never logs secrets: only event names, project labels, and errors.
func debugLog(format string, args ...any) {
	if os.Getenv(debugEnv) != "1" {
		return
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return
	}
	p := filepath.Join(dir, "wirelark", "debug.log")
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, time.Now().Format(time.RFC3339)+" "+format+"\n", args...)
}
