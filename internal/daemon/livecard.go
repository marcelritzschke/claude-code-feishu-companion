package daemon

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/marcelritzschke/claude-code-feishu-companion/internal/debuglog"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/mcp"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/notify"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/session"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/state"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/transcript"
)

// The session card is the one thing in Claude Companion that looks at a
// session continuously, so its whole design is about not becoming a stream.
//
// There is no asking for it and no turning it off. A card opens on the
// first real sign of work in a turn, keeps itself current, and settles
// when the turn ends - which leaves nothing for the user to manage, and
// no state they have to remember being in.
//
// The transcript is polled rather than pushed: the card then needs no extra
// hook, no extra setup, and no change to a running session. Reading a local
// file every few seconds costs nothing; what has to be rationed is Feishu,
// so the card is only rewritten when what it says actually changes.

// pace is how often a live card looks, and how often it may speak.
type pace struct {
	// tick is how often the transcript is re-read.
	tick time.Duration
	// floor is the least time between two rewrites of the card, so a burst
	// of activity cannot turn into a burst of updates.
	floor time.Duration
	// heartbeat rewrites an unchanged card occasionally, so the elapsed
	// time and liveness note it shows stay true rather than quietly
	// freezing.
	heartbeat time.Duration
	// max bounds how long one card stays live. A session card is a
	// check-in, not a subscription.
	max time.Duration
}

// defaultPace is the cadence every live card runs at. A daemon carries its own
// copy so a test can run the same loop in a fraction of the time.
var defaultPace = pace{
	tick:      3 * time.Second,
	floor:     5 * time.Second,
	heartbeat: 60 * time.Second,
	max:       2 * time.Hour,
}

// liveCard is one session's live card: the single message that answers for
// the session until its turn ends.
type liveCard struct {
	sessionID string
	messageID string
	started   time.Time
	cancel    context.CancelFunc

	// mu orders a refresh against whatever ends the card. It is held
	// across the Feishu call on purpose: without that, a refresh already
	// in flight could land after the final card and leave the user looking
	// at a session that is still working long after it stopped.
	mu        sync.Mutex
	stopped   bool
	signature string
	// changed is when the content last actually changed - what the card's
	// "Updated ..." note reports.
	changed time.Time
	// sent is when the card was last rewritten.
	sent time.Time
	// last is the session as the card last saw it, so it can still be
	// addressed to it after the session is gone from the registry.
	last session.Session
	// cached is the turn as of seen, so a card that looks every few
	// seconds does not re-parse a transcript that has not moved.
	cached *transcript.Turn
	seen   stamp
	// notes are the turn's decision records: answered prompts whose own
	// cards were recalled, kept visible here instead.
	notes []string
	// pending is a message pushed from this card that Claude has not taken
	// up yet, and pendingFrom how far the transcript had been written when
	// it went. Until the message appears past that point the transcript is
	// still describing the turn the user replied to, so the card stands in
	// for it rather than passing that turn off as the new one.
	pending     string
	pendingFrom int64
}

// stamp identifies a transcript file well enough to tell that nothing has
// been appended to it.
type stamp struct {
	size int64
	mod  time.Time
}

// turn returns the session's current turn, re-reading the transcript only
// when the file has actually grown. Callers must hold w.mu.
func (w *liveCard) turn(path string) *transcript.Turn {
	fi, err := os.Stat(path)
	fresh := err == nil && w.cached != nil && fi.Size() == w.seen.size && fi.ModTime().Equal(w.seen.mod)
	if fresh {
		return w.cached
	}
	w.cached = transcript.Load(path, "")
	if err == nil {
		w.seen = stamp{size: fi.Size(), mod: fi.ModTime()}
	}
	return w.cached
}

