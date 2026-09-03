package daemon

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

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
