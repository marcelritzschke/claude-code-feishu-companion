package daemon

import (
	"context"
	"time"

	"github.com/marcelritzschke/claude-code-feishu-companion/internal/debuglog"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/update"
)

// updateCheckInterval is how often the daemon polls GitHub for a newer
// release, once it has already checked at startup.
const updateCheckInterval = 24 * time.Hour

// updateFetchTimeout bounds one GitHub Releases call.
const updateFetchTimeout = 10 * time.Second

// watchForUpdates polls GitHub for a newer release at startup and every
// updateCheckInterval after. A dev build has no linked version to compare
// against, so it is skipped entirely rather than polling for nothing.
func (d *Daemon) watchForUpdates(ctx context.Context) {
	if update.IsDevBuild(d.version) {
		return
	}
	d.checkForUpdate(ctx)
	ticker := time.NewTicker(updateCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.checkForUpdate(ctx)
		}
	}
}

// checkForUpdate fetches the latest release, caches it for anything that
// wants a passive look (version, daemon --status), and announces it in
// Feishu the first time it is newer than both the running version and
// whatever was already announced. A version is announced at most once.
func (d *Daemon) checkForUpdate(ctx context.Context) {
	fctx, cancel := context.WithTimeout(ctx, updateFetchTimeout)
	rel, err := d.fetchRelease(fctx)
	cancel()
	if err != nil {
		debuglog.Printf("check update: %v", err)
		return
	}

	store, err := update.OpenStore()
	if err != nil {
		debuglog.Printf("open update store: %v", err)
		return
	}
	if err := store.RecordLatest(rel.Version); err != nil {
		debuglog.Printf("record latest version: %v", err)
	}

	if !update.IsNewer(rel.Version, d.version) {
		return
	}
	_, notified, err := store.Cached()
	if err != nil {
		debuglog.Printf("read update cache: %v", err)
		return
	}
	// notified is empty before anything has ever been announced, and
	// IsNewer treats an empty version as unparseable rather than as
	// "nothing yet" - so an empty cache must fall through, not be
	// compared.
	if notified != "" && !update.IsNewer(rel.Version, notified) {
		return
	}

	d.say(ctx, update.Announce(rel, d.version))
	if err := store.RecordNotified(rel.Version); err != nil {
		debuglog.Printf("record notified version: %v", err)
	}
}
