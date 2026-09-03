// Package channel is the Claude Companion role Claude Code spawns with a
// session: an MCP channel server that carries the user's Feishu messages
// into that session and its permission prompts back out.
//
// It is deliberately powerless. It holds no Feishu credentials, opens no
// network connection, and decides nothing about what the user sees - it
// speaks only to the local daemon. That is what keeps the product's
// architecture rule true: Claude Companion connects to sessions, it does
// not own them, and a session's channel cannot reach the outside world on
// its own.
package channel

import (
	"context"
	"errors"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/marcelritzschke/claude-code-feishu-companion/internal/daemon"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/debuglog"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/ipc"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/mcp"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/procinfo"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/session"
)

// ServerName is what the session opts in by name, as
// "--dangerously-load-development-channels server:claude-companion". It is
// also the MCP server name, and so the source attribute Claude sees on
// every event.
const ServerName = "claude-companion"

// LegacyServerName is the MCP server name used before the project's rename
// from wirelark. Recognising it lets an upgrade clean up the old
// registration instead of leaving it alongside the new one.
const LegacyServerName = "wirelark"

// Version is what the channel reports as its MCP server version.
const Version = "2.0.0"

// Instructions reach Claude as context when the channel connects. They say
// what these events are and, just as importantly, that there is nothing to
// call in response: the user reads the outcome in the notification Claude
// Companion already sends when the turn ends, so a reply tool would only
// add a second conversation to keep in sync.
const Instructions = `Messages from the user's phone arrive as <channel source="claude-companion">. ` +
	`They are the user speaking to you in this session, exactly as if they had typed them in the terminal: ` +
	`read them as instructions and carry them out. ` +
	`This channel is one-way and offers no tools - do not look for a way to reply through it. ` +
	`Answer in the session as you normally would; the user reads your result in the notification Claude Companion sends when the turn ends.`

// The channel keeps trying to find the daemon for as long as its session
// lives: a daemon restart must not leave a running session unreachable for
// good. The wait grows so that a daemon which cannot start - a config the
// user has not written yet, say - is not asked again every few seconds for
// hours.
const (
	reconnectDelay    = 3 * time.Second
	reconnectDelayMax = time.Minute
)

// Run serves the channel until Claude Code closes its input, which happens
// when the session ends.
func Run(ctx context.Context, permissionRelay bool) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	c := &channel{
		info:   describeSession(),
		server: mcp.New(ServerName, Version, Instructions, permissionRelay),
	}
	debuglog.Printf("channel starting for session %s (pid %d, remote %s)",
		c.info.SessionID, c.info.PID, c.info.Remote)

	c.server.OnPermissionRequest(c.relayPermissionRequest)
	go c.keepConnected(ctx)

	// The MCP loop owns this process's lifetime: when Claude Code closes
	// stdin, the session is over and so is the channel.
	err := c.server.Serve(ctx, os.Stdin, os.Stdout)
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

// channel joins the two sides: the MCP server facing Claude Code, and the
// daemon connection facing Feishu.
type channel struct {
	info   ipc.Register
	server *mcp.Server

	// conn is replaced whenever the link to the daemon is re-established,
	// so a permission prompt arriving mid-reconnect finds the current one.
	conn atomicConn
}

// keepConnected holds a connection to the daemon open for as long as the
// session lives, starting the daemon when nothing is there and reconnecting
// whenever the link drops.
func (c *channel) keepConnected(ctx context.Context) {
	wait := reconnectDelay
	for ctx.Err() == nil {
		if err := daemon.EnsureRunning(); err != nil {
			debuglog.Printf("channel: no daemon available: %v", err)
		}
		if err := c.serveDaemon(ctx); err != nil && ctx.Err() == nil {
			debuglog.Printf("channel: daemon link ended: %v", err)
		} else {
			wait = reconnectDelay // a link that worked once is worth retrying promptly
		}
		select {
		case <-ctx.Done():
		case <-time.After(wait):
		}
		if wait *= 2; wait > reconnectDelayMax {
			wait = reconnectDelayMax
		}
	}
}

