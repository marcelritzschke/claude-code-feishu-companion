// wirelark tells you when your coding agent needs you: it forwards Claude
// Code attention, completion, and failure events to a Feishu bot DM, so a
// turn you walked away from produces one understandable message instead of
// a terminal to watch.
//
// It is invoked per-event by Claude Code hooks ("send") and configured
// interactively via "init". The hook contract: read the payload from stdin,
// never write stdout, always exit 0, and never take longer than a few
// seconds - a broken bridge must be invisible to the Claude Code session
// that spawned it.
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}
	switch os.Args[1] {
	case "send":
		os.Exit(runSend(os.Args[2:]))
	case "init":
		if err := runInit(); err != nil {
			fmt.Fprintf(os.Stderr, "init failed: %v\n", err)
			os.Exit(1)
		}
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `usage:
  wirelark send [--dry-run]   run one hook event from stdin (hook entrypoint)
  wirelark init               interactive setup: credentials, test card, hook registration
`)
}
