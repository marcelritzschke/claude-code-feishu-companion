package daemon

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/marcelritzschke/claude-code-feishu-companion/internal/debuglog"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/feishu"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/mcp"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/notify"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/session"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/transcript"
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
	// What the user said is never traced - it is the one thing here that
	// is theirs. That it arrived, and where it went, is what makes a
	// message that vanished diagnosable at all.
	debuglog.Printf("inbound message from Feishu (%d characters)", len(text))
	if overviewWords[strings.ToLower(strings.Trim(text, " ?."))] {
		debuglog.Printf("read as a request for the overview")
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
		debuglog.Printf("no session is selected; asking which one")
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

// sendToSession pushes a message typed in the conversation into the
// session the user selected, and tells them what became of it.
//
// The conversation is where this has to be answered: the user typed into a
// chat window and nothing else on their screen is about to change. A card
// reply is answered on the card instead - see replyOnCard.
func (d *Daemon) sendToSession(ctx context.Context, s session.Session, text string) {
	before := s.State
	if !d.pushMessage(ctx, s, text) {
		return
	}
	d.say(ctx, deliveryAnswer(s, before))
}

// pushMessage delivers one message into a session, reporting whether it
// went.
//
// Everything it says is about something going wrong. What a delivery that
// worked is acknowledged with is the caller's to decide, because the
// answer belongs wherever the user was when they sent it.
func (d *Daemon) pushMessage(ctx context.Context, s session.Session, text string) bool {
	if !s.Remote.Continuable() {
		debuglog.Printf("%s is %s; not delivering", s.Describe(), s.Remote)
		d.say(ctx, s.Label()+" can only send you notifications. It was started without Claude Companion enabled, "+
			"so it cannot receive messages. Open Claude Code to continue it.")
		return false
	}

	// The state before the message is what an answer should describe: a
	// session that was mid-turn will not read this until that turn ends.
	before := s.State

	if err := d.deliverTo(s.ID, text, map[string]string{"project": s.Label()}); err != nil {
		debuglog.Printf("deliver to %s: %v", s.Describe(), err)
		d.reg.Downgrade(s.ID)
		d.say(ctx, "Claude Companion could not reach "+s.Label()+". Your message was not delivered.")
		return false
	}
	if before != session.Waiting {
		// A session blocked on a decision is not working, and a message
		// queued behind that decision does not unblock it. Recording it as
		// working would make its card - which the user is very likely
		// looking at, since they just typed into it - claim to be running
		// the turn it is in fact still waiting to be allowed to start.
		d.reg.MarkWorking(s.ID)
	}
	d.expectDelivery(s, before)
	debuglog.Printf("delivered a message to %s", s.Describe())
	return true
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
	case notify.ActionSay:
		d.replyOnCard(ctx, act.Session, action.MessageID, action.Input)
	default:
		debuglog.Printf("ignoring unknown card action %q", act.Kind)
	}
}

// replyOnCard sends what the user typed in a card's reply box to the
// session that card is about, and answers on that same card.
//
// It is the shortest path this product has: the card already names one
// session, so there is nothing to select, nothing to number, and nothing
// to guess. Everything after the routing is the ordinary send, so a card
// reply and a typed message reach a session by exactly one road.
//
// The answer goes on the card rather than into the conversation. A card
// reply is a card that visibly changes - the box empties, the state moves
// on - and a chat line repeating what the card now says would be a second
// message for one action. The one thing the card cannot show is a message
// waiting behind work already running, so that alone is still said out
// loud.
func (d *Daemon) replyOnCard(ctx context.Context, id, messageID, text string) {
	if text == "" {
		return // an empty box submitted by accident
	}
	s, ok := d.reg.Get(id)
	if !ok {
		d.say(ctx, "That session has ended.")
		d.showOverview(ctx)
		return
	}
	// Answering a card is also choosing a session: whatever the user types
	// next, without a card in front of them, must go where they were just
	// talking.
	d.reg.Select(id)
	debuglog.Printf("card reply to %s", s.Describe())
	before := s.State
	if !d.pushMessage(ctx, s, text) {
		return
	}
	d.adoptSessionCard(ctx, s.ID, messageID, sentOnCard(before, text))
	if before != session.Idle {
		d.say(ctx, deliveryAnswer(s, before))
	}
}

