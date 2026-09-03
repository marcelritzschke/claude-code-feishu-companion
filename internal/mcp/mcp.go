// Package mcp is the small part of the Model Context Protocol a Claude Companion
// channel needs: a JSON-RPC server on stdio that answers the handshake and
// carries Claude Code's channel extensions.
//
// It is hand-written rather than taken from an SDK because the surface is
// tiny and the interesting parts are the extensions - the experimental
// capability keys and the two "notifications/claude/channel*" methods - which
// a general client library treats as escape hatches anyway. The same
// reasoning already produced the hand-marshalled Feishu cards.
//
// One rule governs everything here: stdout belongs to the protocol. The
// process shares its streams with a Claude Code session, so a stray print
// would corrupt the session's link to its own channel. Nothing is ever
// written to stdout but a JSON-RPC message, and every problem goes to the
// debug log.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/marcelritzschke/claude-code-feishu-companion/internal/debuglog"
)

// ProtocolVersion is the revision this server negotiates. It is
// deliberately not the newest: Claude Code refuses to register a channel
// that negotiates revision 2026-07-28 when its MCP client runs with
// protocol negotiation on auto, and a channel that fails to register is a
// channel whose messages vanish in silence.
const ProtocolVersion = "2025-06-18"

// Channel protocol methods. The transport is standard MCP; these methods
// are Claude Code's own extension.
const (
	// MethodChannel pushes an event into the session. Server -> Claude Code.
	MethodChannel = "notifications/claude/channel"
	// MethodPermissionRequest relays a tool approval prompt.
	// Claude Code -> server.
	MethodPermissionRequest = "notifications/claude/channel/permission_request"
	// MethodPermission answers one. Server -> Claude Code.
	MethodPermission = "notifications/claude/channel/permission"
)

// maxMessageBytes bounds one JSON-RPC message.
const maxMessageBytes = 4 << 20

// PermissionRequest is a tool approval prompt Claude Code opened locally
// and relayed here in parallel with its own dialog.
//
// Description and InputPreview are written by the model and only partly
// sanitized by Claude Code. Treat both as untrusted text: they may be
// shown to the user, never acted on.
type PermissionRequest struct {
	// RequestID is five lowercase letters. A verdict is only accepted
	// while it carries an id Claude Code issued.
	RequestID    string `json:"request_id"`
	ToolName     string `json:"tool_name"`
	Description  string `json:"description"`
	InputPreview string `json:"input_preview"`
}

// Server is the channel's MCP endpoint.
type Server struct {
	name        string
	version     string
	instruction string

	// permission records whether this server accepts relayed permission
	// prompts, which it must declare in the handshake to receive any.
	permission bool

	mu  sync.Mutex
	out *bufio.Writer

	onPermissionRequest func(PermissionRequest)
}

// New builds a channel server. instructions reaches Claude as context when
// the server connects: it is where the model learns what these events are
// and what is expected of it.
func New(name, version, instructions string, permissionRelay bool) *Server {
	return &Server{name: name, version: version, instruction: instructions, permission: permissionRelay}
}

// OnPermissionRequest registers the handler for relayed approval prompts.
// It must be set before Serve.
func (s *Server) OnPermissionRequest(f func(PermissionRequest)) { s.onPermissionRequest = f }

// PushEvent sends one event into the session. content becomes the body of
// the <channel> tag Claude sees, and each meta entry an attribute on it.
//
// Claude Code never acknowledges these: a session that did not register
// this server as a channel drops them without a word. So a nil error means
// the message reached the transport, not that it reached Claude.
func (s *Server) PushEvent(content string, meta map[string]string) error {
	return s.notify(MethodChannel, map[string]any{
		"content": content,
		"meta":    sanitizeMeta(meta),
	})
}

// SendVerdict answers a relayed permission request. Claude Code applies
// whichever answer arrives first, this one or the local dialog's, and drops
// the other; an id it has no open request for is discarded in silence.
func (s *Server) SendVerdict(requestID, behavior string) error {
	return s.notify(MethodPermission, map[string]string{
		"request_id": requestID,
		"behavior":   behavior,
	})
}

