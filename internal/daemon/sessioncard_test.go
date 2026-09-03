package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"strings"
	"testing"

	"github.com/marcelritzschke/claude-code-feishu-companion/internal/feishu"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/hook"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/mcp"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/notify"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/session"
)

// The session card opens itself at the first sign of real work: nobody has
// to ask to see that Claude is working.
func TestWorkOpensTheSessionCardAutomatically(t *testing.T) {
	d, rec, _ := fixture(t, session.Ready)
	path := watchable(t, d)
	defer d.closeWatch(context.Background(), "sess-1", "")

	hookEvent(t, d, hook.EventPostToolUse, map[string]any{
		"transcript_path": path,
		"tool_name":       "Read",
	})

	if !d.watching("sess-1") {
		t.Fatal("a working session should carry its own live card")
	}
	if titles := rec.titles(t); len(titles) != 1 || !strings.HasPrefix(titles[0], "🟢 Working") {
		t.Fatalf("cards = %v, want the session card and nothing else", titles)
	}
	// Windows cannot deliver an interrupt to another console's process, so
	// interrupt_windows.go turns Interruptible() off there rather than
	// offering a control that would not work: see
	// TestNotificationOnlySessionCardOffersNoControl for the same honesty
	// principle applied to a session Claude Companion cannot reach at all.
	if runtime.GOOS == "windows" {
		if strings.Contains(rec.cards[0], "Interrupt") {
			t.Errorf("Windows cannot offer Interrupt: %s", rec.cards[0])
		}
	} else if !strings.Contains(rec.cards[0], "Interrupt") {
		t.Errorf("a controllable working session offers [ Interrupt ]: %s", rec.cards[0])
	}
}

// A prompt alone is not work. A purely conversational turn earns no card,
// which is what keeps the experience quiet by default.
func TestAPromptAloneOpensNoSessionCard(t *testing.T) {
	d, rec, _ := fixture(t, session.Ready)
	path := watchable(t, d)

	hookEvent(t, d, hook.EventUserPromptSubmit, map[string]any{"transcript_path": path})

	if d.watching("sess-1") {
		t.Error("a conversational turn must not open a card")
	}
	if len(rec.cards) != 0 {
		t.Errorf("cards = %v, want none", rec.titles(t))
	}
}

// A session that cannot be controlled must never offer control.
func TestNotificationOnlySessionCardOffersNoControl(t *testing.T) {
	d, rec, _ := fixture(t, session.Notifications)
	path := watchable(t, d)
	defer d.closeWatch(context.Background(), "sess-1", "")

	hookEvent(t, d, hook.EventPostToolUse, map[string]any{
		"transcript_path": path,
		"tool_name":       "Read",
	})

	if titles := rec.titles(t); len(titles) != 1 || !strings.HasPrefix(titles[0], "⚪ Working · Notifications only") {
		t.Fatalf("cards = %v, want the honest notifications-only card", titles)
	}
	if strings.Contains(rec.cards[0], "Interrupt") {
		t.Errorf("an uncontrollable session must not offer Interrupt: %s", rec.cards[0])
	}
}

// Session card = state; permission card = action. A permission request
// flips the standing session card to waiting immediately.
func TestPermissionRequestTurnsTheSessionCardToWaiting(t *testing.T) {
	d, rec, _ := fixture(t, session.Ready)
	path := watchable(t, d)
	defer d.closeWatch(context.Background(), "sess-1", "")

	hookEvent(t, d, hook.EventPostToolUse, map[string]any{
		"transcript_path": path,
		"tool_name":       "Read",
	})
	live := rec.ids[0]

	hookEvent(t, d, hook.EventPermissionRequest, map[string]any{
		"transcript_path": path,
		"tool_name":       "Bash",
		"tool_input":      map[string]any{"command": "go test ./..."},
	})

	updates := rec.updates[live]
	if len(updates) == 0 {
		t.Fatal("the session card was not refreshed into the waiting state")
	}
	if got := cardTitle(t, updates[len(updates)-1]); !strings.HasPrefix(got, "🟠 Waiting for permission") {
		t.Errorf("session card = %q, want the waiting state", got)
	}
	// The actionable notification is its own card, on top of the state.
	if titles := rec.titles(t); len(titles) != 2 || !strings.Contains(titles[1], "Permission required") {
		t.Errorf("cards = %v, want the separate permission card", titles)
	}
}

