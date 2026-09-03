package daemon

import (
	"context"

	"github.com/marcelritzschke/claude-code-feishu-companion/internal/debuglog"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/hook"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/mcp"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/notify"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/session"
)

// onPermissionRequest puts a relayed tool approval in front of the user
// with the two answers Claude Code will accept.
//
// The local dialog is open the whole time and either answer ends it. That
// is the shape of the feature: Claude Companion adds a second place to answer from,
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
	if hadCard && standing.kind == promptPermission && !standing.settled && standing.messageID != "" {
		standing.req = req
		standing.relayed = true
		d.byRequest[req.RequestID] = standing
		messageID := standing.messageID
		d.mu.Unlock()
		d.updateCard(ctx, messageID, card, err)
		debuglog.Printf("permission %s relayed by rewriting message %s", req.RequestID, messageID)
		return
	}
	p := &prompt{sessionID: s.ID, kind: promptPermission, req: req, relayed: true}
	d.byRequest[req.RequestID] = p
	d.bySession[s.ID] = p
	d.mu.Unlock()

	messageID := d.sendCard(ctx, card, err)
	d.mu.Lock()
	p.messageID = messageID
	d.mu.Unlock()
	debuglog.Printf("permission %s relayed for %s", req.RequestID, s.Describe())
}

// recordHookPrompt remembers the card a hook-driven attention notification
// became - a permission prompt or a question - so it can be settled once
// the session moves on, and so a permission relay arriving afterwards can
// rewrite it rather than add a second card for the same decision.
func (d *Daemon) recordHookPrompt(sessionID, hookEvent, messageID string) {
	var kind string
	switch hookEvent {
	case hook.EventPermissionRequest:
		kind = promptPermission
	case hook.EventPreToolUse:
		kind = promptQuestion
	default:
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.bySession[sessionID] = &prompt{sessionID: sessionID, kind: kind, messageID: messageID}
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
		d.say(ctx, "Claude Companion could not reach that session to pass on your answer. Please answer in Claude Code.")
		return
	}
	debuglog.Printf("permission %s answered %s", act.Request, act.Verdict)

	if act.Verdict == notify.VerdictAllow {
		d.reg.MarkWorking(p.sessionID)
	}
	// The answered card leaves the conversation; its outcome moves onto the
	// session card. Only when the recall is refused does it settle in place.
	if messageID != "" && !d.deleteCard(ctx, messageID) {
		s, _ := d.reg.Get(p.sessionID)
		card, err := notify.PermissionAnsweredCard(s, p.req, act.Verdict)
		d.updateCard(ctx, messageID, card, err)
	}
	d.noteOnSessionCard(ctx, p.sessionID, notify.VerdictNote(p.req, act.Verdict))
}

// settleStandingPrompt closes an attention card whose moment has passed: a
// permission decided in the terminal, or a question answered there. Claude
// Code says nothing when the local dialog wins, so the proof is the
// session getting on with its work - and until then the card would sit
// there asking for an answer that already exists.
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
	messageID, req, kind := p.messageID, p.req, p.kind
	d.mu.Unlock()

	if messageID == "" {
		return // nothing standing to settle
	}
	// The prompt's moment has passed, so its card leaves the conversation
	// and the session card records that it was handled at the terminal.
	if d.deleteCard(ctx, messageID) {
		note := "✓ Decided in Claude Code"
		if kind == promptQuestion {
			note = "✓ Answered in Claude Code"
		}
		d.noteOnSessionCard(ctx, sessionID, note)
		debuglog.Printf("%s prompt for session %s recalled: answered in Claude Code", kind, sessionID)
		return
	}
	s, _ := d.reg.Get(sessionID)
	var card string
	var err error
	if kind == promptQuestion {
		card, err = notify.QuestionAnsweredCard(s)
	} else {
		card, err = notify.PermissionHandledLocallyCard(s, req)
	}
	d.updateCard(ctx, messageID, card, err)
	debuglog.Printf("%s prompt for session %s settled: answered in Claude Code", kind, sessionID)
}
