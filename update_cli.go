package main

import (
	"context"
	"fmt"
	"os"

	"github.com/marcelritzschke/claude-code-feishu-companion/internal/update"
)

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

// runUpdate does a live check against GitHub, independent of the daemon's
// cache or schedule, and reports what it found.
func runUpdate() int {
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
	if update.IsDevBuild(current) {
		fmt.Printf("latest release is %s (this build is %s, not a release, so there is nothing to compare against)\n", rel.Version, current)
		return 0
	}
	if !update.IsNewer(rel.Version, current) {
		fmt.Println("claude-companion is up to date.")
		return 0
	}

	fmt.Println(update.Announce(rel, current))
	// The user just saw this themselves, so the daemon's background check
	// must not announce it again in Feishu.
	if store != nil {
		if err := store.RecordNotified(rel.Version); err != nil {
			debugLog("update: record notified version: %v", err)
		}
	}
	return 0
}
