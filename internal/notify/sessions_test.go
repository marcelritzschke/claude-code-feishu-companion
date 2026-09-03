package notify

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/marcelritzschke/claude-code-feishu-companion/internal/mcp"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/session"
)

// buttonsOf returns the label and value of every button on a card.
func buttonsOf(t *testing.T, cardJSON string) []Button {
	t.Helper()
	var m struct {
		Elements []struct {
			Tag     string `json:"tag"`
			Actions []struct {
				Text struct {
					Content string `json:"content"`
				} `json:"text"`
				Type  string `json:"type"`
				Value Action `json:"value"`
			} `json:"actions"`
		} `json:"elements"`
	}
	if err := json.Unmarshal([]byte(cardJSON), &m); err != nil {
		t.Fatalf("%v in %s", err, cardJSON)
	}
	var out []Button
	for _, el := range m.Elements {
		if el.Tag != "action" {
			continue
		}
		for _, a := range el.Actions {
			out = append(out, Button{Label: a.Text.Content, Style: a.Type, Action: a.Value})
		}
	}
	return out
}

func TestOverviewCard(t *testing.T) {
	sessions := []session.Session{
		{ID: "s1", Dir: "/work/frontend", Title: "Upgrade React", State: session.Waiting, Remote: session.Ready},
		{ID: "s2", Dir: "/work/payments-api", Title: "Fix token refresh", State: session.Working, Remote: session.Ready},
		{ID: "s3", Dir: "/work/claude-companion", State: session.Idle, Remote: session.Notifications},
	}
	card, err := OverviewCard(sessions)
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"frontend", "Upgrade React", "Waiting for you", "Remote ready",
		"payments-api", "Working", "claude-companion", "Idle", "Notifications only"} {
		if !strings.Contains(card, want) {
			t.Errorf("overview is missing %q: %s", want, card)
		}
	}

	// A button for a session that cannot receive messages would only lead
	// to a refusal, so it is not offered.
	buttons := buttonsOf(t, card)
	if len(buttons) != 2 {
		t.Fatalf("overview offers %d buttons, want one per continuable session", len(buttons))
	}
	for _, b := range buttons {
		if b.Action.Kind != ActionSelect || b.Action.Session == "s3" {
			t.Errorf("button = %+v", b)
		}
	}

	// Card callbacks are a separate Feishu subscription from card delivery,
	// so the overview has to be usable by typing as well as by tapping.
	// The numbers must run 1..n over exactly the sessions that are offered.
	if !strings.Contains(card, "1. frontend") || !strings.Contains(card, "2. payments-api") {
		t.Errorf("continuable sessions are not numbered for a typed reply: %s", card)
	}
	if strings.Contains(card, "3. claude-companion") {
		t.Errorf("a session that cannot be continued was given a number: %s", card)
	}
	if !strings.Contains(card, "reply with its number") {
		t.Errorf("the overview does not say a number can be typed: %s", card)
	}
}

// The overview must never leak the identifiers Claude Companion works with.
func TestOverviewShowsNoTechnicalIdentifiers(t *testing.T) {
	card, err := OverviewCard([]session.Session{
		{ID: "0198c0de-cafe-7000-a1b2-0123456789ab", PID: 4242,
			Dir: "/work/payments-api", State: session.Working, Remote: session.Ready},
	})
	if err != nil {
		t.Fatal(err)
	}
	// The session id travels in the button value, which is not shown; what
	// the user reads must carry neither it nor the process id.
	var m struct {
		Elements []struct {
			Tag  string `json:"tag"`
			Text *struct {
				Content string `json:"content"`
			} `json:"text"`
		} `json:"elements"`
	}
	if err := json.Unmarshal([]byte(card), &m); err != nil {
		t.Fatal(err)
	}
	for _, el := range m.Elements {
		if el.Text == nil {
			continue
		}
		if strings.Contains(el.Text.Content, "0198c0de") || strings.Contains(el.Text.Content, "4242") {
			t.Errorf("the overview shows a technical identifier: %q", el.Text.Content)
		}
	}
}

