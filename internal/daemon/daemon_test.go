package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marcelritzschke/claude-code-feishu-companion/internal/config"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/feishu"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/hook"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/ipc"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/mcp"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/notify"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/session"
)

// recorder stands in for Feishu: it keeps what the user would have seen.
type recorder struct {
	mu      sync.Mutex
	cards   []string
	ids     []string
	updates map[string][]string
	texts   []string
	deleted []string
	// failDelete refuses recalls, the way Feishu does past its window.
	failDelete bool
	next       int
}

func newRecorder() *recorder { return &recorder{updates: map[string][]string{}} }

func (r *recorder) SendCard(_ context.Context, cardJSON string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.next++
	id := "om_" + string(rune('0'+r.next))
	r.cards = append(r.cards, cardJSON)
	r.ids = append(r.ids, id)
	return id, nil
}

func (r *recorder) UpdateCard(_ context.Context, messageID, cardJSON string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.updates[messageID] = append(r.updates[messageID], cardJSON)
	return nil
}

func (r *recorder) SendText(_ context.Context, text string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.texts = append(r.texts, text)
	return "om_text", nil
}

func (r *recorder) DeleteMessage(_ context.Context, messageID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failDelete {
		return errors.New("message cannot be recalled")
	}
	r.deleted = append(r.deleted, messageID)
	return nil
}

// titles returns the header title of every card sent, which is how a user
// tells one notification from another at a glance.
func (r *recorder) titles(t *testing.T) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, c := range r.cards {
		out = append(out, cardTitle(t, c))
	}
	return out
}

func cardTitle(t *testing.T, cardJSON string) string {
	t.Helper()
	var m struct {
		Header struct {
			Title struct {
				Content string `json:"content"`
			} `json:"title"`
		} `json:"header"`
	}
	if err := json.Unmarshal([]byte(cardJSON), &m); err != nil {
		t.Fatalf("%v in %s", err, cardJSON)
	}
	return m.Header.Title.Content
}

// link is a session's channel, recording what was pushed into it.
type link struct {
	mu       sync.Mutex
	injected []string
	verdicts []string
	fail     bool
}

func (l *link) Inject(content string, _ map[string]string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.fail {
		return errors.New("no channel")
	}
	l.injected = append(l.injected, content)
	return nil
}

func (l *link) Verdict(requestID, behavior string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.fail {
		return errors.New("no channel")
	}
	l.verdicts = append(l.verdicts, requestID+":"+behavior)
	return nil
}

func (l *link) sent() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.injected...)
}

