package channel

import (
	"sync"

	"github.com/marcelritzschke/wirelark/internal/ipc"
)

// atomicConn holds the current daemon connection. The MCP loop and the
// daemon loop run concurrently: a permission prompt can arrive from Claude
// Code at the same moment the daemon link is being re-established.
type atomicConn struct {
	mu sync.Mutex
	c  *ipc.Conn
}

func (a *atomicConn) set(c *ipc.Conn) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.c = c
}

// clear drops c only if it is still the current connection, so a slow
// teardown cannot unset a link that has already been replaced.
func (a *atomicConn) clear(c *ipc.Conn) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.c == c {
		a.c = nil
	}
}

func (a *atomicConn) get() *ipc.Conn {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.c
}
