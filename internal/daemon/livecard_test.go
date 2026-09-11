package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marcelritzschke/claude-code-feishu-companion/internal/feishu"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/hook"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/notify"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/session"
)

// turnPrompt is a turn in flight: a prompt, a word about what Claude is
// doing, and one finished tool call.
const turnPrompt = `{"type":"user","promptId":"p-1","timestamp":"2026-08-31T07:00:00.000Z","message":{"role":"user","content":"fix the token refresh"}}
{"type":"assistant","timestamp":"2026-08-31T07:00:01.000Z","message":{"role":"assistant","content":[{"type":"text","text":"Reading the auth package."}]}}
{"type":"assistant","timestamp":"2026-08-31T07:00:02.000Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Read","input":{"file_path":"/work/payments-api/auth/session.go"}}]}}
{"type":"user","timestamp":"2026-08-31T07:00:03.000Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","is_error":false,"content":"ok"}]}}
`

// observable prepares a working session with a transcript to follow, and
// returns the transcript path so a test can let the turn move on.
func observable(t *testing.T, d *Daemon) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(p, []byte(turnPrompt), 0o600); err != nil {
		t.Fatal(err)
	}
	d.reg.Observe(session.Observation{
		ID: "sess-1", PID: 4242, Dir: "/work/payments-api", Title: "Fix token refresh",
		Transcript: p, HookEvent: hook.EventUserPromptSubmit,
	})
	return p
}

func appendLines(t *testing.T, path, lines string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(lines); err != nil {
		t.Fatal(err)
	}
}

// A session at work gets one live card, without anybody asking for it.
func TestWorkingSessionGetsOneLiveCard(t *testing.T) {
	d, rec, _ := fixture(t, session.Ready)
	observable(t, d)
	s, _ := d.reg.Get("sess-1")

	d.ensureSessionCard(context.Background(), s)
	defer d.settleLiveCard(context.Background(), "sess-1", "")

	if titles := rec.titles(t); len(titles) != 1 || !strings.HasPrefix(titles[0], "🔵 Working") {
		t.Fatalf("cards = %v, want one live card", titles)
	}
	if !strings.Contains(rec.cards[0], "Reading the auth package.") {
		t.Errorf("the live card should say what Claude is doing: %s", rec.cards[0])
	}
	if !d.cardStanding("sess-1") {
		t.Error("the session should have a live card")
	}
}

// The whole point of a live card is that it is one card. New activity
// rewrites it; it never becomes a second message.
func TestLiveCardUpdatesTheSameMessageInPlace(t *testing.T) {
	d, rec, _ := fixture(t, session.Ready)
	path := observable(t, d)
	s, _ := d.reg.Get("sess-1")

	d.ensureSessionCard(context.Background(), s)
	defer d.settleLiveCard(context.Background(), "sess-1", "")
	live := rec.ids[0]

	appendLines(t, path, `{"type":"assistant","timestamp":"2026-08-31T07:00:04.000Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t2","name":"Bash","input":{"command":"go test ./..."}}]}}`+"\n")

	d.mu.Lock()
	w := d.live["sess-1"]
	d.mu.Unlock()
	s, _ = d.reg.Get("sess-1")
	d.refreshLiveCard(context.Background(), w, s, true)

	if len(rec.cards) != 1 {
		t.Fatalf("cards = %d, want the one live card and no more", len(rec.cards))
	}
	updates := rec.updates[live]
	if len(updates) != 1 || !strings.Contains(updates[0], "Running go test ./...") {
		t.Errorf("updates to %s = %v, want the new activity in place", live, updates)
	}
}