// ensureSessionCard makes sure an active session has its one live card
// standing. It is the automatic way into the live view: the first sign of
// real work in a turn opens the card quietly, hook events keep it honest
// between ticks, and the turn's end settles it. Opening says nothing in
// the conversation - the card itself is the message.
func (d *Daemon) ensureSessionCard(ctx context.Context, s session.Session) {
	if !s.Observable() || s.State == session.Idle {
		return
	}
	d.mu.Lock()
	w, already := d.live[s.ID]
	d.mu.Unlock()
	if already {
		// A session that just started waiting must say so now, not on the
		// next poll: force skips the pacing that quiets routine refreshes.
		d.refreshLiveCard(ctx, w, s, s.State == session.Waiting)
		return
	}
	d.openLiveCard(ctx, s, adoption{})
}

// adoption is how a live card starts on a message that already stands.
//
// The card the user typed into is the card their message's turn gets. It
// is the only way to empty a box they have already sent from - Feishu
// keeps what was typed until the card is rewritten - and it is what keeps
// one turn to one message when the turn was started from the phone.
type adoption struct {
	// messageID is the standing card to write onto instead of sending a
	// new one. Empty means send one.
	messageID string
	// recall is another card that stood for this session and now describes
	// a turn the user has started another one from. The card holding the
	// live slot is found in the store; this is the one a card was on when
	// it never got that slot.
	recall string
	// sent is the message Claude has not taken up yet, if any: what the
	// card shows until it has. See liveCard.pending.
	sent string
	// from is where in the transcript to look for that message.
	from int64
}

// adoptSessionCard hands a session's live card to the message the user
// just replied on.
//
// Any other card holding the live slot is recalled rather than left
// behind: the moment a new turn is started from an older card, the newer
// one is describing a turn that is over, and two cards for one session is
// exactly what this product does not do.
func (d *Daemon) adoptSessionCard(ctx context.Context, sessionID, messageID, sent string) {
	s, ok := d.reg.Get(sessionID)
	if messageID == "" || !ok || !s.Observable() {
		// Nothing to adopt, or nothing to show on it. The ordinary live
		// card still opens on the turn's first sign of work.
		d.ensureSessionCard(ctx, s)
		return
	}
	take := adoption{messageID: messageID, sent: sent, from: d.pendingFrom(sessionID)}

	d.mu.Lock()
	w, running := d.live[sessionID]
	d.mu.Unlock()
	if running && w.messageID == messageID {
		w.mu.Lock()
		w.pending, w.pendingFrom = take.sent, take.from
		w.mu.Unlock()
		d.refreshLiveCard(ctx, w, s, true)
		return
	}
	if running {
		// The user answered from a card other than the one this session's
		// card is standing on. Theirs is the one they are looking at.
		if stale := d.detachLiveCard(sessionID); stale != nil {
			take.recall = stale.messageID
		}
	}
	d.openLiveCard(ctx, s, take)
}

// repostSessionCard puts a session's one card back at the bottom of the
// conversation, where the user just asked for it.
//
// Refreshing the card where it already stands would be silent - a message
// rewritten in place notifies nobody and may be a long scroll up - and
// silence in answer to a direct question reads as failure. So whatever
// stands is recalled first and the card goes up again as a new message,
// which keeps the rule that one session has one card while still putting
// it where the user is looking.
func (d *Daemon) repostSessionCard(ctx context.Context, s session.Session) {
	d.recallLiveCard(ctx, s.ID)
	if s.State == session.Idle || !s.Observable() {
		d.sendRestingCard(ctx, s)
		return
	}
	d.openLiveCard(ctx, s, adoption{})
}

// sendRestingCard shows a session that has nothing in flight: what its last
// turn came to, or that it has not run one yet.
//
// The card takes the live slot, so the session's next turn writes onto this
// message rather than adding another one, and the turn's outcome recalls it
// the way it recalls any live card.
func (d *Daemon) sendRestingCard(ctx context.Context, s session.Session) {
	card, err := notify.RestingSessionCard(s, transcript.Load(s.Transcript, ""))
	if err != nil {
		debuglog.Printf("build resting card: %v", err)
		return
	}
	store, err := state.Open()
	if err != nil {
		debuglog.Printf("open state: %v", err)
		d.sendCard(ctx, card, nil)
		return
	}
	if err := store.Mutate(func(entries map[string]state.Entry) {
		id := d.sendCard(ctx, card, nil)
		if id == "" {
			return
		}
		entries[s.ID] = state.Entry{MessageID: id, UpdatedAt: time.Now()}
	}); err != nil {
		debuglog.Printf("state: %v", err)
	}
}

