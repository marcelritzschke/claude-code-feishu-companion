package notify

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/marcelritzschke/claude-code-feishu-companion/internal/pathdisp"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/transcript"
)

// A session card carries the whole turn, not the last few things that
// happened. It can do that because schema 2.0 folds: a step the user has
// no reason to look at is one line, and a step they might is a panel that
// stays shut until they open it.
//
// What bounds the card is Feishu's element budget, not a count of steps
// chosen in advance. Plain steps cost nothing extra - consecutive ones
// share a single markdown element - so the cost of a turn is driven by how
// much went wrong in it, which is the right thing for it to be driven by.

const (
	// activityLineCap bounds how many activity lines a card shows before
	// the oldest are folded away. The element budget alone would allow far
	// more, since plain lines share one element; this keeps the card from
	// becoming a very long single element instead of a very long card.
	activityLineCap = 200
	// foldLineCap bounds the lines listed inside the fold. Past this the
	// oldest give way there too: the fold is a record that the work
	// happened, not an archive of it.
	foldLineCap = 40
)

// activitySections renders a turn's steps as card sections that fit the
// given element budget.
//
// Plain steps become lines of prose; whatever is running now and whatever
// went wrong become panels, because those are the two things a user opens
// a card to look into. When the result does not fit, the oldest steps fold
// into a single shut panel rather than disappearing - the card's job is to
// answer "what happened while I was away", which it cannot do if it has
// quietly dropped the first hour.
func activitySections(steps []transcript.Step, budget int) Section {
	items := activityItemsOf(steps)
	if len(items) == 0 {
		return nil
	}

	kept, folded := fitActivity(items, budget)
	var out []Section
	if len(folded) > 0 {
		out = append(out, foldPanel(folded))
	}
	out = append(out, layout(kept)...)
	if len(out) == 0 {
		return nil
	}
	return Block{Sections: out}
}

// activityItem is one entry on the card: a group of steps that reads as a
// single action, and how it ended.
type activityItem struct {
	// line is what the item says when it is only a line.
	line string
	// panel is set when the item deserves one, and nil otherwise.
	panel *Panel
}

func (i activityItem) isPanel() bool { return i.panel != nil }

// fitActivity keeps the newest items that fit the budget and returns the
// rest as the fold. It walks backwards because the newest activity is what
// a live card is for; what gets dropped is always the oldest.
func fitActivity(items []activityItem, budget int) (kept, folded []activityItem) {
	// The fold, if there is one, is a panel of its own and has to be paid
	// for out of the same budget.
	const foldCost = panelCost
	spend, runOpen := 0, false
	first := len(items)
	for i := len(items) - 1; i >= 0; i-- {
		cost := 1
		switch {
		case items[i].isPanel():
			cost = panelCost
		case runOpen:
			cost = 0 // merges into the run of lines already below it
		}
		room := budget - spend - cost
		if i > 0 {
			room -= foldCost // everything older still has to be announced
		}
		if room < 0 || len(items)-i > activityLineCap {
			break
		}
		spend += cost
		runOpen = !items[i].isPanel()
		first = i
	}
	return items[first:], items[:first]
}

// layout turns items into sections, merging consecutive lines into one
// prose section so a long quiet stretch costs one element rather than one
// per step.
func layout(items []activityItem) []Section {
	var out []Section
	var run []string
	flush := func() {
		if len(run) > 0 {
			out = append(out, Prose("**Activity**\n"+strings.Join(run, "\n")))
			run = nil
		}
	}
	for _, it := range items {
		if it.isPanel() {
			flush()
			out = append(out, *it.panel)
			continue
		}
		run = append(run, it.line)
	}
	flush()
	return out
}

// foldPanel is the record of everything older than the card has room for:
// shut, counted, and listing as many of the most recent of them as it can.
func foldPanel(folded []activityItem) Panel {
	lines := make([]string, 0, len(folded))
	for _, it := range folded {
		lines = append(lines, it.line)
	}
	if len(lines) > foldLineCap {
		lines = lines[len(lines)-foldLineCap:]
	}
	body := strings.Join(lines, "\n")
	if len(folded) > len(lines) {
		body = fmt.Sprintf("_The first %d are not listed._\n", len(folded)-len(lines)) + body
	}
	return Panel{
		Title:  fmt.Sprintf("☕ **%d %s earlier**", len(folded), plural(len(folded), "step", "steps")),
		Body:   body,
		Border: "blue",
	}
}

// activityItemsOf groups a turn's steps and renders each group.
//
// Consecutive unremarkable steps of one kind collapse into a single item -
// four reads in a row are one line, not four. Whatever is running now, and
// whatever went wrong, always keeps its own item: those are the two things
// the user opened the card to see.
func activityItemsOf(steps []transcript.Step) []activityItem {
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
		g.input = st.Input
		g.tool = st.Tool
		groups = append(groups, g)
	}

	items := make([]activityItem, 0, len(groups))
	for _, g := range groups {
		items = append(items, g.item())
	}
	return items
}

// group is a run of consecutive actions of one kind.
type group struct {
	act
	tool    string
	input   map[string]any
	count   int
	running bool
	errored bool
	detail  string
}

