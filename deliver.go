package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/marcelritzschke/wirelark/internal/feishu"
	"github.com/marcelritzschke/wirelark/internal/hook"
	"github.com/marcelritzschke/wirelark/internal/notify"
	"github.com/marcelritzschke/wirelark/internal/state"
	"github.com/marcelritzschke/wirelark/internal/transcript"
)

const sendTimeout = 15 * time.Second

// A turn only earns a progress card once it has clearly outlasted a normal
// one, and the card refreshes at most this often: progress updates
// reassure, they must not chatter.
const (
	progressAfter   = 10 * time.Minute
	progressRefresh = 10 * time.Minute
)

// deliverer puts one event's card in front of the user. It holds what
// every delivery needs: the event being reported, where to send it, and
// whether this run is only rehearsing.
type deliverer struct {
	payload *hook.Payload
	client  *feishu.Client
	dryRun  bool
}

// fresh sends a standalone notification (permission, question). It takes
// the card builder's result directly, so a build failure stays quiet.
func (d *deliverer) fresh(card string, err error) {
	if err != nil {
		debugLog("build card: %v", err)
		return
	}
	if d.dryRun {
		fmt.Println(card)
		return
	}
	d.send(card)
}

// settle ends a turn: when the turn has a live progress card, the
// completion (or failure) takes its place so one turn stays one message.
// With liveCardOnly, a turn that has no live card is left unreported -
// that is how a purely conversational exchange stays off the user's phone,
// without ever leaving a "still working" card standing.
func (d *deliverer) settle(card string, err error, liveOnly bool) {
	if err != nil {
		debugLog("build card: %v", err)
		return
	}
	if d.dryRun {
		fmt.Println(card)
		return
	}

	store, err := state.Open()
	if err != nil {
		debugLog("open state: %v", err)
		if liveOnly {
			return // cannot tell whether a card is live; stay quiet
		}
		d.send(card)
		return
	}
	if err := store.Mutate(func(entries map[string]state.Entry) {
		live, hadCard := entries[d.payload.SessionID]
		delete(entries, d.payload.SessionID) // the turn ended; no further updates
		if hadCard && live.MessageID != "" {
			if uerr := d.update(live.MessageID, card); uerr == nil {
				debugLog("settled %s by updating message %s", d.payload.HookEventName, live.MessageID)
				return
			}
			debugLog("update progress card to %s failed; sending a new message", d.payload.HookEventName)
		} else if liveOnly {
			debugLog("skip %s: the turn did no work worth reporting", d.payload.HookEventName)
			return
		}
		d.send(card)
	}); err != nil {
		debugLog("state: %v", err)
	}
}

// progress keeps one live card per long-running turn: it sends the first
// card once the turn outlasts a normal one, then refreshes that card at
// most every progressRefresh.
func (d *deliverer) progress(turn *transcript.Turn) {
	if turn.Start.IsZero() {
		return // no measurable turn start; staying quiet beats guessing
	}
	if time.Since(turn.Start) < progressAfter {
		return
	}

	card, err := notify.ProgressCard(d.payload, turn)
	if err != nil {
		debugLog("build card: %v", err)
		return
	}
	if d.dryRun {
		fmt.Println(card)
		return
	}

	store, err := state.Open()
	if err != nil {
		debugLog("open state: %v", err)
		return
	}
	if err := store.Mutate(func(entries map[string]state.Entry) {
		live, ok := entries[d.payload.SessionID]
		if ok && live.MessageID != "" && live.PromptID == d.payload.PromptID {
			if time.Since(live.UpdatedAt) < progressRefresh {
				return // refreshed recently; stay quiet
			}
			if uerr := d.update(live.MessageID, card); uerr != nil {
				debugLog("update progress card: %v", uerr)
				return
			}
			live.UpdatedAt = time.Now()
			entries[d.payload.SessionID] = live
			return
		}
		// No card for this turn yet (or it belongs to an older turn).
		msgID, ok := d.send(card)
		if !ok {
			return
		}
		entries[d.payload.SessionID] = state.Entry{
			PromptID:  d.payload.PromptID,
			MessageID: msgID,
			UpdatedAt: time.Now(),
		}
	}); err != nil {
		debugLog("state: %v", err)
	}
}

// send delivers a new card and reports the message id it landed as.
func (d *deliverer) send(card string) (string, bool) {
	if d.client == nil {
		debugLog("send %s: no feishu client", d.payload.HookEventName)
		return "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
	defer cancel()
	msgID, err := d.client.SendCard(ctx, card)
	if err != nil {
		debugLog("send %s: %v", d.payload.HookEventName, err)
		return "", false
	}
	debugLog("sent %s as message %s", d.payload.HookEventName, msgID)
	return msgID, true
}

// update rewrites an already-delivered card in place.
func (d *deliverer) update(messageID, card string) error {
	if d.client == nil {
		return errors.New("no feishu client")
	}
	ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
	defer cancel()
	return d.client.UpdateCard(ctx, messageID, card)
}
