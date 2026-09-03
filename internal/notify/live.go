package notify

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/marcelritzschke/claude-code-feishu-companion/internal/pathdisp"
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

// activityItems is how many recent actions a live card lists. The spec's
// "three to five meaningful items" with the low end preferred: the card
// answers what Claude is broadly doing, not what it has emitted.
const activityItems = 4

// LiveCard is the one card a watched session updates in place: what Claude
// is broadly doing right now, and the few actions behind that.
//
// updated is when the card's content last actually changed, which is what
// the "Updated ..." note reports. A card that has not changed is not
// rewritten, so the note is the user's assurance that they are not looking
// at a frozen view.
func LiveCard(s session.Session, turn *transcript.Turn, updated time.Time) (string, error) {
	template, title := "green", "🟢 Claude is working"
	if s.State == session.Waiting {
		template, title = "orange", "⚠️ Claude needs you"
	}

	bodies := []string{"**Current progress**\n" + currentProgress(turn)}
	if lines := activityLines(turn.Steps); len(lines) > 0 {
		bodies = append(bodies, "**Recent activity**\n"+strings.Join(lines, "\n"))
	}

	buttons := []Button{{
		Label:  "Stop watching",
		Action: Action{Kind: ActionUnwatch, Session: s.ID},
	}}
	return card(template, title, liveContext(s, turn), bodies, buttons, updatedNote(updated))
}

// SettledWatchCard is what a watched card becomes when there is nothing
// live left to show: the turn's outcome, and the way back into the session.
//
// It is the fallback settle. A turn that ends while Claude Companion is watching
// normally settles into the ordinary completion or failure notification,
// which is the same card the user already knows from V1; this one covers
// watching a session that is between turns, and watches that end for a
// reason of their own.
func SettledWatchCard(s session.Session, turn *transcript.Turn, note string) (string, error) {
	template, title := "green", "✅ Claude finished"
	if turn.Failed {
		template, title = "red", "❌ Claude couldn't finish"
	}

	summary, _ := splitFinal(turn.Progress)
	if summary == "" {
		summary = accomplishment(turn)
	}
	bodies := []string{summary}
	if v := validationLines(turn.Tests); len(v) > 0 {
		bodies = append(bodies, "**Validation**\n"+strings.Join(v, "\n"))
	}

	var buttons []Button
	if s.Remote.Continuable() {
		buttons = append(buttons, Button{
			Label:  "Continue session",
			Style:  stylePrimary,
			Action: Action{Kind: ActionSelect, Session: s.ID},
		})
	}
	return card(template, title, liveContext(s, turn), bodies, buttons, note)
}

// WatchStoppedCard leaves a watched card at rest while its turn is still
// running. It deliberately does not read as an outcome: the work has not
// finished, and the ordinary completion notification is still to come.
func WatchStoppedCard(s session.Session, turn *transcript.Turn, note string) (string, error) {
	bodies := []string{"**Where it got to**\n" + currentProgress(turn)}
	var buttons []Button
	if s.Remote.Continuable() {
		buttons = append(buttons, Button{
			Label:  "Continue session",
			Style:  stylePrimary,
			Action: Action{Kind: ActionSelect, Session: s.ID},
		})
	}
	if note == "" {
		note = "Claude is still working. Claude Companion will tell you when it finishes."
	}
	return card("grey", "⏸️ Stopped watching", liveContext(s, turn), bodies, buttons, note)
}

// LiveSignature is everything on a live card that is worth rewriting the
// card for: what Claude is doing and the activity behind it, without the
// clock. Two renders with the same signature say the same thing, so the
// card is left alone rather than rewritten every few seconds.
func LiveSignature(s session.Session, turn *transcript.Turn) string {
	const sep = "\x1f"
	return string(s.State) + sep + currentProgress(turn) + sep +
		strings.Join(activityLines(turn.Steps), sep)
}