// A settled card cannot push a notification, so the outcome is also said
// out loud - once, and only when the turn did reportable work.
func TestFinishedTurnSettlesTheCardAndPings(t *testing.T) {
	d, rec, _ := fixture(t, session.Ready)
	path := watchable(t, d)

	hookEvent(t, d, hook.EventPostToolUse, map[string]any{
		"transcript_path": path,
		"tool_name":       "Read",
	})
	live := rec.ids[0]

	hookEvent(t, d, hook.EventStop, map[string]any{
		"transcript_path":        path,
		"last_assistant_message": "Consolidated the refresh validation.",
	})

	updates := rec.updates[live]
	if len(updates) == 0 {
		t.Fatal("the session card was never settled")
	}
	if got := cardTitle(t, updates[len(updates)-1]); !strings.HasPrefix(got, "✅ Completed") {
		t.Errorf("settled card = %q", got)
	}
	if len(rec.texts) != 1 || !strings.HasPrefix(rec.texts[0], "✅ Completed") {
		t.Errorf("pings = %v, want exactly one completion ping", rec.texts)
	}
}

func TestFailedTurnSettlesTheCardAndPings(t *testing.T) {
	d, rec, _ := fixture(t, session.Ready)
	path := watchable(t, d)

	hookEvent(t, d, hook.EventPostToolUse, map[string]any{
		"transcript_path": path,
		"tool_name":       "Read",
	})
	live := rec.ids[0]

	hookEvent(t, d, hook.EventStopFailure, map[string]any{
		"transcript_path": path,
		"error":           "rate_limit",
	})

	updates := rec.updates[live]
	if len(updates) == 0 {
		t.Fatal("the session card was never settled")
	}
	if got := cardTitle(t, updates[len(updates)-1]); !strings.HasPrefix(got, "🔴 Failed") {
		t.Errorf("settled card = %q", got)
	}
	if len(rec.texts) != 1 || !strings.HasPrefix(rec.texts[0], "🔴 Failed") {
		t.Errorf("pings = %v, want exactly one failure ping", rec.texts)
	}
}

// Without a standing session card the completion is its own message, which
// already notifies - a ping on top would be the same news twice.
func TestCompletionWithoutASessionCardDoesNotPing(t *testing.T) {
	d, rec, _ := fixture(t, session.Ready)
	path := watchable(t, d)

	hookEvent(t, d, hook.EventStop, map[string]any{
		"transcript_path":        path,
		"last_assistant_message": "Done.",
	})

	if titles := rec.titles(t); len(titles) != 1 || !strings.HasPrefix(titles[0], "✅ Completed") {
		t.Fatalf("cards = %v, want the completion notification", titles)
	}
	if len(rec.texts) != 0 {
		t.Errorf("pings = %v, want none", rec.texts)
	}
}

// Interrupt stops the turn and nothing else: the session stays, selected
// or not, and its card says honestly what happened.
func TestInterruptButtonStopsTheTurn(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("interrupt is not offered on Windows: see interrupt_windows.go")
	}
	d, rec, _ := fixture(t, session.Ready)
	path := watchable(t, d)

	var interrupted []int
	d.interrupt = func(s session.Session) error {
		interrupted = append(interrupted, s.PID)
		return nil
	}

	hookEvent(t, d, hook.EventPostToolUse, map[string]any{
		"transcript_path": path,
		"tool_name":       "Read",
	})
	live := rec.ids[0]

	value, err := json.Marshal(notify.Action{Kind: notify.ActionInterrupt, Session: "sess-1"})
	if err != nil {
		t.Fatal(err)
	}
	d.onCardAction(context.Background(), feishu.CardAction{Value: value})

	if len(interrupted) != 1 || interrupted[0] != 4242 {
		t.Fatalf("interrupts = %v, want one to the session's process", interrupted)
	}
	if d.watching("sess-1") {
		t.Error("the interrupted turn's card should be settled, not live")
	}
	s, ok := d.reg.Get("sess-1")
	if !ok {
		t.Fatal("interrupting must never remove the session")
	}
	if s.State != session.Idle {
		t.Errorf("state = %q, want idle: the session is back at its prompt", s.State)
	}
	updates := rec.updates[live]
	if len(updates) == 0 {
		t.Fatal("the session card was not settled")
	}
	if got := cardTitle(t, updates[len(updates)-1]); !strings.HasPrefix(got, "⏹️ Interrupted") {
		t.Errorf("settled card = %q", got)
	}
}

func TestInterruptFailureIsToldHonestly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("interrupt is not offered on Windows, so d.interrupt is never reached: see interrupt_windows.go")
	}
	d, rec, _ := fixture(t, session.Ready)
	path := watchable(t, d)
	d.interrupt = func(session.Session) error { return errors.New("no such process") }

	hookEvent(t, d, hook.EventPostToolUse, map[string]any{
		"transcript_path": path,
		"tool_name":       "Read",
	})

	value, _ := json.Marshal(notify.Action{Kind: notify.ActionInterrupt, Session: "sess-1"})
	d.onCardAction(context.Background(), feishu.CardAction{Value: value})
	defer d.closeWatch(context.Background(), "sess-1", "")

	if len(rec.texts) != 1 || !strings.Contains(rec.texts[0], "could not interrupt") {
		t.Errorf("answer = %v", rec.texts)
	}
	if !d.watching("sess-1") {
		t.Error("a failed interrupt must leave the live card standing")
	}
}

