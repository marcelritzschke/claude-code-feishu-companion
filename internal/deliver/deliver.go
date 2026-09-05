// Package deliver puts one hook event's card in front of the user and keeps
// a turn to one message.
//
// It exists as a package because two roles run this exact lifecycle: the
// daemon, which owns the Feishu connection while it is up, and the hook
// process itself, which falls back to delivering the card directly when the
// daemon is not reachable. A notification the user needs must not depend on
// which of the two handled it, so both run the same code.
package deliver

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/marcelritzschke/claude-code-feishu-companion/internal/config"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/debuglog"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/hook"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/notify"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/state"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/transcript"
)

const sendTimeout = 15 * time.Second

// A turn only earns a progress card once it has clearly outlasted a normal
// one, and the card refreshes at most this often: progress updates
// reassure, they must not chatter.
const (
	progressAfter   = 10 * time.Minute
	progressRefresh = 10 * time.Minute
)

// Sender is the Feishu side of a delivery: send a card, or rewrite one that
// is already standing. Narrowed to these two calls so the daemon's tests can
// record cards without a Feishu account.
type Sender interface {
	SendCard(ctx context.Context, cardJSON string) (string, error)
	UpdateCard(ctx context.Context, messageID, cardJSON string) error
}

// Deliverer holds what every delivery needs: the event being reported,
// where to send it, and whether this run is only rehearsing.
//
// Continue and Skip are the daemon's two additions. Neither is available to
// a hook falling back on its own, and neither has to be: a card without a
// button is still the whole notification.
type Deliverer struct {
	// Payload is the hook event being reported.
	Payload *hook.Payload
	// Sender is where cards go. Nil is legal and stays quiet, which is what
	// a dry run and a misconfigured install both need.
	Sender Sender
	// DryRun prints the card instead of sending it.
	DryRun bool
	// ContinueSession, when set, puts a [ Continue ] button on this event's
	// card pointing at that session.
	ContinueSession string
	// Skip vetoes an event the caller reports better itself - a permission
	// prompt the daemon is relaying with real buttons, say. Without it the
	// user would get two cards for one decision.
	Skip func(hookEvent string) bool
	// Sent, when set, is told which Feishu message a card became, so a
	// caller that may have to rewrite that card later knows which one it is.
	Sent func(hookEvent, messageID string)
}

// Event delivers whatever the hook event calls for: an attention card, the
// turn's outcome, or a progress refresh. Events that say nothing to the user
// deliver nothing.
func (d *Deliverer) Event(turn *transcript.Turn, cfg *config.Config) {
	p := d.Payload
	if d.Skip != nil && d.Skip(p.HookEventName) {
		debuglog.Printf("skip %s: reported another way", p.HookEventName)
		return
	}
	opts := notify.Options{ContinueSession: d.ContinueSession}
	switch p.HookEventName {
	case hook.EventPermissionRequest:
		card, err := notify.PermissionCard(p, turn, opts)
		d.Fresh(card, err)
	case hook.EventPreToolUse:
		card, err := notify.QuestionCard(p, turn, opts)
		d.Fresh(card, err)
	case hook.EventStop:
		// A turn can finish without succeeding: if the work it validated
		// was still failing at the end, that needs the user, not a ✅.
		if turn.Failed {
			card, err := notify.FailureCard(p, turn, opts)
			d.Settle(card, err, AlwaysNotify)
			break
		}
		card, err := notify.CompletionCard(p, turn, opts)
		d.Settle(card, err, WithholdChatter(turn))
	case hook.EventStopFailure:
		card, err := notify.FailureCard(p, turn, opts)
		d.Settle(card, err, AlwaysNotify)
	case hook.EventPostToolUse:
		if cfg.ProgressEnabled() {
			d.Progress(turn, opts)
		}
	}
}

// Fresh sends a standalone notification (permission, question). It takes
// the card builder's result directly, so a build failure stays quiet.
func (d *Deliverer) Fresh(card string, err error) {
	if err != nil {
		debuglog.Printf("build card: %v", err)
		return
	}
	if d.DryRun {
		fmt.Println(card)
		return
	}
	if id, ok := d.send(card); ok {
		d.sent(id)
	}
}

