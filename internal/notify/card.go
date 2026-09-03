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
		Label:  "Continue this session",
		Style:  stylePrimary,
		Action: Action{Kind: ActionSelect, Session: o.ContinueSession},
	}}
}

// Card schema (v1) - only the subset Claude Companion needs, marshalled by hand so
// the JSON stays compact and predictable.

type cardConfig struct {
	WideScreenMode bool `json:"wide_screen_mode"`
	EnableForward  bool `json:"enable_forward"`
}

type cardText struct {
	Tag     string `json:"tag"`
	Content string `json:"content"`
}

func plainText(s string) *cardText { return &cardText{Tag: "plain_text", Content: s} }
func larkMD(s string) *cardText    { return &cardText{Tag: "lark_md", Content: s} }

type cardHeader struct {
	Template string    `json:"template"`
	Title    *cardText `json:"title"`
	Subtitle *cardText `json:"subtitle,omitempty"`
}

type divElement struct {
	Tag  string    `json:"tag"`
	Text *cardText `json:"text"`
}

type hrElement struct {
	Tag string `json:"tag"`
}

type noteText struct {
	Tag     string `json:"tag"`
	Content string `json:"content"`
}

type noteElement struct {
	Tag      string     `json:"tag"`
	Elements []noteText `json:"elements"`
}

type buttonElement struct {
	Tag   string    `json:"tag"`
	Text  *cardText `json:"text"`
	Type  string    `json:"type"`
	Value Action    `json:"value"`
}

type actionElement struct {
	Tag     string          `json:"tag"`
	Actions []buttonElement `json:"actions"`
}

type messageCard struct {
	Config   *cardConfig `json:"config"`
	Header   *cardHeader `json:"header"`
	Elements []any       `json:"elements"`
}

// card assembles and marshals a card. Each non-empty body becomes a div
// section, separated by rules; buttons, when present, render as one row of
// actions; footer, when set, renders as a quiet note line.
func card(template, title, subtitle string, bodies []string, buttons []Button, footer string) (string, error) {
	c := &messageCard{
		Config: &cardConfig{WideScreenMode: true, EnableForward: false},
		Header: &cardHeader{Template: template, Title: plainText(title)},
	}
	if subtitle != "" {
		c.Header.Subtitle = larkMD(subtitle)
	}
	for _, b := range bodies {
		if b == "" {
			continue
		}
		if len(c.Elements) > 0 {
			c.Elements = append(c.Elements, &hrElement{Tag: "hr"})
		}
		c.Elements = append(c.Elements, &divElement{Tag: "div", Text: larkMD(b)})
	}
	if len(buttons) > 0 {
		row := &actionElement{Tag: "action"}
		for _, b := range buttons {
			style := b.Style
			if style == "" {
				style = styleDefault
			}
			row.Actions = append(row.Actions, buttonElement{
				Tag:   "button",
				Text:  plainText(b.Label),
				Type:  style,
				Value: b.Action,
			})
		}
		c.Elements = append(c.Elements, row)
	}
	if footer != "" {
		c.Elements = append(c.Elements, &noteElement{
			Tag:      "note",
			Elements: []noteText{{Tag: "plain_text", Content: footer}},
		})
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
