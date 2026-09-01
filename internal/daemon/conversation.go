package daemon

import (
	"context"
	"strings"
	"time"

	"github.com/marcelritzschke/wirelark/internal/debuglog"
	"github.com/marcelritzschke/wirelark/internal/feishu"
	"github.com/marcelritzschke/wirelark/internal/notify"
	"github.com/marcelritzschke/wirelark/internal/session"
)

// overviewWords are the things a user types when they want to see what is
// running rather than say something to a session. The list is short and
// matched whole: a word that could plausibly begin an instruction must
// never swallow the instruction.
var overviewWords = map[string]bool{
	"sessions": true, "/sessions": true, "session": true,
	"wirelark": true, "/wirelark": true, "status": true, "/status": true,
}

// onMessage handles one thing the user said in Feishu. Almost everything
// they say is meant for a Claude Code session; the exceptions are asking to
// see the sessions and picking one.
func (d *Daemon) onMessage(ctx context.Context, msg feishu.Message) {
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		return
	}
	if overviewWords[strings.ToLower(strings.Trim(text, " ?."))] {
		d.showOverview(ctx)
		return
	}

	s, ok := d.reg.Selected()
	if !ok {
		// Nothing is selected, so there is nowhere this message could go
		// that the user chose. Guessing would break the one promise that
		// makes remote continuation safe.
		d.say(ctx, "Which session should this go to?")
		d.showOverview(ctx)
		return
	}
	d.sendToSession(ctx, s, text)
}

// sendToSession pushes the user's message into the session they selected,
// and tells them what became of it.
func (d *Daemon) sendToSession(ctx context.Context, s session.Session, text string) {
	if !s.Remote.Continuable() {
		d.say(ctx, s.Label()+" can only send you notifications. It was started without Wirelark enabled, "+
			"so it cannot receive messages. Open Claude Code to continue it.")
		return
	}

	// The state before the message is what the answer should describe: a
	// session that was mid-turn will not read this until that turn ends.
	before := s.State

	if err := d.deliverTo(s.ID, text, map[string]string{"project": s.Label()}); err != nil {
		debuglog.Printf("deliver to %s: %v", s.Describe(), err)
		d.reg.Downgrade(s.ID)
		d.say(ctx, "Wirelark could not reach "+s.Label()+". Your message was not delivered.")
		return
	}
	d.reg.MarkWorking(s.ID)
	d.expectDelivery(s.ID)
	d.say(ctx, deliveryAnswer(s, before))
	debuglog.Printf("delivered a message to %s", s.Describe())
}

// deliveryAnswer says where the message went and when it will be read. The
// user does not need to know how the queue works - only that the message
// went where they chose, and will not interrupt anything.
func deliveryAnswer(s session.Session, before session.State) string {
	switch before {
	case session.Working:
		return "Queued for " + s.Label() + ".\nClaude is finishing the current turn. Your message will follow."
	case session.Waiting:
		return "Queued for " + s.Label() + ".\nClaude is waiting on a decision in the terminal. Your message follows once that is answered."
	default:
		return "Sent to " + s.Label() + "."
	}
}

// showOverview answers "what is running on my computer".
func (d *Daemon) showOverview(ctx context.Context) {
	card, err := notify.OverviewCard(d.reg.List())
	d.sendCard(ctx, card, err)
}

// onCardAction handles a button the user tapped.
func (d *Daemon) onCardAction(ctx context.Context, action feishu.CardAction) {
	act, ok := notify.ParseAction(action.Value)
	if !ok {
		return
	}
	switch act.Kind {
	case notify.ActionSelect:
		d.selectSession(ctx, act.Session)
	case notify.ActionPermit:
		d.answerPermission(ctx, act, action.MessageID)
	default:
		debuglog.Printf("ignoring unknown card action %q", act.Kind)
	}
}

// selectSession points the user's next messages at one session and says so.
// The confirmation is not a formality: knowing which session you are
// talking to is the whole of what makes this safe.
func (d *Daemon) selectSession(ctx context.Context, id string) {
	s, ok := d.reg.Select(id)
	if !ok {
		d.say(ctx, "That session has ended.")
		d.showOverview(ctx)
		return
	}
	card, err := notify.SelectedCard(s)
	d.sendCard(ctx, card, err)
	debuglog.Printf("selected %s", s.Describe())
}

// deliveryProof is how long a message has to produce some sign of life from
// its session before Wirelark stops believing it arrived.
//
// It exists because Claude Code never acknowledges a channel event: a
// session that did not register Wirelark drops every message in silence,
// and the only honest way to find that out is that nothing happens.
const deliveryProof = 90 * time.Second

// expectDelivery starts waiting for proof that a pushed message arrived.
func (d *Daemon) expectDelivery(sessionID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.awaiting[sessionID] = &delivery{sessionID: sessionID, sentAt: time.Now()}
}

// confirmDelivery records that a session did something after being
// messaged, which is proof enough that it heard.
func (d *Daemon) confirmDelivery(sessionID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.awaiting, sessionID)
}

// expireDeliveries tells the user about messages that went nowhere, and
// stops offering the sessions that swallowed them.
func (d *Daemon) expireDeliveries(ctx context.Context) {
	cutoff := time.Now().Add(-deliveryProof)
	d.mu.Lock()
	var lost []*delivery
	for id, del := range d.awaiting {
		if del.sentAt.Before(cutoff) {
			lost = append(lost, del)
			delete(d.awaiting, id)
		}
	}
	d.mu.Unlock()

	for _, del := range lost {
		s, ok := d.reg.Get(del.sessionID)
		if !ok {
			continue
		}
		d.reg.Downgrade(del.sessionID)
		d.say(ctx, "Wirelark could not reach "+s.Label()+", and your message was not delivered.\n"+
			"That session was most likely started without channels enabled.")
		debuglog.Printf("delivery to %s went unanswered; downgraded", s.Describe())
	}
}