// Settle ends a turn: when the turn has a live progress card, the
// completion (or failure) takes its place so one turn stays one message.
// With LiveCardOnly, a turn that has no live card is left unreported -
// that is how a purely conversational exchange stays off the user's phone,
// without ever leaving a "still working" card standing.
func (d *Deliverer) Settle(card string, err error, liveOnly bool) {
	if err != nil {
		debuglog.Printf("build card: %v", err)
		return
	}
	if d.DryRun {
		fmt.Println(card)
		return
	}

	store, err := state.Open()
	if err != nil {
		debuglog.Printf("open state: %v", err)
		if liveOnly {
			return // cannot tell whether a card is live; stay quiet
		}
		if id, ok := d.send(card); ok {
			d.sent(id)
		}
		return
	}
	if err := store.Mutate(func(entries map[string]state.Entry) {
		live, hadCard := entries[d.Payload.SessionID]
		delete(entries, d.Payload.SessionID) // the turn ended; no further updates
		if hadCard && live.MessageID != "" {
			if uerr := d.update(live.MessageID, card); uerr == nil {
				debuglog.Printf("settled %s by updating message %s", d.Payload.HookEventName, live.MessageID)
				return
			}
			debuglog.Printf("update progress card to %s failed; sending a new message", d.Payload.HookEventName)
		} else if liveOnly {
			debuglog.Printf("skip %s: the turn did no work worth reporting", d.Payload.HookEventName)
			return
		}
		if id, ok := d.send(card); ok {
			d.sent(id)
		}
	}); err != nil {
		debuglog.Printf("state: %v", err)
	}
}

// Progress keeps one live card per long-running turn: it sends the first
// card once the turn outlasts a normal one, then refreshes that card at
// most every progressRefresh.
func (d *Deliverer) Progress(turn *transcript.Turn, opts notify.Options) {
	if turn.Start.IsZero() {
		return // no measurable turn start; staying quiet beats guessing
	}
	if time.Since(turn.Start) < progressAfter {
		return
	}

	card, err := notify.ProgressCard(d.Payload, turn, opts)
	if err != nil {
		debuglog.Printf("build card: %v", err)
		return
	}
	if d.DryRun {
		fmt.Println(card)
		return
	}

	store, err := state.Open()
	if err != nil {
		debuglog.Printf("open state: %v", err)
		return
	}
	if err := store.Mutate(func(entries map[string]state.Entry) {
		live, ok := entries[d.Payload.SessionID]
		if ok && live.MessageID != "" && live.PromptID == d.Payload.PromptID {
			if time.Since(live.UpdatedAt) < progressRefresh {
				return // refreshed recently; stay quiet
			}
			if uerr := d.update(live.MessageID, card); uerr != nil {
				debuglog.Printf("update progress card: %v", uerr)
				return
			}
			live.UpdatedAt = time.Now()
			entries[d.Payload.SessionID] = live
			return
		}
		// No card for this turn yet (or it belongs to an older turn).
		msgID, ok := d.send(card)
		if !ok {
			return
		}
		entries[d.Payload.SessionID] = state.Entry{
			PromptID:  d.Payload.PromptID,
			MessageID: msgID,
			UpdatedAt: time.Now(),
		}
	}); err != nil {
		debuglog.Printf("state: %v", err)
	}
}

// send delivers a new card and reports the message id it landed as.
func (d *Deliverer) send(card string) (string, bool) {
	if d.Sender == nil {
		debuglog.Printf("send %s: no feishu client", d.Payload.HookEventName)
		return "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
	defer cancel()
	msgID, err := d.Sender.SendCard(ctx, card)
	if err != nil {
		debuglog.Printf("send %s: %v", d.Payload.HookEventName, err)
		return "", false
	}
	debuglog.Printf("sent %s as message %s", d.Payload.HookEventName, msgID)
	return msgID, true
}

// sent reports which message a card became, when anyone asked to know.
func (d *Deliverer) sent(messageID string) {
	if d.Sent != nil && messageID != "" {
		d.Sent(d.Payload.HookEventName, messageID)
	}
}

// update rewrites an already-delivered card in place.
func (d *Deliverer) update(messageID, card string) error {
	if d.Sender == nil {
		return errors.New("no feishu client")
	}
	ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
	defer cancel()
	return d.Sender.UpdateCard(ctx, messageID, card)
}
