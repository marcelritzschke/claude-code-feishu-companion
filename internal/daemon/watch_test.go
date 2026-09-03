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

// watchable prepares a working session with a transcript to watch, and
// returns the transcript path so a test can let the turn move on.
func watchable(t *testing.T, d *Daemon) string {
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

func TestWatchOpensOneLiveCard(t *testing.T) {
	d, rec, _ := fixture(t, session.Ready)
	watchable(t, d)
	s, _ := d.reg.Get("sess-1")

	d.startWatch(context.Background(), s)
	defer d.closeWatch(context.Background(), "sess-1", "")

	if titles := rec.titles(t); len(titles) != 1 || !strings.HasPrefix(titles[0], "🟢 Working") {
		t.Fatalf("cards = %v, want one live card", titles)
	}
	if !strings.Contains(rec.cards[0], "Reading the auth package.") {
		t.Errorf("the live card should say what Claude is doing: %s", rec.cards[0])
	}
	if !d.watching("sess-1") {
		t.Error("the session should be watched")
	}
}

// The whole point of a live card is that it is one card. New activity
// rewrites it; it never becomes a second message.
func TestWatchUpdatesTheSameCardInPlace(t *testing.T) {
	d, rec, _ := fixture(t, session.Ready)
	path := watchable(t, d)
	s, _ := d.reg.Get("sess-1")

	d.startWatch(context.Background(), s)
	defer d.closeWatch(context.Background(), "sess-1", "")
	live := rec.ids[0]

	appendLines(t, path, `{"type":"assistant","timestamp":"2026-08-31T07:00:04.000Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t2","name":"Bash","input":{"command":"go test ./..."}}]}}`+"\n")

	d.mu.Lock()
	w := d.watches["sess-1"]
	d.mu.Unlock()
	s, _ = d.reg.Get("sess-1")
	d.refreshWatch(context.Background(), w, s, true)

	if len(rec.cards) != 1 {
		t.Fatalf("cards = %d, want the one live card and no more", len(rec.cards))
	}
	updates := rec.updates[live]
	if len(updates) != 1 || !strings.Contains(updates[0], "Running go test ./...") {
		t.Errorf("updates to %s = %v, want the new activity in place", live, updates)
	}
}

// One turn, one message: the completion notification settles the very card
// the user has been watching rather than adding another.
func TestFinishedTurnSettlesTheWatchedCard(t *testing.T) {
	d, rec, _ := fixture(t, session.Ready)
	path := watchable(t, d)
	s, _ := d.reg.Get("sess-1")

	d.startWatch(context.Background(), s)
	live := rec.ids[0]

	hookEvent(t, d, hook.EventStop, map[string]any{
		"transcript_path":        path,
		"last_assistant_message": "The refresh flow now rotates the token after every refresh.",
	})

	if d.watching("sess-1") {
		t.Error("the turn ended; nothing should still be watching it")
	}
	if len(rec.cards) != 1 {
		t.Fatalf("cards = %d, want the completion to reuse the live card", len(rec.cards))
	}
	updates := rec.updates[live]
	if len(updates) == 0 {
		t.Fatalf("the watched card was never settled")
	}
	final := updates[len(updates)-1]
	if got := cardTitle(t, final); !strings.HasPrefix(got, "✅ Completed") {
		t.Errorf("settled card = %q, want the completion", got)
	}
	if strings.Contains(final, "Stop watching") {
		t.Errorf("a settled card must not still offer to stop watching: %s", final)
	}
}

// While the user is watching, the V1 progress card would be a second live
// card for the same turn saying less.
func TestWatchingStandsDownTheProgressCard(t *testing.T) {
	d, _, _ := fixture(t, session.Ready)
	watchable(t, d)
	s, _ := d.reg.Get("sess-1")

	if d.skipEvent("sess-1")(hook.EventPostToolUse) {
		t.Error("an unwatched session still gets its progress card")
	}
	d.startWatch(context.Background(), s)
	defer d.closeWatch(context.Background(), "sess-1", "")

	if !d.skipEvent("sess-1")(hook.EventPostToolUse) {
		t.Error("a watched turn already has its one live card")
	}
}

// Stopping mid-turn must not leave a card that reads like a result.
func TestStopWatchingLeavesAnHonestCard(t *testing.T) {
	d, rec, _ := fixture(t, session.Ready)
	watchable(t, d)
	s, _ := d.reg.Get("sess-1")

	d.startWatch(context.Background(), s)
	live := rec.ids[0]
	d.onMessage(context.Background(), feishu.Message{Text: "stop watching"})

	if d.watching("sess-1") {
		t.Fatal("the watch should be closed")
	}
	updates := rec.updates[live]
	if len(updates) == 0 {
		t.Fatal("the card was left mid-flight")
	}
	final := updates[len(updates)-1]
	if got := cardTitle(t, final); got != "⏸️ No longer live" {
		t.Errorf("final card = %q", got)
	}
	if strings.Contains(final, "Claude finished") {
		t.Errorf("the turn is still running: %s", final)
	}
}

// A session between turns has no live view to open - only an outcome.
func TestWatchingAnIdleSessionShowsTheLastOutcome(t *testing.T) {
	d, rec, _ := fixture(t, session.Ready)
	path := watchable(t, d)
	d.reg.Observe(session.Observation{ID: "sess-1", PID: 4242, Transcript: path, HookEvent: hook.EventStop})
	s, _ := d.reg.Get("sess-1")

	d.startWatch(context.Background(), s)

	if d.watching("sess-1") {
		t.Error("there is nothing running to watch")
	}
	if titles := rec.titles(t); len(titles) != 1 || !strings.HasPrefix(titles[0], "✅ Completed") {
		t.Errorf("cards = %v, want the last outcome", titles)
	}
}

// A session Claude Companion has only heard from over its channel cannot be shown,
// and saying so beats a card that would sit there empty.
func TestUnwatchableSessionIsHonest(t *testing.T) {
	d, rec, _ := fixture(t, session.Ready)
	s, _ := d.reg.Get("sess-1")

	d.startWatch(context.Background(), s)

	if len(rec.cards) != 0 {
		t.Errorf("cards = %v, want none", rec.titles(t))
	}
	if len(rec.texts) != 1 || !strings.Contains(rec.texts[0], "cannot see inside") {
		t.Errorf("answer = %v", rec.texts)
	}
}

// Watching is never guessed at, for the same reason a message is never
// sent to a session the user did not choose.
func TestWatchWithoutASelectionAsksFirst(t *testing.T) {
	d, rec, _ := fixture(t, session.Ready)
	watchable(t, d)

	d.onMessage(context.Background(), feishu.Message{Text: "watch"})

	if d.watching("sess-1") {
		t.Error("no session was selected; nothing should be watched")
	}
	if titles := rec.titles(t); len(titles) != 1 || titles[0] != "Claude Companion" {
		t.Errorf("cards = %v, want the overview so the user can pick", titles)
	}
}

// "watch" is a Claude Companion command only when it is the whole message.
func TestWatchIsNotStolenFromAnInstruction(t *testing.T) {
	d, _, l := fixture(t, session.Ready)
	watchable(t, d)
	selectSession(t, d, "sess-1")

	d.onMessage(context.Background(), feishu.Message{Text: "watch the integration tests and report back"})

	if got := l.sent(); len(got) != 1 {
		t.Fatalf("session received %v, want the instruction", got)
	}
	if d.watching("sess-1") {
		t.Error("an instruction was mistaken for a Claude Companion command")
	}
}

func TestWatchButtonOpensTheLiveView(t *testing.T) {
	d, rec, _ := fixture(t, session.Ready)
	watchable(t, d)

	value, err := json.Marshal(notify.Action{Kind: notify.ActionWatch, Session: "sess-1"})
	if err != nil {
		t.Fatal(err)
	}
	d.onCardAction(context.Background(), feishu.CardAction{Value: value})
	defer d.closeWatch(context.Background(), "sess-1", "")

	if !d.watching("sess-1") {
		t.Fatal("the Watch button did not open the live view")
	}
	if titles := rec.titles(t); len(titles) != 1 || !strings.HasPrefix(titles[0], "🟢 Working") {
		t.Errorf("cards = %v", titles)
	}
}

// A watch cannot outlive the daemon that polls it, and a card left saying
// "working" would outlast the truth of it.
func TestStoppingTheDaemonPutsLiveCardsToRest(t *testing.T) {
	d, rec, _ := fixture(t, session.Ready)
	watchable(t, d)
	s, _ := d.reg.Get("sess-1")

	d.startWatch(context.Background(), s)
	live := rec.ids[0]
	d.closeAllWatches(context.Background())

	if d.watching("sess-1") {
		t.Error("watches should be closed with the daemon")
	}
	updates := rec.updates[live]
	if len(updates) == 0 || strings.HasPrefix(cardTitle(t, updates[len(updates)-1]), "🟢 Working") {
		t.Errorf("the live card was left running: %v", updates)
	}
}

// A watch does not run forever just because the user forgot about it.
func TestWatchDoesNotOutlastItsWelcome(t *testing.T) {
	if defaultPace.max > 4*time.Hour {
		t.Errorf("watch max = %v, which is a subscription rather than a check-in", defaultPace.max)
	}
}

// quickPace runs the real polling loop at a speed a test can wait for. It
// must be set before the first watch starts.
func quickPace(d *Daemon) {
	d.pace = pace{tick: 5 * time.Millisecond, floor: time.Millisecond, heartbeat: time.Hour, max: time.Hour}
}

// The loop itself, not just a forced refresh: a watch notices new work on
// its own and rewrites the one card it owns.
func TestWatchLoopFollowsTheTurn(t *testing.T) {
	d, rec, _ := fixture(t, session.Ready)
	quickPace(d)
	path := watchable(t, d)
	s, _ := d.reg.Get("sess-1")

	d.startWatch(context.Background(), s)
	defer d.closeWatch(context.Background(), "sess-1", "")
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
		t.Errorf("the watch did not follow the turn: %v", updates)
	}
}

// The loop closes itself when the session it was watching disappears.
func TestWatchEndsWithItsSession(t *testing.T) {
	d, rec, _ := fixture(t, session.Ready)
	quickPace(d)
	watchable(t, d)
	s, _ := d.reg.Get("sess-1")

	d.startWatch(context.Background(), s)
	live := rec.ids[0]
	d.reg.Remove("sess-1")

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		rec.mu.Lock()
		settled := len(rec.updates[live]) > 0
		rec.mu.Unlock()
		if settled && !d.watching("sess-1") {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if d.watching("sess-1") {
		t.Fatal("the watch outlived its session")
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	updates := rec.updates[live]
	if len(updates) == 0 || !strings.Contains(updates[len(updates)-1], "This session has ended.") {
		t.Errorf("the card was left running after the session ended: %v", updates)
	}
}
