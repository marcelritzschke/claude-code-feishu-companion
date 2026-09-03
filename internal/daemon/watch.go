package daemon

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/marcelritzschke/claude-code-feishu-companion/internal/debuglog"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/notify"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/session"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/state"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/transcript"
)

// The session card is the one thing in Claude Companion that looks at a
// session continuously, so its whole design is about not becoming a stream.
//
// The transcript is polled rather than pushed: the card then needs no extra
// hook, no extra setup, and no change to a running session. Reading a local
// file every few seconds costs nothing; what has to be rationed is Feishu,
// so the card is only rewritten when what it says actually changes.

// pace is how often a watch looks, and how often it may speak.
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

// defaultPace is the cadence every watch runs at. A daemon carries its own
// copy so a test can run the same loop in a fraction of the time.
var defaultPace = pace{
	tick:      3 * time.Second,
	floor:     5 * time.Second,
	heartbeat: 60 * time.Second,
	max:       2 * time.Hour,
}

// watch is one session's live card: the single message that answers for
// the session until its turn ends.
type watch struct {
	sessionID string
	messageID string
	started   time.Time
	cancel    context.CancelFunc

	// mu orders a refresh against whatever ends the watch. It is held
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
	// last is the session as the watch last saw it, so a card can still be
	// addressed to it after the session is gone from the registry.
	last session.Session
	// cached is the turn as of seen, so a watch that looks every few
	// seconds does not re-parse a transcript that has not moved.
	cached *transcript.Turn
	seen   stamp
	// notes are the turn's decision records: answered prompts whose own
	// cards were recalled, kept visible here instead.
	notes []string
}

// stamp identifies a transcript file well enough to tell that nothing has
// been appended to it.
type stamp struct {
	size int64
	mod  time.Time
}

// turn returns the session's current turn, re-reading the transcript only
// when the file has actually grown. Callers must hold w.mu.
func (w *watch) turn(path string) *transcript.Turn {
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
	if !s.Watchable() || s.State == session.Idle {
		return
	}
	d.mu.Lock()
	w, already := d.watches[s.ID]
	d.mu.Unlock()
	if already {
		// A session that just started waiting must say so now, not on the
		// next poll: force skips the pacing that quiets routine refreshes.
		d.refreshWatch(ctx, w, s, s.State == session.Waiting)
		return
	}
	d.openWatch(ctx, s)
}

// startWatch opens the live view of a session at the user's request: the
// same card ensureSessionCard maintains, but answered out loud, because
// this time the user asked and silence would read as failure.
func (d *Daemon) startWatch(ctx context.Context, s session.Session) {
	if !s.Watchable() {
		d.say(ctx, "Claude Companion cannot see inside "+s.Label()+" yet. It becomes watchable as soon as that session runs its next turn.")
		return
	}

	d.mu.Lock()
	running, already := d.watches[s.ID]
	d.mu.Unlock()
	if already {
		d.refreshWatch(ctx, running, s, true)
		d.say(ctx, "Already watching "+s.Label()+".")
		return
	}

	if s.State == session.Idle {
		// Nothing is in flight, so there is no live view to open - only
		// the outcome of what the session last did. Saying that now beats
		// a card that would sit there claiming to be live.
		turn := transcript.Load(s.Transcript, "")
		card, err := notify.SettledWatchCard(s, turn, "Nothing is running in this session right now.")
		d.sendCard(ctx, card, err)
		debuglog.Printf("watch %s: nothing running; showed the last outcome", s.Describe())
		return
	}
	d.openWatch(ctx, s)
}

// openWatch puts up a session's live card and starts the loop that keeps
// it current. The watch is registered before the card is sent so two
// concurrent hook events cannot both open one.
func (d *Daemon) openWatch(ctx context.Context, s session.Session) {
	now := time.Now()
	w := &watch{
		sessionID: s.ID,
		started:   now,
		changed:   now,
		sent:      now,
		last:      s,
	}
	d.mu.Lock()
	if _, raced := d.watches[s.ID]; raced {
		d.mu.Unlock()
		return // another event opened this session's card first
	}
	d.watches[s.ID] = w
	d.mu.Unlock()

	turn := transcript.Load(s.Transcript, "")
	w.signature = notify.LiveSignature(s, turn)
	card, err := notify.SessionCard(s, turn, viewOf(s, now, nil))
	if err != nil {
		debuglog.Printf("build session card: %v", err)
		d.dropWatch(w)
		return
	}
	// The claim happens under the watch's own lock so the message id is
	// ordered against refreshes, and against whatever might end the watch
	// while the card is still going up.
	w.mu.Lock()
	claimed := d.claimLiveCard(ctx, s.ID, card, w)
	w.mu.Unlock()
	if !claimed {
		d.dropWatch(w)
		return
	}

	wctx, cancel := context.WithCancel(ctx)
	w.cancel = cancel
	go d.runWatch(wctx, w)
	debuglog.Printf("session card for %s standing as message %s", s.Describe(), w.messageID)
}

// dropWatch forgets a watch whose card never made it up.
func (d *Daemon) dropWatch(w *watch) {
	d.mu.Lock()
	if d.watches[w.sessionID] == w {
		delete(d.watches, w.sessionID)
	}
	d.mu.Unlock()
}

