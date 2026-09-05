// Command cardsmoke renders every card Claude Companion can build and
// posts each one to Feishu, so a schema change is verified against the
// real renderer rather than against a test's idea of one.
//
// It is not a simulation of a session. A session shows one card and
// rewrites it; this posts every state as a separate message so they can
// all be looked at side by side. The label on each line says which of the
// three real cards that state belongs to.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/marcelritzschke/claude-code-feishu-companion/internal/config"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/feishu"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/hook"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/mcp"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/notify"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/session"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/transcript"
)

func turn() *transcript.Turn {
	return &transcript.Turn{
		Start:    time.Now().Add(-258 * time.Second),
		Title:    "Fix token refresh",
		Progress: "Consolidating refresh validation and checking the affected callers.",
		Files:    []string{"session.go", "token.go"},
		Tests: []transcript.TestRun{
			{Command: "go test ./...", Passed: true},
			{Command: "pytest -q tests/", Passed: false},
		},
		Steps: []transcript.Step{
			{Tool: "Read", Input: map[string]any{"file_path": "/w/payments-api/refresh.go"}, Done: true},
			{Tool: "Read", Input: map[string]any{"file_path": "/w/payments-api/token.go"}, Done: true},
			{Tool: "Edit", Input: map[string]any{"file_path": "/w/payments-api/refresh.go"}, Done: true},
			{Tool: "Bash", Input: map[string]any{"command": "go build ./..."}, Done: true, Errored: true,
				Error: "refresh.go:42: undefined: validateOnce"},
			{Tool: "Bash", Input: map[string]any{"command": "go test ./..."}},
		},
		LatestTool: &transcript.ToolCall{Name: "Bash", Input: map[string]any{"command": "go test ./..."}},
	}
}

func payload(event string) *hook.Payload {
	return &hook.Payload{
		HookEventName: event, SessionID: "sess", Cwd: "/home/u/payments-api",
		ToolName: "Bash", ToolInput: map[string]any{"command": "rm -rf ./build"},
		LastAssistantMessage: "The refresh flow now rotates the token after every successful refresh and rejects reused tokens.",
	}
}

// questionPayload is an AskUserQuestion the way Claude Code writes it.
func questionPayload() *hook.Payload {
	p := payload(hook.EventPreToolUse)
	p.ToolName = "AskUserQuestion"
	p.ToolInput = map[string]any{"questions": []any{map[string]any{
		"question": "Which API should remain backwards compatible?",
		"options": []any{
			map[string]any{"label": "v1", "description": "Keep the current clients working"},
			map[string]any{"label": "v2", "description": "Break v1, ship the new shape"},
			map[string]any{"label": "Both", "description": "Dual-serve until the next release"},
		},
	}}}
	return p
}

