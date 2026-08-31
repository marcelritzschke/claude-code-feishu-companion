package notify

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/marcelritzschke/wirelark/internal/hook"
	"github.com/marcelritzschke/wirelark/internal/transcript"
)

// decodeCard parses a card into a generic map so tests assert on structure
// rather than exact JSON strings.
func decodeCard(t *testing.T, card string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(card), &m); err != nil {
		t.Fatalf("card is not valid JSON: %v\n%s", err, card)
	}
	return m
}

func headerOf(t *testing.T, m map[string]any) (template, title, subtitle string) {
	t.Helper()
	hdr := m["header"].(map[string]any)
	template, _ = hdr["template"].(string)
	title = hdr["title"].(map[string]any)["content"].(string)
	if sub, ok := hdr["subtitle"].(map[string]any); ok {
		subtitle, _ = sub["content"].(string)
	}
	return
}

// sections returns the card's div texts (sections) and note text (footer).
func sections(t *testing.T, m map[string]any) (divs []string, note string) {
	t.Helper()
	for _, e := range m["elements"].([]any) {
		el := e.(map[string]any)
		switch el["tag"] {
		case "div":
			divs = append(divs, el["text"].(map[string]any)["content"].(string))
		case "note":
			note = el["elements"].([]any)[0].(map[string]any)["content"].(string)
		}
	}
	return
}

func stopPayload() *hook.Payload {
	return &hook.Payload{
		HookEventName:        hook.EventStop,
		SessionID:            "sess",
		Cwd:                  "/home/u/payments-api",
		LastAssistantMessage: "The refresh flow now rotates the token after every successful refresh and rejects reused tokens. I also updated the session middleware to validate the new token shape.",
	}
}

func sampleTurn() *transcript.Turn {
	start := time.Now().Add(-258 * time.Second) // 4m 18s
	return &transcript.Turn{
		Start: start,
		Title: "Fix token refresh",
		Files: []string{"session.go", "token.go", "session_test.go", "middleware.go"},
		Tests: []transcript.TestRun{
			{Command: "go test ./...", Passed: true},
			{Command: "pytest -q tests/", Passed: true},
		},
		LatestTool: &transcript.ToolCall{Name: "Bash", Input: map[string]any{"command": "go test ./..."}},
	}
}

func TestCompletionCardNormal(t *testing.T) {
	card, err := CompletionCard(stopPayload(), sampleTurn(), Normal)
	if err != nil {
		t.Fatal(err)
	}
	m := decodeCard(t, card)
	template, title, subtitle := headerOf(t, m)
	if template != "green" || title != "✅ Claude finished" {
		t.Errorf("header = %q / %q", template, title)
	}
	if subtitle != "Fix token refresh · payments-api · 4m 18s" {
		t.Errorf("subtitle = %q", subtitle)
	}
	divs, note := sections(t, m)
	if len(divs) != 3 {
		t.Fatalf("divs = %q", divs)
	}
	if !strings.HasPrefix(divs[0], "The refresh flow now rotates") {
		t.Errorf("summary = %q", divs[0])
	}
	if !strings.Contains(divs[1], "**Validation**") || !strings.Contains(divs[1], "✓ go test ./... passed") {
		t.Errorf("validation = %q", divs[1])
	}
	if !strings.Contains(divs[2], "**Claude**") || !strings.Contains(divs[2], `"I also updated`) {
		t.Errorf("quote = %q", divs[2])
	}
	if note != "" {
		t.Errorf("completion needs no footer, got %q", note)
	}
}

func TestCompletionCardCompact(t *testing.T) {
	card, err := CompletionCard(stopPayload(), sampleTurn(), Compact)
	if err != nil {
		t.Fatal(err)
	}
	m := decodeCard(t, card)
	divs, _ := sections(t, m)
	if len(divs) != 1 {
		t.Fatalf("compact completion should be a single section, got %q", divs)
	}
	// Compact states the validation outcome as prose, not as check lines.
	if !strings.Contains(divs[0], "The refresh flow now rotates") || !strings.Contains(divs[0], "2 validation commands passed.") {
		t.Errorf("compact body = %q", divs[0])
	}
	if strings.Contains(divs[0], "✓") {
		t.Errorf("compact must not render check lines: %q", divs[0])
	}
}

