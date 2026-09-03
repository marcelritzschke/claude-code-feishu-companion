package notify

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/marcelritzschke/claude-code-feishu-companion/internal/mcp"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/pathdisp"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/session"
)

// Caps for the V2 surfaces, in runes. A permission a user is about to
// approve is shown far more generously than anything else on a card,
// because approving what you cannot see is the failure this feature must
// not have.
const (
	commandFullCap = 900 // the command a permission card asks about
	titleCap       = 60  // a session's own name for its work
	messageCap     = 120 // an echo of what the user sent
)

// OverviewCard answers the whole of "what is running on my computer": which
// sessions exist, what each is broadly doing, which needs attention, and
// which can be continued from here. Nothing on it identifies a session by
// anything but its project and its work.
func OverviewCard(sessions []session.Session) (string, error) {
	if len(sessions) == 0 {
		return card("grey", "Claude Companion", "", []string{
			"No Claude Code sessions are running on your computer right now.",
		}, nil, "Start one with `claude`, and it will appear here.")
	}

	var (
		bodies  []string
		buttons []Button
		offered int
	)
	for _, s := range sessions {
		// Only the sessions that can be continued are numbered, because the
		// number is an offer to talk to one, and offering a session that
		// would refuse is worse than not offering it.
		number := 0
		if s.Remote.Continuable() {
			offered++
			number = offered
		}
		bodies = append(bodies, overviewRow(s, number))
		if number == 0 {
			continue
		}
		buttons = append(buttons, Button{
			Label:  strconv.Itoa(number) + ". " + truncateRunes(s.Describe(), titleCap),
			Action: Action{Kind: ActionSelect, Session: s.ID},
		})
	}

	footer := "Tap a session, or reply with its number, to continue it."
	if offered == 0 {
		footer = "None of these sessions can be continued from here."
	}
	return card("blue", "Claude Companion", "Your local Claude sessions", bodies, buttons, footer)
}

// overviewRow is one session as the overview reads it: a state anyone can
// scan, the project, its work, and an honest word about reachability. A
// number is shown only for a session the user can pick by typing it.
func overviewRow(s session.Session, number int) string {
	var b strings.Builder
	if number > 0 {
		fmt.Fprintf(&b, "%s **%d. %s**", stateMark(s.State), number, s.Label())
	} else {
		fmt.Fprintf(&b, "%s **%s**", stateMark(s.State), s.Label())
	}
	if s.Title != "" {
		b.WriteString("\n" + truncateRunes(s.Title, titleCap))
	}
	b.WriteString("\n" + stateWord(s.State) + " · " + remoteWord(s.Remote))
	return b.String()
}

// SelectedCard confirms which session the user is now talking to. It exists
// because the one rule this feature cannot bend is that the user always
// knows where their next message is going.
func SelectedCard(s session.Session) (string, error) {
	bodies := []string{sessionIdentity(s)}
	footer := "Send a message here to continue this Claude session."
	if !s.Remote.Continuable() {
		footer = "This session was not started with Claude Companion enabled, so it can only send you notifications."
	}

	// Watching needs no channel - it only reads what the session's hooks
	// already report - so it is offered even for a session that can do
	// nothing else from here.
	var buttons []Button
	if s.Watchable() {
		buttons = append(buttons, Button{
			Label:  "Watch",
			Action: Action{Kind: ActionWatch, Session: s.ID},
		})
		footer += "\nOr tap Watch, or reply  watch  , to see what it is doing."
	}
	return card("blue", s.Label(), "", bodies, buttons, footer)
}

// sessionIdentity is the block that names a session on its own card: its
// work, where it lives, and whether it can hear you.
func sessionIdentity(s session.Session) string {
	var b strings.Builder
	if s.Title != "" {
		b.WriteString(truncateRunes(s.Title, titleCap) + "\n")
	}
	if s.Dir != "" {
		b.WriteString(pathdisp.Home(s.Dir) + "\n")
	}
	b.WriteString("\n" + stateMark(s.State) + " " + remoteWord(s.Remote))
	return b.String()
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

// remoteWord says what the user can do with a session, in their terms. A
// session Claude Companion cannot reach is not broken - it just does less.
func remoteWord(r session.Remote) string {
	switch r {
	case session.Ready:
		return "Remote ready"
	case session.Unconfirmed:
		return "Remote untested"
	default:
		return "Notifications only"
	}
}
