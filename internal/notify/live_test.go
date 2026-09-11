package notify

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/marcelritzschke/claude-code-feishu-companion/internal/session"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/transcript"
)

func liveSession() session.Session {
	return session.Session{
		ID: "s1", Dir: "/work/payments-api", Title: "Fix token refresh",
		State: session.Working, Remote: session.Ready, Transcript: "/tmp/t.jsonl",
	}
}

func step(tool string, input map[string]any, done, errored bool, errText string) transcript.Step {
	return transcript.Step{Tool: tool, Input: input, Done: done, Errored: errored, Error: errText}
}

func TestSessionCardAnswersWhatClaudeIsDoing(t *testing.T) {
	turn := &transcript.Turn{
		Start:    time.Now().Add(-6*time.Minute - 12*time.Second),
		Progress: "Found duplicate refresh validation.\nConsolidating the logic and checking the callers.",
		Steps: []transcript.Step{
			step("Read", map[string]any{"file_path": "/work/payments-api/auth/session.go"}, true, false, ""),
			step("Edit", map[string]any{"file_path": "/work/payments-api/auth/refresh.go"}, true, false, ""),
			step("Bash", map[string]any{"command": "go test ./..."}, false, false, ""),
		},
	}
	card, err := SessionCard(liveSession(), turn, SessionView{ActivityAt: time.Now(), Interruptible: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"🔵 Working · 6m 12s",
		"Fix token refresh · payments-api",
		"Current progress",
		"Found duplicate refresh validation.",
		"Activity",
		"✓ Read session.go",
		"✓ Updated refresh.go",
		"◌ Running go test ./...",
		"Activity just now",
		"Interrupt",
	} {
		if !strings.Contains(card, want) {
			t.Errorf("session card is missing %q: %s", want, card)
		}
	}
}

// activityLines is the lines the activity items read as, which is what
// most of these tests are about.
func activityLines(steps []transcript.Step) []string {
	items := activityItemsOf(steps)
	lines := make([]string, 0, len(items))
	for _, it := range items {
		lines = append(lines, it.line)
	}
	return lines
}

// The card carries the whole turn now, so the thing that has to hold is
// not a count of items but that the result still fits Feishu's element
// budget - the limit that, when exceeded, silently stops the card
// updating for the rest of the turn.
func TestLongTurnStaysWithinTheElementBudget(t *testing.T) {
	var steps []transcript.Step
	for i := range 500 {
		steps = append(steps, step("Read", map[string]any{"file_path": fmt.Sprintf("/x/f%d.go", i)}, true, false, ""))
		steps = append(steps, step("Bash", map[string]any{"command": fmt.Sprintf("go test ./pkg%d", i)}, true, true,
			"a failure long enough to be worth truncating, repeated for realism"))
		steps = append(steps, step("Grep", map[string]any{"pattern": fmt.Sprintf("p%d", i)}, true, false, ""))
	}
	turn := &transcript.Turn{Start: time.Now().Add(-3 * time.Hour), Title: "Long refactor",
		Progress: "Still going.", Steps: steps}

	card, err := SessionCard(liveSession(), turn, SessionView{ActivityAt: time.Now(), Interruptible: true})
	if err != nil {
		t.Fatal(err)
	}
	if n := countElements(t, card); n > elementBudget {
		t.Errorf("card carries %d elements, over the budget of %d", n, elementBudget)
	}
}

// Whatever does not fit is folded, never dropped: the card's job is to
// answer what happened while the user was away.
func TestOverflowIsFoldedRatherThanDiscarded(t *testing.T) {
	var steps []transcript.Step
	for i := range 400 {
		steps = append(steps, step("Bash", map[string]any{"command": fmt.Sprintf("cmd%d", i)}, true, true, "boom"))
	}
	turn := &transcript.Turn{Start: time.Now().Add(-time.Hour), Progress: "Working.", Steps: steps}
	card, err := SessionCard(liveSession(), turn, SessionView{ActivityAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(card, "earlier") {
		t.Errorf("a turn that overflows must say how much it folded away: %s", card[:400])
	}
	if n := countElements(t, card); n > elementBudget {
		t.Errorf("card carries %d elements, over the budget of %d", n, elementBudget)
	}
}

// countElements counts every element on a card the way Feishu does, which
// includes the ones nested inside a panel or a column.
func countElements(t *testing.T, cardJSON string) int {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(cardJSON), &m); err != nil {
		t.Fatalf("card is not valid JSON: %v", err)
	}
	var walk func(v any) int
	walk = func(v any) int {
		n := 0
		switch t := v.(type) {
		case map[string]any:
			if _, ok := t["tag"]; ok {
				n++
			}
			for _, inner := range t {
				n += walk(inner)
			}
		case []any:
			for _, inner := range t {
				n += walk(inner)
			}
		}
		return n
	}
	body, _ := m["body"].(map[string]any)
	return walk(body["elements"])
}

func TestConsecutiveActionsOfOneKindCollapse(t *testing.T) {
	steps := []transcript.Step{
		step("Read", map[string]any{"file_path": "/x/a.go"}, true, false, ""),
		step("Read", map[string]any{"file_path": "/x/b.go"}, true, false, ""),
		step("Read", map[string]any{"file_path": "/x/c.go"}, true, false, ""),
	}
	lines := activityLines(steps)
	if len(lines) != 1 || !strings.Contains(lines[0], "Read 3 files") {
		t.Errorf("three reads in a row should read as one item, got %v", lines)
	}
}

func TestRecoveredFailureIsDistinguishedFromTheTaskFailing(t *testing.T) {
	turn := &transcript.Turn{
		Start:    time.Now().Add(-time.Minute),
		Progress: "Investigating why the integration suite cannot start.",
		Steps: []transcript.Step{
			step("Bash", map[string]any{"command": "go test ./integration"}, true, true,
				"dial tcp 127.0.0.1:5432: connection refused"),
		},
	}
	card, err := SessionCard(liveSession(), turn, SessionView{ActivityAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(card, "⚠ Ran go test ./integration") {
		t.Errorf("a failed tool call should be marked as such: %s", card)
	}
	if !strings.Contains(card, "connection refused") {
		t.Errorf("the detail of a failed step is what makes it useful: %s", card)
	}
	if !strings.Contains(card, "Claude carried on.") {
		t.Errorf("a recovered failure must not read as the task failing: %s", card)
	}
	if strings.Contains(card, "Failed") {
		t.Errorf("the turn has not failed; the card must not say it has: %s", card)
	}
}

func TestBookkeepingNeverBecomesActivity(t *testing.T) {
	lines := activityLines([]transcript.Step{
		step("TodoWrite", map[string]any{}, true, false, ""),
	})
	if len(lines) != 0 {
		t.Errorf("todo updates are not activity the user asked to see: %v", lines)
	}
}

func TestSessionCardNeverShowsReasoning(t *testing.T) {
	// Progress comes from what Claude said out loud; a turn that has said
	// nothing yet falls back to the action, never to anything internal.
	turn := &transcript.Turn{
		Start:      time.Now().Add(-time.Minute),
		LatestTool: &transcript.ToolCall{Name: "Bash", Input: map[string]any{"command": "go test ./..."}},
	}
	card, err := SessionCard(liveSession(), turn, SessionView{ActivityAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(card, "Running go test ./...") {
		t.Errorf("session card = %s", card)
	}
}

func TestWaitingSessionLeadsWithAttention(t *testing.T) {
	s := liveSession()
	s.State = session.Waiting
	s.WaitingOn = session.WaitPermission
	card, err := SessionCard(s, &transcript.Turn{Start: time.Now()}, SessionView{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(card, "🟠 Waiting for permission") {
		t.Errorf("a blocked session must say so first: %s", card)
	}
	if !strings.Contains(card, "Claude needs approval before continuing.") {
		t.Errorf("waiting card = %s", card)
	}
	if strings.Contains(card, "Interrupt") {
		t.Errorf("the waiting card is state, not action: %s", card)
	}

	s.WaitingOn = session.WaitAnswer
	card, err = SessionCard(s, &transcript.Turn{Start: time.Now()}, SessionView{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(card, "🟠 Waiting for answer") {
		t.Errorf("a question and a permission are different waits: %s", card)
	}
}

func TestNotificationOnlySessionCardIsHonest(t *testing.T) {
	s := liveSession()
	s.Remote = session.Notifications
	card, err := SessionCard(s, &transcript.Turn{Start: time.Now(), Progress: "Improving the README."},
		SessionView{ActivityAt: time.Now().Add(-12 * time.Second), Interruptible: false})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"⚪ Working · Notifications only",
		"You cannot reply to this session from here",
		"--dangerously-load-development-channels",
	} {
		if !strings.Contains(card, want) {
			t.Errorf("notification-only card is missing %q: %s", want, card)
		}
	}
	if strings.Contains(card, "Interrupt") {
		t.Errorf("a session that cannot be controlled must not offer control: %s", card)
	}
	if strings.Contains(card, "Message this session") {
		t.Errorf("a session that cannot hear the user must not offer a reply box: %s", card)
	}
}

// A reply box that swallowed messages would be worse than no box, so a
// session that cannot be reached gets none - on every card it can appear
// on, not only the working one. And because an absence explains nothing,
// each of them says why in a sentence.
func TestNoReplyBoxWithoutAWordAboutWhy(t *testing.T) {
	s := liveSession()
	s.Remote = session.Notifications
	turn := &transcript.Turn{Start: time.Now().Add(-time.Minute), Progress: "Improving the README."}
	view := SessionView{ActivityAt: time.Now()}

	cards := map[string]func() (string, error){
		"working":  func() (string, error) { return SessionCard(s, turn, view) },
		"waiting":  func() (string, error) { s := s; s.State = session.Waiting; return SessionCard(s, turn, view) },
		"settled":  func() (string, error) { return SettledSessionCard(s, turn, "") },
		"paused":   func() (string, error) { return PausedSessionCard(s, turn, "") },
		"resting":  func() (string, error) { return RestingSessionCard(s, turn) },
		"no turns": func() (string, error) { return RestingSessionCard(s, &transcript.Turn{}) },
	}
	for name, build := range cards {
		card, err := build()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if strings.Contains(card, "Message this session") {
			t.Errorf("%s card offers a reply box a message would vanish into: %s", name, card)
		}
		if !strings.Contains(card, "You cannot reply to this session from here") {
			t.Errorf("%s card does not say why it has no reply box: %s", name, card)
		}
	}
}

// A session Claude Companion could not check keeps its box - it may well
// work - but says so, rather than letting the user find out by silence.
func TestUnconfirmedSessionSaysItIsUnconfirmed(t *testing.T) {
	s := liveSession()
	s.Remote = session.Unconfirmed
	card, err := SessionCard(s, &transcript.Turn{Start: time.Now(), Progress: "Improving the README."},
		SessionView{ActivityAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(card, "Message this session") {
		t.Errorf("an unconfirmed session may well take messages: %s", card)
	}
	if !strings.Contains(card, "could not check whether this session takes messages") {
		t.Errorf("card does not say the reply may not arrive: %s", card)
	}
}

// The recap's card for a session between turns: the last outcome when
// there was one, and an honest nothing when there was not.
func TestRestingCardShowsTheLastTurnOrNothing(t *testing.T) {
	turn := &transcript.Turn{
		Start:    time.Now().Add(-9 * time.Minute),
		Progress: "Implemented token rotation.",
	}
	card, err := RestingSessionCard(liveSession(), turn)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(card, "Implemented token rotation.") {
		t.Errorf("resting card does not show the last turn: %s", card)
	}

	card, err = RestingSessionCard(liveSession(), &transcript.Turn{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(card, "⚪ Idle") || !strings.Contains(card, "Nothing is running in this session.") {
		t.Errorf("a session with no turn to show = %s", card)
	}
	if strings.Contains(card, "Completed") {
		t.Errorf("a session that has run nothing must not claim a completed turn: %s", card)
	}
}

func TestInterruptedCardPreservesTheSession(t *testing.T) {
	turn := &transcript.Turn{Start: time.Now().Add(-3 * time.Minute), Progress: "Halfway through the callers."}
	card, err := InterruptedSessionCard(liveSession(), turn)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"⏹️ Interrupted",
		"back at its prompt",
		"Message this session",
	} {
		if !strings.Contains(card, want) {
			t.Errorf("interrupted card is missing %q: %s", want, card)
		}
	}
	if strings.Contains(card, "Failed") || strings.Contains(card, "Completed") {
		t.Errorf("an interrupt is neither an outcome nor a failure: %s", card)
	}
}

func TestSettledSessionCardIsAnOutcomeAndAWayBack(t *testing.T) {
	turn := &transcript.Turn{
		Start:    time.Now().Add(-8*time.Minute - 41*time.Second),
		Progress: "Implemented token rotation and consolidated refresh validation. The rest is detail.",
		Tests:    []transcript.TestRun{{Command: "go test ./...", Passed: true}},
	}
	card, err := SettledSessionCard(liveSession(), turn, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"✅ Completed",
		"Implemented token rotation and consolidated refresh validation.",
		"Validation",
		"✓ go test ./... passed",
		"Message this session",
	} {
		if !strings.Contains(card, want) {
			t.Errorf("settled card is missing %q: %s", want, card)
		}
	}
	if strings.Contains(card, "Interrupt") {
		t.Errorf("a settled card must not still offer to stop the turn: %s", card)
	}
}

func TestPausedCardDoesNotReadAsAnOutcome(t *testing.T) {
	turn := &transcript.Turn{Start: time.Now().Add(-time.Minute), Progress: "Still working through the callers."}
	card, err := PausedSessionCard(liveSession(), turn, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(card, "Claude finished") || strings.Contains(card, "Completed") {
		t.Errorf("the turn is still running; the card must not claim it finished: %s", card)
	}
	if !strings.Contains(card, "No longer live") {
		t.Errorf("paused card = %s", card)
	}
}

func TestLiveSignatureIgnoresTheClock(t *testing.T) {
	s := liveSession()
	turn := &transcript.Turn{Start: time.Now().Add(-time.Minute), Progress: "Consolidating."}
	first := LiveSignature(s, turn)

	turn.Start = time.Now().Add(-time.Hour) // only time passed
	if LiveSignature(s, turn) != first {
		t.Error("time passing is not a reason to rewrite the card")
	}
	turn.Steps = append(turn.Steps, step("Bash", map[string]any{"command": "go test ./..."}, false, false, ""))
	if LiveSignature(s, turn) == first {
		t.Error("new activity is a reason to rewrite the card")
	}
}

func TestActivityNoteIsHonestAboutStaleness(t *testing.T) {
	if got := activityNote(time.Now()); got != "Activity just now" {
		t.Errorf("activityNote = %q", got)
	}
	if got := activityNote(time.Now().Add(-time.Minute)); got != "Activity 1m ago" {
		t.Errorf("activityNote = %q", got)
	}
	if got := activityNote(time.Now().Add(-5 * time.Minute)); got != "No new activity for 5m" {
		t.Errorf("activityNote = %q", got)
	}
	if got := activityNote(time.Time{}); got != "" {
		t.Errorf("nothing observed means nothing claimed, got %q", got)
	}
}

// Collapsing is for the unremarkable. What is running now, and what went
// wrong, are the two things the user opened the card to see.
func TestRunningAndFailedActionsKeepTheirOwnLine(t *testing.T) {
	lines := activityLines([]transcript.Step{
		step("Bash", map[string]any{"command": "go build ./..."}, true, false, ""),
		step("Bash", map[string]any{"command": "go test ./integration"}, true, true, "connection refused"),
		step("Bash", map[string]any{"command": "go test ./..."}, false, false, ""),
	})
	if len(lines) != 3 {
		t.Fatalf("lines = %v, want each kept apart", lines)
	}
	if !strings.HasPrefix(lines[0], "✓ Ran go build") {
		t.Errorf("lines[0] = %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "⚠ Ran go test ./integration") {
		t.Errorf("lines[1] = %q", lines[1])
	}
	if lines[2] != "◌ Running go test ./..." {
		t.Errorf("lines[2] = %q", lines[2])
	}
}

// [ Interrupt ] sits on the card the user reads to check on their work,
// one tap away from every scroll. It must ask before it acts.
func TestInterruptAsksBeforeItStopsTheTurn(t *testing.T) {
	turn := &transcript.Turn{Start: time.Now().Add(-time.Minute), Progress: "Running the tests."}
	card, err := SessionCard(liveSession(), turn, SessionView{ActivityAt: time.Now(), Interruptible: true})
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Body struct {
			Elements []struct {
				Tag     string `json:"tag"`
				Confirm *struct {
					Title struct{ Content string } `json:"title"`
					Text  struct{ Content string } `json:"text"`
				} `json:"confirm"`
			} `json:"elements"`
		} `json:"body"`
	}
	if err := json.Unmarshal([]byte(card), &m); err != nil {
		t.Fatal(err)
	}
	for _, el := range m.Body.Elements {
		if el.Tag != "button" {
			continue
		}
		if el.Confirm == nil {
			t.Fatalf("[ Interrupt ] acts on the first tap: %s", card)
		}
		// The dialog has to say what does not happen too, or a user who
		// reads it carefully still cannot tell what they are agreeing to.
		if !strings.Contains(el.Confirm.Text.Content, "back to its prompt") {
			t.Errorf("confirmation = %q, want it to say the session survives", el.Confirm.Text.Content)
		}
		return
	}
	t.Fatalf("the working card offers no [ Interrupt ] to guard: %s", card)
}

// Between a message being sent from a card and Claude taking it up, the
// transcript still describes the turn the user was replying to. The card
// says what it knows rather than passing that turn off as the new one.
func TestSentCardStandsInForTheTurnItStarts(t *testing.T) {
	// A finished turn, six minutes old: exactly what would be shown as
	// "Working · 6m" if the card took the transcript at face value.
	turn := &transcript.Turn{
		Start:    time.Now().Add(-6 * time.Minute),
		Progress: "Added refresh-token rotation.",
		Steps:    []transcript.Step{step("Edit", map[string]any{"file_path": "/x/refresh.go"}, true, false, "")},
	}
	card, err := SessionCard(liveSession(), turn, SessionView{ActivityAt: time.Now(), Sent: "lgtm, ship it"})
	if err != nil {
		t.Fatal(err)
	}
	if got := headerTitle(t, card); got != "🔵 Sent" {
		t.Errorf("title = %q, want the state the session is actually in", got)
	}
	for _, want := range []string{"Your message is with Claude.", "lgtm, ship it", string(ActionSay)} {
		if !strings.Contains(card, want) {
			t.Errorf("sent card is missing %q: %s", want, card)
		}
	}
	for _, unwanted := range []string{"Added refresh-token rotation.", "6m"} {
		if strings.Contains(card, unwanted) {
			t.Errorf("the sent card shows the previous turn's %q: %s", unwanted, card)
		}
	}
}

func headerTitle(t *testing.T, cardJSON string) string {
	t.Helper()
	var m struct {
		Header struct {
			Title struct {
				Content string `json:"content"`
			} `json:"title"`
		} `json:"header"`
	}
	if err := json.Unmarshal([]byte(cardJSON), &m); err != nil {
		t.Fatal(err)
	}
	return m.Header.Title.Content
}
