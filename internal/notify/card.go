// Package notify builds the Feishu cards for the notifications Claude Companion
// sends: attention (permission, question), completion, failure, and
// long-running progress. Every card leads with why the user is seeing it.
package notify

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/marcelritzschke/claude-code-feishu-companion/internal/hook"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/transcript"
)

// Detail is the user's completion-detail preference.
type Detail int

const (
	// Normal is the default completion detail: summary, validation, and
	// an excerpt of Claude's final answer.
	Normal Detail = iota
	// Compact is a one-glance completion: summary and validation only.
	Compact
)

// Options are the things a card needs to know that the hook payload cannot
// say: how much detail the user asked for, and whether this session can be
// continued from Feishu. Only the daemon knows the latter, so a card built
// by a hook falling back on its own simply offers no button.
type Options struct {
	Detail Detail
	// ContinueSession, when set, renders a [ Continue ] button that points
	// the user's next messages at that session.
	ContinueSession string
}

// buttons returns the actions these options add to a notification card.
func (o Options) buttons() []Button {
	if o.ContinueSession == "" {
		return nil
	}
	return []Button{{
		Label:  "Continue",
		Style:  stylePrimary,
		Action: Action{Kind: ActionSelect, Session: o.ContinueSession},
	}}
}

// Card JSON 2.0 - only the subset Claude Companion needs, marshalled by
// hand so the JSON stays compact and predictable.
//
// Every card in this package is built by card() below, so this is the one
// place that knows what Feishu's card JSON looks like. The schema moved
// from v1 to 2.0 for one reason: 2.0 has collapsible_panel, and a card
// that can hide detail behind a tap can keep a turn's whole history
// instead of discarding all but the last few actions.
//
// Three things about 2.0 that v1 did differently, all of them verified
// against the live API rather than read from the documentation:
//
//   - elements live under body.elements, and the "div" + "lark_md" pair
//     is a single "markdown" element;
//   - there is no "note" element; a quiet line is a markdown element with
//     text_size "notation";
//   - there is no "action" container. A button is an ordinary element
//     carrying its callback in behaviors, and a row of them is a
//     column_set. Sending an "action" tag fails with 200861, "cards of
//     schema V2 no longer support this capability".

// cardSchema is the version marker every card carries.
const cardSchema = "2.0"

// textSizeNotation is the small, quiet type a footer is set in. It is what
// replaces v1's note element.
const textSizeNotation = "notation"

type cardConfig struct {
	// UpdateMulti lets the same card be rewritten in place, which every
	// card here relies on: a turn is one message from start to outcome.
	UpdateMulti bool `json:"update_multi"`
	// WidthMode fill is 2.0's equivalent of v1's wide_screen_mode.
	WidthMode string `json:"width_mode,omitempty"`
	// EnableForward is off because a card names a session on the user's
	// own computer, and forwarding it would carry that elsewhere.
	EnableForward bool `json:"enable_forward"`
}

type cardText struct {
	Tag     string `json:"tag"`
	Content string `json:"content"`
}

func plainText(s string) *cardText { return &cardText{Tag: "plain_text", Content: s} }

type cardHeader struct {
	Template string    `json:"template"`
	Title    *cardText `json:"title"`
	Subtitle *cardText `json:"subtitle,omitempty"`
}

type markdownElement struct {
	Tag      string `json:"tag"`
	Content  string `json:"content"`
	TextSize string `json:"text_size,omitempty"`
}

// mdElement is a body section. Newlines become markdown hard breaks: the
// bodies here are written as lines that must stay lines - a heading and
// the items under it - and markdown would otherwise run them together.
func mdElement(content, size string) *markdownElement {
	return &markdownElement{
		Tag:      "markdown",
		Content:  strings.ReplaceAll(content, "\n", "  \n"),
		TextSize: size,
	}
}

type hrElement struct {
	Tag string `json:"tag"`
}

// behavior is what a button does when tapped. Only callbacks are used:
// every action is handled by the daemon, never by opening a link.
type behavior struct {
	Type  string `json:"type"`
	Value Action `json:"value"`
}

type buttonElement struct {
	Tag       string     `json:"tag"`
	Text      *cardText  `json:"text"`
	Type      string     `json:"type"`
	Behaviors []behavior `json:"behaviors"`
}

