package daemon

import (
	"bytes"
	"context"

	"github.com/marcelritzschke/claude-code-feishu-companion/internal/debuglog"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/deliver"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/hook"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/ipc"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/session"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/state"
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
	var stranded, awaited bool
	switch p.HookEventName {
	case hook.EventSessionEnd:
		// The session is over: it must leave the overview, it must not
		// stay selected as somewhere a message could still be sent, and
		// nothing may be left claiming to watch it.
		d.closeWatch(ctx, s.ID, "This session has ended.")
		d.reg.Remove(s.ID)
		return
	case hook.EventStop, hook.EventStopFailure:
		// The turn is over, so the live view is too - and its card is
		// taken out of the conversation, so that the outcome delivered
		// below arrives as a new message rather than as a silent rewrite
		// of one the user has already scrolled past.
		stranded = d.recallLiveCard(ctx, s.ID)
		// A turn that read a message from Feishu answers somebody who is
		// not at the terminal, so its outcome is reported however little
		// work it took.
		awaited = d.claimAwaited(s.ID)
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
		Awaited:         awaited,
		Sent: func(hookEvent, messageID string) {
			d.recordHookPrompt(s.ID, hookEvent, messageID)
		},
	}).Event(turn, d.cfg)

	if stranded {
		d.pingOutcome(ctx, s, p, turn)
	}
}

// recallLiveCard takes a finished turn's live card out of the conversation,
// so that the outcome about to be delivered arrives as a new message.
//
// Rewriting a card notifies nobody, and the one thing this product cannot
// afford is the user not hearing that Claude is done, or stuck. Recalling
// the live card is what buys that push without spending a second message
// on it: one turn stays one message, and that message arrives.
//
// It reports whether a live card is left standing, which happens only when
// Feishu refuses the recall. The outcome then settles that card in place,
// exactly as it used to, and the push has to be found elsewhere.
func (d *Daemon) recallLiveCard(ctx context.Context, sessionID string) (stranded bool) {
	w := d.endWatch(sessionID)

	// The turn's live card is whichever message holds the live slot: the
	// watch's own card, or the progress card the watch took over.
	claimed := false
	store, err := state.Open()
	if err != nil {
		debuglog.Printf("open state: %v", err)
	} else if err := store.Mutate(func(entries map[string]state.Entry) {
		live, ok := entries[sessionID]
		if !ok || live.MessageID == "" {
			return
		}
		claimed = true
		if d.deleteCard(ctx, live.MessageID) {
			delete(entries, sessionID)
			debuglog.Printf("recalled the live card for session %s", sessionID)
			return
		}
		stranded = true
	}); err != nil {
		debuglog.Printf("state: %v", err)
	}
	if !claimed && w != nil && w.messageID != "" {
		// This watch never got the live slot, so the outcome will not
		// settle its card. It has to leave on its own, and a card the
		// outcome will not settle is not one it can be stranded on.
		d.deleteCard(ctx, w.messageID)
	}
	return stranded
}

// pingOutcome is the push a settled card cannot deliver. It is the last
// resort: it only runs for a turn whose live card Feishu refused to recall,
// where the outcome had to be written onto a message the user was already
// shown. Failures always push; a completion that did no reportable work
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
