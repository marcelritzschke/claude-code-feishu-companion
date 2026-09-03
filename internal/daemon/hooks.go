package daemon

import (
	"bytes"
	"context"

	"github.com/marcelritzschke/claude-code-feishu-companion/internal/debuglog"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/deliver"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/hook"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/ipc"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/session"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/transcript"
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

// handleHook updates what Claude Companion knows about the session, settles
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

	s := d.reg.Observe(session.Observation{
		ID: p.SessionID, PID: h.PID, Dir: dir, Title: turn.Title,
		Transcript: p.TranscriptPath, HookEvent: p.HookEventName,
	})
	debuglog.Printf("hook %s from %s", p.HookEventName, s.Describe())

	// Any event that is not the prompt itself proves the session moved on,
	// so a permission or question card still standing for it was answered
	// elsewhere.
	if p.HookEventName != hook.EventPermissionRequest {
		d.settleStandingPrompt(ctx, s.ID)
		d.confirmDelivery(s.ID)
	}
	var settling bool
	switch p.HookEventName {
	case hook.EventSessionEnd:
		// The session is over: it must leave the overview, it must not
		// stay selected as somewhere a message could still be sent, and
		// nothing may be left claiming to watch it.
		d.closeWatch(ctx, s.ID, "This session has ended.")
		d.reg.Remove(s.ID)
		return
	case hook.EventStop, hook.EventStopFailure:
		// The turn is over, so the live view is too - but its card is left
		// standing, because the completion notification below settles that
		// very message into the turn's outcome.
		settling = d.endWatch(s.ID) != nil
	case hook.EventPostToolUse, hook.EventPermissionRequest, hook.EventPreToolUse:
		// The first sign of real work in a turn opens the session's live
		// card; afterwards each event nudges it, so a state that needs the
		// user shows up without waiting for the next poll. Deliberately not
		// on the prompt itself: a purely conversational turn earns no card.
		d.ensureSessionCard(ctx, s)
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

	if settling {
		d.pingOutcome(ctx, s, p, turn)
	}
}

// pingOutcome is the push a settled session card cannot deliver. Rewriting
// a card never notifies anyone, so a turn whose outcome was written onto
// its own live card would otherwise end in silence - and the one thing this
// product cannot afford is the user not hearing that Claude is done, or
// stuck. Failures always push; a completion that did no reportable work
// stays quiet, exactly as its notification would have.
func (d *Daemon) pingOutcome(ctx context.Context, s session.Session, p *hook.Payload, turn *transcript.Turn) {
	failed := p.HookEventName == hook.EventStopFailure || turn.Failed
	if !failed && deliver.WithholdChatter(turn) == deliver.LiveCardOnly {
		return
	}
	text := "✅ Completed · " + s.Describe()
	if failed {
		text = "🔴 Failed · " + s.Describe()
	}
	d.say(ctx, text)
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

// skipEvent vetoes an event the daemon is already reporting better itself:
// a decision it is showing with real buttons, or a turn the user is
// watching live. Either way the rule is one thing, one card - whatever is
// already in front of the user wins, and the other stays quiet.
func (d *Daemon) skipEvent(sessionID string) func(string) bool {
	return func(hookEvent string) bool {
		switch hookEvent {
		case hook.EventPostToolUse:
			// The live view is already this turn's one card, and far more
			// current than a progress refresh would be.
			return d.watching(sessionID)
		case hook.EventPermissionRequest:
			d.mu.Lock()
			defer d.mu.Unlock()
			p, ok := d.bySession[sessionID]
			return ok && p.relayed && !p.settled
		}
		return false
	}
}