type columnElement struct {
	Tag      string `json:"tag"`
	Elements []any  `json:"elements"`
	Weight   int    `json:"weight,omitempty"`
}

type columnSetElement struct {
	Tag     string           `json:"tag"`
	Columns []*columnElement `json:"columns"`
}

type cardBody struct {
	Elements []any `json:"elements"`
}

type messageCard struct {
	Schema string      `json:"schema"`
	Config *cardConfig `json:"config"`
	Header *cardHeader `json:"header"`
	Body   *cardBody   `json:"body"`
}

// buttonOf renders one button and the action it carries.
func buttonOf(b Button) *buttonElement {
	style := b.Style
	if style == "" {
		style = styleDefault
	}
	return &buttonElement{
		Tag:       "button",
		Text:      plainText(b.Label),
		Type:      style,
		Behaviors: []behavior{{Type: "callback", Value: b.Action}},
	}
}

// buttonRow lays buttons out the way v1's action element did: side by
// side. A lone button needs no column_set, which keeps the common case at
// one element instead of three.
func buttonRow(buttons []Button) any {
	if len(buttons) == 1 {
		return buttonOf(buttons[0])
	}
	set := &columnSetElement{Tag: "column_set"}
	for _, b := range buttons {
		set.Columns = append(set.Columns, &columnElement{
			Tag:      "column",
			Weight:   1,
			Elements: []any{buttonOf(b)},
		})
	}
	return set
}

// card assembles and marshals a card. Each non-empty body becomes a
// markdown section, separated by rules; buttons, when present, render as
// one row; footer, when set, renders as a quiet notation line.
func card(template, title, subtitle string, bodies []string, buttons []Button, footer string) (string, error) {
	c := &messageCard{
		Schema: cardSchema,
		Config: &cardConfig{UpdateMulti: true, WidthMode: "fill"},
		Header: &cardHeader{Template: template, Title: plainText(title)},
		Body:   &cardBody{},
	}
	if subtitle != "" {
		c.Header.Subtitle = plainText(subtitle)
	}
	for _, b := range bodies {
		if b == "" {
			continue
		}
		if len(c.Body.Elements) > 0 {
			c.Body.Elements = append(c.Body.Elements, &hrElement{Tag: "hr"})
		}
		c.Body.Elements = append(c.Body.Elements, mdElement(b, ""))
	}
	if len(buttons) > 0 {
		c.Body.Elements = append(c.Body.Elements, buttonRow(buttons))
	}
	if footer != "" {
		c.Body.Elements = append(c.Body.Elements, mdElement(footer, textSizeNotation))
	}
	b, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// contextLine identifies the session without technical identifiers: the
// session title when Claude Code has one, plus the project name.
func contextLine(p *hook.Payload, turn *transcript.Turn) string {
	parts := make([]string, 0, 2)
	if turn != nil && turn.Title != "" {
		parts = append(parts, truncateRunes(turn.Title, 60))
	}
	if proj := p.ProjectLabel(); proj != "" {
		parts = append(parts, proj)
	}
	return strings.Join(parts, " · ")
}

// contextWithDuration is the context line with how long the turn has run,
// e.g. "Fix token refresh · payments-api · 4m 18s".
func contextWithDuration(p *hook.Payload, turn *transcript.Turn) string {
	ctx := contextLine(p, turn)
	if turn == nil || turn.Start.IsZero() {
		return ctx
	}
	d := formatDuration(time.Since(turn.Start))
	if ctx == "" {
		return d
	}
	return ctx + " · " + d
}

// elapsedSuffix is the " · 8m" a session-card title carries: how long the
// turn has been running, next to the state it is in.
func elapsedSuffix(turn *transcript.Turn) string {
	if turn == nil || turn.Start.IsZero() {
		return ""
	}
	return " · " + formatDuration(time.Since(turn.Start))
}

func formatDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	s := int(d.Seconds())
	h, m, sec := s/3600, s%3600/60, s%60
	switch {
	case h > 0:
		return fmt.Sprintf("%dh %dm", h, m)
	case m > 0 && sec > 0:
		return fmt.Sprintf("%dm %ds", m, sec)
	case m > 0:
		return fmt.Sprintf("%dm", m)
	default:
		return fmt.Sprintf("%ds", sec)
	}
}
