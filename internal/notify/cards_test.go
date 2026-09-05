package notify

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/marcelritzschke/claude-code-feishu-companion/internal/hook"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/transcript"
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

// cardElements returns a card's body elements.
func cardElements(t *testing.T, m map[string]any) []any {
	t.Helper()
	body, ok := m["body"].(map[string]any)
	if !ok {
		t.Fatalf("card has no body: %v", m)
	}
	els, _ := body["elements"].([]any)
	return els
}

// sections returns the card's body sections and its footer.
//
// The hard-break markers card() adds are undone here: they are how a
// newline survives markdown rendering, not part of what the card says, and
// a test asserting on content should not have to know about them.
func sections(t *testing.T, m map[string]any) (divs []string, note string) {
	t.Helper()
	for _, e := range cardElements(t, m) {
		el := e.(map[string]any)
		if el["tag"] != "markdown" {
			continue
		}
		content := strings.ReplaceAll(el["content"].(string), "  \n", "\n")
		if el["text_size"] == textSizeNotation {
			note = content
			continue
		}
		divs = append(divs, content)
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
	card, err := CompletionCard(stopPayload(), sampleTurn(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	m := decodeCard(t, card)
	template, title, subtitle := headerOf(t, m)
	if template != "green" || title != "✅ Completed · 4m 18s" {
		t.Errorf("header = %q / %q", template, title)
	}
	if subtitle != "Fix token refresh · payments-api" {
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

// A finished turn keeps what it did, folded away. "What happened while I
// was away" is half of what this product answers, and a card that dropped
// the history the moment the turn ended could not answer it.
func TestCompletionCardKeepsTheTurnsStepsFolded(t *testing.T) {
	turn := sampleTurn()
	turn.Steps = []transcript.Step{
		{Tool: "Read", Input: map[string]any{"file_path": "/w/refresh.go"}, Done: true},
		{Tool: "Edit", Input: map[string]any{"file_path": "/w/refresh.go"}, Done: true},
		{Tool: "Bash", Input: map[string]any{"command": "go test ./..."}, Done: true},
	}
	card, err := CompletionCard(stopPayload(), turn, Options{})
	if err != nil {
		t.Fatal(err)
	}
	panels := panelsOf(t, decodeCard(t, card))
	if len(panels) != 1 {
		t.Fatalf("completion should carry exactly one history panel, got %d", len(panels))
	}
	if panels[0].expanded {
		t.Error("a settled card has nothing running; its history must arrive shut")
	}
	if !strings.Contains(panels[0].title, "3 steps") {
		t.Errorf("history panel title = %q, want the step count", panels[0].title)
	}
	for _, want := range []string{"Read refresh.go", "Updated refresh.go", "Ran go test ./..."} {
		if !strings.Contains(panels[0].body, want) {
			t.Errorf("history is missing %q: %s", want, panels[0].body)
		}
	}
}

// panelInfo is a collapsible panel as a test reads it.
type panelInfo struct {
	title    string
	body     string
	expanded bool
}

// panelsOf returns the collapsible panels on a card, in order.
func panelsOf(t *testing.T, m map[string]any) []panelInfo {
	t.Helper()
	var out []panelInfo
	for _, e := range cardElements(t, m) {
		el := e.(map[string]any)
		if el["tag"] != "collapsible_panel" {
			continue
		}
		hdr := el["header"].(map[string]any)
		p := panelInfo{title: hdr["title"].(map[string]any)["content"].(string)}
		p.expanded, _ = el["expanded"].(bool)
		if inner, ok := el["elements"].([]any); ok && len(inner) > 0 {
			p.body, _ = inner[0].(map[string]any)["content"].(string)
		}
		p.title = strings.ReplaceAll(p.title, "  \n", "\n")
		p.body = strings.ReplaceAll(p.body, "  \n", "\n")
		out = append(out, p)
	}
	return out
}

func TestCompletionCardFailedValidation(t *testing.T) {
	turn := sampleTurn()
	turn.Tests = []transcript.TestRun{{Command: "go test ./...", Passed: false}}
	card, err := CompletionCard(stopPayload(), turn, Options{})
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
	card, err := CompletionCard(p, sampleTurn(), Options{})
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
	card, err := CompletionCard(p, sampleTurn(), Options{})
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
	card, err := PermissionCard(p, sampleTurn(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	m := decodeCard(t, card)
	template, title, subtitle := headerOf(t, m)
	if template != "orange" || title != "⚠️ Permission required" {
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
	card, err := PermissionCard(p, nil, Options{})
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
	card, err := QuestionCard(p, sampleTurn(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	m := decodeCard(t, card)
	template, title, _ := headerOf(t, m)
	if template != "blue" || title != "❓ Claude needs your input" {
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
	// A question is a terminal dialog: no channel can answer it, so the
	// card must say where it has to be answered rather than imply Claude Companion
	// could take the answer.
	if note != "This must currently be answered in Claude Code." {
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
	card, err := FailureCard(p, sampleTurn(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	m := decodeCard(t, card)
	template, title, subtitle := headerOf(t, m)
	if template != "red" || title != "🔴 Failed · 4m 18s" {
		t.Errorf("header = %q / %q", template, title)
	}
	if !strings.Contains(subtitle, "payments-api") {
		t.Errorf("subtitle = %q", subtitle)
	}
	divs, note := sections(t, m)
	if !strings.Contains(divs[0], "billing problem") {
		t.Errorf("error text = %q", divs[0])
	}
	// sampleTurn's validation passed; the turn failed for an API reason, and
	// the card shows both facts apart so neither is mistaken for the other.
	if !strings.Contains(divs[1], "**Validation**") {
		t.Errorf("validation = %q", divs[1])
	}
	if !strings.Contains(divs[2], "**Last relevant error**") || !strings.Contains(divs[2], "credit balance too low") {
		t.Errorf("details = %q", divs[2])
	}
	if note != "Open Claude Code to continue." {
		t.Errorf("note = %q", note)
	}
}

func TestFailureCardUnknownError(t *testing.T) {
	p := &hook.Payload{HookEventName: hook.EventStopFailure, Error: "something_new"}
	card, err := FailureCard(p, nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(card, "The turn ended with an API error.") {
		t.Errorf("card = %s", card)
	}
}

func TestProgressCard(t *testing.T) {
	p := &hook.Payload{HookEventName: hook.EventPostToolUse, Cwd: "/home/u/payments-api"}
	card, err := ProgressCard(p, sampleTurn(), Options{})
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
	card, err := ProgressCard(p, turn, Options{})
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
