package daemon

import (
	"bytes"
	"context"

	"github.com/marcelritzschke/wirelark/internal/debuglog"
	"github.com/marcelritzschke/wirelark/internal/deliver"
	"github.com/marcelritzschke/wirelark/internal/hook"
	"github.com/marcelritzschke/wirelark/internal/ipc"
	"github.com/marcelritzschke/wirelark/internal/session"
	"github.com/marcelritzschke/wirelark/internal/transcript"
)

// serveHook takes one hook event off the hook process's hands.
//
// The acknowledgement goes out before the work, on purpose. A hook runs
// inside the Claude Code session and must not be held up, and the ack means
// only "this is mine now" - which is exactly the fact the hook needs, since
// it decides on that basis not to deliver the notification itself.
func (d *Daemon) serveHook(ctx context.Context, conn *ipc.Conn, first ipc.Envelope) {
	var h ipc.Hook
	if err := first.Into(&h); err != nil {
		d.reply(conn, ipc.Ack{Err: "undecodable hook event"})
		return
	}
	p, err := hook.Decode(bytes.NewReader(h.Payload))
	if err != nil {
		d.reply(conn, ipc.Ack{Err: "undecodable hook payload"})
		return
	}
	d.reply(conn, ipc.Ack{OK: true})
	d.handleHook(ctx, p, h)
}

// handleHook updates what Wirelark knows about the session, settles
// anything the event proves is over, and delivers whatever card the event
// calls for.
func (d *Daemon) handleHook(ctx context.Context, p *hook.Payload, h ipc.Hook) {
	dir := h.ProjectDir
	if dir == "" {
		dir = p.Cwd
	}
	// The daemon serves every session, so the project this event belongs to
	// has to travel with the event rather than be read from the daemon's own
	// environment - which belongs to whichever session happened to start it.
	p.ProjectDir = dir
	turn := transcript.Load(p.TranscriptPath, p.PromptID)

	s := d.reg.Observe(p.SessionID, h.PID, dir, turn.Title, p.HookEventName)
	debuglog.Printf("hook %s from %s", p.HookEventName, s.Describe())

	// Any event that is not the prompt itself proves the session moved on,
	// so a permission card still standing for it was answered elsewhere.
	if p.HookEventName != hook.EventPermissionRequest {
		d.settleStandingPrompt(ctx, s.ID)
		d.confirmDelivery(s.ID)
	}
	if p.HookEventName == hook.EventSessionEnd {
		// The session is over: it must leave the overview, and it must not
		// stay selected as somewhere a message could still be sent.
		d.reg.Remove(s.ID)
		return
	}

	(&deliver.Deliverer{
		Payload:         p,
		Sender:          d.out,
		ContinueSession: continueTarget(s),
		Skip:            d.skipEvent(s.ID),
		Sent: func(hookEvent, messageID string) {
			d.recordHookPrompt(s.ID, hookEvent, messageID)
		},
	}).Event(turn, d.cfg)
}

// continueTarget is the session a card's [ Continue ] button should point
// at, or nothing when this session cannot be continued. A button that
// selects a session the user cannot then talk to is worse than no button.
func continueTarget(s session.Session) string {
	if !s.Remote.Continuable() {
		return ""
	}
	return s.ID
}

// skipEvent vetoes the hook-driven card for a decision the daemon is
// already showing with real buttons. One decision, one card - whichever
// arrives first wins the message, and the other stays quiet.
func (d *Daemon) skipEvent(sessionID string) func(string) bool {
	return func(hookEvent string) bool {
		if hookEvent != hook.EventPermissionRequest {
			return false
		}
		d.mu.Lock()
		defer d.mu.Unlock()
		p, ok := d.bySession[sessionID]
		return ok && p.relayed && !p.settled
	}
}
