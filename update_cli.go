package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/marcelritzschke/claude-code-feishu-companion/internal/daemon"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/tui"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/update"
)

// downloadTimeout bounds the whole download: two release assets over
// HTTPS, a few megabytes together.
const downloadTimeout = 5 * time.Minute

// updateNotice reports, without making a network call, whether a newer
// release than current is cached from the daemon's last background check.
// It returns "" when there is nothing to say: current is a dev build, no
// check has completed yet, or the cache holds nothing newer.
func updateNotice(current string) string {
	if update.IsDevBuild(current) {
		return ""
	}
	store, err := update.OpenStore()
	if err != nil {
		return ""
	}
	latest, _, err := store.Cached()
	if err != nil || latest == "" || !update.IsNewer(latest, current) {
		return ""
	}
	return fmt.Sprintf("claude-companion %s is available (you're on %s). Run `claude-companion update` for details.", latest, current)
}

// runUpdate checks GitHub for a newer release and installs it.
//
// Installing is the whole point of the command, but it is also the one
// thing here that cannot be undone by running it again, so it is offered
// rather than assumed: the user is told what will change - including the
// part they have to decide, which is what happens to a permission card
// still standing on their phone - and answers before anything moves.
func runUpdate(args []string) int {
	defer tui.Close()

	var checkOnly, assumeYes bool
	for _, a := range args {
		switch a {
		case "--check", "-check":
			checkOnly = true
		case "--yes", "-y":
			assumeYes = true
		default:
			fmt.Fprintf(os.Stderr, "update: unknown option %q\n", a)
			return 1
		}
	}

	rel, err := update.Fetch(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "update: %v\n", err)
		return 1
	}

	store, err := update.OpenStore()
	if err != nil {
		debugLog("update: open store: %v", err)
	} else if err := store.RecordLatest(rel.Version); err != nil {
		debugLog("update: record latest version: %v", err)
	}

	current := currentVersion()
	switch {
	case update.IsDevBuild(current):
		// Nothing here can say whether the release is ahead of a build
		// that names no release, so installing it is a choice about which
		// program to run rather than an upgrade.
		fmt.Printf("latest release is %s (this build is %s, not a release, so there is nothing to compare against)\n", rel.Version, current)
	case update.IsNewer(rel.Version, current):
		fmt.Println(update.Announce(rel, current))
	default:
		fmt.Println("claude-companion is up to date.")
		return 0
	}

	if checkOnly {
		return 0
	}
	if err := install(rel, current, assumeYes); err != nil {
		if errors.Is(err, tui.ErrAborted) {
			fmt.Fprintln(os.Stderr, "update cancelled")
			return 130
		}
		fmt.Fprintf(os.Stderr, "update: %v\n", err)
		return 1
	}

	// The user just saw this themselves, so the daemon's background check
	// must not announce it again in Feishu.
	if store != nil {
		if err := store.RecordNotified(rel.Version); err != nil {
			debugLog("update: record notified version: %v", err)
		}
	}
	return 0
}

// install replaces this program with the release, and puts the daemon back
// the way it found it.
func install(rel update.Release, current string, assumeYes bool) error {
	target, err := update.TargetPath()
	if err != nil {
		return err
	}
	// Writability is settled before the network is touched: a binary the
	// user cannot replace should cost them a message, not a download and
	// a stopped daemon.
	if err := update.Writable(target); err != nil {
		return fmt.Errorf("%w\ninstall it another way, or re-run install.sh with a writable INSTALL_DIR", err)
	}

	if !assumeYes && !tui.Interactive() {
		return errors.New("this replaces the program, so it asks first - and there is no terminal here to ask in.\nRe-run with --yes to install without being asked, or --check to only report")
	}
	if !assumeYes {
		ok, err := tui.Confirm(fmt.Sprintf("Install claude-companion %s over %s?", rel.Version, current),
			"Replaces "+target+" and restarts the daemon. Hooks pick the new version up at once;\n"+
				"a Claude Code session already running keeps its old channel until it restarts.\n"+
				"Answer any permission card still waiting in Feishu first - a restart leaves it unanswerable.")
		if err != nil {
			return err
		}
		if !ok {
			return tui.ErrAborted
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), downloadTimeout)
	defer cancel()
	fmt.Printf("downloading %s\n", update.AssetName(rel.Version, runtime.GOOS, runtime.GOARCH))
	staged, err := update.Download(ctx, rel.Version, filepath.Dir(target))
	if err != nil {
		return err
	}

	// Only now is anything taken away from the user. The daemon goes first
	// because Windows will not replace a running program's file, and
	// because a daemon left up would be the old version either way.
	running := daemon.Running()
	if running {
		fmt.Println("stopping the daemon")
		if err := daemon.StopAndWait(); err != nil {
			os.Remove(staged)
			return fmt.Errorf("stopping the daemon before replacing it: %w", err)
		}
	}

	if err := update.Install(target, staged); err != nil {
		if running {
			// The old program is still there and still works; the user
			// should not be left without a bridge because of a failed
			// upgrade.
			if startErr := daemon.EnsureRunning(); startErr != nil {
				debugLog("update: restart after failed install: %v", startErr)
			}
		}
		return err
	}
	fmt.Printf("installed %s to %s\n", rel.Version, target)

	if running {
		// EnsureRunning starts this program's own path, which now holds
		// the release that was just installed.
		if err := daemon.EnsureRunning(); err != nil {
			return fmt.Errorf("the update is installed, but the daemon did not restart: %w\nstart it with `claude-companion daemon`", err)
		}
		fmt.Println("daemon restarted")
	}

	// Say what is now installed, from the installed program rather than
	// from this one: this process is still the old version, and the user
	// asked what they have, not what asked for it.
	if out, err := exec.Command(target, "--version").Output(); err == nil {
		fmt.Print(string(out))
	}
	return nil
}
