package notify

import (
	"strings"
	"time"

	"github.com/marcelritzschke/claude-code-feishu-companion/internal/session"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/transcript"
)

// Caps for the live card, in runes. A live card is read in a few seconds
// or it has failed, so everything on it is shorter than the same thing on
// a notification the user only ever reads once.
const (
	progressCap = 240 // "current progress", roughly three phone lines
	itemCap     = 64  // one recent-activity item
	detailCap   = 120 // the detail line under an item that went wrong
)

// SessionView is what the daemon knows about a session's live card that
// neither the session nor its transcript can say: how fresh the observed
// activity is, whether an interrupt could actually be delivered, and the
// decisions whose own cards have been recalled.
type SessionView struct {
	// ActivityAt is when meaningful activity was last observed. It is what
	// the card's liveness note reports, and it is deliberately separate
	// from elapsed time: a turn can run long while its activity is recent,
	// or run short and go quiet.
	ActivityAt time.Time
	// Interruptible puts [ Interrupt ] on the working card.
	Interruptible bool
	// Notes are one-line records of this turn's answered prompts. An
	// answered permission card is recalled from the conversation, so the
	// session card is where its outcome stays visible.
	Notes []string
	// Sent is a message pushed into this session from its own card that
	// the session has not begun a turn for yet. While it is set, the turn
	// the transcript describes is the one the user's message came after,
	// so the card says the one thing that is true - the message went -
	// rather than dressing up finished work as work in progress.
	Sent string
}

// SessionCard is the one live representation of a Claude Code session:
// what it is doing, whether it is still active, and whether it needs the
// user. It updates in place instead of producing a new message for every
// state change, and it stays quiet - attention is pushed by the separate
// permission and question cards, never by this one.
func SessionCard(s session.Session, turn *transcript.Turn, view SessionView) (string, error) {
	switch {
	case view.Sent != "":
		return sentSessionCard(s, view)
	case s.State == session.Waiting:
		return waitingSessionCard(s, turn, view)
	}
	return workingSessionCard(s, turn, view)
}

// sentSessionCard is the live card in the moment between a message being
// pushed from it and Claude taking that message up.
//
// It exists because the transcript is one turn behind the user: the work
// it describes is the work they just replied to. A card that showed it as
// "Working" would put the previous turn's progress and the previous
// turn's clock under a heading about the message just sent, which is the
// most confusing thing a live card could say.
func sentSessionCard(s session.Session, view SessionView) (string, error) {
	bodies := []string{
		"Your message is with Claude.",
		"**You**\n\"" + truncateRunes(view.Sent, quoteCap) + "\"",
		notesBody(view.Notes),
	}
	sections := append(proseOf(bodies), replyTo(continuable(s)))
	return cardOf("blue", "🔵 Sent", s.Describe(), sections, nil,
		"This card follows the turn as soon as Claude starts it.")
}

// workingSessionCard shows a turn in flight: the latest meaningful
// progress, the few actions behind it, and how recently anything was
// observed. A session Claude Companion cannot control says so on the card
// itself rather than offering controls that would not work.
//
// It is blue rather than green, because green is the colour of a turn that
// is over. A card that keeps its colour from the first tool call to the
// outcome would make the one thing the user looks for - is this finished? -
// the one thing they cannot see at a glance.
func workingSessionCard(s session.Session, turn *transcript.Turn, view SessionView) (string, error) {
	template, title := "blue", "🔵 Working"+elapsedSuffix(turn)
	footer := activityNote(view.ActivityAt)
	var buttons []Button
	switch {
	case !s.Remote.Continuable():
		template, title = "grey", "⚪ Working · Notifications only"
		footer = joinNotes(footer, "Notifications only — this session cannot be controlled from here.")
	case view.Interruptible:
		buttons = append(buttons, Button{
			Label:   "Interrupt",
			Action:  Action{Kind: ActionInterrupt, Session: s.ID},
			Confirm: interruptConfirm,
		})
	}

	fixed := []Section{Prose("**Current progress**\n" + currentProgress(turn))}
	if notes := notesBody(view.Notes); notes != "" {
		fixed = append(fixed, Prose(notes))
	}
	// The reply box goes last, under everything the card has to say: it is
	// the answer to what the user just read.
	tail := []Section{replyTo(continuable(s))}
	sections := withActivity(fixed, turn.Steps, tail, buttons, footer)
	return cardOf(template, title, s.Describe(), sections, buttons, footer)
}