func sess(state session.State) session.Session {
	return session.Session{
		ID: "s1", Dir: "/home/u/payments-api", Title: "Fix token refresh",
		State: state, Remote: session.Ready, PID: os.Getpid(),
	}
}

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Println("config:", err)
		os.Exit(1)
	}
	c, err := feishu.New(cfg)
	if err != nil {
		fmt.Println("client:", err)
		os.Exit(1)
	}
	if callbackMode() {
		os.Exit(checkCallback(cfg, c))
	}

	ctx := context.Background()
	t := turn()
	view := notify.SessionView{ActivityAt: time.Now().Add(-8 * time.Second), Interruptible: true,
		Notes: []string{"Allowed once · go test ./..."}}
	req := mcp.PermissionRequest{RequestID: "r1", ToolName: "Bash",
		Description: "Remove the build directory", InputPreview: "rm -rf ./build"}

	failed := turn()
	failed.Failed = true
	notifyOnly := sess(session.Working)
	notifyOnly.Remote = session.Notifications

	// The prefix names the one message this state belongs to in a real
	// session: session/... states rewrite a single card from working
	// through to its outcome, permission/... likewise, and reply/... are
	// the only ones that are genuinely separate messages.
	cards := []struct {
		name string
		fn   func() (string, error)
	}{
		{"session/working", func() (string, error) { return notify.SessionCard(sess(session.Working), t, view) }},
		{"session/waiting", func() (string, error) { return notify.SessionCard(sess(session.Waiting), t, view) }},
		{"session/notify-only", func() (string, error) { return notify.SessionCard(notifyOnly, t, view) }},
		{"session/interrupted", func() (string, error) { return notify.InterruptedSessionCard(sess(session.Idle), t) }},
		{"session/settled-ok", func() (string, error) { return notify.SettledWatchCard(sess(session.Idle), t, "") }},
		{"session/settled-failed", func() (string, error) { return notify.SettledWatchCard(sess(session.Idle), failed, "") }},
		{"session/no-longer-live", func() (string, error) { return notify.WatchStoppedCard(sess(session.Working), t, "") }},
		{"permission/asked-by-hook", func() (string, error) {
			return notify.PermissionCard(payload(hook.EventPreToolUse), t, notify.Options{})
		}},
		{"question/asked", func() (string, error) { return notify.QuestionCard(questionPayload(), t, notify.Options{}) }},
		{"session/completed", func() (string, error) {
			return notify.CompletionCard(payload(hook.EventStop), t, notify.Options{ContinueSession: "s1"})
		}},
		{"session/failed", func() (string, error) {
			return notify.FailureCard(payload(hook.EventStop), failed, notify.Options{ContinueSession: "s1"})
		}},
		{"session/progress", func() (string, error) { return notify.ProgressCard(payload(hook.EventStop), t, notify.Options{}) }},
		{"question/answered", func() (string, error) { return notify.QuestionAnsweredCard(sess(session.Idle)) }},
		{"reply/sessions", func() (string, error) {
			return notify.OverviewCard([]session.Session{sess(session.Working), notifyOnly})
		}},
		{"reply/selected", func() (string, error) { return notify.SelectedCard(sess(session.Working)) }},
		{"permission/asked-relayed", func() (string, error) { return notify.PermissionRelayCard(sess(session.Waiting), req) }},
		{"permission/answered", func() (string, error) {
			return notify.PermissionAnsweredCard(sess(session.Working), req, notify.VerdictAllow)
		}},
		{"permission/answered-locally", func() (string, error) {
			return notify.PermissionHandledLocallyCard(sess(session.Working), req)
		}},
	}

	// A long turn is the case the element budget exists for: if the
	// budget is wrong the card stops updating partway through, which is
	// exactly the failure a user would never report as a card bug.
	for _, n := range []int{50, 200, 1000} {
		long := turn()
		long.Steps = nil
		for i := range n {
			long.Steps = append(long.Steps,
				transcript.Step{Tool: "Read", Input: map[string]any{"file_path": fmt.Sprintf("/w/p/f%d.go", i)}, Done: true},
				transcript.Step{Tool: "Bash", Input: map[string]any{"command": fmt.Sprintf("go test ./pkg%d", i)},
					Done: true, Errored: true, Error: "dial tcp 127.0.0.1:5432: connection refused"},
				transcript.Step{Tool: "Grep", Input: map[string]any{"pattern": fmt.Sprintf("tok%d", i)}, Done: true})
		}
		long.Steps = append(long.Steps, transcript.Step{Tool: "Bash", Input: map[string]any{"command": "go test ./..."}})
		cards = append(cards, struct {
			name string
			fn   func() (string, error)
		}{fmt.Sprintf("session/working-%d-steps", len(long.Steps)), func() (string, error) {
			return notify.SessionCard(sess(session.Working), long, view)
		}})
	}

	var ids []string
	bad := 0
	for _, cd := range cards {
		body, err := cd.fn()
		if err != nil {
			fmt.Printf("%-30s BUILD FAILED %v\n", cd.name, err)
			bad++
			continue
		}
		id, err := c.SendCard(ctx, body)
		if err != nil {
			fmt.Printf("%-30s SEND FAILED %v\n", cd.name, err)
			bad++
			continue
		}
		fmt.Printf("%-30s ok (%d bytes) %s\n", cd.name, len(body), id)
		ids = append(ids, id)
	}
	fmt.Printf("\n%d/%d accepted. In a session these are three cards, not %d messages.\n",
		len(cards)-bad, len(cards), len(cards))
	if len(os.Args) > 1 && os.Args[1] == "-keep" {
		fmt.Println("cards left standing for visual review")
		return
	}
	for _, id := range ids {
		_ = c.DeleteMessage(ctx, id)
	}
	fmt.Println("recalled")
}
