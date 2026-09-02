// wirelark tells you when your coding agent needs you, and lets you
// continue the session it is telling you about.
//
// It forwards Claude Code attention, completion, and failure events to a
// Feishu bot DM, so a turn you walked away from produces one understandable
// message instead of a terminal to watch. From that DM you can pick one of
// the Claude Code sessions running on your computer and send it a follow-up
// instruction: the same session, still on your machine, still yours when
// you get back to the keyboard.
//
// Three roles share this binary. "send" is the hook entrypoint, invoked
// per-event by Claude Code: read the payload from stdin, never write
// stdout, always exit 0, and never take longer than a few seconds - a
// broken bridge must be invisible to the session that spawned it.
// "channel" is the MCP channel server Claude Code spawns with a session,
// under the same rule for the same reason. "daemon" is the one persistent
// role, and the only one that talks to Feishu.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/marcelritzschke/wirelark/internal/channel"
	"github.com/marcelritzschke/wirelark/internal/config"
	"github.com/marcelritzschke/wirelark/internal/daemon"
	"github.com/marcelritzschke/wirelark/internal/ipc"
	"github.com/marcelritzschke/wirelark/internal/tui"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}
	switch os.Args[1] {
	case "send":
		os.Exit(runSend(os.Args[2:]))
	case "channel":
		os.Exit(runChannel())
	case "daemon":
		if err := runDaemon(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "daemon: %v\n", err)
			os.Exit(1)
		}
	case "init":
		if err := runInit(); err != nil {
			// Quitting a setup question is a decision, not a fault. It
			// gets an exit code so a script can tell, but not the shape
			// of a crash report.
			if errors.Is(err, tui.ErrAborted) {
				fmt.Fprintln(os.Stderr, "setup cancelled")
				os.Exit(130)
			}
			fmt.Fprintf(os.Stderr, "init failed: %v\n", err)
			os.Exit(1)
		}
	case "-h", "--help", "help":
		usage()
	case "-v", "--version", "version":
		printVersion()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `usage:
  wirelark init               interactive setup: scan a QR code in Feishu, test card, hook registration
  wirelark send [--dry-run]   run one hook event from stdin (hook entrypoint)
  wirelark channel            serve one Claude Code session's channel (spawned by Claude Code)
  wirelark daemon [--stop|--status]
                              run the bridge (started automatically when needed)
  wirelark version            print the version
`)
}

// runChannel serves one session's channel. Like the hook entrypoint it
// always succeeds: this process shares stdio with a Claude Code session, so
// a failure here must cost the user their remote link and nothing else.
func runChannel() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Without a readable config there is no daemon to relay to and no way
	// to know whether the user allowed remote approvals - so relay nothing
	// and let the session run on as if Wirelark were not there.
	relay := false
	if cfg, err := config.Load(); err == nil {
		relay = cfg.RemotePermissionsEnabled()
	} else {
		debugLog("channel: load config: %v", err)
	}

	if err := channel.Run(ctx, relay); err != nil {
		debugLog("channel: %v", err)
	}
	return 0
}

// runDaemon runs, stops, or reports on the bridge.
func runDaemon(args []string) error {
	for _, a := range args {
		switch a {
		case "--stop", "-stop":
			return daemon.Stop()
		case "--status", "-status":
			if ipc.Ping(daemonProbeTimeout) {
				fmt.Println("wirelark daemon is running")
				return nil
			}
			fmt.Println("wirelark daemon is not running")
			return nil
		default:
			return fmt.Errorf("unknown option %q", a)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return daemon.Run(ctx)
}
