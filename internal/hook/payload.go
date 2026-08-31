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

// Handled reports whether the event carries a notification Wirelark sends:
// a permission prompt, a question, a finished turn, a failed turn, or a
// tool call (which only matters as a progress checkpoint).
func (p *Payload) Handled() bool {
	switch p.HookEventName {
	case EventPermissionRequest, EventPostToolUse, EventStop, EventStopFailure:
		return true
	case EventPreToolUse:
		return p.ToolName == QuestionTool
	}
	return false
}

// ProjectLabel returns the human-facing project name for the session:
// the basename of CLAUDE_PROJECT_DIR (stable even when Claude cd's
// elsewhere), falling back to the payload cwd.
func (p *Payload) ProjectLabel() string {
	dir := p.Cwd
	if env := os.Getenv("CLAUDE_PROJECT_DIR"); env != "" {
		dir = env
	}
	if dir == "" {
		return "unknown project"
	}
	if label, ok := pathdisp.Label(dir); ok {
		return label
	}
	return dir
}
