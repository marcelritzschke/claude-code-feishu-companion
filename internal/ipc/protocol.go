// Package ipc is the private local link between the Claude Companion roles: the
// daemon that owns the Feishu connection, the channels attached to running
// Claude Code sessions, and the one-shot hook processes.
//
// It is deliberately local and private. A channel must never reach Feishu on
// its own, and nothing outside this machine may reach a channel: the
// transport is a unix socket the user alone can open (a loopback port
// guarded by a secret on Windows, which has no such sockets), and the
// framing is one JSON object per line.
package ipc

import (
	"encoding/json"
	"fmt"

	"github.com/marcelritzschke/claude-code-feishu-companion/internal/session"
)

// Message types. The name says who is talking to whom, because the same
// connection carries both directions.
const (
	// TypeRegister introduces a channel's session. Channel -> daemon.
	TypeRegister = "register"
	// TypeHook forwards one Claude Code hook event. Hook -> daemon.
	TypeHook = "hook"
	// TypeInject pushes a user message into a session. Daemon -> channel.
	TypeInject = "inject"
	// TypePermissionRequest relays a tool approval prompt. Channel -> daemon.
	TypePermissionRequest = "permission_request"
	// TypePermissionVerdict answers one. Daemon -> channel.
	TypePermissionVerdict = "permission_verdict"
	// TypeAck closes a one-shot exchange. Daemon -> caller.
	TypeAck = "ack"
	// TypeStatus asks what the daemon is doing. Tooling -> daemon.
	TypeStatus = "status"
	// TypeStop asks the daemon to exit. Tooling -> daemon.
	TypeStop = "stop"
	// TypeAwaitInbound waits for the next Feishu message to arrive, so
	// setup can prove the return path works. Tooling -> daemon.
	TypeAwaitInbound = "await_inbound"
)

// Register introduces the Claude Code session a channel is attached to.
// It carries no Feishu identity: a channel has none.
type Register struct {
	SessionID string `json:"session_id"`
	// PID is the claude process, the one identity that survives /clear.
	PID int `json:"pid"`
	// Dir is where the session was started; ProjectDir is
	// CLAUDE_PROJECT_DIR when Claude Code set one.
	Dir        string `json:"dir"`
	ProjectDir string `json:"project_dir,omitempty"`
	// Remote is what the channel could determine about whether this
	// session will actually accept injected messages.
	Remote session.Remote `json:"remote"`
}

// Hook forwards one hook event exactly as Claude Code wrote it, plus the
// two environment facts only the hook process can see.
type Hook struct {
	Payload    json.RawMessage `json:"payload"`
	ProjectDir string          `json:"project_dir,omitempty"`
	PID        int             `json:"pid,omitempty"`
}

// Inject is a message on its way into a session.
type Inject struct {
	Content string            `json:"content"`
	Meta    map[string]string `json:"meta,omitempty"`
}

// PermissionRequest is a tool approval prompt Claude Code opened locally and
// relayed to the channel, mirroring the fields of the channel protocol.
type PermissionRequest struct {
	RequestID    string `json:"request_id"`
	ToolName     string `json:"tool_name"`
	Description  string `json:"description"`
	InputPreview string `json:"input_preview"`
}

// PermissionVerdict answers one permission request.
type PermissionVerdict struct {
	RequestID string `json:"request_id"`
	Behavior  string `json:"behavior"` // "allow" or "deny"
}

// Ack ends a one-shot exchange. Err is set when the daemon declined, so the
// caller can fall back rather than assume the message landed.
type Ack struct {
	OK  bool   `json:"ok"`
	Err string `json:"err,omitempty"`
}

// Envelope is one line on the wire: a type and its opaque body.
type Envelope struct {
	Type string          `json:"t"`
	Body json.RawMessage `json:"b,omitempty"`
}

// Into decodes an envelope's body into v.
func (e Envelope) Into(v any) error {
	if len(e.Body) == 0 {
		return fmt.Errorf("%s message carried no body", e.Type)
	}
	return json.Unmarshal(e.Body, v)
}
