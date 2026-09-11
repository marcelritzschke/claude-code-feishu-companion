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
	"strings"
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

// args reads the two things this tool takes: whether to leave the cards
// standing, and a name fragment to post only the cards that match - a
// schema change usually breaks one card, and looking at it should not cost
// a screenful of the others.
func args() (keep bool, only string) {
	for _, a := range os.Args[1:] {
		if a == "-keep" || a == "--keep" {
			keep = true
			continue
		}
		only = a
	}
	return keep, only
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
		{"session/sent", func() (string, error) {
			return notify.SessionCard(sess(session.Working), t,
				notify.SessionView{ActivityAt: view.ActivityAt, Sent: "lgtm, ship it"})
		}},
		{"session/notify-only", func() (string, error) { return notify.SessionCard(notifyOnly, t, view) }},
		{"session/interrupted", func() (string, error) { return notify.InterruptedSessionCard(sess(session.Idle), t) }},
		{"session/settled-ok", func() (string, error) { return notify.SettledSessionCard(sess(session.Idle), t, "") }},
		{"session/settled-failed", func() (string, error) { return notify.SettledSessionCard(sess(session.Idle), failed, "") }},
		{"session/no-longer-live", func() (string, error) { return notify.PausedSessionCard(sess(session.Working), t, "") }},
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
		{"setup/callback-probe", notify.CallbackProbeCard},
		{"session/resting", func() (string, error) { return notify.RestingSessionCard(sess(session.Idle), t) }},
		{"session/resting-empty", func() (string, error) {
			return notify.RestingSessionCard(sess(session.Idle), &transcript.Turn{})
		}},
		{"session/resting-notify-only", func() (string, error) { return notify.RestingSessionCard(notifyOnly, t) }},
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

	keep, only := args()
	var ids []string
	bad := 0
	for _, cd := range cards {
		if only != "" && !strings.Contains(cd.name, only) {
			continue
		}
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
	fmt.Printf("\n%d sent, %d failed.\n", len(ids), bad)
	if keep {
		fmt.Println("cards left standing for visual review")
		return
	}
	for _, id := range ids {
		_ = c.DeleteMessage(ctx, id)
	}
	fmt.Println("recalled")
}
