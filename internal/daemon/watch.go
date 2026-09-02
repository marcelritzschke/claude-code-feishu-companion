package daemon

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/marcelritzschke/wirelark/internal/debuglog"
	"github.com/marcelritzschke/wirelark/internal/notify"
	"github.com/marcelritzschke/wirelark/internal/session"
	"github.com/marcelritzschke/wirelark/internal/state"
	"github.com/marcelritzschke/wirelark/internal/transcript"
)

// Watching is the one thing in Wirelark that looks at a session
// continuously, so its whole design is about not becoming a stream.
//
// The transcript is polled rather than pushed: watching then needs no extra
// hook, no extra setup, and no change to a running session - a session that
// can be continued can simply be watched. Reading a local file every few
// seconds costs nothing; what has to be rationed is Feishu, so the card is
// only rewritten when what it says actually changes.

// pace is how often a watch looks, and how often it may speak.
type pace struct {
	// tick is how often the transcript is re-read.
	tick time.Duration
	// floor is the least time between two rewrites of the card, so a burst
	// of activity cannot turn into a burst of updates.
	floor time.Duration
	// heartbeat rewrites an unchanged card occasionally, so the elapsed
	// time it shows stays true rather than quietly freezing.
	heartbeat time.Duration
	// max bounds a watch the user forgot about. Watching is a check-in,
	// not a subscription.
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

// watch is one session the user asked to see, and the single card that
// answers for it.
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

// startWatch opens the live view of a session: one card that updates in
// place until the turn it is watching ends.
func (d *Daemon) startWatch(ctx context.Context, s session.Session) {
	if !s.Watchable() {
		d.say(ctx, "Wirelark cannot see inside "+s.Label()+" yet. It becomes watchable as soon as that session runs its next turn.")
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

	turn := transcript.Load(s.Transcript, "")
	if s.State == session.Idle {
		// Nothing is in flight, so there is no live view to open - only
		// the outcome of what the session last did. Saying that now beats
		// a card that would sit there claiming to be live.
		card, err := notify.SettledWatchCard(s, turn, "Nothing is running in this session right now.")
		d.sendCard(ctx, card, err)
		debuglog.Printf("watch %s: nothing running; showed the last outcome", s.Describe())
		return
	}

	now := time.Now()
	w := &watch{
		sessionID: s.ID,
		started:   now,
		signature: notify.LiveSignature(s, turn),
		changed:   now,
		sent:      now,
		last:      s,
	}
	card, err := notify.LiveCard(s, turn, now)
	if err != nil {
		debuglog.Printf("build live card: %v", err)
		return
	}
	if !d.claimLiveCard(ctx, s.ID, card, w) {
		return
	}

	wctx, cancel := context.WithCancel(ctx)
	w.cancel = cancel
	d.mu.Lock()
	d.watches[s.ID] = w
	d.mu.Unlock()
	go d.runWatch(wctx, w)
	debuglog.Printf("watching %s as message %s", s.Describe(), w.messageID)
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
		s, ok := d.reg.Get(w.sessionID)
		var note string
		switch {
		case !ok:
			note = "This session has ended."
		case s.State == session.Idle:
			// The turn ended without a Stop event reaching Wirelark.
		case time.Since(w.started) > d.pace.max:
			note = "Wirelark stopped watching after two hours. Ask again to look in."
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
	if w.stopped {
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

	card, err := notify.LiveCard(s, turn, w.changed)
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
// watch cannot survive the process that polls it, and a card left saying
// "Claude is working" would outlast the truth of it.
func (d *Daemon) closeAllWatches(ctx context.Context) {
	d.mu.Lock()
	ids := make([]string, 0, len(d.watches))
	for id := range d.watches {
		ids = append(ids, id)
	}
	d.mu.Unlock()
	for _, id := range ids {
		d.closeWatch(ctx, id, "Wirelark stopped watching. Ask again to look in.")
	}
}
