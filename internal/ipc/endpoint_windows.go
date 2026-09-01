//go:build windows

package ipc

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/marcelritzschke/wirelark/internal/paths"
	"github.com/marcelritzschke/wirelark/internal/secfile"
)

// Windows has no unix sockets, so the endpoint is a loopback port. A port is
// reachable by every process on the machine, which a 0600 socket file is
// not, so the daemon writes a secret alongside the address in a 0600 file
// and admits only peers that present it.
const addrFile = "daemon.addr"

// Listen binds a loopback port and publishes its address and secret.
func Listen() (*Listener, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen on loopback: %w", err)
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		l.Close()
		return nil, fmt.Errorf("generate endpoint secret: %w", err)
	}
	token := hex.EncodeToString(secret)

	p, err := paths.File(addrFile)
	if err != nil {
		l.Close()
		return nil, err
	}
	if err := secfile.WriteAtomic(p, []byte(l.Addr().String()+"\n"+token+"\n"), 0o600); err != nil {
		l.Close()
		return nil, fmt.Errorf("publish endpoint: %w", err)
	}
	return &Listener{
		l:       l,
		admit:   func(c *Conn) error { return checkToken(c, token) },
		cleanup: func() { os.Remove(p) },
	}, nil
}

// Dial connects to a running daemon and presents the published secret.
func Dial(timeout time.Duration) (*Conn, error) {
	addr, token, err := readEndpoint()
	if err != nil {
		return nil, err
	}
	raw, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, err
	}
	c := newConn(raw)
	if _, err := raw.Write([]byte(token + "\n")); err != nil {
		c.Close()
		return nil, fmt.Errorf("present endpoint secret: %w", err)
	}
	return c, nil
}

// readEndpoint reads the address and secret the daemon published.
func readEndpoint() (addr, token string, err error) {
	p, err := paths.File(addrFile)
	if err != nil {
		return "", "", err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return "", "", err
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 2 {
		return "", "", errors.New("endpoint file is incomplete")
	}
	return strings.TrimSpace(lines[0]), strings.TrimSpace(lines[1]), nil
}

// checkToken reads the peer's first line and compares it to the secret in
// constant time.
func checkToken(c *Conn, token string) error {
	line, err := c.br.ReadString('\n')
	if err != nil {
		return fmt.Errorf("read endpoint secret: %w", err)
	}
	if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(line)), []byte(token)) != 1 {
		return errors.New("endpoint secret did not match")
	}
	return nil
}