func TestCompletionCardFailedValidation(t *testing.T) {
	turn := sampleTurn()
	turn.Tests = []transcript.TestRun{{Command: "go test ./...", Passed: false}}
	card, err := CompletionCard(stopPayload(), turn, Normal)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(card, "✗ go test ./... failed") {
		t.Errorf("failed test not marked: %s", card)
	}
}

func TestCompletionCardWithoutFinalMessage(t *testing.T) {
	p := stopPayload()
	p.LastAssistantMessage = ""
	card, err := CompletionCard(p, sampleTurn(), Normal)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(card, "Updated 4 files.") {
		t.Errorf("fallback summary missing: %s", card)
	}
}

func TestCompletionCardSkipsMarkdownHeadings(t *testing.T) {
	p := stopPayload()
	p.LastAssistantMessage = "## Summary\n- Fixed the token rotation.\n\nDetails follow."
	card, err := CompletionCard(p, sampleTurn(), Normal)
	if err != nil {
		t.Fatal(err)
	}
	m := decodeCard(t, card)
	divs, _ := sections(t, m)
	if !strings.HasPrefix(divs[0], "Fixed the token rotation.") {
		t.Errorf("summary = %q", divs[0])
	}
}

func TestPermissionCard(t *testing.T) {
	p := &hook.Payload{
		HookEventName: hook.EventPermissionRequest,
		Cwd:           "/home/u/payments-api",
		ToolName:      "Bash",
		ToolInput:     map[string]any{"command": "rm -rf node_modules && npm install", "description": "Reinstall"},
	}
	card, err := PermissionCard(p, sampleTurn())
	if err != nil {
		t.Fatal(err)
	}
	m := decodeCard(t, card)
	template, title, subtitle := headerOf(t, m)
	if template != "orange" || title != "⚠️ Claude needs your attention" {
		t.Errorf("header = %q / %q", template, title)
	}
	if subtitle != "Fix token refresh · payments-api" {
		t.Errorf("subtitle = %q", subtitle)
	}
	divs, note := sections(t, m)
	if !strings.Contains(divs[0], "waiting for permission") {
		t.Errorf("lead = %q", divs[0])
	}
	if !strings.Contains(divs[1], "**Requested action**") || !strings.Contains(divs[1], "rm -rf node_modules && npm install") {
		t.Errorf("action = %q", divs[1])
	}
	if note != "Open Claude Code to respond." {
		t.Errorf("note = %q", note)
	}
}