// sentOnCard is what the card should stand in with until Claude picks the
// message up, empty when it should show the session instead.
//
// A message that goes to a resting session has nothing true to show yet:
// the transcript still describes the turn the user just replied to. A
// message queued behind a turn already running is the opposite - that
// turn is real, current, and exactly what the card should keep showing.
func sentOnCard(before session.State, text string) string {
	if before != session.Idle {
		return ""
	}
	return text
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

// How long a session has to show a pushed message in its transcript before
// Claude Companion stops believing it arrived.
//
// These exist because Claude Code never acknowledges a channel event: a
// session that did not register Claude Companion as a channel drops every
// message in silence, and the only honest way to find that out is that
// nothing happens. How long "nothing" has to last depends on what the
// session was doing: one that was resting reads a message at once, while
// one mid-turn reads it after, and being told a message failed while it is
// merely waiting its turn would be worse than saying nothing.
const (
	idleProof    = 20 * time.Second
	queuedProof  = 90 * time.Second
	deliveryPoll = 5 * time.Second
)

// expectDelivery starts waiting for proof that a pushed message arrived,
// which is the message itself appearing in the session's transcript.
func (d *Daemon) expectDelivery(s session.Session, before session.State) {
	now := time.Now()
	del := &delivery{
		sessionID:  s.ID,
		sentAt:     now,
		transcript: s.Transcript,
		grewAt:     now,
		queued:     before != session.Idle,
	}
	del.offset = transcript.Size(s.Transcript)
	del.size = del.offset

	d.mu.Lock()
	defer d.mu.Unlock()
	d.awaiting[s.ID] = del
}

// confirmDelivery records that a session did something after being
// messaged. It settles only a delivery there is no transcript to check,
// because a session busy with its own work produces these constantly -
// and a message dropped on the way in would be confirmed by every one of
// them.
func (d *Daemon) confirmDelivery(sessionID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if del, ok := d.awaiting[sessionID]; ok && del.transcript == "" {
		del.arrived = true
	}
}

// markArrived records the proof that a pushed message was read, and
// hasArrived reads that record back. Both take the lock, because the proof
// is written from two goroutines: the poller below, and a hook event for a
// session whose transcript there is nothing to read.
func (d *Daemon) markArrived(sessionID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if del, ok := d.awaiting[sessionID]; ok {
		del.arrived = true
	}
}

func (d *Daemon) hasArrived(sessionID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	del, ok := d.awaiting[sessionID]
	return ok && del.arrived
}

// claimAwaited reports that the turn now ending is the one that read a
// message from Feishu, and forgets the delivery on the way out.
//
// It is what makes a two-word answer to a two-word message reach the
// phone. Such a turn runs no tool and would otherwise be withheld as
// conversation - which is the right rule for someone sitting at the
// terminal watching the answer appear, and the wrong one for someone who
// asked from a train and has no other way to know it was read.
func (d *Daemon) claimAwaited(sessionID string) bool {
	d.mu.Lock()
	del, ok := d.awaiting[sessionID]
	var arrived bool
	var path string
	var offset int64
	if ok {
		arrived, path, offset = del.arrived, del.transcript, del.offset
	}
	d.mu.Unlock()
	if !ok {
		return false
	}
	// The proof is looked for here rather than waited on, because a
	// two-word answer to a two-word message finishes well inside the
	// interval the pending messages are polled at. It is the same check
	// expireDeliveries makes, taken at the one moment it decides anything -
	// and taken outside the lock, because it reads a file.
	if !arrived && !(path != "" && transcript.Delivered(path, offset, mcp.ServerName)) {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.awaiting, sessionID)
	return true
}

// pendingFrom is how far a session's transcript had been written when the
// message now in flight was pushed into it, which is where a card looks
// to find out whether Claude has taken that message up. Zero when nothing
// is in flight.
func (d *Daemon) pendingFrom(sessionID string) int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	if del, ok := d.awaiting[sessionID]; ok {
		return del.offset
	}
	return 0
}

// expireDeliveries tells the user about messages that went nowhere, and
// stops offering the sessions that swallowed them.
func (d *Daemon) expireDeliveries(ctx context.Context) {
	d.mu.Lock()
	pending := make([]*delivery, 0, len(d.awaiting))
	for _, del := range d.awaiting {
		pending = append(pending, del)
	}
	d.mu.Unlock()

	var lost []*delivery
	for _, del := range pending {
		s, live := d.reg.Get(del.sessionID)
		switch {
		case !live:
			d.settleDelivery(del.sessionID) // the session ended; nothing to report
		case d.hasArrived(del.sessionID):
			// Already proven. The record stays until the turn it started
			// ends, which is what tells that turn it owes an outcome to a
			// user who is not at the terminal.
		case del.transcript == "":
			// Nothing to read: hook activity is the only signal, and
			// confirmDelivery is where it lands.
		case transcript.Delivered(del.transcript, del.offset, mcp.ServerName):
			d.markArrived(del.sessionID)
			debuglog.Printf("message to %s arrived", s.Describe())
		case d.stillReaching(del, s):
		default:
			lost = append(lost, del)
			d.settleDelivery(del.sessionID)
		}
	}

	for _, del := range lost {
		s, ok := d.reg.Get(del.sessionID)
		if !ok {
			continue
		}
		d.reg.Downgrade(del.sessionID)
		d.abandonPendingCard(ctx, del.sessionID,
			"Your message was not delivered, so this card has nothing to follow.")
		d.say(ctx, "Claude Companion could not reach "+s.Label()+", and your message was not delivered.\n"+
			"That session is not listening on the Claude Companion channel. Restart it with:\n"+
			"claude --dangerously-load-development-channels server:"+mcp.ServerName)
		debuglog.Printf("message to %s never reached the session; downgraded", s.Describe())
	}
}

// stillReaching reports whether a message that has not shown up yet still
// has reason to. A turn already running is read first and the message
// after it, so neither a session working through one nor a session
// blocked on the user has failed to hear anything.
func (d *Daemon) stillReaching(del *delivery, s session.Session) bool {
	if s.State == session.Waiting {
		return true // blocked on a decision, exactly as the user was told
	}
	if size := transcript.Size(del.transcript); size > del.size {
		del.size, del.grewAt = size, time.Now()
		del.queued = true // a turn is being written; the message is behind it
		return true
	}
	proof := idleProof
	if del.queued {
		proof = queuedProof
	}
	return time.Since(del.grewAt) < proof
}

// settleDelivery stops waiting on a message, however it turned out.
func (d *Daemon) settleDelivery(sessionID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.awaiting, sessionID)
}
