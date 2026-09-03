package daemon

import (
	"context"
	"strconv"

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

// interruptRequest stops the turn of the session the user named: the one
// they picked out of the last overview, or the one they are talking to.
// Like every other command, it never guesses.
func (d *Daemon) interruptRequest(ctx context.Context, number int) {
	if number > 0 {
		id, ok := d.pickFromOverview(strconv.Itoa(number))
		if !ok {
			d.say(ctx, "There is no session with that number.")
			d.showOverview(ctx)
			return
		}
		d.interruptSession(ctx, id)
		return
	}
	s, ok := d.reg.Selected()
	if !ok {
		d.say(ctx, "Which session do you want to interrupt?")
		d.showOverview(ctx)
		return
	}
	d.interruptSession(ctx, s.ID)
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
	w := d.endWatch(s.ID)
	d.releaseLiveCard(s.ID)
	if w == nil || w.messageID == "" {
		d.say(ctx, "Interrupted "+s.Label()+". The session is back at its prompt.")
		return
	}
	s.State = session.Idle
	card, err := notify.InterruptedSessionCard(s, transcript.Load(s.Transcript, ""))
	d.updateCard(ctx, w.messageID, card, err)
}