// One turn, one message: the live card is recalled as the turn ends and
// the completion takes its place, so the conversation still holds exactly
// one message for the turn - one that notifies.
func TestFinishedTurnReplacesTheLiveCard(t *testing.T) {
	d, rec, _ := fixture(t, session.Ready)
	path := observable(t, d)
	s, _ := d.reg.Get("sess-1")

	d.ensureSessionCard(context.Background(), s)
	live := rec.ids[0]

	hookEvent(t, d, hook.EventStop, map[string]any{
		"transcript_path":        path,
		"last_assistant_message": "The refresh flow now rotates the token after every refresh.",
	})

	if d.cardStanding("sess-1") {
		t.Error("the turn ended; no card should still be following it")
	}
	if len(rec.deleted) != 1 || rec.deleted[0] != live {
		t.Fatalf("deleted = %v, want the live card recalled", rec.deleted)
	}
	if len(rec.cards) != 2 {
		t.Fatalf("cards = %d, want the live card and the completion that replaced it", len(rec.cards))
	}
	final := rec.cards[1]
	if got := cardTitle(t, final); !strings.HasPrefix(got, "✅ Completed") {
		t.Errorf("outcome card = %q, want the completion", got)
	}
	if strings.Contains(final, "Interrupt") {
		t.Errorf("a settled card must not still offer to stop the turn: %s", final)
	}
}

// Beside a live card, the attention-mode progress card would be a second
// live card for the same turn saying less.
func TestALiveCardStandsDownTheProgressCard(t *testing.T) {
	d, _, _ := fixture(t, session.Ready)
	observable(t, d)
	s, _ := d.reg.Get("sess-1")

	if d.skipEvent("sess-1")(hook.EventPostToolUse) {
		t.Error("a session with no live card still gets its progress card")
	}
	d.ensureSessionCard(context.Background(), s)
	defer d.settleLiveCard(context.Background(), "sess-1", "")

	if !d.skipEvent("sess-1")(hook.EventPostToolUse) {
		t.Error("this turn already has its one live card")
	}
}

// Asking to see the sessions must answer where the user is looking. A
// card rewritten in place notifies nobody and may be a long scroll up, so
// the standing card is recalled and put up again as a new message.
func TestRecapRepostsTheLiveCard(t *testing.T) {
	d, rec, _ := fixture(t, session.Ready)
	observable(t, d)
	s, _ := d.reg.Get("sess-1")

	d.ensureSessionCard(context.Background(), s)
	first := rec.ids[0]

	d.onMessage(context.Background(), feishu.Message{Text: "sessions"})
	defer d.settleLiveCard(context.Background(), "sess-1", "")

	if len(rec.deleted) != 1 || rec.deleted[0] != first {
		t.Errorf("deleted = %v, want the card that was standing recalled", rec.deleted)
	}
	if len(rec.cards) != 2 {
		t.Fatalf("cards = %d, want the first card and the one that replaced it", len(rec.cards))
	}
	if got := cardTitle(t, rec.cards[1]); !strings.HasPrefix(got, "🔵 Working") {
		t.Errorf("reposted card = %q, want the live card again", got)
	}
	if !d.cardStanding("sess-1") {
		t.Error("the session lost its live card")
	}
}

// A session between turns has no live card to open - only an outcome.
func TestRecapOfAnIdleSessionShowsTheLastOutcome(t *testing.T) {
	d, rec, _ := fixture(t, session.Ready)
	path := observable(t, d)
	d.reg.Observe(session.Observation{ID: "sess-1", PID: 4242, Transcript: path, HookEvent: hook.EventStop})
	s, _ := d.reg.Get("sess-1")

	d.repostSessionCard(context.Background(), s)

	if d.cardStanding("sess-1") {
		t.Error("nothing is running; no live card should be polling")
	}
	if titles := rec.titles(t); len(titles) != 1 || !strings.HasPrefix(titles[0], "✅ Completed") {
		t.Errorf("cards = %v, want the last outcome", titles)
	}
}

// A session Claude Companion has only heard from over its channel has no
// work to show. It is still the session the user is most likely to want -
// nothing is running in it, so it reads a message at once - and it gets a
// card that says exactly that rather than an empty live view.
func TestSessionWithNothingToShowStillGetsACard(t *testing.T) {
	d, rec, _ := fixture(t, session.Ready)
	s, _ := d.reg.Get("sess-1")

	d.repostSessionCard(context.Background(), s)

	if d.cardStanding("sess-1") {
		t.Error("there is nothing to follow; no live card should be polling")
	}
	if titles := rec.titles(t); len(titles) != 1 || titles[0] != "⚪ Idle" {
		t.Fatalf("cards = %v, want the resting card", titles)
	}
	if !strings.Contains(rec.cards[0], "Nothing is running in this session.") {
		t.Errorf("resting card = %s", rec.cards[0])
	}
}

