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

func watched() session.Session {
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
	card, err := SessionCard(watched(), turn, SessionView{ActivityAt: time.Now(), Interruptible: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"🟢 Working · 6m 12s",
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

	card, err := SessionCard(watched(), turn, SessionView{ActivityAt: time.Now(), Interruptible: true})
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
	card, err := SessionCard(watched(), turn, SessionView{ActivityAt: time.Now()})
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
	card, err := SessionCard(watched(), turn, SessionView{ActivityAt: time.Now()})
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
	card, err := SessionCard(watched(), turn, SessionView{ActivityAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(card, "Running go test ./...") {
		t.Errorf("session card = %s", card)
	}
}

func TestWaitingSessionLeadsWithAttention(t *testing.T) {
	s := watched()
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
	s := watched()
	s.Remote = session.Notifications
	card, err := SessionCard(s, &transcript.Turn{Start: time.Now(), Progress: "Improving the README."},
		SessionView{ActivityAt: time.Now().Add(-12 * time.Second), Interruptible: false})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"⚪ Working · Notifications only",
		"Notifications only",
	} {
		if !strings.Contains(card, want) {
			t.Errorf("notification-only card is missing %q: %s", want, card)
		}
	}
	if strings.Contains(card, "Interrupt") {
		t.Errorf("a session that cannot be controlled must not offer control: %s", card)
	}
}

func TestInterruptedCardPreservesTheSession(t *testing.T) {
	turn := &transcript.Turn{Start: time.Now().Add(-3 * time.Minute), Progress: "Halfway through the callers."}
	card, err := InterruptedSessionCard(watched(), turn)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"⏹️ Interrupted",
		"back at its prompt",
		"Continue",
	} {
		if !strings.Contains(card, want) {
			t.Errorf("interrupted card is missing %q: %s", want, card)
		}
	}
	if strings.Contains(card, "Failed") || strings.Contains(card, "Completed") {
		t.Errorf("an interrupt is neither an outcome nor a failure: %s", card)
	}
}

func TestSettledWatchCardIsAnOutcomeAndAWayBack(t *testing.T) {
	turn := &transcript.Turn{
		Start:    time.Now().Add(-8*time.Minute - 41*time.Second),
		Progress: "Implemented token rotation and consolidated refresh validation. The rest is detail.",
		Tests:    []transcript.TestRun{{Command: "go test ./...", Passed: true}},
	}
	card, err := SettledWatchCard(watched(), turn, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"✅ Completed",
		"Implemented token rotation and consolidated refresh validation.",
		"Validation",
		"✓ go test ./... passed",
		"Continue",
	} {
		if !strings.Contains(card, want) {
			t.Errorf("settled card is missing %q: %s", want, card)
		}
	}
	if strings.Contains(card, "Stop watching") {
		t.Errorf("a settled card must not still offer to stop watching: %s", card)
	}
}

func TestStoppedWatchDoesNotReadAsAnOutcome(t *testing.T) {
	turn := &transcript.Turn{Start: time.Now().Add(-time.Minute), Progress: "Still working through the callers."}
	card, err := WatchStoppedCard(watched(), turn, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(card, "Claude finished") || strings.Contains(card, "Completed") {
		t.Errorf("the turn is still running; the card must not claim it finished: %s", card)
	}
	if !strings.Contains(card, "No longer live") {
		t.Errorf("stopped card = %s", card)
	}
}

func TestLiveSignatureIgnoresTheClock(t *testing.T) {
	s := watched()
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
