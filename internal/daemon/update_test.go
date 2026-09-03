package daemon

import (
	"context"
	"errors"
	"testing"

	"github.com/marcelritzschke/claude-code-feishu-companion/internal/session"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/update"
)

func TestCheckForUpdateNotifiesOnceForNewerVersion(t *testing.T) {
	d, rec, _ := fixture(t, session.Ready)
	d.version = "1.0.0"
	d.fetchRelease = func(context.Context) (update.Release, error) {
		return update.Release{Version: "1.1.0", URL: "https://example.com/releases/v1.1.0"}, nil
	}

	d.checkForUpdate(context.Background())
	if len(rec.texts) != 1 {
		t.Fatalf("texts = %v, want exactly one notice", rec.texts)
	}
	if got := rec.texts[0]; got == "" {
		t.Fatal("notice text is empty")
	}

	// A second check finding the same release must not notify again.
	d.checkForUpdate(context.Background())
	if len(rec.texts) != 1 {
		t.Fatalf("texts after second check = %v, want still exactly one", rec.texts)
	}
}

func TestCheckForUpdateStaysQuietWhenCurrent(t *testing.T) {
	d, rec, _ := fixture(t, session.Ready)
	d.version = "1.1.0"
	d.fetchRelease = func(context.Context) (update.Release, error) {
		return update.Release{Version: "1.1.0"}, nil
	}

	d.checkForUpdate(context.Background())
	if len(rec.texts) != 0 {
		t.Fatalf("texts = %v, want none", rec.texts)
	}
}

func TestCheckForUpdateStaysQuietOnFetchError(t *testing.T) {
	d, rec, _ := fixture(t, session.Ready)
	d.version = "1.0.0"
	d.fetchRelease = func(context.Context) (update.Release, error) {
		return update.Release{}, errors.New("network down")
	}

	d.checkForUpdate(context.Background())
	if len(rec.texts) != 0 {
		t.Fatalf("texts = %v, want none", rec.texts)
	}
}

func TestWatchForUpdatesSkipsDevBuild(t *testing.T) {
	d, _, _ := fixture(t, session.Ready)
	d.version = "dev"
	called := false
	d.fetchRelease = func(context.Context) (update.Release, error) {
		called = true
		return update.Release{Version: "9.9.9"}, nil
	}

	// A dev build has nothing to compare against, so watchForUpdates must
	// return immediately rather than entering its poll loop.
	d.watchForUpdates(context.Background())
	if called {
		t.Fatal("fetchRelease was called for a dev build")
	}
}