// openLiveCard puts up a session's live card and starts the loop that keeps
// it current. The card is registered before it is sent so two
// concurrent hook events cannot both open one. take, when it names a
// message, is a card already standing that this one is written onto.
func (d *Daemon) openLiveCard(ctx context.Context, s session.Session, take adoption) {
	now := time.Now()
	w := &liveCard{
		sessionID:   s.ID,
		started:     now,
		changed:     now,
		sent:        now,
		last:        s,
		pending:     take.sent,
		pendingFrom: take.from,
	}
	d.mu.Lock()
	if _, raced := d.live[s.ID]; raced {
		d.mu.Unlock()
		return // another event opened this session's card first
	}
	d.live[s.ID] = w
	d.mu.Unlock()

	turn := transcript.Load(s.Transcript, "")
	w.signature = notify.LiveSignature(s, turn)
	view := viewOf(s, now, nil)
	view.Sent = w.pending
	card, err := notify.SessionCard(s, turn, view)
	if err != nil {
		debuglog.Printf("build session card: %v", err)
		d.dropLiveCard(w)
		return
	}
	// The claim happens under the card's own lock so the message id is
	// ordered against refreshes, and against whatever might end the card
	// while it is still going up.
	w.mu.Lock()
	claimed := d.claimLiveCard(ctx, s.ID, card, w, take)
	w.mu.Unlock()
	if !claimed {
		d.dropLiveCard(w)
		return
	}

	wctx, cancel := context.WithCancel(ctx)
	w.cancel = cancel
	go d.runLiveCard(wctx, w)
	debuglog.Printf("session card for %s standing as message %s", s.Describe(), w.messageID)
}

// dropLiveCard forgets a live card that never made it up.
func (d *Daemon) dropLiveCard(w *liveCard) {
	d.mu.Lock()
	if d.live[w.sessionID] == w {
		delete(d.live, w.sessionID)
	}
	d.mu.Unlock()
}

// viewOf is what the daemon knows about a session card beyond the
// transcript: how fresh the activity is, whether [ Interrupt ] would
// actually work, and the decisions recorded on this turn.
func viewOf(s session.Session, activityAt time.Time, notes []string) notify.SessionView {
	return notify.SessionView{ActivityAt: activityAt, Interruptible: s.Interruptible(), Notes: notes}
}

// takenUp reports that Claude has picked up the message this card is
// standing in for, and clears it. Callers must hold w.mu.
//
// The proof is the same one a delivery is settled by: the message itself,
// in the transcript, past where the file stood when it went. Nothing else
// will do - a session busy with the previous turn writes lines all day.
func (w *liveCard) takenUp(path string) bool {
	if w.pending == "" {
		return false
	}
	if !transcript.Delivered(path, w.pendingFrom, mcp.ServerName) {
		return false
	}
	w.pending = ""
	return true
}

// maxCardNotes bounds the decision records one card carries; the oldest
// give way, as everywhere else on a card.
const maxCardNotes = 3

// noteOnSessionCard adds a one-line decision record to a session's live
// card. A prompt whose card was recalled would otherwise leave no trace of
// what was decided; the session card is where that record belongs.
func (d *Daemon) noteOnSessionCard(ctx context.Context, sessionID, note string) {
	d.mu.Lock()
	w, ok := d.live[sessionID]
	d.mu.Unlock()
	if !ok {
		return // no live card standing; the outcome ping still covers the turn
	}
	w.mu.Lock()
	w.notes = append(w.notes, note)
	if len(w.notes) > maxCardNotes {
		w.notes = w.notes[len(w.notes)-maxCardNotes:]
	}
	w.mu.Unlock()
	if s, live := d.reg.Get(sessionID); live {
		d.refreshLiveCard(ctx, w, s, true)
	}
}