// liveContext identifies the watched session and how long its turn has run.
func liveContext(s session.Session, turn *transcript.Turn) string {
	ctx := s.Describe()
	if turn == nil || turn.Start.IsZero() {
		return ctx
	}
	return ctx + " · " + formatDuration(time.Since(turn.Start))
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

// updatedNote says how fresh the card is, which is the difference between a
// quiet view and a stale one.
func updatedNote(updated time.Time) string {
	if updated.IsZero() || time.Since(updated) < 45*time.Second {
		return "Updated just now"
	}
	return "Updated " + formatDuration(time.Since(updated)) + " ago"
}

// group is a run of consecutive actions of one kind, which is how a live
// card stays short: four reads in a row are one line, not four.
//
// Only unremarkable actions are collapsed. Whatever is running now, and
// whatever went wrong, keeps its own line - those are the two things the
// user opened the card to see.
type group struct {
	act
	count   int
	running bool
	errored bool
	detail  string
}

// activityLines renders the turn's recent activity as the few marked items
// a live card shows. The list describes progress; it does not enumerate
// tool calls.
func activityLines(steps []transcript.Step) []string {
	var groups []group
	for _, st := range steps {
		a, ok := activityOf(st)
		if !ok {
			continue
		}
		plain := st.Done && !st.Errored
		if n := len(groups); plain && n > 0 && groups[n-1].done == a.done &&
			!groups[n-1].running && !groups[n-1].errored {
			groups[n-1].count++
			groups[n-1].subject = a.subject
			continue
		}
		g := group{act: a, count: 1, running: !st.Done, errored: st.Errored}
		if st.Errored {
			g.detail = st.Error
		}
		groups = append(groups, g)
	}
	if len(groups) > activityItems {
		groups = groups[len(groups)-activityItems:]
	}

	lines := make([]string, 0, len(groups))
	for _, g := range groups {
		lines = append(lines, g.render())
	}
	return lines
}

// render is one activity item: a mark saying how it went, what it was, and
// - only when something went wrong - a line saying what, and that Claude
// carried on. That last part is the point: a tool hitting a problem is not
// the task failing, and the user must never have to guess which they are
// looking at.
func (g group) render() string {
	switch {
	case g.errored:
		line := "⚠ " + g.text(g.done)
		if detail := truncateRunes(flatten(g.detail), detailCap); detail != "" {
			return line + "\n└ " + detail + " — Claude carried on."
		}
		return line + "\n└ Claude carried on."
	case g.running:
		return "◌ " + g.text(g.doing)
	}
	return "✓ " + g.text(g.done)
}

// text names what a group did, collapsing a run into a count.
func (g group) text(verb string) string {
	if g.count > 1 {
		return fmt.Sprintf("%s %d %s", verb, g.count, g.noun)
	}
	if g.subject == "" {
		return verb
	}
	return verb + " " + truncateRunes(g.subject, itemCap)
}

// act is how one tool call reads as an activity item: what it is called
// once it is over, what it is called while it runs, what it acted on, and
// the plural a run of them collapses to.
type act struct {
	done    string
	doing   string
	subject string
	noun    string
}

// activityOf describes one tool call as a live-card item.
//
// Not every call earns a line. Bookkeeping the user never asked to see -
// a todo list being rewritten - is left out entirely, which is the V3
// noise policy applied at its source.
func activityOf(st transcript.Step) (act, bool) {
	str := func(field string) string {
		v, _ := st.Input[field].(string)
		return v
	}
	switch st.Tool {
	case "Read":
		return act{"Read", "Reading", pathdisp.Base(str("file_path")), "files"}, true
	case "Edit", "Write":
		return act{"Updated", "Updating", pathdisp.Base(str("file_path")), "files"}, true
	case "NotebookEdit":
		return act{"Updated", "Updating", pathdisp.Base(str("notebook_path")), "files"}, true
	case "Bash":
		return act{"Ran", "Running", cleanCommand(flatten(str("command"))), "commands"}, true
	case "Grep", "Glob":
		return act{"Searched", "Searching", "for " + flatten(str("pattern")), "times"}, true
	case "WebSearch":
		return act{"Searched the web", "Searching the web", "for " + flatten(str("query")), "times"}, true
	case "WebFetch":
		return act{"Fetched", "Fetching", webHost(str("url")), "pages"}, true
	case "Task", "Agent":
		return act{"Delegated", "Delegating", flatten(str("description")), "subtasks"}, true
	case "ExitPlanMode":
		return act{"Proposed a plan", "Writing up a plan", "", "plans"}, true
	case "AskUserQuestion":
		return act{"Asked you a question", "Asking you a question", "", "questions"}, true
	case "TodoWrite":
		return act{}, false
	}
	return act{"Used", "Using", readableTool(st.Tool), "tools"}, true
}

// webHost shortens a fetched URL to its host, which is all the user needs
// to recognise it and all a phone line has room for.
func webHost(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return u.Host
	}
	return flatten(raw)
}
