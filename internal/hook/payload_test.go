package hook

import (
	"strings"
	"testing"
)

func TestDecode(t *testing.T) {
	in := `{"session_id":"abcdef123456","prompt_id":"p-1","cwd":"/home/user/repo",
		"hook_event_name":"PermissionRequest","tool_name":"Bash",
		"tool_input":{"command":"rm -rf node_modules","description":"Clean"},
		"unknown_future_field":{"x":1}}`
	p, err := Decode(strings.NewReader(in))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if p.SessionID != "abcdef123456" || p.Cwd != "/home/user/repo" || p.PromptID != "p-1" {
		t.Errorf("got %+v", p)
	}
	if p.HookEventName != EventPermissionRequest {
		t.Errorf("event = %q", p.HookEventName)
	}
	if p.ToolName != "Bash" {
		t.Errorf("tool = %q", p.ToolName)
	}
	if cmd, _ := p.ToolInput["command"].(string); cmd != "rm -rf node_modules" {
		t.Errorf("tool_input = %#v", p.ToolInput)
	}
}

func TestDecodeStopFailure(t *testing.T) {
	in := `{"hook_event_name":"StopFailure","error":"rate_limit","error_details":"retry after 60s",
		"last_assistant_message":"partial"}`
	p, err := Decode(strings.NewReader(in))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if p.Error != "rate_limit" || p.ErrorDetails != "retry after 60s" {
		t.Errorf("got %+v", p)
	}
	if p.LastAssistantMessage != "partial" {
		t.Errorf("last message = %q", p.LastAssistantMessage)
	}
}

func TestSubagent(t *testing.T) {
	p, _ := Decode(strings.NewReader(`{"hook_event_name":"Stop","agent_id":"a1"}`))
	if !p.Subagent() {
		t.Error("agent_id set should mark subagent")
	}
	p, _ = Decode(strings.NewReader(`{"hook_event_name":"Stop"}`))
	if p.Subagent() {
		t.Error("missing agent_id should not mark subagent")
	}
}

func TestHandled(t *testing.T) {
	for _, e := range []string{EventPermissionRequest, EventPostToolUse, EventStop, EventStopFailure} {
		p, _ := Decode(strings.NewReader(`{"hook_event_name":"` + e + `"}`))
		if !p.Handled() {
			t.Errorf("%s should be handled", e)
		}
	}
	// PreToolUse only matters when Claude asks a question.
	p, _ := Decode(strings.NewReader(`{"hook_event_name":"PreToolUse","tool_name":"AskUserQuestion"}`))
	if !p.Handled() {
		t.Error("PreToolUse on AskUserQuestion should be handled")
	}
	for _, e := range []string{"PreToolUse", "Notification", "SessionStart", "SessionEnd",
		"SubagentStop", "UserPromptSubmit", "", "Stop2"} {
		p, _ := Decode(strings.NewReader(`{"hook_event_name":"` + e + `","tool_name":"Bash"}`))
		if p.Handled() {
			t.Errorf("%q should not be handled", e)
		}
	}
}

func TestProjectLabel(t *testing.T) {
	t.Setenv("CLAUDE_PROJECT_DIR", "/home/user/my-repo")
	p := &Payload{SessionID: "0123456789abcdef", Cwd: "/tmp/somewhere"}
	if got := p.ProjectLabel(); got != "my-repo" {
		t.Errorf("project = %q", got)
	}
}

func TestProjectLabelFallbacks(t *testing.T) {
	t.Setenv("CLAUDE_PROJECT_DIR", "")
	if got := (&Payload{Cwd: "/home/user/other"}).ProjectLabel(); got != "other" {
		t.Errorf("project = %q", got)
	}
	if got := (&Payload{}).ProjectLabel(); got != "unknown project" {
		t.Errorf("project = %q", got)
	}
}

// Native Windows is a supported runtime: a backslash path must yield the
// project name, not the whole path.
func TestProjectLabelWindowsPath(t *testing.T) {
	t.Setenv("CLAUDE_PROJECT_DIR", "")
	if got := (&Payload{Cwd: `C:\work\payments-api`}).ProjectLabel(); got != "payments-api" {
		t.Errorf("project label = %q, want %q", got, "payments-api")
	}
	// A volume root has no project name; showing the path beats inventing one.
	if got := (&Payload{Cwd: `C:\`}).ProjectLabel(); got != `C:\` {
		t.Errorf("project label = %q, want %q", got, `C:\`)
	}
}
