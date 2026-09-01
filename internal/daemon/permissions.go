package daemon

import (
	"context"

	"github.com/marcelritzschke/wirelark/internal/debuglog"
	"github.com/marcelritzschke/wirelark/internal/hook"
	"github.com/marcelritzschke/wirelark/internal/mcp"
	"github.com/marcelritzschke/wirelark/internal/notify"
	"github.com/marcelritzschke/wirelark/internal/session"
)

// onPermissionRequest puts a relayed tool approval in front of the user
// with the two answers Claude Code will accept.
//
// The local dialog is open the whole time and either answer ends it. That
// is the shape of the feature: Wirelark adds a second place to answer from,
// it does not move the decision off the computer.
func (d *Daemon) onPermissionRequest(ctx context.Context, link session.Channel, req mcp.PermissionRequest) {
	if !d.cfg.RemotePermissionsEnabled() {
		debuglog.Printf("permission %s not relayed: remote approval is switched off", req.RequestID)
		return
	}
	// After a /clear the channel is unchanged but its session has a new
	// id, so the channel - not the id it registered with - names the session.
	s, ok := d.reg.SessionOf(link)
	if !ok {
		return
	}

	card, err := notify.PermissionRelayCard(s, req)

	// The hook for this same prompt may already have put a card up. Whoever
	// got here first owns the message; this one only rewrites it, so one
	// decision is one card however the two events are ordered.
	d.mu.Lock()
	standing, hadCard := d.bySession[s.ID]
	if hadCard && !standing.settled && standing.messageID != "" {
		standing.req = req
		standing.relayed = true
		d.byRequest[req.RequestID] = standing
		messageID := standing.messageID
		d.mu.Unlock()
		d.updateCard(ctx, messageID, card, err)
		debuglog.Printf("permission %s relayed by rewriting message %s", req.RequestID, messageID)
		return
	}
	p := &prompt{sessionID: s.ID, req: req, relayed: true}
	d.byRequest[req.RequestID] = p
	d.bySession[s.ID] = p
	d.mu.Unlock()

	messageID := d.sendCard(ctx, card, err)
	d.mu.Lock()
	p.messageID = messageID
	d.mu.Unlock()
	debuglog.Printf("permission %s relayed for %s", req.RequestID, s.Describe())
}

// recordHookPrompt remembers the card a hook-driven permission notification
// became, so a relay arriving afterwards can rewrite it rather than add a
// second card for the same decision.
func (d *Daemon) recordHookPrompt(sessionID, hookEvent, messageID string) {
	if hookEvent != hook.EventPermissionRequest {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.bySession[sessionID] = &prompt{sessionID: sessionID, messageID: messageID}
}

// answerPermission carries the user's tap back to the session and settles
// the card. An id Claude Code has no open request for is discarded there,
// so the card says the decision is already made rather than pretending the
// tap did something.
func (d *Daemon) answerPermission(ctx context.Context, act notify.Action, messageID string) {
	d.mu.Lock()
	p, ok := d.byRequest[act.Request]
	if ok && p.settled {
		d.mu.Unlock()
		debuglog.Printf("permission %s was already answered", act.Request)
		return
	}
	if ok {
		p.settled = true
		if messageID == "" {
			messageID = p.messageID
		}
	}
	d.mu.Unlock()

	if !ok {
		d.say(ctx, "That decision is no longer open. Claude Code has already moved on.")
		return
	}

	if err := d.verdictTo(p.sessionID, act.Request, act.Verdict); err != nil {
		debuglog.Printf("answer permission %s: %v", act.Request, err)
		d.say(ctx, "Wirelark could not reach that session to pass on your answer. Please answer in Claude Code.")
		return
	}
	debuglog.Printf("permission %s answered %s", act.Request, act.Verdict)

	s, _ := d.reg.Get(p.sessionID)
	if messageID != "" {
		card, err := notify.PermissionAnsweredCard(s, p.req, act.Verdict)
		d.updateCard(ctx, messageID, card, err)
	}
	if act.Verdict == notify.VerdictAllow {
		d.reg.MarkWorking(p.sessionID)
	}
}

// settleStandingPrompt closes a permission card whose decision was made in
// the terminal instead. Claude Code says nothing when the local dialog
// wins, so the proof is the session getting on with its work - and until
// then the card would sit there asking for an answer that already exists.
func (d *Daemon) settleStandingPrompt(ctx context.Context, sessionID string) {
	d.mu.Lock()
	p, ok := d.bySession[sessionID]
	if !ok {
		d.mu.Unlock()
		return
	}
	// The record goes either way. It is only kept after an answer so that a
	// second tap - a double press, a Feishu retry - is a quiet no-op, and
	// the session moving on is the moment that stops mattering.
	delete(d.bySession, sessionID)
	if p.req.RequestID != "" {
		delete(d.byRequest, p.req.RequestID)
	}
	if p.settled {
		d.mu.Unlock()
		return
	}
	p.settled = true
	messageID, req, relayed := p.messageID, p.req, p.relayed
	d.mu.Unlock()

	if messageID == "" || !relayed {
		return // nothing standing that offers an answer it can no longer take
	}
	s, _ := d.reg.Get(sessionID)
	card, err := notify.PermissionHandledLocallyCard(s, req)
	d.updateCard(ctx, messageID, card, err)
	debuglog.Printf("permission %s settled: answered in Claude Code", req.RequestID)
}
