package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/marcelritzschke/wirelark/internal/config"
	"github.com/marcelritzschke/wirelark/internal/feishu"
	"github.com/marcelritzschke/wirelark/internal/hook"
	"github.com/marcelritzschke/wirelark/internal/ipc"
	"github.com/marcelritzschke/wirelark/internal/mcp"
	"github.com/marcelritzschke/wirelark/internal/notify"
	"github.com/marcelritzschke/wirelark/internal/session"
)

// recorder stands in for Feishu: it keeps what the user would have seen.
type recorder struct {
	mu      sync.Mutex
	cards   []string
	ids     []string
	updates map[string][]string
	texts   []string
	next    int
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

// fixture builds a daemon with one attached session, wired to a recorder.
func fixture(t *testing.T, remote session.Remote) (*Daemon, *recorder, *link) {
	t.Helper()
	t.Setenv("WIRELARK_STATE_DIR", t.TempDir())
	rec := newRecorder()
	d := New(&config.Config{
		Notify: config.NotifyImportant, Detail: config.DetailNormal,
		Remote: config.On, RemotePermissions: config.On,
	}, rec, nil)
	l := &link{}
	d.reg.Attach("sess-1", 4242, "/work/payments-api", remote, l)
	d.reg.Observe(session.Observation{ID: "sess-1", PID: 4242, Dir: "/work/payments-api", Title: "Fix token refresh", HookEvent: hook.EventStop})
	return d, rec, l
}

func selectSession(t *testing.T, d *Daemon, id string) {
	t.Helper()
	if _, ok := d.reg.Select(id); !ok {
		t.Fatalf("could not select %s", id)
	}
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

func TestMessageReachesTheSelectedSessionOnce(t *testing.T) {
	d, rec, l := fixture(t, session.Ready)
	selectSession(t, d, "sess-1")

	d.onMessage(context.Background(), feishu.Message{Text: "check the mobile client first"})

	if got := l.sent(); len(got) != 1 || got[0] != "check the mobile client first" {
		t.Fatalf("session received %v, want the message exactly once", got)
	}
	if len(rec.texts) != 1 || !strings.Contains(rec.texts[0], "payments-api") {
		t.Errorf("confirmation = %v, want one that names the session", rec.texts)
	}
}

// The user must always know where their message went, and a message must
// never go somewhere they did not choose.
func TestMessageWithoutASelectionGoesNowhere(t *testing.T) {
	d, rec, l := fixture(t, session.Ready)

	d.onMessage(context.Background(), feishu.Message{Text: "ship it"})

	if got := l.sent(); len(got) != 0 {
		t.Fatalf("the session received %v with nothing selected", got)
	}
	if titles := rec.titles(t); len(titles) != 1 || titles[0] != "Wirelark" {
		t.Errorf("cards = %v, want the overview so the user can pick", titles)
	}
}

// A selection that ended is not quietly replaced by another live session.
func TestMessageAfterTheSelectedSessionEndedGoesNowhere(t *testing.T) {
	d, rec, l := fixture(t, session.Ready)
	other := &link{}
	d.reg.Attach("sess-2", 5252, "/work/frontend", session.Ready, other)
	selectSession(t, d, "sess-1")
	d.reg.Remove("sess-1")

	d.onMessage(context.Background(), feishu.Message{Text: "ship it"})

	if got := other.sent(); len(got) != 0 {
		t.Fatalf("the message was redirected to another session: %v", got)
	}
	if got := l.sent(); len(got) != 0 {
		t.Fatalf("the ended session received %v", got)
	}
	if titles := rec.titles(t); len(titles) != 1 || titles[0] != "Wirelark" {
		t.Errorf("cards = %v, want the overview", titles)
	}
}

// A session Wirelark cannot reach is told about honestly rather than being
// sent a message that would vanish.
func TestNotificationsOnlySessionRefusesHonestly(t *testing.T) {
	d, rec, l := fixture(t, session.Notifications)
	selectSession(t, d, "sess-1")

	d.onMessage(context.Background(), feishu.Message{Text: "ship it"})

	if got := l.sent(); len(got) != 0 {
		t.Fatalf("a message was pushed into an unreachable session: %v", got)
	}
	if len(rec.texts) != 1 || !strings.Contains(rec.texts[0], "notifications") {
		t.Errorf("answer = %v, want it to say the session can only send notifications", rec.texts)
	}
}

// The state before the message decides what the answer promises: a session
// mid-turn will not read this until that turn ends, and saying "sent" would
// be a small lie the user would notice.
func TestBusySessionSaysQueued(t *testing.T) {
	d, rec, _ := fixture(t, session.Ready)
	d.reg.Observe(session.Observation{ID: "sess-1", PID: 4242, Dir: "/work/payments-api", HookEvent: hook.EventUserPromptSubmit})
	selectSession(t, d, "sess-1")

	d.onMessage(context.Background(), feishu.Message{Text: "also check the tests"})

	if len(rec.texts) != 1 || !strings.Contains(rec.texts[0], "Queued") {
		t.Errorf("answer = %v, want it to say the message is queued", rec.texts)
	}
}

func TestOverviewIsShownOnRequest(t *testing.T) {
	d, rec, l := fixture(t, session.Ready)
	selectSession(t, d, "sess-1")

	d.onMessage(context.Background(), feishu.Message{Text: "sessions"})

	if got := l.sent(); len(got) != 0 {
		t.Errorf("asking for the overview was sent to a session: %v", got)
	}
	if titles := rec.titles(t); len(titles) != 1 || titles[0] != "Wirelark" {
		t.Errorf("cards = %v, want the overview", titles)
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
	if got := len(rec.updates[cardID]); got != 1 {
		t.Errorf("the card was rewritten %d times, want once", got)
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

	settled := rec.updates[cardID]
	if len(settled) != 1 || !strings.Contains(settled[0], "handled in Claude Code") {
		t.Errorf("the card settled to %v, want it to say the answer came from the terminal", settled)
	}
}

// A session that swallowed a message is not offered again, and the user is
// told rather than left assuming it arrived.
func TestAnUndeliverableMessageIsReportedAndDowngraded(t *testing.T) {
	d, rec, l := fixture(t, session.Unconfirmed)
	l.fail = true
	selectSession(t, d, "sess-1")

	d.onMessage(context.Background(), feishu.Message{Text: "ship it"})

	if len(rec.texts) != 1 || !strings.Contains(rec.texts[0], "not delivered") {
		t.Errorf("answer = %v, want it to say the message did not arrive", rec.texts)
	}
	s, _ := d.reg.Get("sess-1")
	if s.Remote.Continuable() {
		t.Error("a session that refused a message is still being offered")
	}
}

// Cards for a continuable session carry the button that makes the whole
// loop work: read the outcome, then keep going without leaving Feishu.
func TestCompletionOffersToContinue(t *testing.T) {
	d, rec, _ := fixture(t, session.Ready)

	hookEvent(t, d, hook.EventStop, map[string]any{
		"last_assistant_message": "Added refresh-token rotation.",
	})

	if len(rec.cards) != 1 {
		t.Fatalf("cards = %v, want one completion", rec.titles(t))
	}
	if !strings.Contains(rec.cards[0], notify.ActionSelect) {
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
	if strings.Contains(rec.cards[0], notify.ActionSelect) {
		t.Error("an unreachable session was offered as continuable")
	}
}

func TestSelectingASessionConfirmsWhichOne(t *testing.T) {
	d, rec, _ := fixture(t, session.Ready)

	value, _ := json.Marshal(notify.Action{Kind: notify.ActionSelect, Session: "sess-1"})
	d.onCardAction(context.Background(), feishu.CardAction{Value: value})

	if titles := rec.titles(t); len(titles) != 1 || titles[0] != "payments-api" {
		t.Fatalf("cards = %v, want a confirmation naming the session", titles)
	}
	s, ok := d.reg.Selected()
	if !ok || s.ID != "sess-1" {
		t.Errorf("selection = %+v, want sess-1", s)
	}
}

// A session that ended must leave the overview, and must not stay selected
// as somewhere a message could still be sent.
func TestSessionEndRemovesTheSession(t *testing.T) {
	d, rec, _ := fixture(t, session.Ready)
	selectSession(t, d, "sess-1")

	hookEvent(t, d, hook.EventSessionEnd, nil)

	if got := len(d.reg.List()); got != 0 {
		t.Errorf("registry holds %d sessions after SessionEnd, want 0", got)
	}
	if _, ok := d.reg.Selected(); ok {
		t.Error("a session that ended is still selected")
	}
	if len(rec.cards) != 0 || len(rec.texts) != 0 {
		t.Errorf("SessionEnd notified the user: cards=%v texts=%v", rec.titles(t), rec.texts)
	}
}

// The lifecycle events exist for the overview, not for the phone.
func TestLifecycleEventsAreSilent(t *testing.T) {
	d, rec, _ := fixture(t, session.Ready)

	hookEvent(t, d, hook.EventSessionStart, nil)
	hookEvent(t, d, hook.EventUserPromptSubmit, nil)

	if len(rec.cards) != 0 || len(rec.texts) != 0 {
		t.Errorf("lifecycle events produced messages: cards=%v texts=%v", rec.titles(t), rec.texts)
	}
	s, _ := d.reg.Get("sess-1")
	if s.State != session.Working {
		t.Errorf("state = %q, want the overview to know the session is working", s.State)
	}
}

// /clear renames a session while its channel keeps running. The user's
// selection has to follow it, or their next message goes nowhere.
func TestClearedSessionKeepsItsChannelAndSelection(t *testing.T) {
	d, _, l := fixture(t, session.Ready)
	selectSession(t, d, "sess-1")

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

// Card callbacks are a separate Feishu subscription from card delivery, and
// an app can send perfectly good cards whose every button is inert. Picking
// a session by typing its number has to work on its own.
func TestSessionCanBePickedByTyping(t *testing.T) {
	d, _, l := fixture(t, session.Ready)
	other := &link{}
	d.reg.Attach("sess-2", 5252, "/work/frontend", session.Ready, other)

	d.onMessage(context.Background(), feishu.Message{Text: "sessions"})
	d.onMessage(context.Background(), feishu.Message{Text: "1"})
	d.onMessage(context.Background(), feishu.Message{Text: "carry on"})

	first := d.reg.List()[0]
	got, ignored := l.sent(), other.sent()
	if first.ID == "sess-2" {
		got, ignored = other.sent(), l.sent()
	}
	if len(got) != 1 || got[0] != "carry on" {
		t.Errorf("the picked session received %v, want the message", got)
	}
	if len(ignored) != 0 {
		t.Errorf("the other session received %v", ignored)
	}
}

// A number is resolved against the overview the user was looking at. If
// that session has since ended, being told so is right; silently landing on
// whatever now occupies that slot is not.
func TestAStaleNumberPicksNothing(t *testing.T) {
	d, rec, l := fixture(t, session.Ready)
	d.onMessage(context.Background(), feishu.Message{Text: "sessions"})

	d.reg.Remove("sess-1")
	replacement := &link{}
	d.reg.Attach("sess-2", 5252, "/work/frontend", session.Ready, replacement)

	d.onMessage(context.Background(), feishu.Message{Text: "1"})
	d.onMessage(context.Background(), feishu.Message{Text: "carry on"})

	if got := replacement.sent(); len(got) != 0 {
		t.Errorf("a stale number reached a different session: %v", got)
	}
	if got := l.sent(); len(got) != 0 {
		t.Errorf("the ended session received %v", got)
	}
	if len(rec.texts) == 0 || !strings.Contains(rec.texts[0], "has ended") {
		t.Errorf("answers = %v, want the user told the session is gone", rec.texts)
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
	if settled := rec.updates[cardID]; len(settled) != 1 || !strings.Contains(settled[0], "Allowed") {
		t.Errorf("the card settled to %v", settled)
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

// The ordinary case must not be swallowed by the reply forms: a message
// that merely mentions a session goes to the selected one, as text.
func TestOrdinaryMessagesAreNotMistakenForCommands(t *testing.T) {
	d, _, l := fixture(t, session.Ready)
	d.onMessage(context.Background(), feishu.Message{Text: "sessions"})
	d.onMessage(context.Background(), feishu.Message{Text: "1"})

	for _, text := range []string{
		"yes, go ahead and do that",
		"no, use the other approach",
		"check whether 1 is off by one",
	} {
		d.onMessage(context.Background(), feishu.Message{Text: text})
	}

	got := l.sent()
	if len(got) != 3 {
		t.Fatalf("the session received %v, want all three messages", got)
	}
}
