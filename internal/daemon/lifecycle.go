package daemon

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/marcelritzschke/claude-code-feishu-companion/internal/config"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/debuglog"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/flock"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/ipc"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/paths"
)

// The daemon must be running for Feishu to reach a session, but starting it
// is not something the user should have to think about. So whoever needs it
// starts it: a hook process, or a channel coming up with its session. The
// lock file is what keeps that from producing a second daemon.
const (
	lockFileName = "daemon.lock"
	// startTimeout is how long a starter waits for the new daemon to answer
	// before giving up and getting on with its own work.
	startTimeout = 5 * time.Second
	// dialTimeout bounds a single attempt to reach the daemon.
	dialTimeout = 2 * time.Second
	// stopTimeout is how long a replacement waits for the daemon it asked
	// to leave. A stopping daemon still has cards to settle, so this is
	// longer than a start: it holds the single-daemon lock until it exits,
	// and a new one started too early would only lose the race for it.
	stopTimeout = 20 * time.Second
)

// ErrAlreadyRunning reports that another daemon holds the lock.
var ErrAlreadyRunning = errors.New("a claude-companion daemon is already running")

// acquire takes the single-daemon lock. The returned file must stay open
// for as long as the daemon runs: the lock lives on the descriptor, so
// closing it - or the process exiting, however it exits - releases it.
func acquire() (*os.File, error) {
	p, err := paths.File(lockFileName)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := flock.TryLock(f); err != nil {
		f.Close()
		if errors.Is(err, flock.ErrLocked) {
			return nil, ErrAlreadyRunning
		}
		return nil, err
	}
	return f, nil
}

// EnsureRunning starts a daemon if none is answering, and returns once one
// is - or once it is clear one will not be.
//
// It is called from a hook process, so it must be quick and it must never
// be fatal: a daemon that will not start costs remote continuation, and the
// caller falls back to delivering its own notification.
func EnsureRunning() error {
	if ipc.Ping(dialTimeout) {
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}

	cmd := exec.Command(exe, "daemon")
	// The daemon outlives whatever started it, and it shares no stream with
	// the Claude Code session: a hook's stdout belongs to the hook contract
	// and a channel's to the MCP protocol.
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer devNull.Close()
	cmd.Stdin, cmd.Stdout, cmd.Stderr = devNull, devNull, devNull
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start daemon: %w", err)
	}
	// Nothing will ever wait on this process, so hand it to init rather
	// than leaving a zombie behind in the session's process tree.
	go func() { _ = cmd.Wait() }()
	debuglog.Printf("started daemon as pid %d", cmd.Process.Pid)

	deadline := time.Now().Add(startTimeout)
	for time.Now().Before(deadline) {
		if ipc.Ping(dialTimeout) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return errors.New("the daemon did not start answering in time")
}

// EnsureCurrent starts a daemon running the configuration that is on disk
// now, replacing one that predates it.
//
// A daemon reads the config once and opens its Feishu connection from what
// it read: credentials the user has since replaced live on in a process
// that is still perfectly happy to answer a ping. That daemon holds the
// return path for the previous Feishu app, so anything the user sends the
// new bot reaches nobody, and setup would report that Feishu cannot reach
// this computer when the truth is that this computer is listening to the
// wrong app.
//
// Setup calls this because setup is what changes the configuration. Hooks
// and channels keep to EnsureRunning: they have no reason to take a
// running daemon away from the sessions attached to it.
func EnsureCurrent() error {
	if _, err := replaceIfStale(); err != nil {
		return err
	}
	return EnsureRunning()
}

// replaceIfStale stops a running daemon that read an older configuration
// than the one on disk, and reports whether it stopped one. A daemon that
// is current, and no daemon at all, are both left alone: starting one is
// EnsureCurrent's business.
func replaceIfStale() (bool, error) {
	stamp, err := config.Stamp()
	if err != nil {
		return false, err
	}
	st, ok := status()
	if !ok || !st.ConfigStamp.Before(stamp) {
		return false, nil
	}
	debuglog.Printf("daemon predates the configuration; restarting it")
	if err := Stop(); err != nil {
		return false, fmt.Errorf("replacing the daemon that is running an older configuration: %w", err)
	}
	if err := waitUntilGone(); err != nil {
		return false, err
	}
	return true, nil
}

// status asks a running daemon what it is running. A false return means no
// daemon answered, which is not an error: starting one is the caller's
// next move either way.
func status() (ipc.Status, bool) {
	env, err := ipc.Request(ipc.TypeStatus, nil, dialTimeout)
	if err != nil {
		return ipc.Status{}, false
	}
	var st ipc.Status
	if err := env.Into(&st); err != nil {
		return ipc.Status{}, false
	}
	return st, true
}

// waitUntilGone waits for a stopping daemon to let go of the single-daemon
// lock.
//
// The lock is the signal, not the socket. A daemon closes its listener
// early in its shutdown and then stays alive to settle the cards standing
// on the user's phone, so it stops answering seconds before it exits. A
// replacement started in that window takes the lock check, not the socket,
// and exits with ErrAlreadyRunning - leaving the caller waiting for a
// daemon that already gave up.
func waitUntilGone() error {
	deadline := time.Now().Add(stopTimeout)
	for {
		// Taking the lock is the proof it was free. It is handed straight
		// back for the replacement to take: nothing else is racing for it
		// here, and a daemon that another hook starts in the meantime is
		// exactly what EnsureRunning is looking for anyway.
		if f, err := acquire(); err == nil {
			f.Close()
			return nil
		} else if !errors.Is(err, ErrAlreadyRunning) {
			return err
		}
		if !time.Now().Before(deadline) {
			return errors.New("the daemon did not stop in time")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// Stop asks a running daemon to exit.
func Stop() error {
	env, err := ipc.Request(ipc.TypeStop, nil, dialTimeout)
	if err != nil {
		return fmt.Errorf("no daemon answered: %w", err)
	}
	var ack ipc.Ack
	if err := env.Into(&ack); err != nil {
		return err
	}
	if !ack.OK {
		return errors.New(ack.Err)
	}
	return nil
}