// A live card cannot outlive the daemon that polls it, and a card left
// saying "working" would outlast the truth of it.
func TestStoppingTheDaemonPutsLiveCardsToRest(t *testing.T) {
	d, rec, _ := fixture(t, session.Ready)
	observable(t, d)
	s, _ := d.reg.Get("sess-1")

	d.ensureSessionCard(context.Background(), s)
	live := rec.ids[0]
	d.settleAllLiveCards(context.Background())

	if d.cardStanding("sess-1") {
		t.Error("live cards should be put to rest with the daemon")
	}
	updates := rec.updates[live]
	if len(updates) == 0 || strings.HasPrefix(cardTitle(t, updates[len(updates)-1]), "🔵 Working") {
		t.Errorf("the live card was left running: %v", updates)
	}
}

// A live card does not poll forever just because a session was left open.
func TestLiveCardDoesNotOutlastItsWelcome(t *testing.T) {
	if defaultPace.max > 4*time.Hour {
		t.Errorf("live card max = %v, which is a subscription rather than a check-in", defaultPace.max)
	}
}

// quickPace runs the real polling loop at a speed a test can wait for. It
// must be set before the first live card opens.
func quickPace(d *Daemon) {
	d.pace = pace{tick: 5 * time.Millisecond, floor: time.Millisecond, heartbeat: time.Hour, max: time.Hour}
}

// The loop itself, not just a forced refresh: a live card notices new work
// on its own and rewrites the one message it owns.
func TestLiveCardLoopFollowsTheTurn(t *testing.T) {
	d, rec, _ := fixture(t, session.Ready)
	quickPace(d)
	path := observable(t, d)
	s, _ := d.reg.Get("sess-1")

	d.ensureSessionCard(context.Background(), s)
	defer d.settleLiveCard(context.Background(), "sess-1", "")
	live := rec.ids[0]

	appendLines(t, path, `{"type":"assistant","timestamp":"2026-08-31T07:00:04.000Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t2","name":"Bash","input":{"command":"go test ./..."}}]}}`+"\n")

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		rec.mu.Lock()
		seen := len(rec.updates[live])
		rec.mu.Unlock()
		if seen > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.cards) != 1 {
		t.Fatalf("cards = %d, want the one live card", len(rec.cards))
	}
	updates := rec.updates[live]
	if len(updates) == 0 || !strings.Contains(updates[len(updates)-1], "Running go test ./...") {
		t.Errorf("the live card did not follow the turn: %v", updates)
	}
}

// The loop closes itself when the session it was following disappears.
func TestLiveCardEndsWithItsSession(t *testing.T) {
	d, rec, _ := fixture(t, session.Ready)
	quickPace(d)
	observable(t, d)
	s, _ := d.reg.Get("sess-1")

	d.ensureSessionCard(context.Background(), s)
	live := rec.ids[0]
	d.reg.Remove("sess-1")

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		rec.mu.Lock()
		settled := len(rec.updates[live]) > 0
		rec.mu.Unlock()
		if settled && !d.cardStanding("sess-1") {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if d.cardStanding("sess-1") {
		t.Fatal("the live card outlived its session")
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	updates := rec.updates[live]
	if len(updates) == 0 || !strings.Contains(updates[len(updates)-1], "This session has ended.") {
		t.Errorf("the card was left running after the session ended: %v", updates)
	}
}

// replyOn types a message into the reply box of one standing card, the way
// Feishu delivers it: a callback naming both the session and the card.
func replyOn(t *testing.T, d *Daemon, messageID, text string) {
	t.Helper()
	value, err := json.Marshal(notify.Action{Kind: notify.ActionSay, Session: "sess-1"})
	if err != nil {
		t.Fatal(err)
	}
	d.onCardAction(context.Background(), feishu.CardAction{
		Value: value, Input: text, MessageID: messageID,
	})
}