func TestPermissionCardTruncatesLongCommand(t *testing.T) {
	p := &hook.Payload{
		HookEventName: hook.EventPermissionRequest,
		ToolName:      "Bash",
		ToolInput:     map[string]any{"command": strings.Repeat("x", 400)},
	}
	card, err := PermissionCard(p, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(card, "(truncated)") {
		t.Errorf("long command must be marked truncated: %s", card)
	}
}

func TestQuestionCard(t *testing.T) {
	p := &hook.Payload{
		HookEventName: hook.EventPreToolUse,
		ToolName:      "AskUserQuestion",
		Cwd:           "/home/u/payments-api",
		ToolInput: map[string]any{
			"questions": []any{
				map[string]any{
					"question": "Which API behavior should I keep?",
					"header":   "API",
					"options": []any{
						map[string]any{"label": "Return 401", "description": "when the refresh token is expired"},
						map[string]any{"label": "Silent refresh"},
					},
				},
			},
		},
	}
	card, err := QuestionCard(p, sampleTurn())
	if err != nil {
		t.Fatal(err)
	}
	m := decodeCard(t, card)
	template, title, _ := headerOf(t, m)
	if template != "blue" || title != "❓ Claude has a question" {
		t.Errorf("header = %q / %q", template, title)
	}
	divs, note := sections(t, m)
	if len(divs) != 1 || !strings.Contains(divs[0], "Which API behavior should I keep?") {
		t.Fatalf("divs = %q", divs)
	}
	if !strings.Contains(divs[0], "A. Return 401 - when the refresh token is expired") {
		t.Errorf("options missing detail: %q", divs[0])
	}
	if !strings.Contains(divs[0], "B. Silent refresh") {
		t.Errorf("options = %q", divs[0])
	}
	if note != "Open Claude Code to answer." {
		t.Errorf("note = %q", note)
	}
}

func TestFailureCard(t *testing.T) {
	p := &hook.Payload{
		HookEventName: hook.EventStopFailure,
		Cwd:           "/home/u/payments-api",
		Error:         "billing_error",
		ErrorDetails:  "credit balance too low on account acme-123",
	}
	card, err := FailureCard(p, sampleTurn())
	if err != nil {
		t.Fatal(err)
	}
	m := decodeCard(t, card)
	template, title, subtitle := headerOf(t, m)
	if template != "red" || title != "❌ Claude couldn't finish" {
		t.Errorf("header = %q / %q", template, title)
	}
	if !strings.Contains(subtitle, "payments-api") {
		t.Errorf("subtitle = %q", subtitle)
	}
	divs, note := sections(t, m)
	if !strings.Contains(divs[0], "billing problem") {
		t.Errorf("error text = %q", divs[0])
	}
	if !strings.Contains(divs[1], "**Last relevant error**") || !strings.Contains(divs[1], "credit balance too low") {
		t.Errorf("details = %q", divs[1])
	}
	if note != "Open Claude Code to continue." {
		t.Errorf("note = %q", note)
	}
}

func TestFailureCardUnknownError(t *testing.T) {
	p := &hook.Payload{HookEventName: hook.EventStopFailure, Error: "something_new"}
	card, err := FailureCard(p, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(card, "The turn ended with an API error.") {
		t.Errorf("card = %s", card)
	}
}

func TestProgressCard(t *testing.T) {
	p := &hook.Payload{HookEventName: hook.EventPostToolUse, Cwd: "/home/u/payments-api"}
	card, err := ProgressCard(p, sampleTurn())
	if err != nil {
		t.Fatal(err)
	}
	m := decodeCard(t, card)
	template, title, subtitle := headerOf(t, m)
	if template != "yellow" || title != "🟡 Claude is still working" {
		t.Errorf("header = %q / %q", template, title)
	}
	if !strings.Contains(subtitle, "payments-api") {
		t.Errorf("subtitle = %q", subtitle)
	}
	divs, _ := sections(t, m)
	if !strings.Contains(divs[0], "**Current activity**") || !strings.Contains(divs[0], "Running go test ./...") {
		t.Errorf("activity = %q", divs[0])
	}
	if !strings.Contains(divs[1], "**So far**") || !strings.Contains(divs[1], "• Updated 4 files") || !strings.Contains(divs[1], "go test ./... passed") {
		t.Errorf("facts = %q", divs[1])
	}
}

func TestProgressCardWithoutFacts(t *testing.T) {
	p := &hook.Payload{HookEventName: hook.EventPostToolUse}
	turn := &transcript.Turn{Start: time.Now().Add(-time.Minute)}
	card, err := ProgressCard(p, turn)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(card, "So far") {
		t.Errorf("no facts should mean no So-far section: %s", card)
	}
}

func TestContextLineFallsBackToProject(t *testing.T) {
	p := &hook.Payload{Cwd: "/home/u/payments-api"}
	if got := contextLine(p, nil); got != "payments-api" {
		t.Errorf("context = %q", got)
	}
}

func TestContextLineOmitsDurationWhenStartUnknown(t *testing.T) {
	p := &hook.Payload{Cwd: "/home/u/payments-api"}
	if got := contextWithDuration(p, &transcript.Turn{Title: "Fix token refresh"}); got != "Fix token refresh · payments-api" {
		t.Errorf("context = %q", got)
	}
}
