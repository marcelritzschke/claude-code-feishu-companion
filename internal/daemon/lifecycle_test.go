package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/marcelritzschke/claude-code-feishu-companion/internal/config"
)

// runningDaemon serves on the test's own socket until it is asked to stop,
// standing in for the daemon a setup run finds already there.
func runningDaemon(t *testing.T, stamp time.Time) (*Daemon, <-chan struct{}) {
	t.Helper()
	d := New(&config.Config{Notify: config.NotifyImportant, Remote: config.On}, newRecorder(), nil, "dev")
	d.configStamp = stamp

	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		if err := d.Serve(ctx); err != nil {
			t.Errorf("serve: %v", err)
		}
	}()
	t.Cleanup(func() { cancel(); <-stopped })

	deadline := time.Now().Add(10 * time.Second)
	for !ipcAnswers() {
		if time.Now().After(deadline) {
			t.Fatal("the daemon never started answering")
		}
		time.Sleep(time.Millisecond)
	}
	return d, stopped
}

func ipcAnswers() bool {
	_, ok := status()
	return ok
}

// writeConfig puts a config file on disk and returns when it was written.
func writeConfig(t *testing.T, at time.Time) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte("app_id = 'cli_test'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, at, at); err != nil {
		t.Fatal(err)
	}
	t.Setenv(config.EnvVar, p)
}

// A daemon started before setup rewrote the config holds the Feishu
// connection for credentials that are gone. It has to go.
func TestADaemonOlderThanTheConfigIsReplaced(t *testing.T) {
	t.Setenv("CLAUDE_COMPANION_STATE_DIR", t.TempDir())
	writeConfig(t, time.Now())
	_, stopped := runningDaemon(t, time.Now().Add(-time.Hour))

	replaced, err := replaceIfStale()
	if err != nil {
		t.Fatal(err)
	}
	if !replaced {
		t.Fatal("want the stale daemon replaced")
	}
	select {
	case <-stopped:
	case <-time.After(10 * time.Second):
		t.Fatal("the stale daemon is still running")
	}
	if ipcAnswers() {
		t.Fatal("the stale daemon is still answering")
	}
}

// A daemon that read the configuration that is on disk is left alone:
// restarting it would take the sessions attached to it down with it.
func TestACurrentDaemonIsLeftAlone(t *testing.T) {
	t.Setenv("CLAUDE_COMPANION_STATE_DIR", t.TempDir())
	written := time.Now().Add(-time.Hour)
	writeConfig(t, written)
	runningDaemon(t, written.Add(time.Second))

	replaced, err := replaceIfStale()
	if err != nil {
		t.Fatal(err)
	}
	if replaced {
		t.Fatal("a daemon holding the current configuration must not be replaced")
	}
	if !ipcAnswers() {
		t.Fatal("the daemon should still be running")
	}
}

// Nothing to replace is not a failure: setup starts one either way.
func TestNoDaemonIsNothingToReplace(t *testing.T) {
	t.Setenv("CLAUDE_COMPANION_STATE_DIR", t.TempDir())
	writeConfig(t, time.Now())

	replaced, err := replaceIfStale()
	if err != nil {
		t.Fatal(err)
	}
	if replaced {
		t.Fatal("want nothing replaced when no daemon is running")
	}
}

// A daemon closes its socket seconds before it exits: it stays alive to
// settle the cards on the user's phone, and holds the single-daemon lock
// the whole time. A replacement started in that window would be turned
// away by the lock, so the wait has to outlast it.
func TestWaitingOutADaemonThatIsStillHoldingTheLock(t *testing.T) {
	t.Setenv("CLAUDE_COMPANION_STATE_DIR", t.TempDir())
	held, err := acquire()
	if err != nil {
		t.Fatal(err)
	}
	const shutdown = 200 * time.Millisecond
	go func() {
		time.Sleep(shutdown)
		held.Close()
	}()

	start := time.Now()
	if err := waitUntilGone(); err != nil {
		t.Fatal(err)
	}
	if waited := time.Since(start); waited < shutdown {
		t.Fatalf("returned after %s, before the daemon let go of the lock at %s", waited, shutdown)
	}
}