// claimLiveCard puts the card up as the turn's one live card.
//
// When a progress card is already standing for this turn, the live card
// takes that message over instead of adding a second one - one turn stays
// one message, and the completion notification settles whichever of the two
// the user is actually looking at.
//
// An adoption is the other way in: the message it names is a card the user
// is already looking at, and it takes the slot from whatever held it.
func (d *Daemon) claimLiveCard(ctx context.Context, sessionID, cardJSON string, w *liveCard, take adoption) bool {
	adopt := take.messageID
	store, err := state.Open()
	if err != nil {
		// The card can still be shown; it just will not be settled by the
		// turn's own completion notification. The live card settles it instead.
		debuglog.Printf("open state: %v", err)
		if adopt != "" {
			d.updateCard(ctx, adopt, cardJSON, nil)
			w.messageID = adopt
			return true
		}
		w.messageID = d.sendCard(ctx, cardJSON, nil)
		return w.messageID != ""
	}
	if err := store.Mutate(func(entries map[string]state.Entry) {
		live, held := entries[sessionID]
		if adopt != "" {
			d.recallOthers(ctx, adopt, live.MessageID, take.recall)
			d.updateCard(ctx, adopt, cardJSON, nil)
			w.messageID = adopt
			entries[sessionID] = state.Entry{MessageID: adopt, UpdatedAt: time.Now()}
			return
		}
		if held && live.MessageID != "" {
			d.updateCard(ctx, live.MessageID, cardJSON, nil)
			w.messageID = live.MessageID
			live.UpdatedAt = time.Now()
			entries[sessionID] = live
			return
		}
		id := d.sendCard(ctx, cardJSON, nil)
		if id == "" {
			return
		}
		w.messageID = id
		entries[sessionID] = state.Entry{MessageID: id, UpdatedAt: time.Now()}
	}); err != nil {
		debuglog.Printf("state: %v", err)
	}
	return w.messageID != ""
}

// recallOthers takes down every other card that stood for a session whose
// live card has just been adopted, each one only once.
func (d *Daemon) recallOthers(ctx context.Context, keep string, others ...string) {
	gone := map[string]bool{"": true, keep: true}
	for _, id := range others {
		if gone[id] {
			continue
		}
		gone[id] = true
		d.deleteCard(ctx, id)
	}
}

// releaseLiveCard gives up the turn's live-card slot, so a card already
// put to rest is not rewritten again by the turn's completion.
func (d *Daemon) releaseLiveCard(sessionID string) {
	store, err := state.Open()
	if err != nil {
		return
	}
	if err := store.Mutate(func(entries map[string]state.Entry) {
		delete(entries, sessionID)
	}); err != nil {
		debuglog.Printf("state: %v", err)
	}
}

// runLiveCard keeps one card current until there is nothing left to follow.
func (d *Daemon) runLiveCard(ctx context.Context, w *liveCard) {
	ticker := time.NewTicker(d.pace.tick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		w.mu.Lock()
		stopped := w.stopped
		w.mu.Unlock()
		if stopped {
			return // something else already put this card to rest
		}
		s, ok := d.reg.Get(w.sessionID)
		var note string
		switch {
		case !ok:
			note = "This session has ended."
		case s.State == session.Idle:
			// The turn ended without a Stop event reaching Claude Companion.
		case time.Since(w.started) > d.pace.max:
			note = "This card stopped updating after two hours. Reply  sessions  to look in again."
		default:
			d.refreshLiveCard(ctx, w, s, false)
			continue
		}
		// The last write happens on a context of its own: a card that is
		// being put to rest must still be able to say so.
		d.settleLiveCard(context.WithoutCancel(ctx), w.sessionID, note)
		return
	}
}