// item renders one group: a line for the ordinary case, a panel for the
// step running now and for anything that went wrong. A panel is only worth
// its three elements when there is something inside worth opening.
func (g group) item() activityItem {
	switch {
	case g.errored:
		return activityItem{
			line: "⚠ " + g.text(g.done),
			panel: &Panel{
				Title:  "⚠ " + g.text(g.done),
				Body:   g.errorBody(),
				Border: "red",
			},
		}
	case g.running:
		return activityItem{
			line: "◌ " + g.text(g.doing),
			panel: &Panel{
				Title:    "◌ " + g.text(g.doing),
				Body:     inputDetail(g.tool, g.input),
				Expanded: true,
			},
		}
	}
	return activityItem{line: "✓ " + g.text(g.done)}
}

// errorBody is what an errored step reveals when opened: what it was
// asked to do, what came back, and that the turn went on regardless. That
// last part is the point - a tool hitting a problem is not the task
// failing, and the user must never have to guess which they are reading.
func (g group) errorBody() string {
	parts := make([]string, 0, 3)
	if detail := inputDetail(g.tool, g.input); detail != "" {
		parts = append(parts, detail)
	}
	if detail := truncateRunes(flatten(g.detail), panelErrorCap); detail != "" {
		parts = append(parts, "**Error**\n"+detail)
	}
	return strings.Join(append(parts, "_Claude carried on._"), "\n\n")
}

// panelErrorCap bounds the error text on a panel. It is more generous
// than the single line a card used to allow, because a panel is opened
// deliberately.
const panelErrorCap = 400

// inputCap bounds one field of a step's input.
const inputCap = 300

// inputDetail is what a step shows when it is opened: exactly what Claude
// asked the tool to do.
//
// Input only, never output. Input is bounded and is the question a user
// actually has when they open a step - what did it run? - while output is
// unbounded and is where a shell command's secrets would end up on a card
// in a chat.
func inputDetail(tool string, input map[string]any) string {
	str := func(field string) string {
		v, _ := input[field].(string)
		return v
	}
	switch tool {
	case "Bash":
		if cmd := str("command"); cmd != "" {
			return "**Command**\n`" + truncateRunes(flatten(cmd), inputCap) + "`"
		}
	case "Read", "Edit", "Write":
		if p := str("file_path"); p != "" {
			return "**File**\n`" + pathdisp.Home(p) + "`"
		}
	case "NotebookEdit":
		if p := str("notebook_path"); p != "" {
			return "**File**\n`" + pathdisp.Home(p) + "`"
		}
	case "Grep", "Glob":
		lines := make([]string, 0, 2)
		if p := str("pattern"); p != "" {
			lines = append(lines, "**Pattern**\n`"+truncateRunes(flatten(p), inputCap)+"`")
		}
		if p := str("path"); p != "" {
			lines = append(lines, "**In**\n`"+pathdisp.Home(p)+"`")
		}
		return strings.Join(lines, "\n")
	case "WebFetch":
		if u := str("url"); u != "" {
			return "**URL**\n" + truncateRunes(u, inputCap)
		}
	case "WebSearch":
		if q := str("query"); q != "" {
			return "**Query**\n" + truncateRunes(flatten(q), inputCap)
		}
	case "Task", "Agent":
		if d := str("description"); d != "" {
			return truncateRunes(flatten(d), inputCap)
		}
	}
	return ""
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
// a todo list being rewritten - is left out entirely, which is the
// live-companion noise policy applied at its source.
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

// activityDigest is everything the activity list says, without the clock:
// what a live card compares to decide whether it has anything new to say.
func activityDigest(steps []transcript.Step) string {
	const sep = "\x1f"
	items := activityItemsOf(steps)
	parts := make([]string, 0, len(items))
	for _, it := range items {
		parts = append(parts, it.line)
	}
	return strings.Join(parts, sep)
}

// historyLineCap bounds the lines a settled card's history panel lists.
const historyLineCap = 60

// historyPanel is a finished turn's activity, folded into one shut panel.
//
// A card that has settled is a notification, not a live view: it says what
// happened, and the steps behind that belong out of the way. But they do
// belong on it - "what happened while I was away" is half of what this
// product is for, and a card that discarded the history the moment the
// turn ended could not answer it.
func historyPanel(steps []transcript.Step) Section {
	items := activityItemsOf(steps)
	if len(items) == 0 {
		return nil
	}
	lines := make([]string, 0, len(items))
	for _, it := range items {
		lines = append(lines, it.line)
	}
	dropped := 0
	if len(lines) > historyLineCap {
		dropped = len(lines) - historyLineCap
		lines = lines[dropped:]
	}
	body := strings.Join(lines, "\n")
	if dropped > 0 {
		body = fmt.Sprintf("_The first %d are not listed._\n", dropped) + body
	}
	return Panel{
		Title: fmt.Sprintf("☕ **%d %s**", len(items), plural(len(items), "step", "steps")),
		Body:  body,
	}
}

// withHistory adds a settled card's history panel when there is one, and
// leaves the card alone when the turn did nothing worth recording.
func withHistory(sections []Section, turn *transcript.Turn) []Section {
	if turn == nil {
		return sections
	}
	if h := historyPanel(turn.Steps); h != nil {
		return append(sections, h)
	}
	return sections
}

// proseOf turns a card's ordinary string bodies into sections.
func proseOf(bodies []string) []Section {
	out := make([]Section, 0, len(bodies))
	for _, b := range bodies {
		if b != "" {
			out = append(out, Prose(b))
		}
	}
	return out
}