// A typed "interrupt" is the same contract as the button, because buttons
// can be silently inert when card callbacks are not configured.
func TestTypedInterruptReachesTheSelectedSession(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("interrupt is not offered on Windows: see interrupt_windows.go")
	}
	d, _, _ := fixture(t, session.Ready)
	watchable(t, d)
	selectSession(t, d, "sess-1")
	d.reg.MarkWorking("sess-1")

	var interrupted int
	d.interrupt = func(session.Session) error { interrupted++; return nil }

	d.onMessage(context.Background(), feishu.Message{Text: "interrupt"})

	if interrupted != 1 {
		t.Fatalf("interrupts = %d, want exactly one", interrupted)
	}
}

// "interrupt" is a Claude Companion command only when it is the whole message.
func TestInterruptIsNotStolenFromAnInstruction(t *testing.T) {
	d, _, l := fixture(t, session.Ready)
	watchable(t, d)
	selectSession(t, d, "sess-1")

	var interrupted int
	d.interrupt = func(session.Session) error { interrupted++; return nil }

	d.onMessage(context.Background(), feishu.Message{Text: "interrupt the benchmark if it takes too long"})

	if interrupted != 0 {
		t.Error("an instruction was mistaken for an interrupt")
	}
	if got := l.sent(); len(got) != 1 {
		t.Fatalf("session received %v, want the instruction", got)
	}
}

// A question card must stop reading as actionable once the session moved on.
func TestQuestionCardSettlesOnceAnswered(t *testing.T) {
	d, rec, _ := fixture(t, session.Ready)
	path := watchable(t, d)
	defer d.closeWatch(context.Background(), "sess-1", "")

	hookEvent(t, d, hook.EventPreToolUse, map[string]any{
		"transcript_path": path,
		"tool_name":       "AskUserQuestion",
		"tool_input": map[string]any{
			"questions": []any{map[string]any{
				"question": "Which API should remain backwards compatible?",
				"options": []any{
					map[string]any{"label": "v1"},
					map[string]any{"label": "v2"},
				},
			}},
		},
	})

	var questionID string
	for i, c := range rec.cards {
		if strings.Contains(cardTitle(t, c), "Claude needs your input") {
			questionID = rec.ids[i]
		}
	}
	if questionID == "" {
		t.Fatalf("cards = %v, want the question card", rec.titles(t))
	}

	// The user answered in the terminal; the next event proves it. The
	// question card leaves the conversation and the session card keeps
	// the record.
	hookEvent(t, d, hook.EventPostToolUse, map[string]any{
		"transcript_path": path,
		"tool_name":       "AskUserQuestion",
	})

	if len(rec.deleted) != 1 || rec.deleted[0] != questionID {
		t.Fatalf("deleted = %v, want the answered question card recalled", rec.deleted)
	}
	live := rec.ids[0]
	updates := rec.updates[live]
	if len(updates) == 0 || !strings.Contains(updates[len(updates)-1], "Answered in Claude Code") {
		t.Errorf("the session card does not record the answer: %v", updates)
	}
}

// The verdict the user gave from Feishu stays visible after its card is
// recalled: it moves onto the session card as a one-line record.
func TestVerdictIsRecordedOnTheSessionCard(t *testing.T) {
	d, rec, l := fixture(t, session.Ready)
	path := watchable(t, d)
	defer d.closeWatch(context.Background(), "sess-1", "")

	hookEvent(t, d, hook.EventPostToolUse, map[string]any{
		"transcript_path": path,
		"tool_name":       "Read",
	})
	live := rec.ids[0]

	d.onPermissionRequest(context.Background(), l, mcpRequest("abcde", "npm install"))
	permission := rec.ids[len(rec.ids)-1]

	value, _ := json.Marshal(notify.Action{
		Kind: notify.ActionPermit, Session: "sess-1", Request: "abcde", Verdict: notify.VerdictAllow,
	})
	d.onCardAction(context.Background(), feishu.CardAction{Value: value, MessageID: permission})

	if len(rec.deleted) != 1 || rec.deleted[0] != permission {
		t.Fatalf("deleted = %v, want the permission card recalled", rec.deleted)
	}
	updates := rec.updates[live]
	if len(updates) == 0 {
		t.Fatal("the session card was not refreshed with the decision")
	}
	final := updates[len(updates)-1]
	if !strings.Contains(final, "✓ Allowed once") || !strings.Contains(final, "npm install") {
		t.Errorf("session card does not record the verdict: %s", final)
	}
}

// mcpRequest is a relayed permission prompt with a shell command preview.
func mcpRequest(id, command string) mcp.PermissionRequest {
	return mcp.PermissionRequest{RequestID: id, ToolName: "Bash", InputPreview: command}
}
