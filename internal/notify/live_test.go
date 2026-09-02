package notify

import (
	"strings"
	"testing"
	"time"

	"github.com/marcelritzschke/wirelark/internal/session"
	"github.com/marcelritzschke/wirelark/internal/transcript"
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

func TestLiveCardAnswersWhatClaudeIsDoing(t *testing.T) {
	turn := &transcript.Turn{
		Start:    time.Now().Add(-6*time.Minute - 12*time.Second),
		Progress: "Found duplicate refresh validation.\nConsolidating the logic and checking the callers.",
		Steps: []transcript.Step{
			step("Read", map[string]any{"file_path": "/work/payments-api/auth/session.go"}, true, false, ""),
			step("Edit", map[string]any{"file_path": "/work/payments-api/auth/refresh.go"}, true, false, ""),
			step("Bash", map[string]any{"command": "go test ./..."}, false, false, ""),
		},
	}
	card, err := LiveCard(watched(), turn, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"🟢 Claude is working",
		"Fix token refresh · payments-api · 6m 12s",
		"Current progress",
		"Found duplicate refresh validation.",
		"Recent activity",
		"✓ Read session.go",
		"✓ Updated refresh.go",
		"◌ Running go test ./...",
		"Updated just now",
		"Stop watching",
	} {
		if !strings.Contains(card, want) {
			t.Errorf("live card is missing %q: %s", want, card)
		}
	}
}

func TestLiveCardKeepsRecentActivityShort(t *testing.T) {
	var steps []transcript.Step
	for _, name := range []string{"a", "b", "c", "d", "e", "f"} {
		steps = append(steps, step("Grep", map[string]any{"pattern": name}, true, false, ""))
		steps = append(steps, step("Bash", map[string]any{"command": "echo " + name}, true, false, ""))
	}
	lines := activityLines(steps)
	if len(lines) > 5 {
		t.Errorf("recent activity should stay to a handful of items, got %d: %v", len(lines), lines)
	}
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
	card, err := LiveCard(watched(), turn, time.Now())
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
	if strings.Contains(card, "❌") {
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

func TestLiveCardNeverShowsReasoning(t *testing.T) {
	// Progress comes from what Claude said out loud; a turn that has said
	// nothing yet falls back to the action, never to anything internal.
	turn := &transcript.Turn{
		Start:      time.Now().Add(-time.Minute),
		LatestTool: &transcript.ToolCall{Name: "Bash", Input: map[string]any{"command": "go test ./..."}},
	}
	card, err := LiveCard(watched(), turn, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(card, "Running go test ./...") {
		t.Errorf("live card = %s", card)
	}
}

func TestWaitingSessionLeadsWithAttention(t *testing.T) {
	s := watched()
	s.State = session.Waiting
	card, err := LiveCard(s, &transcript.Turn{Start: time.Now()}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(card, "⚠️ Claude needs you") {
		t.Errorf("a blocked session must say so first: %s", card)
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
		"✅ Claude finished",
		"Implemented token rotation and consolidated refresh validation.",
		"Validation",
		"✓ go test ./... passed",
		"Continue session",
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
	if strings.Contains(card, "Claude finished") {
		t.Errorf("the turn is still running; the card must not claim it finished: %s", card)
	}
	if !strings.Contains(card, "Stopped watching") {
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

func TestUpdatedNoteIsHonestAboutStaleness(t *testing.T) {
	if got := updatedNote(time.Now()); got != "Updated just now" {
		t.Errorf("updatedNote = %q", got)
	}
	if got := updatedNote(time.Now().Add(-5 * time.Minute)); got != "Updated 5m ago" {
		t.Errorf("updatedNote = %q", got)
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
