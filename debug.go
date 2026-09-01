package main

import "github.com/marcelritzschke/wirelark/internal/debuglog"

// debugLog keeps the short call sites in package main while the log itself
// lives in one place every role shares.
func debugLog(format string, args ...any) { debuglog.Printf(format, args...) }
