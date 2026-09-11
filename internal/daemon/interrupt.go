package daemon

import (
	"context"
	"fmt"

	"github.com/marcelritzschke/claude-code-feishu-companion/internal/debuglog"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/notify"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/session"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/transcript"
)

// Interrupting means one thing: stop the work Claude is currently doing and
// return the existing session to an interactive state. It never terminates
// Claude Code, deletes the session, or closes the terminal - Claude Companion
// can interact with a session, it does not own its lifecycle.
//
// No confirmation is asked for. The action is restricted to the configured
// user, it only stops a turn, and the session stays exactly where it was.

// interruptRequest stops the turn of the session the word can only have
// meant, and never guesses at which that is.
//
// It is the typed form of the button, and it exists because the button
// depends on a Feishu subscription this app may not have: where card
// callbacks are inert, typing is the only way left to stop a runaway turn.
// One turn running is no choice at all; several, and the cards are where
// the choice is visible.
func (d *Daemon) interruptRequest(ctx context.Context) {
	var running []session.Session
	for _, s := range d.reg.List() {
		if s.State != session.Idle {
			running = append(running, s)
		}
	}
	switch len(running) {
	case 1:
		d.interruptSession(ctx, running[0].ID)
	case 0:
		d.say(ctx, "Nothing is running on your computer right now.")
	default:
		d.say(ctx, fmt.Sprintf("%d sessions are running, so nothing was stopped.\n"+
			"Tap Interrupt on the card of the one you mean - send  sessions  to bring the cards back.", len(running)))
	}
}

// interruptSession stops one session's current turn and settles its live
// card into the interrupted state.
func (d *Daemon) interruptSession(ctx context.Context, id string) {
	s, ok := d.reg.Get(id)
	if !ok {
		d.say(ctx, "That session has ended.")
		return
	}
	if s.State == session.Idle {
		d.say(ctx, "Nothing is running in "+s.Label()+" right now.")
		return
	}
	if !s.Interruptible() {
		d.say(ctx, s.Label()+" cannot be interrupted from here. Use its terminal.")
		return
	}
	if err := d.interrupt(s); err != nil {
		debuglog.Printf("interrupt %s: %v", s.Describe(), err)
		d.say(ctx, "Claude Companion could not interrupt "+s.Label()+". Use its terminal.")
		return
	}
	debuglog.Printf("interrupted %s", s.Describe())

	// No hook event reports an interrupt, so everything a Stop event would
	// have settled is settled here: the session's state, its live card, and
	// any permission or question card the stopped turn left open.
	d.reg.MarkIdle(s.ID)
	d.settleStandingPrompt(ctx, s.ID)
	d.settleDelivery(s.ID) // this turn is not going to report an outcome
	w := d.detachLiveCard(s.ID)
	d.releaseLiveCard(s.ID)
	if w == nil || w.messageID == "" {
		d.say(ctx, "Interrupted "+s.Label()+". The session is back at its prompt.")
		return
	}
	s.State = session.Idle
	card, err := notify.InterruptedSessionCard(s, transcript.Load(s.Transcript, ""))
	d.updateCard(ctx, w.messageID, card, err)
}