// interruptConfirm is the dialog [ Interrupt ] opens instead of acting.
//
// The action itself is mild - it stops a turn and nothing else - but the
// button sits on the card the user reads to check on their work, one tap
// away from every scroll. What the dialog is really guarding against is
// the accident, so it says both halves: what stops, and what does not.
var interruptConfirm = &Confirm{
	Title: "Stop this turn?",
	Text:  "Claude stops what it is doing and the session goes back to its prompt. Nothing is closed, and nothing done so far is lost.",
}

// continuable is the session id a card may offer to talk to, empty when
// this session cannot be continued from here. A box that swallowed
// messages a session will never read would be worse than no box.
func continuable(s session.Session) string {
	if !s.Remote.Continuable() {
		return ""
	}
	return s.ID
}

// withActivity places a turn's activity after the sections that always
// appear, giving it whatever element budget those leave behind.
//
// The budget is worked out here rather than guessed at, because what fits
// depends on the rest of the card: a session that can be interrupted
// spends elements on a button, and every section costs a rule to separate
// it from the one before.
func withActivity(fixed []Section, steps []transcript.Step, tail []Section, buttons []Button, footer string) []Section {
	spent, sections := 0, len(fixed)
	for _, sec := range fixed {
		spent += sec.cost()
	}
	for _, sec := range tail {
		if sec == nil {
			continue
		}
		spent += sec.cost()
		sections++
	}
	// The activity arrives as a single block, so it adds exactly one more
	// section and therefore exactly one more rule.
	budget := elementBudget - spent - chromeCost(sections+1, buttons, footer)
	out := fixed
	if activity := activitySections(steps, budget); activity != nil {
		out = append(out, activity)
	}
	for _, sec := range tail {
		if sec != nil {
			out = append(out, sec)
		}
	}
	return out
}

// waitingSessionCard is the session card while Claude is blocked on the
// user. It states only the fact: the actionable notification is the
// separate permission or question card. Session card = state, permission
// and question cards = action.
func waitingSessionCard(s session.Session, turn *transcript.Turn, view SessionView) (string, error) {
	title, body := "🟠 Waiting for permission", "Claude needs approval before continuing."
	if s.WaitingOn == session.WaitAnswer {
		title, body = "🟠 Waiting for answer", "Claude needs your answer before continuing."
	}
	bodies := []string{body, "**Where it got to**\n" + currentProgress(turn), notesBody(view.Notes)}
	sections := append(proseOf(bodies), replyTo(continuable(s)))
	return cardOf("orange", title+elapsedSuffix(turn), s.Describe(), sections, nil, "")
}

// notesBody renders the turn's decision records, empty when there are none.
func notesBody(notes []string) string {
	if len(notes) == 0 {
		return ""
	}
	return "**Decisions**\n" + strings.Join(notes, "\n")
}

// InterruptedSessionCard is what a session card settles into when the user
// stops the turn from here. Interrupting stops the work and returns the
// session to its prompt; it neither ends the session nor closes anything,
// and the card must read that way.
func InterruptedSessionCard(s session.Session, turn *transcript.Turn) (string, error) {
	bodies := []string{
		"You interrupted this turn. The session is back at its prompt in Claude Code.",
		"**Where it got to**\n" + currentProgress(turn),
	}
	sections := append(proseOf(bodies), replyTo(continuable(s)))
	return cardOf("grey", "⏹️ Interrupted"+elapsedSuffix(turn), s.Describe(), sections, nil, "")
}

