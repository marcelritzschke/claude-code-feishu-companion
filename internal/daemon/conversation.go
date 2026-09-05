package daemon

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/marcelritzschke/claude-code-feishu-companion/internal/debuglog"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/feishu"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/notify"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/session"
)

// overviewWords are the things a user types when they want to see what is
// running rather than say something to a session. The list is short and
// matched whole: a word that could plausibly begin an instruction must
// never swallow the instruction.
var overviewWords = map[string]bool{
	"sessions": true, "/sessions": true, "session": true,
	"claude-companion": true, "/claude-companion": true,
	"status": true, "/status": true,
}

// onMessage handles one thing the user said in Feishu. Almost everything
// they say is meant for a Claude Code session; the exceptions are asking to
// see the sessions, picking one, and answering a permission request.
//
// Those three are checked first and are the same actions the card buttons
// perform, so a Feishu app whose card callbacks are not configured is
// merely less convenient rather than unusable.
func (d *Daemon) onMessage(ctx context.Context, msg feishu.Message) {
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		return
	}
	if overviewWords[strings.ToLower(strings.Trim(text, " ?."))] {
		d.showOverview(ctx)
		return
	}
	if requestID, allow, ok := parseVerdict(text); ok {
		verdict := notify.VerdictDeny
		if allow {
			verdict = notify.VerdictAllow
		}
		d.answerPermission(ctx, notify.Action{
			Kind: notify.ActionPermit, Request: requestID, Verdict: verdict,
		}, "")
		return
	}
	if number, ok := parseWatch(text); ok {
		d.watchRequest(ctx, number)
		return
	}
	if parseStopWatch(text) {
		d.stopWatchRequest(ctx)
		return
	}
	if number, ok := parseInterrupt(text); ok {
		d.interruptRequest(ctx, number)
		return
	}
	if id, ok := d.pickFromOverview(text); ok {
		d.selectSession(ctx, id)
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

// pickFromOverview resolves a numbered reply against the overview the user
// is actually looking at, not against the current list.
//
// The distinction matters: a session can end between the overview being
// sent and the reply arriving, and resolving "2" against a list that has
// since shifted would deliver the message to a session the user never
// chose. Resolved this way, a stale number names a session that is gone,
// and being told so is the correct outcome.
func (d *Daemon) pickFromOverview(text string) (string, bool) {
	d.mu.Lock()
	listed := append([]string(nil), d.lastOverview...)
	d.mu.Unlock()

	i, ok := parsePick(text, len(listed))
	if !ok {
		return "", false
	}
	return listed[i], true
}

// sendToSession pushes the user's message into the session they selected,
// and tells them what became of it.
func (d *Daemon) sendToSession(ctx context.Context, s session.Session, text string) {
	if !s.Remote.Continuable() {
		d.say(ctx, s.Label()+" can only send you notifications. It was started without Claude Companion enabled, "+
			"so it cannot receive messages. Open Claude Code to continue it.")
		return
	}

	// The state before the message is what the answer should describe: a
	// session that was mid-turn will not read this until that turn ends.
	before := s.State

	if err := d.deliverTo(s.ID, text, map[string]string{"project": s.Label()}); err != nil {
		debuglog.Printf("deliver to %s: %v", s.Describe(), err)
		d.reg.Downgrade(s.ID)
		d.say(ctx, "Claude Companion could not reach "+s.Label()+". Your message was not delivered.")
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

// showOverview answers "what is running on my computer", and remembers
// exactly which sessions it offered so a numbered reply means what the user
// saw when they typed it.
func (d *Daemon) showOverview(ctx context.Context) {
	sessions := d.reg.List()
	offered := make([]string, 0, len(sessions))
	for _, s := range sessions {
		if s.Remote.Continuable() {
			offered = append(offered, s.ID)
		}
	}
	d.mu.Lock()
	d.lastOverview = offered
	d.mu.Unlock()

	card, err := notify.OverviewCard(sessions)
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
	case notify.ActionWatch:
		d.watchSession(ctx, act.Session)
	case notify.ActionUnwatch:
		d.closeWatch(ctx, act.Session, "You stopped watching this session.")
	case notify.ActionInterrupt:
		d.interruptSession(ctx, act.Session)
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

// watchRequest opens the live view of the session the user named: the one
// they picked out of the last overview, or the one they are already
// talking to. It never guesses - the same rule that governs where a
// message goes governs which session the user is shown.
func (d *Daemon) watchRequest(ctx context.Context, number int) {
	if number > 0 {
		id, ok := d.pickFromOverview(strconv.Itoa(number))
		if !ok {
			d.say(ctx, "There is no session with that number.")
			d.showOverview(ctx)
			return
		}
		d.watchSession(ctx, id)
		return
	}
	s, ok := d.reg.Selected()
	if !ok {
		d.say(ctx, "Which session do you want to watch?")
		d.showOverview(ctx)
		return
	}
	d.watchSession(ctx, s.ID)
}

// watchSession opens the live view of one session by id.
func (d *Daemon) watchSession(ctx context.Context, id string) {
	s, ok := d.reg.Get(id)
	if !ok {
		d.say(ctx, "That session has ended.")
		d.showOverview(ctx)
		return
	}
	d.startWatch(ctx, s)
}

// stopWatchRequest closes the live view the user meant: the selected
// session's, or the only one open when nothing is selected.
func (d *Daemon) stopWatchRequest(ctx context.Context) {
	note := "You stopped watching this session."
	if s, ok := d.reg.Selected(); ok && d.watching(s.ID) {
		d.closeWatch(ctx, s.ID, note)
		d.say(ctx, "Stopped watching "+s.Label()+".")
		return
	}
	d.mu.Lock()
	open := make([]string, 0, len(d.watches))
	for id := range d.watches {
		open = append(open, id)
	}
	d.mu.Unlock()

	switch len(open) {
	case 0:
		d.say(ctx, "You are not watching any session.")
	case 1:
		d.closeWatch(ctx, open[0], note)
		d.say(ctx, "Stopped watching.")
	default:
		d.say(ctx, "Pick the session you want to stop watching first.")
		d.showOverview(ctx)
	}
}

// deliveryProof is how long a message has to produce some sign of life from
// its session before Claude Companion stops believing it arrived.
//
// It exists because Claude Code never acknowledges a channel event: a
// session that did not register Claude Companion drops every message in silence,
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
		d.say(ctx, "Claude Companion could not reach "+s.Label()+", and your message was not delivered.\n"+
			"That session was most likely started without channels enabled.")
		debuglog.Printf("delivery to %s went unanswered; downgraded", s.Describe())
	}
}
