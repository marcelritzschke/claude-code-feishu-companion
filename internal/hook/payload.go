// Package hook decodes Claude Code hook payloads from stdin and classifies
// them into the events Wirelark notifies on.
package hook

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/marcelritzschke/wirelark/internal/pathdisp"
)

// Hook event names emitted by Claude Code that Wirelark acts on.
const (
	EventPermissionRequest = "PermissionRequest"
	EventPreToolUse        = "PreToolUse"
	EventPostToolUse       = "PostToolUse"
	EventStop              = "Stop"
	EventStopFailure       = "StopFailure"

	// The lifecycle events carry no notification of their own. They exist
	// so the daemon knows which sessions are running and what each one is
	// doing, which is what the Feishu session overview is made of.
	EventSessionStart     = "SessionStart"
	EventSessionEnd       = "SessionEnd"
	EventUserPromptSubmit = "UserPromptSubmit"
)

// QuestionTool is the tool Claude uses to ask the user a multiple-choice
// question. Only its PreToolUse event is attention-worthy; every other
// tool-level event is routine work.
const QuestionTool = "AskUserQuestion"

// maxPayloadBytes bounds stdin so a runaway hook payload cannot OOM the
// process. Claude Code payloads are a few KB at most.
const maxPayloadBytes = 1 << 20

// Payload is the subset of the Claude Code hook stdin JSON Wirelark uses.
// Unknown fields are ignored so new Claude Code versions stay compatible.
type Payload struct {
	SessionID      string `json:"session_id"`
	PromptID       string `json:"prompt_id"`
	TranscriptPath string `json:"transcript_path"`
	Cwd            string `json:"cwd"`
	HookEventName  string `json:"hook_event_name"`

	// AgentID is only populated when the hook fired inside a subagent.
	// Subagent activity is routine work and never notified.
	AgentID string `json:"agent_id"`

	// ProjectDir is the project this event belongs to, set by whoever
	// knows it for certain. A hook process can read it from its own
	// environment; the daemon cannot, because it handles events from every
	// session and its environment belongs to whichever one started it.
	ProjectDir string `json:"-"`

	// Tool events (PermissionRequest, PreToolUse, PostToolUse).
	ToolName  string         `json:"tool_name"`
	ToolInput map[string]any `json:"tool_input"`

	// Stop and StopFailure.
	LastAssistantMessage string `json:"last_assistant_message"`

	// StopFailure.
	Error        string `json:"error"`
	ErrorDetails string `json:"error_details"`
}

// Decode reads a hook payload from r, bounded by maxPayloadBytes.
func Decode(r io.Reader) (*Payload, error) {
	var p Payload
	dec := json.NewDecoder(io.LimitReader(r, maxPayloadBytes))
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("decode hook payload: %w", err)
	}
	return &p, nil
}

// Subagent reports whether the event came from a subagent rather than the
// top-level session the user is watching.
func (p *Payload) Subagent() bool {
	return p.AgentID != ""
}

// Handled reports whether the event is one Wirelark acts on: a permission
// prompt, a question, a finished turn, a failed turn, a tool call (which
// matters as a progress checkpoint), or a lifecycle event that says a
// session started, ended, or was given something to do.
func (p *Payload) Handled() bool {
	switch p.HookEventName {
	case EventPermissionRequest, EventPostToolUse, EventStop, EventStopFailure,
		EventSessionStart, EventSessionEnd, EventUserPromptSubmit:
		return true
	case EventPreToolUse:
		return p.ToolName == QuestionTool
	}
	return false
}

// ProjectLabel returns the human-facing project name for the session: the
// basename of the project directory (stable even when Claude cd's
// elsewhere), falling back to the payload cwd.
//
// The environment is consulted only as the hook process's own shortcut. A
// caller that knows which session this event came from sets ProjectDir and
// is believed, because a process handling events from several sessions has
// no business reading any one session's environment.
func (p *Payload) ProjectLabel() string {
	dir := p.Cwd
	if env := os.Getenv("CLAUDE_PROJECT_DIR"); env != "" {
		dir = env
	}
	if p.ProjectDir != "" {
		dir = p.ProjectDir
	}
	if dir == "" {
		return "unknown project"
	}
	if label, ok := pathdisp.Label(dir); ok {
		return label
	}
	return dir
}
