package main

import "fmt"

// version, commit, and date are set by the release build via
// -ldflags "-X main.version=... -X main.commit=... -X main.date=...".
// A build outside that pipeline (go build, go install) keeps the
// zero-value defaults below.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func printVersion() {
	fmt.Printf("wirelark %s (commit %s, built %s)\n", version, commit, date)
}