// Serve runs the protocol loop until the input ends or ctx is cancelled.
// Claude Code closes the input when the session exits, which is how a
// channel learns to shut down.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	s.mu.Lock()
	s.out = bufio.NewWriter(out)
	s.mu.Unlock()

	lines := make(chan []byte)
	errs := make(chan error, 1)
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(in)
		sc.Buffer(make([]byte, 0, 64*1024), maxMessageBytes)
		for sc.Scan() {
			line := make([]byte, len(sc.Bytes()))
			copy(line, sc.Bytes())
			select {
			case lines <- line:
			case <-ctx.Done():
				return
			}
		}
		errs <- sc.Err()
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case line, ok := <-lines:
			if !ok {
				select {
				case err := <-errs:
					return err
				default:
					return nil
				}
			}
			s.handle(line)
		}
	}
}

// message is one JSON-RPC message. A request carries an id and expects a
// reply; a notification carries none and expects nothing.
type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// handle dispatches one incoming message. Nothing in here may return an
// error to the caller: a malformed message from the peer must not end a
// session's channel.
func (s *Server) handle(line []byte) {
	var msg message
	if err := json.Unmarshal(line, &msg); err != nil {
		debuglog.Printf("mcp: undecodable message: %v", err)
		return
	}
	if len(msg.ID) == 0 {
		s.handleNotification(msg)
		return
	}
	switch msg.Method {
	case "initialize":
		s.reply(msg.ID, s.initializeResult(), nil)
	case "ping":
		s.reply(msg.ID, map[string]any{}, nil)
	default:
		// Unknown requests are answered, not ignored: a peer waiting on a
		// reply that never comes is worse than one told no.
		s.reply(msg.ID, nil, &rpcError{Code: -32601, Message: "method not found: " + msg.Method})
	}
}

func (s *Server) handleNotification(msg message) {
	if msg.Method != MethodPermissionRequest {
		return // initialized, cancelled, whatever else: nothing to do
	}
	if s.onPermissionRequest == nil {
		return
	}
	var req PermissionRequest
	if err := json.Unmarshal(msg.Params, &req); err != nil {
		debuglog.Printf("mcp: undecodable permission request: %v", err)
		return
	}
	if req.RequestID == "" {
		return // unanswerable: a verdict without an id is discarded anyway
	}
	s.onPermissionRequest(req)
}

// initializeResult is the handshake. The experimental capability keys are
// what make this server a channel: "claude/channel" registers the
// notification listener, and "claude/channel/permission" opts in to
// receiving approval prompts.
func (s *Server) initializeResult() map[string]any {
	experimental := map[string]any{"claude/channel": map[string]any{}}
	if s.permission {
		experimental["claude/channel/permission"] = map[string]any{}
	}
	return map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{"experimental": experimental},
		"serverInfo":      map[string]any{"name": s.name, "version": s.version},
		"instructions":    s.instruction,
	}
}

func (s *Server) reply(id json.RawMessage, result any, rpcErr *rpcError) {
	s.write(response{JSONRPC: "2.0", ID: id, Result: result, Error: rpcErr})
}

func (s *Server) notify(method string, params any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("encode %s: %w", method, err)
	}
	return s.write(message{JSONRPC: "2.0", Method: method, Params: raw})
}

// write emits one message as a line. Writes are serialized and flushed
// immediately: a buffered notification is a message that never arrives.
func (s *Server) write(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.out == nil {
		return errors.New("mcp: not serving")
	}
	if _, err := s.out.Write(append(data, '\n')); err != nil {
		return err
	}
	return s.out.Flush()
}

// sanitizeMeta drops entries Claude Code would drop anyway. Its keys become
// attributes on the <channel> tag and must be identifiers; a key with a
// hyphen is silently discarded there, so it is discarded here where the
// debug log can say so.
func sanitizeMeta(meta map[string]string) map[string]string {
	if len(meta) == 0 {
		return nil
	}
	out := make(map[string]string, len(meta))
	for k, v := range meta {
		if !identifier(k) {
			debuglog.Printf("mcp: dropping meta key %q: not an identifier", k)
			continue
		}
		out[k] = v
	}
	return out
}

func identifier(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
		default:
			return false
		}
	}
	return true
}