// private points every path the daemon uses at a directory of the test's
// own, so a test never disturbs the user's running daemon.
//
// It uses a short-named MkdirTemp rather than t.TempDir(): t.TempDir()
// nests the full test name under the OS temp dir, and on macOS that
// combination routinely exceeds the ~104-byte length unix domain sockets
// allow for sun_path, so Listen fails with "bind: invalid argument".
func private(t *testing.T) {
	t.Helper()
	dir, err := os.MkdirTemp("", "wl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Setenv("CLAUDE_COMPANION_STATE_DIR", dir)
}

// fixture builds a daemon with one attached session, wired to a recorder.
func fixture(t *testing.T, remote session.Remote) (*Daemon, *recorder, *link) {
	t.Helper()
	private(t)
	rec := newRecorder()
	d := New(&config.Config{
		Notify: config.NotifyImportant,
		Remote: config.On, RemotePermissions: config.On,
	}, rec, nil, "1.0.0")
	l := &link{}
	d.reg.Attach("sess-1", 4242, "/work/payments-api", remote, l)
	d.reg.Observe(session.Observation{ID: "sess-1", PID: 4242, Dir: "/work/payments-api", Title: "Fix token refresh", HookEvent: hook.EventStop})
	return d, rec, l
}

// hookEvent drives one hook event through the daemon the way the hook
// process would.
func hookEvent(t *testing.T, d *Daemon, event string, extra map[string]any) {
	t.Helper()
	payload := map[string]any{
		"hook_event_name": event,
		"session_id":      "sess-1",
		"cwd":             "/work/payments-api",
	}
	for k, v := range extra {
		payload[k] = v
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	p, err := hook.Decode(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	d.handleHook(context.Background(), p, ipc.Hook{PID: 4242, ProjectDir: "/work/payments-api"})
}

// One session running is not a choice: the message can only have meant
// that one, and the answer names it.
func TestMessageReachesTheOneSessionOnce(t *testing.T) {
	d, rec, l := fixture(t, session.Ready)

	d.onMessage(context.Background(), feishu.Message{Text: "check the mobile client first"})

	if got := l.sent(); len(got) != 1 || got[0] != "check the mobile client first" {
		t.Fatalf("session received %v, want the message exactly once", got)
	}
	if len(rec.texts) != 1 || !strings.Contains(rec.texts[0], "payments-api") {
		t.Errorf("confirmation = %v, want one that names the session", rec.texts)
	}
}

// Two sessions is a choice, and the choice is the user's. The message
// stays put, nothing is guessed at, and the answer says where to make it.
func TestMessageWithTwoSessionsGoesNowhere(t *testing.T) {
	d, rec, l := fixture(t, session.Ready)
	other := &link{}
	d.reg.Attach("sess-2", 5252, "/work/frontend", session.Ready, other)

	d.onMessage(context.Background(), feishu.Message{Text: "ship it"})

	if got := l.sent(); len(got) != 0 {
		t.Errorf("a session received %v with two to choose from", got)
	}
	if got := other.sent(); len(got) != 0 {
		t.Errorf("the other session received %v", got)
	}
	if len(rec.cards) != 0 {
		t.Errorf("cards = %v, want one line rather than a card for every session", rec.titles(t))
	}
	if len(rec.texts) != 1 || !strings.Contains(rec.texts[0], "card of the session you mean") {
		t.Errorf("answer = %v, want it to say where the choice is made", rec.texts)
	}
}

// A message meant for a session that has since ended must never land in
// whatever else happens to be running.
func TestMessageIsNotHandedToAnotherSession(t *testing.T) {
	d, rec, _ := fixture(t, session.Notifications)
	d.reg.Downgrade("sess-1")
	other := &link{}
	d.reg.Attach("sess-2", 5252, "/work/frontend", session.Notifications, other)

	d.onMessage(context.Background(), feishu.Message{Text: "ship it"})

	if got := other.sent(); len(got) != 0 {
		t.Fatalf("the message was redirected to another session: %v", got)
	}
	if len(rec.texts) != 1 || !strings.Contains(rec.texts[0], "without the Claude Companion channel") {
		t.Errorf("answer = %v, want it to say why nothing here can take a message", rec.texts)
	}
}

// The notification a turn ends with must explain its missing reply box for
// the same reason the live card does: an absence says nothing, and the
// user is otherwise left wondering why this card has no box when the last
// one did.
func TestOutcomeOfAnUnreachableSessionSaysWhyItCannotBeAnswered(t *testing.T) {
	d, rec, _ := fixture(t, session.Notifications)

	hookEvent(t, d, hook.EventStop, map[string]any{
		"last_assistant_message": "Rewrote the refresh flow.",
	})

	if len(rec.cards) != 1 {
		t.Fatalf("cards = %v, want the completion", rec.titles(t))
	}
	if strings.Contains(rec.cards[0], "Message this session") {
		t.Errorf("an unreachable session was offered a reply box: %s", rec.cards[0])
	}
	if !strings.Contains(rec.cards[0], "You cannot reply to this session from here") {
		t.Errorf("the completion does not say why it cannot be answered: %s", rec.cards[0])
	}
}

// A session Claude Companion cannot reach is told about honestly rather than being
// sent a message that would vanish.
func TestNotificationsOnlySessionRefusesHonestly(t *testing.T) {
	d, rec, l := fixture(t, session.Notifications)

	d.onMessage(context.Background(), feishu.Message{Text: "ship it"})

	if got := l.sent(); len(got) != 0 {
		t.Fatalf("a message was pushed into an unreachable session: %v", got)
	}
	if len(rec.texts) != 1 || !strings.Contains(rec.texts[0], "without the Claude Companion channel") {
		t.Errorf("answer = %v, want it to say why nothing here can take a message", rec.texts)
	}
}

// The state before the message decides what the answer promises: a session
// mid-turn will not read this until that turn ends, and saying "sent" would
// be a small lie the user would notice.
func TestBusySessionSaysQueued(t *testing.T) {
	d, rec, _ := fixture(t, session.Ready)
	d.reg.Observe(session.Observation{ID: "sess-1", PID: 4242, Dir: "/work/payments-api", HookEvent: hook.EventUserPromptSubmit})

	d.onMessage(context.Background(), feishu.Message{Text: "also check the tests"})

	if len(rec.texts) != 1 || !strings.Contains(rec.texts[0], "Queued") {
		t.Errorf("answer = %v, want it to say the message is queued", rec.texts)
	}
}

func TestOverviewIsShownOnRequest(t *testing.T) {
	d, rec, l := fixture(t, session.Ready)

	d.onMessage(context.Background(), feishu.Message{Text: "sessions"})

	if got := l.sent(); len(got) != 0 {
		t.Errorf("asking for the overview was sent to a session: %v", got)
	}
	if titles := rec.titles(t); len(titles) != 1 || titles[0] != "⚪ Idle" {
		t.Errorf("cards = %v, want the remaining session's card", titles)
	}
}

// A glance does not scroll. Beyond a handful of sessions the recap shows
// the ones that need the user - the registry lists those first - and says
// how many it left out, so nobody counts cards and concludes a session
// vanished.
func TestRecapIsCappedAndSaysSo(t *testing.T) {
	d, rec, _ := fixture(t, session.Ready)
	for i := 2; i <= 8; i++ {
		id := fmt.Sprintf("sess-%d", i)
		d.reg.Attach(id, 5000+i, fmt.Sprintf("/work/p%d", i), session.Ready, &link{})
	}

	d.onMessage(context.Background(), feishu.Message{Text: "sessions"})

	if len(rec.cards) != maxRecapCards {
		t.Fatalf("cards = %d, want the recap capped at %d", len(rec.cards), maxRecapCards)
	}
	if len(rec.texts) != 1 || !strings.Contains(rec.texts[0], "3 quieter sessions are not shown") {
		t.Errorf("texts = %v, want the ones left out accounted for", rec.texts)
	}
}

// One decision, one card - whichever of the two events describing it
// arrives first.
func TestRelayAfterHookRewritesTheSameCard(t *testing.T) {
	d, rec, l := fixture(t, session.Ready)

	hookEvent(t, d, hook.EventPermissionRequest, map[string]any{
		"tool_name":  "Bash",
		"tool_input": map[string]any{"command": "npm install"},
	})
	if len(rec.cards) != 1 {
		t.Fatalf("the hook produced %d cards, want 1", len(rec.cards))
	}
	hookCardID := rec.ids[0]

	d.onPermissionRequest(context.Background(), l, mcp.PermissionRequest{
		RequestID: "abcde", ToolName: "Bash",
		Description: "Install dependencies", InputPreview: `{"command":"npm install"}`,
	})

	if len(rec.cards) != 1 {
		t.Errorf("the relay added a second card for one decision: %v", rec.titles(t))
	}
	rewritten := rec.updates[hookCardID]
	if len(rewritten) != 1 {
		t.Fatalf("the standing card was rewritten %d times, want once", len(rewritten))
	}
	if !strings.Contains(rewritten[0], "Allow once") {
		t.Error("the rewritten card does not offer the buttons the relay made possible")
	}
}

func TestHookAfterRelayAddsNoSecondCard(t *testing.T) {
	d, rec, l := fixture(t, session.Ready)

	d.onPermissionRequest(context.Background(), l, mcp.PermissionRequest{
		RequestID: "abcde", ToolName: "Bash", InputPreview: `{"command":"npm install"}`,
	})
	hookEvent(t, d, hook.EventPermissionRequest, map[string]any{
		"tool_name":  "Bash",
		"tool_input": map[string]any{"command": "npm install"},
	})

	if len(rec.cards) != 1 {
		t.Errorf("one decision produced %d cards: %v", len(rec.cards), rec.titles(t))
	}
	if !strings.Contains(rec.cards[0], "Allow once") {
		t.Error("the card that stands is not the one the user can act on")
	}
}

// With remote approval switched off, the prompt is not relayed at all: the
// hook's own notification is the whole of what the user gets, as in v1.
func TestRelayIsSilentWhenRemoteApprovalIsOff(t *testing.T) {
	d, rec, l := fixture(t, session.Ready)
	d.cfg.RemotePermissions = config.Off

	d.onPermissionRequest(context.Background(), l, mcp.PermissionRequest{
		RequestID: "abcde", ToolName: "Bash", InputPreview: `{"command":"npm install"}`,
	})

	if len(rec.cards) != 0 {
		t.Errorf("a permission card was relayed with remote approval off: %v", rec.titles(t))
	}
}

func TestVerdictReachesTheSessionAndSettlesTheCard(t *testing.T) {
	d, rec, l := fixture(t, session.Ready)
	d.onPermissionRequest(context.Background(), l, mcp.PermissionRequest{
		RequestID: "abcde", ToolName: "Bash", InputPreview: `{"command":"npm install"}`,
	})
	cardID := rec.ids[0]

	value, _ := json.Marshal(notify.Action{
		Kind: notify.ActionPermit, Session: "sess-1", Request: "abcde", Verdict: notify.VerdictAllow,
	})
	d.onCardAction(context.Background(), feishu.CardAction{Value: value, MessageID: cardID})

	l.mu.Lock()
	verdicts := append([]string(nil), l.verdicts...)
	l.mu.Unlock()
	if len(verdicts) != 1 || verdicts[0] != "abcde:allow" {
		t.Fatalf("session received verdicts %v, want abcde:allow once", verdicts)
	}
	if len(rec.deleted) != 1 || rec.deleted[0] != cardID {
		t.Errorf("deleted = %v, want the answered card recalled", rec.deleted)
	}
	if len(rec.updates[cardID]) != 0 {
		t.Errorf("a recalled card was also rewritten: %v", rec.updates[cardID])
	}
}

// When Feishu refuses the recall, the card must still stop asking: it
// settles in place as the decision that was made.
func TestUnrecallableAnsweredCardSettlesInPlace(t *testing.T) {
	d, rec, l := fixture(t, session.Ready)
	rec.failDelete = true
	d.onPermissionRequest(context.Background(), l, mcp.PermissionRequest{
		RequestID: "abcde", ToolName: "Bash", InputPreview: `{"command":"npm install"}`,
	})
	cardID := rec.ids[0]

	value, _ := json.Marshal(notify.Action{
		Kind: notify.ActionPermit, Session: "sess-1", Request: "abcde", Verdict: notify.VerdictAllow,
	})
	d.onCardAction(context.Background(), feishu.CardAction{Value: value, MessageID: cardID})

	settled := rec.updates[cardID]
	if len(settled) != 1 || !strings.Contains(settled[0], "Allowed") {
		t.Errorf("the card settled to %v, want it to say the decision was made", settled)
	}
}

// Tapping twice must not answer twice: Claude Code applies the first answer
// and drops the rest, so the second tap would be a card claiming something
// that did not happen.
func TestASecondTapChangesNothing(t *testing.T) {
	d, rec, l := fixture(t, session.Ready)
	d.onPermissionRequest(context.Background(), l, mcp.PermissionRequest{
		RequestID: "abcde", ToolName: "Bash", InputPreview: `{"command":"npm install"}`,
	})
	cardID := rec.ids[0]
	value, _ := json.Marshal(notify.Action{
		Kind: notify.ActionPermit, Session: "sess-1", Request: "abcde", Verdict: notify.VerdictAllow,
	})

	d.onCardAction(context.Background(), feishu.CardAction{Value: value, MessageID: cardID})
	d.onCardAction(context.Background(), feishu.CardAction{Value: value, MessageID: cardID})

	l.mu.Lock()
	n := len(l.verdicts)
	l.mu.Unlock()
	if n != 1 {
		t.Errorf("the session received %d verdicts, want 1", n)
	}
	if got := len(rec.deleted); got != 1 {
		t.Errorf("the card was recalled %d times, want once", got)
	}
}

// Claude Code says nothing when the terminal answers first, so the proof is
// the session getting on with its work. A card left asking is worse than no
// card at all.
func TestALocalAnswerSettlesTheStandingCard(t *testing.T) {
	d, rec, l := fixture(t, session.Ready)
	d.onPermissionRequest(context.Background(), l, mcp.PermissionRequest{
		RequestID: "abcde", ToolName: "Bash", InputPreview: `{"command":"npm install"}`,
	})
	cardID := rec.ids[0]

	hookEvent(t, d, hook.EventStop, map[string]any{"last_assistant_message": "Installed."})

	if len(rec.deleted) != 1 || rec.deleted[0] != cardID {
		t.Errorf("deleted = %v, want the locally answered card recalled", rec.deleted)
	}
}

// A session that swallowed a message is not offered again, and the user is
// told rather than left assuming it arrived.
func TestAnUndeliverableMessageIsReportedAndDowngraded(t *testing.T) {
	d, rec, l := fixture(t, session.Unconfirmed)
	l.fail = true

	d.onMessage(context.Background(), feishu.Message{Text: "ship it"})

	if len(rec.texts) != 1 || !strings.Contains(rec.texts[0], "not delivered") {
		t.Errorf("answer = %v, want it to say the message did not arrive", rec.texts)
	}
	s, _ := d.reg.Get("sess-1")
	if s.Remote.Continuable() {
		t.Error("a session that refused a message is still being offered")
	}
}

// The message going out is not the message arriving. A session that never
// registered the channel takes the write and drops it in silence, and the
// user has to be told rather than left believing Claude read it.
func TestAMessageThatNeverReachesTheSessionIsReported(t *testing.T) {
	d, rec, _ := fixture(t, session.Unconfirmed)
	path := observable(t, d)
	d.reg.Observe(session.Observation{ID: "sess-1", Transcript: path, HookEvent: hook.EventStop})

	d.onMessage(context.Background(), feishu.Message{Text: "ship it"})

	// The session goes on with its own work, which used to be taken for
	// proof that it had heard.
	hookEvent(t, d, hook.EventPostToolUse, map[string]any{"transcript_path": path, "tool_name": "Read"})
	appendLines(t, path, turnPrompt)
	d.expireDeliveries(context.Background())
	if len(rec.texts) != 1 {
		t.Fatalf("answers = %v, want only the confirmation while the turn is still being written", rec.texts)
	}

	// Quiet now, and the message is still not in the transcript.
	d.mu.Lock()
	d.awaiting["sess-1"].grewAt = time.Now().Add(-2 * queuedProof)
	d.mu.Unlock()
	d.expireDeliveries(context.Background())

	if len(rec.texts) != 2 || !strings.Contains(rec.texts[1], "not delivered") {
		t.Fatalf("answers = %v, want it to say the message did not arrive", rec.texts)
	}
	if !strings.Contains(rec.texts[1], "server:"+mcp.ServerName) {
		t.Errorf("answer = %q, want it to name the flag that fixes this", rec.texts[1])
	}
	if s, _ := d.reg.Get("sess-1"); s.Remote.Continuable() {
		t.Error("a session that swallowed a message is still being offered")
	}
}

// A session that was resting reads a message at once or not at all, so
// waiting a minute and a half to say so would be a minute and a half of
// the user believing Claude is on it.
func TestARestingSessionIsGivenUpOnQuickly(t *testing.T) {
	d, rec, _ := fixture(t, session.Unconfirmed)
	path := observable(t, d)
	d.reg.Observe(session.Observation{ID: "sess-1", Transcript: path, HookEvent: hook.EventStop})

	d.onMessage(context.Background(), feishu.Message{Text: "ship it"})

	d.mu.Lock()
	d.awaiting["sess-1"].grewAt = time.Now().Add(-idleProof / 2)
	d.mu.Unlock()
	d.expireDeliveries(context.Background())
	if len(rec.texts) != 1 {
		t.Fatalf("answers = %v, want nothing said this soon", rec.texts)
	}

	d.mu.Lock()
	d.awaiting["sess-1"].grewAt = time.Now().Add(-idleProof - time.Second)
	d.mu.Unlock()
	d.expireDeliveries(context.Background())
	if len(rec.texts) != 2 || !strings.Contains(rec.texts[1], "not delivered") {
		t.Errorf("answers = %v, want the message reported as undelivered", rec.texts)
	}
}

// A message Claude actually read is in the transcript, and nothing more is
// said about it.
func TestAMessageInTheTranscriptIsNotReportedLost(t *testing.T) {
	d, rec, _ := fixture(t, session.Unconfirmed)
	path := observable(t, d)
	d.reg.Observe(session.Observation{ID: "sess-1", Transcript: path, HookEvent: hook.EventStop})

	d.onMessage(context.Background(), feishu.Message{Text: "ship it"})
	appendLines(t, path, `{"type":"user","message":{"role":"user","content":"<channel source=\"`+
		mcp.ServerName+`\" project=\"payments-api\">\nship it\n</channel>"}}`+"\n")

	d.mu.Lock()
	d.awaiting["sess-1"].grewAt = time.Now().Add(-2 * queuedProof)
	d.mu.Unlock()
	d.expireDeliveries(context.Background())

	if len(rec.texts) != 1 || strings.Contains(rec.texts[0], "not delivered") {
		t.Errorf("answers = %v, want only the confirmation that it was sent", rec.texts)
	}
	if s, _ := d.reg.Get("sess-1"); !s.Remote.Continuable() {
		t.Error("a session that read the message is no longer offered")
	}
}

// Cards for a continuable session carry the box that makes the whole loop
// work: read the outcome, then keep going without leaving Feishu.
func TestCompletionOffersToContinue(t *testing.T) {
	d, rec, _ := fixture(t, session.Ready)

	hookEvent(t, d, hook.EventStop, map[string]any{
		"last_assistant_message": "Added refresh-token rotation.",
	})

	if len(rec.cards) != 1 {
		t.Fatalf("cards = %v, want one completion", rec.titles(t))
	}
	if !strings.Contains(rec.cards[0], notify.ActionSay) {
		t.Error("the completion card offers no way to continue the session")
	}
}

// A session that cannot receive messages must not be offered a button that
// would only lead to a refusal.
func TestCompletionOffersNoContinueForUnreachableSessions(t *testing.T) {
	d, rec, _ := fixture(t, session.Notifications)

	hookEvent(t, d, hook.EventStop, map[string]any{
		"last_assistant_message": "Added refresh-token rotation.",
	})

	if len(rec.cards) != 1 {
		t.Fatalf("cards = %v, want one completion", rec.titles(t))
	}
	if strings.Contains(rec.cards[0], notify.ActionSay) {
		t.Error("an unreachable session was offered as continuable")
	}
}

// The reply box on a card is the shortest way to continue a session: what
// the user types reaches that session, and that session becomes the one
// they are talking to.
//
// Nothing is said in the conversation about it. The card the user typed
// into is the card that answers them, and a chat line repeating what that
// card now shows would be a second message for one action.
func TestCardReplyReachesTheSessionItNames(t *testing.T) {
	d, rec, l := fixture(t, session.Ready)

	value, _ := json.Marshal(notify.Action{Kind: notify.ActionSay, Session: "sess-1"})
	d.onCardAction(context.Background(), feishu.CardAction{
		Value:     value,
		Input:     "check the mobile client first",
		MessageID: "om_card",
	})

	if got := l.sent(); len(got) != 1 || got[0] != "check the mobile client first" {
		t.Fatalf("session received %v, want what was typed on the card", got)
	}
	if len(rec.texts) != 0 {
		t.Errorf("answers = %v, want the card to be the whole answer", rec.texts)
	}
}

// The one thing the card cannot show is a message waiting behind work
// already running: it is honestly describing that work. So that, and only
// that, is still said out loud.
func TestCardReplyBehindARunningTurnSaysItIsQueued(t *testing.T) {
	d, rec, _ := fixture(t, session.Ready)
	d.reg.Observe(session.Observation{ID: "sess-1", PID: 4242, Dir: "/work/payments-api", HookEvent: hook.EventUserPromptSubmit})

	value, _ := json.Marshal(notify.Action{Kind: notify.ActionSay, Session: "sess-1"})
	d.onCardAction(context.Background(), feishu.CardAction{
		Value: value, Input: "also check the tests", MessageID: "om_card",
	})

	if len(rec.texts) != 1 || !strings.Contains(rec.texts[0], "Queued") {
		t.Errorf("answers = %v, want it to say the message is queued", rec.texts)
	}
}

// An empty box submitted by accident is not a message.
func TestEmptyCardReplySendsNothing(t *testing.T) {
	d, rec, l := fixture(t, session.Ready)

	value, _ := json.Marshal(notify.Action{Kind: notify.ActionSay, Session: "sess-1"})
	d.onCardAction(context.Background(), feishu.CardAction{Value: value, Input: ""})

	if got := l.sent(); len(got) != 0 {
		t.Errorf("session received %v, want nothing", got)
	}
	if len(rec.texts) != 0 {
		t.Errorf("answers = %v, want none", rec.texts)
	}
}

// A session that ended must leave the registry, so nothing goes on
// offering it as somewhere a message could be sent.
func TestSessionEndRemovesTheSession(t *testing.T) {
	d, rec, _ := fixture(t, session.Ready)

	hookEvent(t, d, hook.EventSessionEnd, nil)

	if got := len(d.reg.List()); got != 0 {
		t.Errorf("registry holds %d sessions after SessionEnd, want 0", got)
	}
	if got := d.reg.Continuable(); len(got) != 0 {
		t.Errorf("an ended session is still offered: %v", got)
	}
	if len(rec.cards) != 0 || len(rec.texts) != 0 {
		t.Errorf("SessionEnd notified the user: cards=%v texts=%v", rec.titles(t), rec.texts)
	}
}

// The lifecycle events exist for what Claude Companion knows, not for the
// phone.
func TestLifecycleEventsAreSilent(t *testing.T) {
	d, rec, _ := fixture(t, session.Ready)

	hookEvent(t, d, hook.EventSessionStart, nil)
	hookEvent(t, d, hook.EventUserPromptSubmit, nil)

	if len(rec.cards) != 0 || len(rec.texts) != 0 {
		t.Errorf("lifecycle events produced messages: cards=%v texts=%v", rec.titles(t), rec.texts)
	}
	s, _ := d.reg.Get("sess-1")
	if s.State != session.Working {
		t.Errorf("state = %q, want Claude Companion to know the session is working", s.State)
	}
}

// /clear renames a session while its channel keeps running. The user's
// selection has to follow it, or their next message goes nowhere.
func TestClearedSessionKeepsItsChannelAndSelection(t *testing.T) {
	d, _, l := fixture(t, session.Ready)

	// A hook arrives under a new session id from the same claude process.
	payload, err := json.Marshal(map[string]any{
		"hook_event_name": hook.EventUserPromptSubmit,
		"session_id":      "sess-cleared",
		"cwd":             "/work/payments-api",
	})
	if err != nil {
		t.Fatal(err)
	}
	p, err := hook.Decode(strings.NewReader(string(payload)))
	if err != nil {
		t.Fatal(err)
	}
	d.handleHook(context.Background(), p, ipc.Hook{PID: 4242, ProjectDir: "/work/payments-api"})

	d.onMessage(context.Background(), feishu.Message{Text: "carry on"})

	if got := l.sent(); len(got) != 1 || got[0] != "carry on" {
		t.Errorf("the cleared session received %v, want the message to have followed it", got)
	}
}

// The same mistake, one layer up: a card must name the session the event
// came from, whatever the daemon's own environment says.
func TestCardsNameTheEventsOwnProject(t *testing.T) {
	t.Setenv("CLAUDE_PROJECT_DIR", "/home/user/some-other-project")
	d, rec, _ := fixture(t, session.Ready)

	hookEvent(t, d, hook.EventStop, map[string]any{"last_assistant_message": "Done."})

	if len(rec.cards) != 1 {
		t.Fatalf("cards = %v, want one completion", rec.titles(t))
	}
	if !strings.Contains(rec.cards[0], "payments-api") {
		t.Errorf("the card does not name the session's own project: %s", rec.cards[0])
	}
	if strings.Contains(rec.cards[0], "some-other-project") {
		t.Errorf("the card named the daemon's environment instead: %s", rec.cards[0])
	}
}

// An answered decision must not stay on the books forever. The session
// getting on with its work is the moment a second tap stops being possible
// anyway, so it is the moment to forget the whole thing.
func TestAnsweredPromptsAreForgotten(t *testing.T) {
	d, rec, l := fixture(t, session.Ready)
	d.onPermissionRequest(context.Background(), l, mcp.PermissionRequest{
		RequestID: "abcde", ToolName: "Bash", InputPreview: `{"command":"npm install"}`,
	})
	value, _ := json.Marshal(notify.Action{
		Kind: notify.ActionPermit, Session: "sess-1", Request: "abcde", Verdict: notify.VerdictAllow,
	})
	d.onCardAction(context.Background(), feishu.CardAction{Value: value, MessageID: rec.ids[0]})

	hookEvent(t, d, hook.EventStop, map[string]any{"last_assistant_message": "Installed."})

	d.mu.Lock()
	prompts, sessions := len(d.byRequest), len(d.bySession)
	d.mu.Unlock()
	if prompts != 0 || sessions != 0 {
		t.Errorf("the daemon still holds %d prompts and %d sessions' cards", prompts, sessions)
	}
}

// A bare number used to pick a session out of a numbered list. Nothing
// numbers sessions any more, so it is an ordinary thing to say to Claude
// again - and with one session running it goes there like any other
// message.
func TestABareNumberIsJustAMessage(t *testing.T) {
	d, _, l := fixture(t, session.Ready)

	d.onMessage(context.Background(), feishu.Message{Text: "2"})

	if got := l.sent(); len(got) != 1 || got[0] != "2" {
		t.Errorf("session received %v, want the message delivered as typed", got)
	}
}

// A permission answered by typing must reach the session exactly as a tap
// would, and settle the card the same way.
func TestPermissionCanBeAnsweredByTyping(t *testing.T) {
	d, rec, l := fixture(t, session.Ready)
	d.onPermissionRequest(context.Background(), l, mcp.PermissionRequest{
		RequestID: "abcde", ToolName: "Bash", InputPreview: `{"command":"npm install"}`,
	})
	cardID := rec.ids[0]

	d.onMessage(context.Background(), feishu.Message{Text: "y abcde"})

	l.mu.Lock()
	verdicts := append([]string(nil), l.verdicts...)
	injected := append([]string(nil), l.injected...)
	l.mu.Unlock()
	if len(verdicts) != 1 || verdicts[0] != "abcde:allow" {
		t.Fatalf("verdicts = %v, want abcde:allow", verdicts)
	}
	if len(injected) != 0 {
		t.Errorf("the answer was also pushed into the session as a message: %v", injected)
	}
	if len(rec.deleted) != 1 || rec.deleted[0] != cardID {
		t.Errorf("deleted = %v, want the answered card recalled", rec.deleted)
	}
}

// The permission card must spell out the typed form, because that is the
// one that works when the buttons do not.
func TestPermissionCardTeachesTheTypedAnswer(t *testing.T) {
	d, rec, l := fixture(t, session.Ready)

	d.onPermissionRequest(context.Background(), l, mcp.PermissionRequest{
		RequestID: "abcde", ToolName: "Bash", InputPreview: `{"command":"npm install"}`,
	})

	if len(rec.cards) != 1 {
		t.Fatalf("cards = %v", rec.titles(t))
	}
	if !strings.Contains(rec.cards[0], "y abcde") || !strings.Contains(rec.cards[0], "n abcde") {
		t.Errorf("the card does not say how to answer by typing: %s", rec.cards[0])
	}
}

// The ordinary case must not be swallowed by the reply forms. Each of
// these begins with, or contains, a word that means something to Claude
// Companion; all of them are things a person says to Claude.
func TestOrdinaryMessagesAreNotMistakenForCommands(t *testing.T) {
	d, _, l := fixture(t, session.Ready)

	texts := []string{
		"yes, go ahead and do that",
		"no, use the other approach",
		"check whether 1 is off by one",
		"interrupt the build if it hangs",
		"watch the integration tests and report back",
		"sessions are getting dropped under load - find out why",
	}
	for _, text := range texts {
		d.onMessage(context.Background(), feishu.Message{Text: text})
	}

	got := l.sent()
	if len(got) != len(texts) {
		t.Fatalf("the session received %v, want all %d messages", got, len(texts))
	}
}
