package feishu

import (
	"context"
	"encoding/json"
	"strings"

	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkevent "github.com/larksuite/oapi-sdk-go/v3/event"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"

	"github.com/marcelritzschke/claude-code-feishu-companion/internal/config"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/debuglog"
)

// Inbound is the return path Feishu has no other way to open: a WebSocket
// long connection the daemon dials out on, so nothing has to be exposed to
// the internet and there is no webhook URL to host.
//
// Everything it emits is gated on the sender being the configured user.
// The channel it feeds pushes text straight into a Claude Code session with
// the user's own files and tools, so a message from anyone else is not
// input - it is an injection attempt, and it is dropped without a word.
type Inbound struct {
	cfg *config.Config
	ws  *larkws.Client

	messages chan Message
	actions  chan CardAction
}

// Message is a direct message the user sent the bot.
type Message struct {
	// Text is the message body. Only text messages are carried: a file or
	// an image is not something a session can be continued with.
	Text string
	// MessageID identifies the message, for replies.
	MessageID string
}

// CardAction is a button the user tapped on a card.
type CardAction struct {
	// Value is the button's payload, as the card wrote it.
	Value json.RawMessage
	// MessageID is the card the button belongs to, so it can be settled in
	// place rather than replaced by a new one.
	MessageID string
}

// inboundBuffer is how many events may wait while the daemon is busy. A
// user cannot type faster than this; a flood that fills it is not a user.
const inboundBuffer = 32

// NewInbound builds the return path. Nothing connects until Run.
func NewInbound(cfg *config.Config) *Inbound {
	in := &Inbound{
		cfg:      cfg,
		messages: make(chan Message, inboundBuffer),
		actions:  make(chan CardAction, inboundBuffer),
	}
	handler := dispatcher.NewEventDispatcher("", "").
		OnP2MessageReceiveV1(in.onMessage).
		OnP2CardActionTrigger(in.onCardAction)
	// The SDK logs to stdout by default. The daemon's own trace goes to the
	// debug log, and a background service should not narrate itself into
	// whatever stream it happened to inherit.
	handler.InitConfig(larkevent.WithLogger(discardLogger{}), larkevent.WithLogLevel(larkcore.LogLevelError))
	in.ws = larkws.NewClient(cfg.AppID, cfg.AppSecret,
		larkws.WithEventHandler(handler),
		larkws.WithDomain(cfg.OpenBaseURL()),
		larkws.WithAutoReconnect(true),
		larkws.WithLogLevel(larkcore.LogLevelError),
		larkws.WithLogger(discardLogger{}))
	return in
}

// Messages and Actions are what the user did in Feishu.
func (in *Inbound) Messages() <-chan Message   { return in.messages }
func (in *Inbound) Actions() <-chan CardAction { return in.actions }

// Run holds the connection open until ctx ends, reconnecting on its own.
func (in *Inbound) Run(ctx context.Context) error { return in.ws.Start(ctx) }

// onMessage forwards a direct message from the configured user.
func (in *Inbound) onMessage(_ context.Context, event *larkim.P2MessageReceiveV1) error {
	if event == nil || event.Event == nil || event.Event.Message == nil {
		return nil
	}
	msg := event.Event.Message
	if !in.fromConfiguredUser(event.Event.Sender) {
		debuglog.Printf("inbound: dropped a message from another sender")
		return nil
	}
	if deref(msg.MessageType) != larkim.MsgTypeText {
		debuglog.Printf("inbound: ignoring a %s message", deref(msg.MessageType))
		return nil
	}
	text := textOf(deref(msg.Content))
	if text == "" {
		return nil
	}
	in.emitMessage(Message{Text: text, MessageID: deref(msg.MessageId)})
	return nil
}

// onCardAction forwards a button tap from the configured user.
//
// The reply is empty on purpose: the daemon settles the card by rewriting
// it, so that the same card reads the same way whether the answer came from
// a tap here or from the terminal.
func (in *Inbound) onCardAction(_ context.Context, event *callback.CardActionTriggerEvent) (*callback.CardActionTriggerResponse, error) {
	if event == nil || event.Event == nil || event.Event.Action == nil {
		return nil, nil
	}
	if !in.operatorIsConfiguredUser(event.Event.Operator) {
		debuglog.Printf("inbound: dropped a card action from another sender")
		return nil, nil
	}
	value, err := json.Marshal(event.Event.Action.Value)
	if err != nil {
		return nil, nil
	}
	var messageID string
	if event.Event.Context != nil {
		messageID = event.Event.Context.OpenMessageID
	}
	select {
	case in.actions <- CardAction{Value: value, MessageID: messageID}:
	default:
		debuglog.Printf("inbound: dropped a card action; the daemon is not keeping up")
	}
	return nil, nil
}

func (in *Inbound) emitMessage(m Message) {
	select {
	case in.messages <- m:
	default:
		debuglog.Printf("inbound: dropped a message; the daemon is not keeping up")
	}
}

// fromConfiguredUser reports whether a message came from the one account
// Claude Companion answers to, matching against whichever id kind was configured.
func (in *Inbound) fromConfiguredUser(sender *larkim.EventSender) bool {
	if sender == nil || sender.SenderId == nil {
		return false
	}
	if deref(sender.SenderType) == "bot" {
		return false // the bot's own messages are not instructions
	}
	return in.matchesConfiguredUser(
		deref(sender.SenderId.OpenId),
		deref(sender.SenderId.UserId),
		deref(sender.SenderId.UnionId))
}

// operatorIsConfiguredUser is the same check for a card action, which
// reports the tapper as an operator rather than a sender.
func (in *Inbound) operatorIsConfiguredUser(op *callback.Operator) bool {
	if op == nil {
		return false
	}
	var userID string
	if op.UserID != nil {
		userID = *op.UserID
	}
	return in.matchesConfiguredUser(op.OpenID, userID, "")
}

// matchesConfiguredUser compares the ids an event carries with the one
// configured. Any of them matching is enough: which kind is configured is
// the user's choice, not the event's.
func (in *Inbound) matchesConfiguredUser(openID, userID, unionID string) bool {
	want := in.cfg.OpenID
	if want == "" {
		return false // nothing configured: trust no one
	}
	for _, got := range []string{openID, userID, unionID} {
		if got != "" && got == want {
			return true
		}
	}
	return false
}

// textOf pulls the body out of a text message's JSON content.
func textOf(content string) string {
	var body struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(content), &body); err != nil {
		return ""
	}
	return strings.TrimSpace(body.Text)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