// SettledWatchCard is what a session card becomes when there is nothing
// live left to show: the turn's outcome, and the way back into the session.
//
// It is the fallback settle. A turn that ends while its card is standing
// normally settles into the ordinary completion or failure notification;
// this one covers looking at a session that is between turns, and cards
// put to rest for a reason of their own.
func SettledWatchCard(s session.Session, turn *transcript.Turn, note string) (string, error) {
	template, title := "green", "✅ Completed"
	if turn.Failed {
		template, title = "red", "🔴 Failed"
	}

	summary, _ := splitFinal(turn.Progress)
	if summary == "" {
		summary = accomplishment(turn)
	}
	bodies := []string{summary}
	if v := validationLines(turn.Tests); len(v) > 0 {
		bodies = append(bodies, "**Validation**\n"+strings.Join(v, "\n"))
	}

	sections := append(withHistory(proseOf(bodies), turn), replyTo(continuable(s)))
	return cardOf(template, title+elapsedSuffix(turn), s.Describe(), sections, nil, note)
}

// WatchStoppedCard leaves a session card at rest while its turn is still
// running. It deliberately does not read as an outcome: the work has not
// finished, and the ordinary completion notification is still to come.
func WatchStoppedCard(s session.Session, turn *transcript.Turn, note string) (string, error) {
	bodies := []string{"**Where it got to**\n" + currentProgress(turn)}
	if note == "" {
		note = "Claude is still working. Claude Companion will tell you when it finishes."
	}
	sections := append(proseOf(bodies), replyTo(continuable(s)))
	return cardOf("grey", "⏸️ No longer live", s.Describe(), sections, nil, note)
}

// LiveSignature is everything on a session card that is worth rewriting
// the card for: the state, what Claude is doing, and the activity behind
// it, without the clock. Two renders with the same signature say the same
// thing, so the card is left alone rather than rewritten every few seconds.
func LiveSignature(s session.Session, turn *transcript.Turn) string {
	const sep = "\x1f"
	return string(s.State) + sep + string(s.WaitingOn) + sep + currentProgress(turn) + sep +
		activityDigest(turn.Steps)
}

// currentProgress is the short human-readable description of the work in
// flight: what Claude last said about it, falling back to the action it is
// taking. Reasoning is never a source - only what Claude said out loud.
func currentProgress(turn *transcript.Turn) string {
	if p := trimProse(turn.Progress); p != "" {
		return p
	}
	return describeActivity(turn.LatestTool, "")
}

// trimProse reduces Claude's prose to the first few real lines: headings,
// bullets, and blank runs are stripped so the card reads as a sentence
// about the work rather than as a fragment of a report.
func trimProse(s string) string {
	var kept []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "```") {
			continue
		}
		kept = append(kept, stripBullet(line))
		if len(kept) == 3 {
			break
		}
	}
	return truncateRunes(strings.Join(kept, " "), progressCap)
}

// Liveness thresholds. Elapsed time alone is not enough: "Working · 8m,
// activity just now" and "Working · 8m, no new activity for 3m" are very
// different states, and the note is what tells them apart.
const (
	// justNow is how recent activity reads as "just now".
	justNow = 45 * time.Second
	// quietAfter is when a lack of activity is called out as such. It is
	// an honest observation, not an alarm: the card never fakes activity
	// merely because its timer keeps increasing.
	quietAfter = 2 * time.Minute
)

// activityNote says how recently meaningful activity was observed.
func activityNote(at time.Time) string {
	if at.IsZero() {
		return ""
	}
	since := time.Since(at)
	switch {
	case since < justNow:
		return "Activity just now"
	case since < quietAfter:
		return "Activity " + formatDuration(since) + " ago"
	}
	return "No new activity for " + formatDuration(since)
}

// joinNotes combines footer notes, skipping empty ones.
func joinNotes(notes ...string) string {
	kept := notes[:0]
	for _, n := range notes {
		if n != "" {
			kept = append(kept, n)
		}
	}
	return strings.Join(kept, " · ")
}