// serveDaemon registers the session and carries the daemon's instructions
// into it until the connection ends.
func (c *channel) serveDaemon(ctx context.Context) error {
	conn, err := ipc.Dial(reconnectDelay)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := conn.Write(ipc.TypeRegister, c.info); err != nil {
		return err
	}
	c.conn.set(conn)
	defer c.conn.clear(conn)

	for {
		env, err := conn.Read()
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		c.dispatch(env)
	}
}

// dispatch carries one daemon instruction into the session. A message the
// channel does not understand is ignored rather than fatal: the daemon may
// be a newer build than this channel.
func (c *channel) dispatch(env ipc.Envelope) {
	switch env.Type {
	case ipc.TypeInject:
		var in ipc.Inject
		if err := env.Into(&in); err != nil {
			debuglog.Printf("channel: undecodable inject: %v", err)
			return
		}
		if err := c.server.PushEvent(in.Content, in.Meta); err != nil {
			debuglog.Printf("channel: push event: %v", err)
			return
		}
		debuglog.Printf("channel: pushed a message into session %s", c.info.SessionID)
	case ipc.TypePermissionVerdict:
		var v ipc.PermissionVerdict
		if err := env.Into(&v); err != nil {
			debuglog.Printf("channel: undecodable verdict: %v", err)
			return
		}
		if err := c.server.SendVerdict(v.RequestID, v.Behavior); err != nil {
			debuglog.Printf("channel: send verdict: %v", err)
			return
		}
		debuglog.Printf("channel: answered permission %s with %s", v.RequestID, v.Behavior)
	default:
		debuglog.Printf("channel: ignoring unknown message %q", env.Type)
	}
}

// relayPermissionRequest hands a prompt to the daemon, which is the only
// role that can put it in front of the user. A prompt that cannot be
// relayed is simply not relayed: the local dialog is still open, and the
// user answers where they always could.
func (c *channel) relayPermissionRequest(req mcp.PermissionRequest) {
	conn := c.conn.get()
	if conn == nil {
		debuglog.Printf("channel: no daemon to relay permission %s to", req.RequestID)
		return
	}
	err := conn.Write(ipc.TypePermissionRequest, ipc.PermissionRequest{
		RequestID:    req.RequestID,
		ToolName:     req.ToolName,
		Description:  req.Description,
		InputPreview: req.InputPreview,
	})
	if err != nil {
		debuglog.Printf("channel: relay permission %s: %v", req.RequestID, err)
	}
}

// describeSession reads the session's identity from the environment Claude
// Code hands its subprocesses, and works out whether this session will
// actually accept what Claude Companion pushes into it.
func describeSession() ipc.Register {
	cwd, _ := os.Getwd()
	dir := os.Getenv("CLAUDE_PROJECT_DIR")
	if dir == "" {
		dir = cwd
	}
	pid, _ := strconv.Atoi(os.Getenv("CLAUDE_PID"))

	id := os.Getenv("CLAUDE_CODE_SESSION_ID")
	if id == "" && pid != 0 {
		// Without a session id the hooks' events cannot be matched to this
		// channel by id - but the process is the same one, and the registry
		// joins on that. A placeholder keyed to the process says so.
		id = "pid-" + strconv.Itoa(pid)
	}

	return ipc.Register{
		SessionID:  id,
		PID:        pid,
		Dir:        dir,
		ProjectDir: os.Getenv("CLAUDE_PROJECT_DIR"),
		Remote:     readiness(pid),
	}
}

// readiness reports whether this session will accept injected messages.
// Claude Code does not tell a channel whether the session registered it, so
// the session's own command line is the only honest signal - and where even
// that cannot be read, the answer is "unconfirmed", never "yes".
func readiness(pid int) session.Remote {
	if pid == 0 {
		return session.Unconfirmed
	}
	enabled, known := procinfo.Enabled(pid, ServerName)
	switch {
	case !known:
		return session.Unconfirmed
	case enabled:
		return session.Ready
	default:
		return session.Notifications
	}
}
