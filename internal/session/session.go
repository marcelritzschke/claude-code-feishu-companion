// Package session is Claude Companion's picture of the Claude Code sessions running
// on this machine: which exist, what each is broadly doing, and whether it
// can be continued from Feishu.
//
// Two sources feed it and neither is sufficient alone. Hook events say what
// a session is doing but arrive as one-shot processes that vanish; a
// channel's connection says a session is alive and reachable but is silent
// about its work. The registry joins them, and it prefers admitting
// ignorance to guessing: a session it cannot confirm is reachable is never
// shown as reachable.
package session

import (
	"sort"
	"strings"
	"time"

	"github.com/marcelritzschke/claude-code-feishu-companion/internal/pathdisp"
)

// State is what a session is broadly doing, in the only three shades the
// user needs to tell apart from a phone.
type State string

const (
	// Idle: no turn in flight.
	Idle State = "idle"
	// Working: a turn is running.
	Working State = "working"
	// Waiting: Claude is blocked on the user - a permission decision or a
	// question. This is the state that earns attention.
	Waiting State = "waiting"
)

// Wait names what a Waiting session is blocked on, so its card can say
// "Waiting for permission" rather than a vaguer "Waiting for you".
type Wait string

const (
	// WaitNothing: the session is not blocked on the user.
	WaitNothing Wait = ""
	// WaitPermission: a tool approval is open.
	WaitPermission Wait = "permission"
	// WaitAnswer: Claude asked the user a question.
	WaitAnswer Wait = "answer"
)

// Remote is how far Claude Companion can trust that a session accepts remote input.
type Remote string

const (
	// Ready: the session was started with a channels flag naming Claude Companion.
	Ready Remote = "ready"
	// Notifications: the session runs without Claude Companion as a channel. It
	// still produces notifications; it cannot be continued.
	Notifications Remote = "notifications"
	// Unconfirmed: this platform could not tell. Never presented as ready -
	// the user finds out honestly on the first message instead.
	Unconfirmed Remote = "unconfirmed"
)

// Continuable reports whether Claude Companion may offer to continue this session.
// Unconfirmed counts: refusing to try would strand every Windows user, and
// a delivery that goes nowhere corrects the record honestly.
func (r Remote) Continuable() bool { return r == Ready || r == Unconfirmed }

// Channel is the live link into one session. The registry holds it as an
// interface so it stays a picture of sessions rather than of connections.
type Channel interface {
	// Inject pushes a message into the session.
	Inject(content string, meta map[string]string) error
	// Verdict answers a relayed permission request.
	Verdict(requestID, behavior string) error
}

// Session is one running Claude Code session.
type Session struct {
	// ID is Claude Code's session id. It changes on /clear; PID does not,
	// which is what lets a cleared session stay one session here.
	ID  string
	PID int
	// Dir is the project directory the session belongs to.
	Dir string
	// Title is the session's own name for its work, e.g. "Fix token
	// refresh". Empty until Claude Code has titled it.
	Title string
	State State
	// WaitingOn says what a Waiting session is blocked on. WaitNothing in
	// every other state.
	WaitingOn Wait
	Remote    Remote
	// Transcript is the path to the session's Claude Code transcript, as
	// reported by its hooks. It is what a live view is read from, so a
	// session without one cannot be watched - only heard from.
	Transcript string
	// LastSeen is when anything was last heard about this session.
	LastSeen time.Time

	// channel is nil for a session Claude Companion only hears about through hooks.
	channel Channel
}

// Label is the project name shown to the user, never a path or an id.
func (s *Session) Label() string {
	if label, ok := pathdisp.Label(s.Dir); ok {
		return label
	}
	if s.Dir != "" {
		return s.Dir
	}
	return "unknown project"
}

// Describe is the one-line identification a card leads with: the session's
// own title and its project, e.g. "Fix token refresh · payments-api".
func (s *Session) Describe() string {
	parts := make([]string, 0, 2)
	if s.Title != "" {
		parts = append(parts, s.Title)
	}
	parts = append(parts, s.Label())
	return strings.Join(parts, " · ")
}

// Attached reports whether a channel is currently connected to the session.
func (s *Session) Attached() bool { return s.channel != nil }

// Watchable reports whether Claude Companion can show what this session is doing.
// It needs no channel: watching only reads the transcript the session's
// hooks already point at, so a notifications-only session can be watched
// even though it cannot be continued.
func (s *Session) Watchable() bool { return s.Transcript != "" }

// Channel returns the live link, or nil when there is none.
func (s *Session) Channel() Channel { return s.channel }

// Interruptible reports whether Claude Companion may offer to interrupt this
// session's current turn. It is a control, so it is only offered for a
// session the user can also talk to, and only where this platform can
// actually deliver the interrupt.
func (s *Session) Interruptible() bool {
	return s.Remote.Continuable() && s.PID > 0 && interruptSupported
}

// Interrupt stops the session's current turn, exactly as pressing Ctrl+C
// in its terminal would: the work stops and the session returns to its
// prompt. It never terminates Claude Code or the session itself.
func (s *Session) Interrupt() error {
	return interruptProcess(s.PID)
}

// waitFor maps a hook event to what it proves the session is blocked on.
func waitFor(hookEvent string) Wait {
	switch hookEvent {
	case "PermissionRequest":
		return WaitPermission
	case "PreToolUse":
		return WaitAnswer
	}
	return WaitNothing
}

// StateFor maps a Claude Code hook event to the state it proves the session
// is in. Events that prove nothing (a tool call finishing, say) report
// false and leave the state alone.
func StateFor(hookEvent string) (State, bool) {
	switch hookEvent {
	case "UserPromptSubmit":
		return Working, true
	case "PermissionRequest", "PreToolUse":
		return Waiting, true
	case "PostToolUse":
		return Working, true
	case "Stop", "StopFailure":
		return Idle, true
	case "SessionStart":
		return Idle, true
	}
	return "", false
}

// byAttention orders sessions the way the overview reads: whatever needs
// the user first, then what is running, then what is resting, and within
// each group the most recently active first.
func byAttention(sessions []*Session) {
	rank := map[State]int{Waiting: 0, Working: 1, Idle: 2}
	sort.SliceStable(sessions, func(i, j int) bool {
		ri, rj := rank[sessions[i].State], rank[sessions[j].State]
		if ri != rj {
			return ri < rj
		}
		return sessions[i].LastSeen.After(sessions[j].LastSeen)
	})
}
