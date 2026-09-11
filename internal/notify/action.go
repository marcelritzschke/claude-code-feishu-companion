package notify

import "encoding/json"

// Action is what tapping a card button asks Claude Companion to do. It travels in
// the button's value and comes back on the card callback, so it is the one
// piece of card JSON that is also parsed rather than only written.
//
// The fields are short because Feishu carries this on every button, and
// opaque because the user never sees them: what identifies a session here
// is not what identifies it on the card.
type Action struct {
	Kind    string `json:"k"`
	Session string `json:"s,omitempty"`
	// Request is a relayed permission request's id.
	Request string `json:"r,omitempty"`
	// Verdict is "allow" or "deny" for ActionPermit.
	Verdict string `json:"v,omitempty"`
}

// Action kinds.
const (
	// ActionPermit answers a relayed permission request.
	ActionPermit = "permit"
	// ActionInterrupt stops a session's current turn, returning the session
	// to its prompt. It never terminates the session itself.
	ActionInterrupt = "interrupt"
	// ActionSay sends what the user typed in a card's reply box to the
	// session that card is about.
	ActionSay = "say"
	// ActionProbe is setup's card callback check and means nothing else.
	ActionProbe = "probe"
)

// Verdicts a permission button carries.
const (
	VerdictAllow = "allow"
	VerdictDeny  = "deny"
)

// ParseAction reads the value Feishu returns from a tapped button.
func ParseAction(raw json.RawMessage) (Action, bool) {
	var a Action
	if err := json.Unmarshal(raw, &a); err != nil || a.Kind == "" {
		return Action{}, false
	}
	return a, true
}

// replyTo is the box a card offers for typing straight to one session.
func replyTo(sessionID string) Section {
	if sessionID == "" {
		return nil
	}
	return Reply{
		Placeholder: "Message this session",
		Action:      Action{Kind: ActionSay, Session: sessionID},
	}
}

// Button is one card button.
type Button struct {
	Label string
	// Style is Feishu's button emphasis: "primary", "danger", or "default".
	Style  string
	Action Action
	// Confirm, when set, puts a dialog between the tap and the action.
	Confirm *Confirm
}

// Confirm is the dialog Feishu shows before a button's action runs.
//
// It is for the button whose action cannot be taken back by tapping
// again. A card is read on a phone, in a pocket, one-handed, and the
// difference between reading a card and stopping the work described on it
// should not be one mistaken tap.
type Confirm struct {
	// Title is the question, e.g. "Interrupt this turn?".
	Title string
	// Text says what happens if they go ahead - and, just as importantly,
	// what does not.
	Text string
}

// Button styles, named for what they mean rather than how they look.
const (
	styleDefault = "default"
	stylePrimary = "primary"
	styleDanger  = "danger"
)