func TestOverviewWithNoSessions(t *testing.T) {
	card, err := OverviewCard(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(card, "No Claude Code sessions are running") {
		t.Errorf("empty overview = %s", card)
	}
	if len(buttonsOf(t, card)) != 0 {
		t.Error("the empty overview offers something to pick")
	}
}

func TestSelectedCardSaysWhereMessagesGo(t *testing.T) {
	card, err := SelectedCard(session.Session{
		ID: "s1", Dir: "/work/payments-api", Title: "Fix token refresh",
		State: session.Idle, Remote: session.Ready,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"payments-api", "Fix token refresh", "Send a message here"} {
		if !strings.Contains(card, want) {
			t.Errorf("selection card is missing %q: %s", want, card)
		}
	}
}

func TestSelectedCardIsHonestAboutUnreachableSessions(t *testing.T) {
	card, err := SelectedCard(session.Session{
		ID: "s1", Dir: "/work/claude-companion", State: session.Idle, Remote: session.Notifications,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(card, "Send a message here") {
		t.Error("a session that cannot receive messages was offered as if it could")
	}
	if !strings.Contains(card, "only send you notifications") {
		t.Errorf("selection card = %s", card)
	}
}

func TestPermissionRelayCardOffersBothAnswers(t *testing.T) {
	card, err := PermissionRelayCard(
		session.Session{ID: "s1", Dir: "/work/payments-api", Title: "Fix token refresh"},
		mcp.PermissionRequest{RequestID: "abcde", ToolName: "Bash",
			Description: "Install dependencies", InputPreview: `{"command":"npm install"}`})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(card, "npm install") {
		t.Errorf("the command being approved is not on the card: %s", card)
	}
	if !strings.Contains(card, "payments-api") {
		t.Errorf("the card does not say which session is asking: %s", card)
	}

	buttons := buttonsOf(t, card)
	if len(buttons) != 2 {
		t.Fatalf("permission card offers %d buttons, want allow and deny", len(buttons))
	}
	for _, b := range buttons {
		if b.Action.Request != "abcde" || b.Action.Kind != ActionPermit {
			t.Errorf("button %q carries %+v", b.Label, b.Action)
		}
	}
	if buttons[0].Action.Verdict != VerdictAllow || buttons[1].Action.Verdict != VerdictDeny {
		t.Errorf("buttons = %+v, want allow then deny for an ordinary action", buttons)
	}
}

// A tap is quicker than reading a terminal dialog, so an action that cannot
// be undone has to look different and must not offer allow as the easy one.
func TestHighRiskPermissionCardChangesShape(t *testing.T) {
	ordinary, err := PermissionRelayCard(session.Session{ID: "s1", Dir: "/work/api"},
		mcp.PermissionRequest{RequestID: "abcde", ToolName: "Bash", InputPreview: `{"command":"npm install"}`})
	if err != nil {
		t.Fatal(err)
	}
	risky, err := PermissionRelayCard(session.Session{ID: "s1", Dir: "/work/api"},
		mcp.PermissionRequest{RequestID: "abcde", ToolName: "Bash",
			InputPreview: `{"command":"rm -rf ./build && sudo make install"}`})
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(risky, `"template":"red"`) {
		t.Error("a destructive action does not stand out from an ordinary one")
	}
	if strings.Contains(ordinary, `"template":"red"`) {
		t.Error("an ordinary install was flagged as destructive")
	}

	buttons := buttonsOf(t, risky)
	if buttons[0].Style == stylePrimary {
		t.Error("a destructive action still offers Allow as the emphasised answer")
	}
	if !strings.Contains(risky, "cannot easily be undone") {
		t.Errorf("the card does not say why it looks different: %s", risky)
	}
}

// The user cannot approve what they cannot read, so a permission card is
// allowed far more of the command than any other card shows.
func TestPermissionCardShowsTheWholeCommand(t *testing.T) {
	command := "go test ./internal/... -run TestSomethingWithAnExtremelyLongName -count 1 -race -v"
	card, err := PermissionRelayCard(session.Session{ID: "s1", Dir: "/work/api"},
		mcp.PermissionRequest{RequestID: "abcde", ToolName: "Bash",
			InputPreview: `{"command":"` + command + `"}`})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(card, command) {
		t.Errorf("the command was cut short on the card: %s", card)
	}
}

func TestPermissionAnsweredCardStates(t *testing.T) {
	req := mcp.PermissionRequest{RequestID: "abcde", ToolName: "Bash", InputPreview: `{"command":"npm install"}`}
	s := session.Session{ID: "s1", Dir: "/work/api"}

	allowed, err := PermissionAnsweredCard(s, req, VerdictAllow)
	if err != nil {
		t.Fatal(err)
	}
	denied, err := PermissionAnsweredCard(s, req, VerdictDeny)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(allowed, "You allowed") || !strings.Contains(denied, "You denied") {
		t.Errorf("settled cards do not say what was decided:\n%s\n%s", allowed, denied)
	}
	// A settled decision offers nothing more to decide.
	if len(buttonsOf(t, allowed)) != 0 || len(buttonsOf(t, denied)) != 0 {
		t.Error("a settled permission card still offers buttons")
	}
}

func TestPermissionHandledLocallyCard(t *testing.T) {
	card, err := PermissionHandledLocallyCard(
		session.Session{ID: "s1", Dir: "/work/api"},
		mcp.PermissionRequest{RequestID: "abcde", ToolName: "Bash", InputPreview: `{"command":"npm install"}`})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(card, "handled in Claude Code") {
		t.Errorf("card = %s", card)
	}
	if len(buttonsOf(t, card)) != 0 {
		t.Error("a decision already made still offers buttons")
	}
}

// A [ Continue ] button is what turns a notification into the start of the
// next instruction.
func TestOptionsAddAContinueButton(t *testing.T) {
	with := Options{ContinueSession: "sess-1"}.buttons()
	if len(with) != 1 || with[0].Action.Kind != ActionSelect || with[0].Action.Session != "sess-1" {
		t.Errorf("buttons = %+v", with)
	}
	if got := (Options{}).buttons(); len(got) != 0 {
		t.Errorf("buttons = %+v, want none when there is no session to continue", got)
	}
}

func TestParseAction(t *testing.T) {
	raw, err := json.Marshal(Action{Kind: ActionPermit, Request: "abcde", Verdict: VerdictAllow})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := ParseAction(raw)
	if !ok || got.Kind != ActionPermit || got.Request != "abcde" || got.Verdict != VerdictAllow {
		t.Errorf("ParseAction = %+v, %v", got, ok)
	}
	if _, ok := ParseAction([]byte(`{"unrelated":"payload"}`)); ok {
		t.Error("a value that is not a Claude Companion action was accepted as one")
	}
	if _, ok := ParseAction([]byte(`not json`)); ok {
		t.Error("undecodable card values must not parse")
	}
}

// The typed answer is the one that always works, so the card has to carry
// it rather than assume the buttons will do.
func TestPermissionCardCarriesTheTypedAnswer(t *testing.T) {
	card, err := PermissionRelayCard(session.Session{ID: "s1", Dir: "/work/api"},
		mcp.PermissionRequest{RequestID: "abcde", ToolName: "Bash", InputPreview: `{"command":"npm install"}`})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(card, "y abcde") || !strings.Contains(card, "n abcde") {
		t.Errorf("the card does not spell out how to answer by typing: %s", card)
	}
}
