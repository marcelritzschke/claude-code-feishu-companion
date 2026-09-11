package notify

import (
	"fmt"
	"strings"

	"github.com/marcelritzschke/claude-code-feishu-companion/internal/mcp"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/pathdisp"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/session"
)

// commandFullCap is how much of a command a permission card shows, in
// runes. It is far more generous than anything else on a card, because
// approving what you cannot see is the failure this feature must not have.
const commandFullCap = 900

// PickList is the typed way to choose a session, for when the cards
// cannot be tapped.
//
// Card callbacks are a separate Feishu subscription from card delivery, so
// an app can send perfectly good session cards while every reply box on
// them is inert. A plain-text list needs nothing but a chat, which is why
// it is what the numbered replies resolve against.
//
// It is empty when there is nothing to choose between: one session, or
// none that can be continued, is answered by that session's own card.
func PickList(sessions []session.Session) string {
	var lines []string
	for _, s := range sessions {
		if !s.Remote.Continuable() {
			continue
		}
		lines = append(lines, fmt.Sprintf("%d. %s %s · %s",
			len(lines)+1, stateMark(s.State), s.Label(), stateWord(s.State)))
	}
	if len(lines) < 2 {
		return ""
	}
	return "Reply with a number to send your next message to one of these:\n" + strings.Join(lines, "\n")
}

// PermissionRelayCard puts a tool approval in front of the user with the
// two answers Claude Code will accept.
//
// A high-risk action changes the card rather than only its wording: red
// rather than orange, Deny given the emphasis, and Allow left plain. The
// command itself is shown at length, because a decision made from an
// excerpt is not a decision.
func PermissionRelayCard(s session.Session, req mcp.PermissionRequest) (string, error) {
	risk := Classify(req.Description, req.InputPreview)

	asked := "**" + readableTool(req.ToolName) + "**"
	if subject := permissionSubject(req); subject != "" {
		asked += "\n" + subject
	}
	bodies := []string{"Claude wants to run:", asked}
	if s.Dir != "" {
		bodies = append(bodies, "**In**\n"+pathdisp.Home(s.Dir))
	}

	template, title := "orange", "⚠️ Permission requested"
	allow, deny := stylePrimary, styleDefault
	note := "You can also answer in Claude Code."
	if risk == RiskHigh {
		template, title = "red", "🛑 Permission requested"
		allow, deny = styleDefault, styleDanger
		note = "This action cannot easily be undone. Read it before allowing."
	}
	// The typed form is spelled out because it is the one that always
	// works: card buttons depend on a callback subscription this app may
	// not have, and a prompt nobody can answer stops the session dead.
	footer := "Or reply  y " + req.RequestID + "  to allow,  n " + req.RequestID + "  to deny.\n" + note

	buttons := []Button{
		{Label: "Allow once", Style: allow, Action: Action{
			Kind: ActionPermit, Session: s.ID, Request: req.RequestID, Verdict: VerdictAllow}},
		{Label: "Deny", Style: deny, Action: Action{
			Kind: ActionPermit, Session: s.ID, Request: req.RequestID, Verdict: VerdictDeny}},
	}
	return card(template, title, s.Describe(), bodies, buttons, footer)
}

// permissionSubject is what the user is actually approving. The preview
// carries the arguments and the description only summarises them, so the
// preview leads and the description fills in when there is no preview.
func permissionSubject(req mcp.PermissionRequest) string {
	if req.InputPreview != "" {
		return truncateRunes(req.InputPreview, commandFullCap)
	}
	if req.Description != "" {
		return truncateRunes(flatten(req.Description), actionCap)
	}
	return ""
}

// PermissionAnsweredCard is what a permission card becomes when it was
// answered but could not be recalled, so no prompt is ever left standing
// after it stopped mattering.
func PermissionAnsweredCard(s session.Session, req mcp.PermissionRequest, verdict string) (string, error) {
	template, title, said := "green", "✓ Allowed once", "You allowed:"
	if verdict != VerdictAllow {
		template, title, said = "grey", "✕ Denied", "You denied:"
	}
	bodies := []string{said, permissionSubject(req)}
	footer := "Claude resumed working."
	if verdict != VerdictAllow {
		footer = "Claude was told no and continued from there."
	}
	return card(template, title, s.Describe(), bodies, nil, footer)
}

// noteCap bounds the subject on a session-card decision line.
const noteCap = 60

// VerdictNote is the one-line record of an answered permission, which is
// what remains on the session card once the prompt's own card is recalled.
func VerdictNote(req mcp.PermissionRequest, verdict string) string {
	mark, word := "✓", "Allowed once"
	if verdict != VerdictAllow {
		mark, word = "✕", "Denied"
	}
	if subject := truncateRunes(flatten(permissionSubject(req)), noteCap); subject != "" {
		return mark + " " + word + " · " + subject
	}
	return mark + " " + word
}

// PermissionHandledLocallyCard settles a permission card the user answered
// in the terminal instead. Claude Code says nothing when the local dialog
// wins, so this is drawn the moment the session is seen working again.
func PermissionHandledLocallyCard(s session.Session, req mcp.PermissionRequest) (string, error) {
	bodies := []string{"This was handled in Claude Code.", permissionSubject(req)}
	return card("grey", "✔️ Already answered", s.Describe(), bodies, nil, "")
}

// stateMark and stateWord are the two halves of how a state reads: a mark
// to scan a list by, and a word to read one session by.
func stateMark(st session.State) string {
	switch st {
	case session.Waiting:
		return "🟠"
	case session.Working:
		return "🟢"
	default:
		return "⚪"
	}
}

func stateWord(st session.State) string {
	switch st {
	case session.Waiting:
		return "Waiting for you"
	case session.Working:
		return "Working"
	default:
		return "Idle"
	}
}
