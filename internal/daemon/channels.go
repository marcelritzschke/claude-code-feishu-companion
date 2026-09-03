package daemon

import (
	"context"

	"github.com/marcelritzschke/claude-code-feishu-companion/internal/debuglog"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/ipc"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/mcp"
)

// channelLink is a session's live connection, as the registry sees it.
type channelLink struct {
	conn *ipc.Conn
}

func (l *channelLink) Inject(content string, meta map[string]string) error {
	return l.conn.Write(ipc.TypeInject, ipc.Inject{Content: content, Meta: meta})
}

func (l *channelLink) Verdict(requestID, behavior string) error {
	return l.conn.Write(ipc.TypePermissionVerdict, ipc.PermissionVerdict{
		RequestID: requestID,
		Behavior:  behavior,
	})
}

// serveChannel keeps one session's link for as long as the session lives.
// The connection ending is the session ending: the channel is spawned with
// Claude Code and outlives every turn in it.
func (d *Daemon) serveChannel(ctx context.Context, conn *ipc.Conn, first ipc.Envelope) {
	var reg ipc.Register
	if err := first.Into(&reg); err != nil {
		debuglog.Printf("channel: undecodable registration: %v", err)
		return
	}
	dir := reg.ProjectDir
	if dir == "" {
		dir = reg.Dir
	}

	link := &channelLink{conn: conn}
	s := d.reg.Attach(reg.SessionID, reg.PID, dir, reg.Remote, link)
	defer d.reg.Detach(link)
	debuglog.Printf("channel attached: %s (%s)", s.Describe(), reg.Remote)

	for {
		env, err := conn.Read()
		if err != nil || ctx.Err() != nil {
			debuglog.Printf("channel detached: %s", s.Describe())
			return
		}
		switch env.Type {
		case ipc.TypePermissionRequest:
			var req ipc.PermissionRequest
			if err := env.Into(&req); err != nil {
				debuglog.Printf("channel: undecodable permission request: %v", err)
				continue
			}
			d.onPermissionRequest(ctx, link, mcp.PermissionRequest(req))
		default:
			debuglog.Printf("channel: ignoring unknown message %q", env.Type)
		}
	}
}

// deliverTo pushes a message into a session, by way of whatever channel is
// currently attached to it. It takes the session id rather than a channel
// so a caller can never reach a session other than the one it named.
func (d *Daemon) deliverTo(id, content string, meta map[string]string) error {
	s, ok := d.reg.Get(id)
	if !ok {
		return errNoChannel
	}
	ch := s.Channel()
	if ch == nil {
		return errNoChannel
	}
	return ch.Inject(content, meta)
}

// verdictTo answers a permission request through a session's channel.
func (d *Daemon) verdictTo(id, requestID, behavior string) error {
	s, ok := d.reg.Get(id)
	if !ok {
		return errNoChannel
	}
	ch := s.Channel()
	if ch == nil {
		return errNoChannel
	}
	return ch.Verdict(requestID, behavior)
}
