package ipc

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/marcelritzschke/wirelark/internal/debuglog"
)

// maxLineBytes bounds one message. Hook payloads are a few KB; a channel
// message from Feishu is smaller still. The bound is what keeps a confused
// peer from growing the daemon's memory without limit.
const maxLineBytes = 1 << 20

// Conn is one framed connection. Writes are serialized because the daemon
// pushes to a channel from whichever goroutine handled the Feishu event,
// while that channel's own reader runs concurrently.
type Conn struct {
	c  net.Conn
	br *bufio.Reader

	mu sync.Mutex
}

func newConn(c net.Conn) *Conn {
	return &Conn{c: c, br: bufio.NewReaderSize(c, 8192)}
}

// Write sends one message. body may be nil for messages that carry none.
func (c *Conn) Write(msgType string, body any) error {
	env := Envelope{Type: msgType}
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode %s: %w", msgType, err)
		}
		env.Body = raw
	}
	line, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("encode %s: %w", msgType, err)
	}
	line = append(line, '\n')

	c.mu.Lock()
	defer c.mu.Unlock()
	if _, err := c.c.Write(line); err != nil {
		return fmt.Errorf("write %s: %w", msgType, err)
	}
	return nil
}

// Read returns the next message, or io.EOF when the peer went away - which
// for a channel is how the daemon learns its Claude Code session ended.
func (c *Conn) Read() (Envelope, error) {
	line, err := c.readLine()
	if err != nil {
		return Envelope{}, err
	}
	var env Envelope
	if err := json.Unmarshal(line, &env); err != nil {
		return Envelope{}, fmt.Errorf("decode message: %w", err)
	}
	return env, nil
}

// readLine reads one whole message however long it is, up to the bound.
// The buffered reader is small because almost every message fits in it; a
// hook payload that does not is assembled here rather than being rejected
// for the size of a buffer, and a peer sending no newline at all is cut off
// at maxLineBytes rather than being allowed to grow the process.
func (c *Conn) readLine() ([]byte, error) {
	var whole []byte
	for {
		chunk, err := c.br.ReadSlice('\n')
		if err != nil && err != bufio.ErrBufferFull {
			return nil, err
		}
		if len(whole) == 0 && err == nil {
			if len(chunk) > maxLineBytes {
				return nil, errTooLong
			}
			return chunk, nil
		}
		whole = append(whole, chunk...)
		if len(whole) > maxLineBytes {
			return nil, errTooLong
		}
		if err == nil {
			return whole, nil
		}
	}
}

// errTooLong reports a message that overran the bound.
var errTooLong = fmt.Errorf("message longer than %d bytes", maxLineBytes)

// SetDeadline bounds a whole exchange, so a one-shot caller in a hook can
// never wait longer than the hook contract allows.
func (c *Conn) SetDeadline(t time.Time) error { return c.c.SetDeadline(t) }

// Close ends the connection.
func (c *Conn) Close() error { return c.c.Close() }

// Listener accepts connections from channels, hooks, and tooling.
type Listener struct {
	l net.Listener
	// admit runs on each new connection where the transport itself cannot
	// prove who the peer is. On a unix socket the file mode already did;
	// on a loopback port it is a shared-secret check.
	admit     func(*Conn) error
	cleanup   func()
	closeOnce sync.Once
}

// Accept waits for the next peer that proves it belongs here. A peer that
// fails to is dropped without a word and Accept keeps waiting: an
// unauthorized connection is not the caller's problem to handle.
func (l *Listener) Accept() (*Conn, error) {
	for {
		raw, err := l.l.Accept()
		if err != nil {
			return nil, err
		}
		c := newConn(raw)
		if l.admit == nil {
			return c, nil
		}
		if err := c.admit(l.admit); err != nil {
			debuglog.Printf("ipc: refused a connection: %v", err)
			c.Close()
			continue
		}
		return c, nil
	}
}

// admit runs the listener's check under a short deadline, then clears it so
// the connection can live as long as its session does.
func (c *Conn) admit(check func(*Conn) error) error {
	if err := c.SetDeadline(time.Now().Add(admitTimeout)); err != nil {
		return err
	}
	if err := check(c); err != nil {
		return err
	}
	return c.SetDeadline(time.Time{})
}

// admitTimeout bounds how long an unproven peer may hold a slot.
const admitTimeout = 5 * time.Second

// Close stops listening and removes whatever the endpoint left on disk, so
// a restarted daemon never inherits a stale address. Calling it twice is
// safe: the daemon closes it once to unblock its accept loop and once more
// on the way out.
func (l *Listener) Close() error {
	var err error
	l.closeOnce.Do(func() {
		err = l.l.Close()
		if l.cleanup != nil {
			l.cleanup()
		}
	})
	return err
}

// Request runs one short exchange: connect, send, read the reply. It is
// what a hook process and the command-line tooling use, and it never
// outlives timeout - a hook must not hold up the session that spawned it.
func Request(msgType string, body any, timeout time.Duration) (Envelope, error) {
	c, err := Dial(timeout)
	if err != nil {
		return Envelope{}, err
	}
	defer c.Close()
	if err := c.SetDeadline(time.Now().Add(timeout)); err != nil {
		return Envelope{}, err
	}
	if err := c.Write(msgType, body); err != nil {
		return Envelope{}, err
	}
	return c.Read()
}

// Ping reports whether a daemon is listening and answering.
func Ping(timeout time.Duration) bool {
	env, err := Request(TypeStatus, nil, timeout)
	return err == nil && env.Type != ""
}