// viewOf is what the daemon knows about a session card beyond the
// transcript: how fresh the activity is, whether [ Interrupt ] would
// actually work, and the decisions recorded on this turn.
func viewOf(s session.Session, activityAt time.Time, notes []string) notify.SessionView {
	return notify.SessionView{ActivityAt: activityAt, Interruptible: s.Interruptible(), Notes: notes}
}

// maxCardNotes bounds the decision records one card carries; the oldest
// give way, as everywhere else on a card.
const maxCardNotes = 3

// noteOnSessionCard adds a one-line decision record to a session's live
// card. A prompt whose card was recalled would otherwise leave no trace of
// what was decided; the session card is where that record belongs.
func (d *Daemon) noteOnSessionCard(ctx context.Context, sessionID, note string) {
	d.mu.Lock()
	w, ok := d.watches[sessionID]
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
		d.refreshWatch(ctx, w, s, true)
	}
}

// claimLiveCard puts the watch card up as the turn's one live card.
//
// When a progress card is already standing for this turn, the watch takes
// that message over instead of adding a second live card - one turn stays
// one message, and the completion notification settles whichever of the two
// the user is actually looking at.
func (d *Daemon) claimLiveCard(ctx context.Context, sessionID, cardJSON string, w *watch) bool {
	store, err := state.Open()
	if err != nil {
		// The card can still be shown; it just will not be settled by the
		// turn's own completion notification. The watch settles it instead.
		debuglog.Printf("open state: %v", err)
		w.messageID = d.sendCard(ctx, cardJSON, nil)
		return w.messageID != ""
	}
	if err := store.Mutate(func(entries map[string]state.Entry) {
		if live, ok := entries[sessionID]; ok && live.MessageID != "" {
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

// releaseLiveCard gives up the turn's live-card slot, so a card the watch
// has already put to rest is not rewritten again by the turn's completion.
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

// runWatch keeps one card current until there is nothing left to watch.
func (d *Daemon) runWatch(ctx context.Context, w *watch) {
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
			note = "This card stopped updating after two hours. Reply  watch  to look in again."
		default:
			d.refreshWatch(ctx, w, s, false)
			continue
		}
		// The last card is written on a context of its own: a watch that
		// is ending must still be able to say that it ended.
		d.closeWatch(context.WithoutCancel(ctx), w.sessionID, note)
		return
	}
}

// refreshWatch rewrites the card when it has something new to say. force
// rewrites it regardless, which is what re-opening an existing watch does.
func (d *Daemon) refreshWatch(ctx context.Context, w *watch, s session.Session, force bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopped || w.messageID == "" {
		return
	}
	w.last = s
	turn := w.turn(s.Transcript)
	signature := notify.LiveSignature(s, turn)

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

	card, err := notify.SessionCard(s, turn, viewOf(s, w.changed, w.notes))
	d.updateCard(ctx, w.messageID, card, err)
	w.signature, w.sent = signature, now
}

// endWatch stops watching a session and hands back the watch that was
// running, leaving its card exactly as it stands.
//
// That is what the end of a turn wants: the completion notification is
// about to rewrite the very same message into the turn's outcome, which is
// the settled state V3 asks for and the card V1 already taught the user.
func (d *Daemon) endWatch(sessionID string) *watch {
	d.mu.Lock()
	w, ok := d.watches[sessionID]
	if ok {
		delete(d.watches, sessionID)
	}
	d.mu.Unlock()
	if !ok {
		return nil
	}
	if w.cancel != nil {
		w.cancel()
	}
	// Taking the lock waits for a refresh already in flight, so nothing
	// this watch writes can land after whatever the caller writes next.
	w.mu.Lock()
	w.stopped = true
	w.mu.Unlock()
	debuglog.Printf("stopped watching session %s", sessionID)
	return w
}

// closeWatch stops watching and puts the card into a resting state of its
// own: the outcome when the turn is over, an honest "still working" when it
// is not. Either way there is no spinner left running.
func (d *Daemon) closeWatch(ctx context.Context, sessionID, note string) {
	w := d.endWatch(sessionID)
	if w == nil || w.messageID == "" {
		return
	}
	d.releaseLiveCard(sessionID)

	s, live := d.reg.Get(sessionID)
	if !live {
		// The session is gone from the registry, so the watch's own last
		// sighting is the only thing left that can name it on the card.
		w.mu.Lock()
		s = w.last
		w.mu.Unlock()
	}
	turn := transcript.Load(s.Transcript, "")

	card, err := notify.SettledWatchCard(s, turn, note)
	if live && s.State != session.Idle {
		card, err = notify.WatchStoppedCard(s, turn, note)
	}
	d.updateCard(ctx, w.messageID, card, err)
}

// watching reports whether a session's live view is open, which is how the
// V1 progress card knows to stand down: while the user is watching, the
// live card is already the one card this turn gets.
func (d *Daemon) watching(sessionID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.watches[sessionID]
	return ok
}

// closeAllWatches puts every live card to rest as the daemon stops. A
// session card cannot survive the process that polls it, and a card left
// saying "Working" would outlast the truth of it.
func (d *Daemon) closeAllWatches(ctx context.Context) {
	d.mu.Lock()
	ids := make([]string, 0, len(d.watches))
	for id := range d.watches {
		ids = append(ids, id)
	}
	d.mu.Unlock()
	for _, id := range ids {
		d.closeWatch(ctx, id, "Claude Companion stopped, so this card is no longer live.")
	}
}
