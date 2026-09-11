package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

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
	path := observable(t, d)
	defer d.settleLiveCard(context.Background(), "sess-1", "")

	hookEvent(t, d, hook.EventPostToolUse, map[string]any{
		"transcript_path": path,
		"tool_name":       "Read",
	})

	if !d.cardStanding("sess-1") {
		t.Fatal("a working session should carry its own live card")
	}
	if titles := rec.titles(t); len(titles) != 1 || !strings.HasPrefix(titles[0], "🔵 Working") {
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
	path := observable(t, d)

	hookEvent(t, d, hook.EventUserPromptSubmit, map[string]any{"transcript_path": path})

	if d.cardStanding("sess-1") {
		t.Error("a conversational turn must not open a card")
	}
	if len(rec.cards) != 0 {
		t.Errorf("cards = %v, want none", rec.titles(t))
	}
}

// A session that cannot be controlled must never offer control.
func TestNotificationOnlySessionCardOffersNoControl(t *testing.T) {
	d, rec, _ := fixture(t, session.Notifications)
	path := observable(t, d)
	defer d.settleLiveCard(context.Background(), "sess-1", "")

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
	path := observable(t, d)
	defer d.settleLiveCard(context.Background(), "sess-1", "")

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

// The live card is recalled when its turn ends, so the outcome arrives as
// a new message the user is actually notified of - one message for the
// turn, and no second one saying the same thing.
func TestFinishedTurnReplacesTheLiveCardWithItsOutcome(t *testing.T) {
	d, rec, _ := fixture(t, session.Ready)
	path := observable(t, d)

	hookEvent(t, d, hook.EventPostToolUse, map[string]any{
		"transcript_path": path,
		"tool_name":       "Read",
	})
	live := rec.ids[0]

	hookEvent(t, d, hook.EventStop, map[string]any{
		"transcript_path":        path,
		"last_assistant_message": "Consolidated the refresh validation.",
	})

	if len(rec.deleted) != 1 || rec.deleted[0] != live {
		t.Fatalf("deleted = %v, want the live card recalled", rec.deleted)
	}
	titles := rec.titles(t)
	if len(titles) != 2 || !strings.HasPrefix(titles[1], "✅ Completed") {
		t.Errorf("cards = %v, want the live card followed by the outcome", titles)
	}
	if len(rec.texts) != 0 {
		t.Errorf("texts = %v, want the outcome card to be the only message", rec.texts)
	}
}

// A recall Feishu refuses leaves the outcome to settle the live card in
// place, which notifies nobody - so that turn, and only that turn, is also
// said out loud.
func TestOutcomeStrandedOnAnUnrecallableCardIsSaidOutLoud(t *testing.T) {
	d, rec, _ := fixture(t, session.Ready)
	rec.failDelete = true
	path := observable(t, d)

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

func TestFailedTurnReplacesTheLiveCardWithItsOutcome(t *testing.T) {
	d, rec, _ := fixture(t, session.Ready)
	path := observable(t, d)

	hookEvent(t, d, hook.EventPostToolUse, map[string]any{
		"transcript_path": path,
		"tool_name":       "Read",
	})
	live := rec.ids[0]

	hookEvent(t, d, hook.EventStopFailure, map[string]any{
		"transcript_path": path,
		"error":           "rate_limit",
	})

	if len(rec.deleted) != 1 || rec.deleted[0] != live {
		t.Fatalf("deleted = %v, want the live card recalled", rec.deleted)
	}
	titles := rec.titles(t)
	if len(titles) != 2 || !strings.HasPrefix(titles[1], "🔴 Failed") {
		t.Errorf("cards = %v, want the live card followed by the failure", titles)
	}
	if len(rec.texts) != 0 {
		t.Errorf("texts = %v, want the failure card to be the only message", rec.texts)
	}
}

// Without a standing session card the completion is its own message, which
// already notifies - a ping on top would be the same news twice.
func TestCompletionWithoutASessionCardDoesNotPing(t *testing.T) {
	d, rec, _ := fixture(t, session.Ready)
	path := observable(t, d)

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
	path := observable(t, d)

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
	if d.cardStanding("sess-1") {
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
	path := observable(t, d)
	d.interrupt = func(session.Session) error { return errors.New("no such process") }

	hookEvent(t, d, hook.EventPostToolUse, map[string]any{
		"transcript_path": path,
		"tool_name":       "Read",
	})

	value, _ := json.Marshal(notify.Action{Kind: notify.ActionInterrupt, Session: "sess-1"})
	d.onCardAction(context.Background(), feishu.CardAction{Value: value})
	defer d.settleLiveCard(context.Background(), "sess-1", "")

	if len(rec.texts) != 1 || !strings.Contains(rec.texts[0], "could not interrupt") {
		t.Errorf("answer = %v", rec.texts)
	}
	if !d.cardStanding("sess-1") {
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
	observable(t, d)
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
	observable(t, d)
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
	path := observable(t, d)
	defer d.settleLiveCard(context.Background(), "sess-1", "")

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
	path := observable(t, d)
	defer d.settleLiveCard(context.Background(), "sess-1", "")

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

// answerNow appends what Claude Code writes when a session takes a message
// from the Claude Companion channel and answers it without running
// anything: no tool ran, because none was needed.
//
// The timestamps are of this moment, because how long the turn took is
// half of what decides whether it is reported: a wordless answer old
// enough to have been walked away from is reported anyway, and a turn
// this test is about is seconds old.
func answerNow(t *testing.T, path string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	appendLines(t, path,
		`{"type":"user","promptId":"p-2","timestamp":"`+now+`","message":{"role":"user","content":"<channel source=\"claude-companion\" project=\"payments-api\">\nlgtm\n</channel>"}}`+"\n"+
			`{"type":"assistant","timestamp":"`+now+`","message":{"role":"assistant","content":[{"type":"text","text":"Merged and pushed."}]}}`+"\n")
}

// The failure this exists for: a completed card, a two-word reply typed
// into it, and a turn short enough to need no tool. Such a turn does no
// reportable work, so it is ordinarily withheld - which is right for
// somebody at the terminal watching the answer appear, and leaves somebody
// on a phone looking at an answer to the message before theirs.
func TestFastAnswerToACardReplyStillReachesThePhone(t *testing.T) {
	d, rec, _ := fixture(t, session.Ready)
	path := observable(t, d)

	hookEvent(t, d, hook.EventStop, map[string]any{
		"transcript_path":        path,
		"last_assistant_message": "Added refresh-token rotation.",
	})
	if titles := rec.titles(t); len(titles) != 1 || !strings.HasPrefix(titles[0], "✅ Completed") {
		t.Fatalf("cards = %v, want the completion the user replies to", titles)
	}
	completed := rec.ids[0]

	replyOn(t, d, completed, "lgtm")
	defer d.settleLiveCard(context.Background(), "sess-1", "")

	// Claude takes the message and answers it without running anything.
	answerNow(t, path)
	hookEvent(t, d, hook.EventStop, map[string]any{
		"transcript_path":        path,
		"last_assistant_message": "Merged and pushed.",
	})

	titles := rec.titles(t)
	if len(titles) != 2 || !strings.HasPrefix(titles[1], "✅ Completed") {
		t.Fatalf("cards = %v, want the answer to reach the phone as its own card", titles)
	}
	if !strings.Contains(rec.cards[1], "Merged and pushed.") {
		t.Errorf("the second card does not carry the new answer: %s", rec.cards[1])
	}
}

// A turn nobody is waiting on from Feishu stays withheld: the rule that
// keeps a conversational exchange off the phone is unchanged.
func TestFastTurnNobodyAskedForStaysQuiet(t *testing.T) {
	d, rec, _ := fixture(t, session.Ready)
	path := observable(t, d)
	answerNow(t, path)

	hookEvent(t, d, hook.EventStop, map[string]any{
		"transcript_path":        path,
		"last_assistant_message": "Merged and pushed.",
	})

	if len(rec.cards) != 0 {
		t.Errorf("cards = %v, want none for a turn that did no work", rec.titles(t))
	}
}

// The box on a card keeps what was typed into it until the card is
// rewritten, so the card the user replied on becomes their message's card.
func TestCardReplyRewritesTheCardItWasTypedInto(t *testing.T) {
	d, rec, _ := fixture(t, session.Ready)
	path := observable(t, d)

	hookEvent(t, d, hook.EventStop, map[string]any{
		"transcript_path":        path,
		"last_assistant_message": "Added refresh-token rotation.",
	})
	completed := rec.ids[0]

	replyOn(t, d, completed, "lgtm")
	defer d.settleLiveCard(context.Background(), "sess-1", "")

	updates := rec.updates[completed]
	if len(updates) == 0 {
		t.Fatal("the card the reply was typed into was never rewritten, so its box still holds the message")
	}
	if got := cardTitle(t, updates[len(updates)-1]); got != "🔵 Sent" {
		t.Errorf("card = %q, want it to stand in for the turn the message starts", got)
	}
	if !strings.Contains(updates[len(updates)-1], "lgtm") {
		t.Errorf("the card does not show what was sent: %s", updates[len(updates)-1])
	}
	if len(rec.cards) != 1 {
		t.Errorf("cards = %v, want no second message for one reply", rec.titles(t))
	}
}

// The card stands in only until Claude takes the message up; from then on
// it is the live view of the turn, which is what the user asked to see.
func TestTheSentCardBecomesTheTurnItStarts(t *testing.T) {
	d, rec, _ := fixture(t, session.Ready)
	path := observable(t, d)

	hookEvent(t, d, hook.EventStop, map[string]any{
		"transcript_path":        path,
		"last_assistant_message": "Added refresh-token rotation.",
	})
	completed := rec.ids[0]
	replyOn(t, d, completed, "now run the tests")
	defer d.settleLiveCard(context.Background(), "sess-1", "")

	answerNow(t, path)
	hookEvent(t, d, hook.EventPostToolUse, map[string]any{
		"transcript_path": path,
		"tool_name":       "Bash",
	})

	updates := rec.updates[completed]
	if got := cardTitle(t, updates[len(updates)-1]); !strings.HasPrefix(got, "🔵 Working") {
		t.Errorf("card = %q, want the live view of the turn the message started", got)
	}
}

// A session blocked on a decision is not working, and a message queued
// behind that decision does not unblock it. The card the user just typed
// into must not claim to be running a turn that has not started.
func TestCardReplyToAWaitingSessionKeepsItWaiting(t *testing.T) {
	d, rec, _ := fixture(t, session.Ready)
	path := observable(t, d)
	defer d.settleLiveCard(context.Background(), "sess-1", "")

	hookEvent(t, d, hook.EventPermissionRequest, map[string]any{
		"transcript_path": path,
		"tool_name":       "Bash",
		"tool_input":      map[string]any{"command": "go test ./..."},
	})
	live := rec.ids[0]

	replyOn(t, d, live, "go ahead once that is approved")

	if s, _ := d.reg.Get("sess-1"); s.State != session.Waiting {
		t.Errorf("state = %q, want the session still waiting on its decision", s.State)
	}
	updates := rec.updates[live]
	if len(updates) == 0 {
		t.Fatal("the card the reply was typed into was never rewritten")
	}
	if got := cardTitle(t, updates[len(updates)-1]); !strings.HasPrefix(got, "🟠 Waiting") {
		t.Errorf("card = %q, want it to keep saying what the session is waiting for", got)
	}
	if len(rec.texts) != 1 || !strings.Contains(rec.texts[0], "Queued") {
		t.Errorf("answers = %v, want the queueing said out loud, since the card cannot show it", rec.texts)
	}
}

// Two cards for one session is what this product does not do. Answering
// from an older card makes that one the session's card, and takes the
// other down.
func TestCardReplyRecallsTheOtherCardStandingForTheSession(t *testing.T) {
	d, rec, _ := fixture(t, session.Ready)
	path := observable(t, d)
	defer d.settleLiveCard(context.Background(), "sess-1", "")

	hookEvent(t, d, hook.EventPostToolUse, map[string]any{
		"transcript_path": path,
		"tool_name":       "Read",
	})
	live := rec.ids[0]

	replyOn(t, d, "om_older_card", "actually, do the mobile client first")

	if len(rec.deleted) != 1 || rec.deleted[0] != live {
		t.Errorf("recalled %v, want the card the user is no longer looking at (%s)", rec.deleted, live)
	}
	if got := rec.updates["om_older_card"]; len(got) == 0 {
		t.Error("the card the reply came from did not become the session's card")
	}
}
