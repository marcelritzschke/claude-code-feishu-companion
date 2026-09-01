package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// serve feeds a scripted peer's messages through a server and returns
// everything it wrote back, the way Claude Code would see it. Claude Code
// closing the session's stdin is what ends a real channel, so the scripted
// input ending is what ends this one.
func serve(t *testing.T, s *Server, incoming ...string) []map[string]any {
	t.Helper()
	in := strings.NewReader(strings.Join(incoming, "\n") + "\n")
	var out bytes.Buffer

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Serve(ctx, in, &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	var got []map[string]any
	dec := json.NewDecoder(&out)
	for {
		var m map[string]any
		if err := dec.Decode(&m); err != nil {
			return got
		}
		got = append(got, m)
	}
}

// idle starts a server on an input that is already at end of stream, so the
// protocol loop finishes and the test can push messages without racing it.
func idle(t *testing.T, s *Server) *bytes.Buffer {
	t.Helper()
	var out bytes.Buffer
	if err := s.Serve(context.Background(), strings.NewReader(""), &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	return &out
}

const initialize = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`

// The handshake is what makes this server a channel. Without the
// experimental key Claude Code registers no listener and every message
// Wirelark pushes disappears without an error.
func TestInitializeDeclaresTheChannelCapability(t *testing.T) {
	s := New("wirelark", "2.0.0", "instructions", true)
	replies := serve(t, s, initialize)
	if len(replies) != 1 {
		t.Fatalf("got %d replies to initialize, want 1", len(replies))
	}

	result, ok := replies[0]["result"].(map[string]any)
	if !ok {
		t.Fatalf("initialize reply carried no result: %+v", replies[0])
	}
	if result["protocolVersion"] != ProtocolVersion {
		t.Errorf("protocolVersion = %v, want %q", result["protocolVersion"], ProtocolVersion)
	}
	if result["instructions"] != "instructions" {
		t.Errorf("instructions = %v, want them delivered to Claude", result["instructions"])
	}

	caps, _ := result["capabilities"].(map[string]any)
	exp, _ := caps["experimental"].(map[string]any)
	if _, ok := exp["claude/channel"]; !ok {
		t.Errorf("experimental capabilities = %+v, want claude/channel", exp)
	}
	if _, ok := exp["claude/channel/permission"]; !ok {
		t.Errorf("experimental capabilities = %+v, want claude/channel/permission", exp)
	}
	if _, ok := caps["tools"]; ok {
		t.Error("the channel declared a tools capability; it is one-way by design")
	}
}

// Permission relay is a capability that lets anyone who can reach the
// channel approve tool use. When it is switched off it must not be
// declared, because declaring it is what makes Claude Code send prompts.
func TestPermissionRelayIsNotDeclaredWhenOff(t *testing.T) {
	s := New("wirelark", "2.0.0", "", false)
	replies := serve(t, s, initialize)
	result := replies[0]["result"].(map[string]any)
	exp := result["capabilities"].(map[string]any)["experimental"].(map[string]any)
	if _, ok := exp["claude/channel/permission"]; ok {
		t.Error("permission relay was declared while switched off")
	}
	if _, ok := exp["claude/channel"]; !ok {
		t.Error("the channel capability itself must still be declared")
	}
}

// A peer waiting on a reply that never comes is worse than one told no.
func TestUnknownRequestIsAnswered(t *testing.T) {
	s := New("wirelark", "2.0.0", "", true)
	replies := serve(t, s, `{"jsonrpc":"2.0","id":7,"method":"tools/list"}`)
	if len(replies) != 1 {
		t.Fatalf("got %d replies, want the request answered", len(replies))
	}
	rpcErr, ok := replies[0]["error"].(map[string]any)
	if !ok {
		t.Fatalf("reply = %+v, want an error", replies[0])
	}
	if rpcErr["code"].(float64) != -32601 {
		t.Errorf("error code = %v, want -32601 (method not found)", rpcErr["code"])
	}
}

func TestPingIsAnswered(t *testing.T) {
	s := New("wirelark", "2.0.0", "", true)
	replies := serve(t, s, `{"jsonrpc":"2.0","id":2,"method":"ping"}`)
	if len(replies) != 1 || replies[0]["result"] == nil {
		t.Errorf("ping reply = %+v, want an empty result", replies)
	}
}

// A malformed message from the peer must not take the channel down: the
// session would lose its only way to be reached.
func TestGarbageDoesNotEndTheChannel(t *testing.T) {
	s := New("wirelark", "2.0.0", "", true)
	replies := serve(t, s, `not json at all`, initialize)
	if len(replies) != 1 || replies[0]["result"] == nil {
		t.Errorf("replies = %+v, want the channel to have survived and answered initialize", replies)
	}
}

func TestPushEventShape(t *testing.T) {
	s := New("wirelark", "2.0.0", "", true)
	out := idle(t, s)

	if err := s.PushEvent("check the mobile client first", map[string]string{
		"project":     "payments-api",
		"chat-id":     "dropped: not an identifier",
		"sender_name": "feishu",
	}); err != nil {
		t.Fatal(err)
	}

	var msg struct {
		Method string `json:"method"`
		Params struct {
			Content string            `json:"content"`
			Meta    map[string]string `json:"meta"`
		} `json:"params"`
	}
	if err := json.Unmarshal([]byte(out.String()), &msg); err != nil {
		t.Fatalf("%v in %q", err, out.String())
	}
	if msg.Method != MethodChannel {
		t.Errorf("method = %q, want %q", msg.Method, MethodChannel)
	}
	if msg.Params.Content != "check the mobile client first" {
		t.Errorf("content = %q", msg.Params.Content)
	}
	if _, ok := msg.Params.Meta["chat-id"]; ok {
		t.Error("a meta key that is not an identifier must be dropped here, not silently by Claude Code")
	}
	if msg.Params.Meta["project"] != "payments-api" {
		t.Errorf("meta = %+v, want the project carried through", msg.Params.Meta)
	}
}

func TestSendVerdictShape(t *testing.T) {
	s := New("wirelark", "2.0.0", "", true)
	out := idle(t, s)

	if err := s.SendVerdict("abcde", "allow"); err != nil {
		t.Fatal(err)
	}

	var msg struct {
		Method string            `json:"method"`
		Params map[string]string `json:"params"`
	}
	if err := json.Unmarshal([]byte(out.String()), &msg); err != nil {
		t.Fatalf("%v in %q", err, out.String())
	}
	if msg.Method != MethodPermission {
		t.Errorf("method = %q, want %q", msg.Method, MethodPermission)
	}
	if msg.Params["request_id"] != "abcde" || msg.Params["behavior"] != "allow" {
		t.Errorf("params = %+v", msg.Params)
	}
}

func TestPermissionRequestReachesTheHandler(t *testing.T) {
	s := New("wirelark", "2.0.0", "", true)
	got := make(chan PermissionRequest, 1)
	s.OnPermissionRequest(func(r PermissionRequest) { got <- r })

	serve(t, s, `{"jsonrpc":"2.0","method":"`+MethodPermissionRequest+`","params":`+
		`{"request_id":"qwert","tool_name":"Bash","description":"Install dependencies","input_preview":"{\"command\":\"npm install\"}"}}`)

	select {
	case req := <-got:
		if req.RequestID != "qwert" || req.ToolName != "Bash" {
			t.Errorf("request = %+v", req)
		}
		if req.InputPreview == "" {
			t.Error("input_preview was dropped; it carries the command the user is approving")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the permission request never reached the handler")
	}
}

// A verdict without an id is discarded by Claude Code anyway; relaying one
// to the user would offer them a button that cannot work.
func TestPermissionRequestWithoutIDIsIgnored(t *testing.T) {
	s := New("wirelark", "2.0.0", "", true)
	got := make(chan PermissionRequest, 1)
	s.OnPermissionRequest(func(r PermissionRequest) { got <- r })

	serve(t, s, `{"jsonrpc":"2.0","method":"`+MethodPermissionRequest+`","params":{"tool_name":"Bash"}}`)

	select {
	case req := <-got:
		t.Errorf("an unanswerable request reached the handler: %+v", req)
	case <-time.After(200 * time.Millisecond):
	}
}
