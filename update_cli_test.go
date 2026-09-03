package main

import (
	"strings"
	"testing"

	"github.com/marcelritzschke/claude-code-feishu-companion/internal/update"
)

func TestUpdateNoticeIsQuietForDevBuild(t *testing.T) {
	t.Setenv("CLAUDE_COMPANION_STATE_DIR", t.TempDir())
	if got := updateNotice("dev"); got != "" {
		t.Errorf("updateNotice(dev) = %q, want empty", got)
	}
}

func TestUpdateNoticeIsQuietWithNothingCached(t *testing.T) {
	t.Setenv("CLAUDE_COMPANION_STATE_DIR", t.TempDir())
	if got := updateNotice("1.0.0"); got != "" {
		t.Errorf("updateNotice = %q, want empty with no cached check", got)
	}
}

func TestUpdateNoticeReportsNewerCachedVersion(t *testing.T) {
	t.Setenv("CLAUDE_COMPANION_STATE_DIR", t.TempDir())
	store, err := update.OpenStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordLatest("1.1.0"); err != nil {
		t.Fatal(err)
	}

	got := updateNotice("1.0.0")
	if got == "" {
		t.Fatal("updateNotice = empty, want a notice")
	}
	for _, want := range []string{"1.1.0", "1.0.0"} {
		if !strings.Contains(got, want) {
			t.Errorf("notice %q missing %q", got, want)
		}
	}
}

func TestUpdateNoticeIsQuietWhenCachedIsNotNewer(t *testing.T) {
	t.Setenv("CLAUDE_COMPANION_STATE_DIR", t.TempDir())
	store, err := update.OpenStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordLatest("1.0.0"); err != nil {
		t.Fatal(err)
	}

	if got := updateNotice("1.0.0"); got != "" {
		t.Errorf("updateNotice = %q, want empty", got)
	}
}