// refreshLiveCard rewrites the card when it has something new to say. force
// rewrites it regardless, which is what re-posting an existing card does.
func (d *Daemon) refreshLiveCard(ctx context.Context, w *liveCard, s session.Session, force bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopped || w.messageID == "" {
		return
	}
	w.last = s
	turn := w.turn(s.Transcript)
	signature := notify.LiveSignature(s, turn)

	// The turn a card was standing in for has begun. That is news whatever
	// the signature says: the card is about to stop describing the message
	// and start describing the work.
	force = w.takenUp(s.Transcript) || force

	now := time.Now()
	switch {
	case force:
	case signature != w.signature:
		if now.Sub(w.sent) < d.pace.floor {
			return // too soon; the next tick will carry it
		}
		w.changed = now
	case now.Sub(w.sent) >= d.pace.heartbeat:
	default:
		return
	}

	view := viewOf(s, w.changed, w.notes)
	view.Sent = w.pending
	card, err := notify.SessionCard(s, turn, view)
	d.updateCard(ctx, w.messageID, card, err)
	w.signature, w.sent = signature, now
}

// detachLiveCard stops following a session and hands back the card that
// was running, leaving the message exactly as it stands.
//
// That is what the end of a turn wants: the completion notification is
// about to rewrite the very same message into the turn's outcome, which is
// the settled state the live companion asks for and the attention card
// already taught the user.
func (d *Daemon) detachLiveCard(sessionID string) *liveCard {
	d.mu.Lock()
	w, ok := d.live[sessionID]
	if ok {
		delete(d.live, sessionID)
	}
	d.mu.Unlock()
	if !ok {
		return nil
	}
	if w.cancel != nil {
		w.cancel()
	}
	// Taking the lock waits for a refresh already in flight, so nothing
	// this card writes can land after whatever the caller writes next.
	w.mu.Lock()
	w.stopped = true
	w.mu.Unlock()
	debuglog.Printf("stopped following session %s", sessionID)
	return w
}

// settleLiveCard stops following the session and puts the card into a
// resting state of its own: the outcome when the turn is over, an honest
// "still working" when it is not. Either way nothing is left claiming to
// be live.
func (d *Daemon) settleLiveCard(ctx context.Context, sessionID, note string) {
	w := d.detachLiveCard(sessionID)
	if w == nil || w.messageID == "" {
		return
	}
	d.releaseLiveCard(sessionID)

	s, live := d.reg.Get(sessionID)
	if !live {
		// The session is gone from the registry, so the card's own last
		// sighting is the only thing left that can name it.
		w.mu.Lock()
		s = w.last
		w.mu.Unlock()
	}
	turn := transcript.Load(s.Transcript, "")

	card, err := notify.SettledSessionCard(s, turn, note)
	if live && s.State != session.Idle {
		card, err = notify.PausedSessionCard(s, turn, note)
	}
	d.updateCard(ctx, w.messageID, card, err)
}

// abandonPendingCard puts down a card that is still standing in for a
// message the session never took.
//
// Saying so in the conversation is not enough. The card is what the user
// is looking at, and it is claiming their message is with Claude.
func (d *Daemon) abandonPendingCard(ctx context.Context, sessionID, note string) {
	d.mu.Lock()
	w, ok := d.live[sessionID]
	d.mu.Unlock()
	if !ok {
		return
	}
	w.mu.Lock()
	standing := w.pending != ""
	w.mu.Unlock()
	if standing {
		d.settleLiveCard(ctx, sessionID, note)
	}
}

// cardStanding reports whether a session has a live card up, which is how
// the attention-mode progress card knows to stand down: the live card is
// already the one card this turn gets, and a more current one.
func (d *Daemon) cardStanding(sessionID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.live[sessionID]
	return ok
}

// settleAllLiveCards puts every live card to rest as the daemon stops. A
// session card cannot survive the process that polls it, and a card left
// saying "Working" would outlast the truth of it.
func (d *Daemon) settleAllLiveCards(ctx context.Context) {
	d.mu.Lock()
	ids := make([]string, 0, len(d.live))
	for id := range d.live {
		ids = append(ids, id)
	}
	d.mu.Unlock()
	for _, id := range ids {
		d.settleLiveCard(ctx, id, "Claude Companion stopped, so this card is no longer live.")
	}
}
