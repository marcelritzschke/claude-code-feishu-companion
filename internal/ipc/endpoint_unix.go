//go:build !windows

package ipc

import (
	"fmt"
	"net"
	"os"
	"time"

	"github.com/marcelritzschke/wirelark/internal/paths"
)

// The endpoint is a unix socket in Wirelark's private 0700 directory, mode
// 0600. Filesystem permissions are the whole access control story here: no
// other user can open it, and nothing off this machine can reach it.
func endpointPath() (string, error) { return paths.File("daemon.sock") }

// Listen binds the daemon's endpoint. A leftover socket file is removed
// first: the caller holds the single-daemon lock, so anything still there
// belongs to a daemon that is already gone.
func Listen() (*Listener, error) {
	p, err := endpointPath()
	if err != nil {
		return nil, err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("remove stale socket %s: %w", p, err)
	}
	l, err := net.Listen("unix", p)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", p, err)
	}
	if err := os.Chmod(p, 0o600); err != nil {
		l.Close()
		return nil, fmt.Errorf("restrict %s: %w", p, err)
	}
	return &Listener{l: l, cleanup: func() { os.Remove(p) }}, nil
}

// Dial connects to a running daemon.
func Dial(timeout time.Duration) (*Conn, error) {
	p, err := endpointPath()
	if err != nil {
		return nil, err
	}
	c, err := net.DialTimeout("unix", p, timeout)
	if err != nil {
		return nil, err
	}
	return newConn(c), nil
}
