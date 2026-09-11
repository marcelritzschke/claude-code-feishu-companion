package notify

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/marcelritzschke/claude-code-feishu-companion/internal/mcp"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/session"
)

// buttonsOf returns the label and value of every button on a card.
//
// Schema 2.0 has no action container: a lone button is an element of its
// own, and a row of them is a column_set, so both shapes are walked.
func buttonsOf(t *testing.T, cardJSON string) []Button {
	t.Helper()
	var m struct {
		Body struct {
			Elements []json.RawMessage `json:"elements"`
		} `json:"body"`
	}
	if err := json.Unmarshal([]byte(cardJSON), &m); err != nil {
		t.Fatalf("%v in %s", err, cardJSON)
	}

	var out []Button
	var walk func(raw json.RawMessage)
	walk = func(raw json.RawMessage) {
		var el struct {
			Tag  string `json:"tag"`
			Text struct {
				Content string `json:"content"`
			} `json:"text"`
			Type      string `json:"type"`
			Behaviors []struct {
				Type  string `json:"type"`
				Value Action `json:"value"`
			} `json:"behaviors"`
			Columns []struct {
				Elements []json.RawMessage `json:"elements"`
			} `json:"columns"`
		}
		if err := json.Unmarshal(raw, &el); err != nil {
			return
		}
		switch el.Tag {
		case "button":
			var action Action
			if len(el.Behaviors) > 0 {
				action = el.Behaviors[0].Value
			}
			out = append(out, Button{Label: el.Text.Content, Style: el.Type, Action: action})
		case "column_set":
			for _, col := range el.Columns {
				for _, inner := range col.Elements {
					walk(inner)
				}
			}
		}
	}
	for _, raw := range m.Body.Elements {
		walk(raw)
	}
	return out
}

// The typed list is the fallback for an app whose card callbacks are not
// configured, so it must number exactly the sessions a number can reach.
func TestPickListNumbersOnlyTheSessionsThatCanBeContinued(t *testing.T) {
	list := PickList([]session.Session{
		{ID: "s1", Dir: "/work/frontend", Title: "Upgrade React", State: session.Waiting, Remote: session.Ready},
		{ID: "s2", Dir: "/work/payments-api", State: session.Working, Remote: session.Ready},
		{ID: "s3", Dir: "/work/claude-companion", State: session.Idle, Remote: session.Notifications},
	})
	for _, want := range []string{"1. ", "frontend", "Waiting for you", "2. ", "payments-api", "Working"} {
		if !strings.Contains(list, want) {
			t.Errorf("list is missing %q: %s", want, list)
		}
	}
	if strings.Contains(list, "claude-companion") {
		t.Errorf("a session that cannot be continued was numbered: %s", list)
	}
}

// One session is not a choice. Its own card says everything the line would.
func TestPickListSaysNothingAboutOneSession(t *testing.T) {
	if list := PickList([]session.Session{
		{ID: "s1", Dir: "/work/payments-api", State: session.Working, Remote: session.Ready},
		{ID: "s2", Dir: "/work/frontend", State: session.Idle, Remote: session.Notifications},
	}); list != "" {
		t.Errorf("list = %q, want nothing to choose between", list)
	}
}

// Nothing the user reads may carry the identifiers Claude Companion works
// with.
func TestPickListShowsNoTechnicalIdentifiers(t *testing.T) {
	list := PickList([]session.Session{
		{ID: "0198c0de-cafe-7000-a1b2-0123456789ab", PID: 4242,
			Dir: "/work/payments-api", State: session.Working, Remote: session.Ready},
		{ID: "0198c0de-cafe-7000-a1b2-0123456789ac", PID: 4243,
			Dir: "/work/frontend", State: session.Idle, Remote: session.Ready},
	})
	if strings.Contains(list, "0198c0de") || strings.Contains(list, "4242") {
		t.Errorf("the list shows a technical identifier: %q", list)
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

// A reply box is what turns a notification into the start of the next
// instruction: the card names one session, so answering it needs no
// selection and no round trip through the overview.
func TestOptionsAddAReplyBox(t *testing.T) {
	with, ok := replyTo("sess-1").(Reply)
	if !ok || with.Action.Kind != ActionSay || with.Action.Session != "sess-1" {
		t.Errorf("reply = %+v", with)
	}
	if got := replyTo(""); got != nil {
		t.Errorf("reply = %+v, want none when there is no session to answer", got)
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
