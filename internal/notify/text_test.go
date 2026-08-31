package notify

import (
	"strings"
	"testing"
	"time"

	"github.com/marcelritzschke/wirelark/internal/transcript"
)

func TestTruncateRunes(t *testing.T) {
	if got := truncateRunes("hello", 10); got != "hello" {
		t.Errorf("got %q", got)
	}
	if got := truncateRunes("hello", 5); got != "hello" {
		t.Errorf("got %q", got)
	}
	long := strings.Repeat("word ", 100)
	got := truncateRunes(long, 40)
	if !strings.HasSuffix(got, " ... (truncated)") || len([]rune(got)) > 40+len(" ... (truncated)") {
		t.Errorf("got %q", got)
	}
	// Unicode must not be split mid-rune.
	cjk := strings.Repeat("界", 100)
	got = truncateRunes(cjk, 10)
	if !strings.HasPrefix(got, strings.Repeat("界", 10)) {
		t.Errorf("got %q", got)
	}
}

func TestSplitFinal(t *testing.T) {
	cases := []struct {
		name, msg, wantSummary, wantRest string
	}{
		{
			name:        "two sentences",
			msg:         "Added refresh-token rotation. The flow now rejects reused tokens everywhere.",
			wantSummary: "Added refresh-token rotation.",
			wantRest:    "The flow now rejects reused tokens everywhere.",
		},
		{
			name:        "skips heading",
			msg:         "## Summary\nFixed the bug. Details below.",
			wantSummary: "Fixed the bug.",
			wantRest:    "Details below.",
		},
		{
			name:        "strips bullet",
			msg:         "- Consolidated the validation logic. More detail.",
			wantSummary: "Consolidated the validation logic.",
			wantRest:    "More detail.",
		},
		{
			name:        "single sentence",
			msg:         "All tests pass.",
			wantSummary: "All tests pass.",
			wantRest:    "",
		},
		{
			name:        "no sentence end",
			msg:         "Investigated the streaming card handling",
			wantSummary: "Investigated the streaming card handling",
			wantRest:    "",
		},
		{
			name:        "empty",
			msg:         "",
			wantSummary: "",
			wantRest:    "",
		},
		{
			name:        "cjk sentence end",
			msg:         "修复了令牌刷新。同时更新了中间件。",
			wantSummary: "修复了令牌刷新。",
			wantRest:    "同时更新了中间件。",
		},
		{
			name:        "version number does not split",
			msg:         "Upgraded to v1.2 and fixed the parser.",
			wantSummary: "Upgraded to v1.2 and fixed the parser.",
			wantRest:    "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			summary, rest := splitFinal(tc.msg)
			if summary != tc.wantSummary {
				t.Errorf("summary = %q, want %q", summary, tc.wantSummary)
			}
			if rest != tc.wantRest {
				t.Errorf("rest = %q, want %q", rest, tc.wantRest)
			}
		})
	}
}

func TestSplitFinalLongSummary(t *testing.T) {
	msg := strings.Repeat("sentence ", 100) + "end."
	summary, _ := splitFinal(msg)
	if len([]rune(summary)) > summaryCap {
		t.Errorf("summary exceeds cap: %d runes", len([]rune(summary)))
	}
	if !strings.HasSuffix(summary, " ... (truncated)") {
		t.Errorf("summary = %q", summary)
	}
}

func TestDescribeAction(t *testing.T) {
	cases := []struct {
		tool string
		in   map[string]any
		want string
	}{
		{"Bash", map[string]any{"command": "npm install"}, "Run:\nnpm install"},
		{"Bash", map[string]any{"command": "go test \\\n  ./..."}, "Run:\ngo test ./..."},
		{"Edit", map[string]any{"file_path": "/home/u/repo/auth/session.go"}, "Edit file:\nauth/session.go"},
		{"Write", map[string]any{"file_path": "/elsewhere/x.go"}, "Write file:\nx.go"},
		{"WebFetch", map[string]any{"url": "https://example.com"}, "Fetch URL:\nhttps://example.com"},
		{"WebSearch", map[string]any{"query": "feishu cards"}, "Search the web:\nfeishu cards"},
		{"Grep", map[string]any{"pattern": "RefreshToken"}, "Search code:\nRefreshToken"},
		{"Agent", map[string]any{"description": "Explore the repo"}, "Start subagent:\nExplore the repo"},
		{"ExitPlanMode", map[string]any{"plan": "## Refactor\n1. Extract helpers"}, "Approve plan:\n## Refactor 1. Extract helpers"},
		{"Bash", map[string]any{}, "Run."},
		{"Mystery", nil, "Use the Mystery tool."},
	}
	for _, tc := range cases {
		if got := describeAction(tc.tool, tc.in, "/home/u/repo"); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.tool, got, tc.want)
		}
	}
}

// A permission prompt shows what is being approved, exactly. Cleaning a
// command here would hide the redirect the user is agreeing to.
func TestDescribeActionKeepsCommandIntact(t *testing.T) {
	got := describeAction("Bash", map[string]any{"command": "cat secrets > /etc/passwd"}, "")
	if got != "Run:\ncat secrets > /etc/passwd" {
		t.Errorf("got %q", got)
	}
}

// The spec forbids dumping the underlying event payload: an unknown tool is
// named, never rendered as JSON.
func TestDescribeActionUnknownToolNeverDumpsPayload(t *testing.T) {
	got := describeAction("FutureTool", map[string]any{"path": "/x/y.go", "mode": 3}, "")
	if strings.ContainsAny(got, "{}") || strings.Contains(got, "mode") || strings.Contains(got, "/x/y.go") {
		t.Errorf("payload leaked into card: %q", got)
	}
	if got != "Use the FutureTool tool." {
		t.Errorf("got %q", got)
	}
}

func TestDescribeActionMCPToolReadsAsAName(t *testing.T) {
	got := describeAction("mcp__github__create_issue", map[string]any{"title": "x"}, "")
	if got != "Use create issue (github)." {
		t.Errorf("got %q", got)
	}
}

func TestDescribeActivity(t *testing.T) {
	cases := []struct {
		name string
		call *transcript.ToolCall
		want string
	}{
		{"command is named", &transcript.ToolCall{Name: "Bash", Input: map[string]any{"command": "go test ./..."}}, "Running go test ./..."},
		{"command is cleaned", &transcript.ToolCall{Name: "Bash", Input: map[string]any{"command": "go test ./... 2>&1 | tail"}}, "Running go test ./..."},
		{"edits aggregate", &transcript.ToolCall{Name: "Edit", Input: map[string]any{"file_path": "/home/u/repo/a.go"}}, "Editing files."},
		{"writes aggregate", &transcript.ToolCall{Name: "Write", Input: map[string]any{"file_path": "/home/u/repo/a.go"}}, "Editing files."},
		{"no tool yet", nil, "Working."},
		{"empty command", &transcript.ToolCall{Name: "Bash", Input: map[string]any{}}, "Working."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := describeActivity(tc.call, "/home/u/repo"); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// V1's noise policy rules these out: a progress card must show meaningful
// progress, never "Claude read foo.go" or "Claude searched for X".
func TestDescribeActivityNeverNarratesRoutineWork(t *testing.T) {
	routine := []*transcript.ToolCall{
		{Name: "Read", Input: map[string]any{"file_path": "/home/u/repo/b.go"}},
		{Name: "Grep", Input: map[string]any{"pattern": "RefreshToken"}},
		{Name: "Glob", Input: map[string]any{"pattern": "**/*.go"}},
		{Name: "WebSearch", Input: map[string]any{"query": "feishu"}},
		{Name: "WebFetch", Input: map[string]any{"url": "https://example.com"}},
		{Name: "Agent", Input: map[string]any{"description": "explore"}},
		{Name: "OddTool", Input: map[string]any{}},
	}
	for _, call := range routine {
		if got := describeActivity(call, "/home/u/repo"); got != "Working." {
			t.Errorf("%s narrated as %q, want %q", call.Name, got, "Working.")
		}
	}
}

func TestCapLinesMarksWhatItDropped(t *testing.T) {
	lines := []string{"a", "b", "c", "d", "e"}
	got := capLines(lines, 3)
	if len(got) != 4 || got[3] != "… and 2 more" {
		t.Errorf("got %q", got)
	}
	// The input must not be clobbered by the cap.
	if lines[3] != "d" {
		t.Errorf("capLines mutated its input: %q", lines)
	}
	if got := capLines(lines, 5); len(got) != 5 {
		t.Errorf("nothing to cap, got %q", got)
	}
}

func TestValidationSentence(t *testing.T) {
	cases := []struct {
		name  string
		tests []transcript.TestRun
		want  string
	}{
		{"none", nil, ""},
		{"one passed", []transcript.TestRun{{Command: "go test ./...", Passed: true}}, "go test ./... passed."},
		{"one failed", []transcript.TestRun{{Command: "go test ./...", Passed: false}}, "go test ./... failed."},
		{"all passed", []transcript.TestRun{
			{Command: "go test ./...", Passed: true},
			{Command: "pytest -q", Passed: true},
		}, "2 validation commands passed."},
		{"some failed", []transcript.TestRun{
			{Command: "go test ./...", Passed: false},
			{Command: "pytest -q", Passed: true},
		}, "1 of 2 validation commands failed."},
		{"variants collapse", []transcript.TestRun{
			{Command: "go test ./... 2>&1 | head", Passed: false},
			{Command: "go test ./...", Passed: true},
		}, "go test ./... passed."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := validationSentence(tc.tests); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		d    string
		want string
	}{
		{"42s", "42s"},
		{"60s", "1m"},
		{"102s", "1m 42s"},
		{"258s", "4m 18s"},
		{"7200s", "2h 0m"},
		{"7260s", "2h 1m"},
	}
	for _, tc := range cases {
		d, err := time.ParseDuration(tc.d)
		if err != nil {
			t.Fatal(err)
		}
		if got := formatDuration(d); got != tc.want {
			t.Errorf("formatDuration(%s) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

func TestCleanCommand(t *testing.T) {
	cases := []struct{ in, want string }{
		{"go test ./...", "go test ./..."},
		{"go test ./... 2>&1 | tail -5", "go test ./..."},
		{"go test ./... > out.txt", "go test ./..."},
		{"go test ./... 2>/dev/null", "go test ./..."},
		{"npm test && echo done", "npm test && echo done"},
	}
	for _, tc := range cases {
		if got := cleanCommand(tc.in); got != tc.want {
			t.Errorf("cleanCommand(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestValidationLinesDedupesFilteredVariants(t *testing.T) {
	tests := []transcript.TestRun{
		{Command: "go test ./... 2>&1 | head -40", Passed: false},
		{Command: "go test ./...", Passed: true},
		{Command: "pytest -q tests/", Passed: true},
	}
	lines := validationLines(tests)
	if len(lines) != 2 {
		t.Fatalf("lines = %q", lines)
	}
	if lines[0] != "✓ go test ./... passed" {
		t.Errorf("line 0 = %q", lines[0])
	}
	if lines[1] != "✓ pytest -q tests/ passed" {
		t.Errorf("line 1 = %q", lines[1])
	}
}

func TestValidationLinesCapsWithACount(t *testing.T) {
	var tests []transcript.TestRun
	for _, c := range []string{"go test ./a", "go test ./b", "go test ./c", "go test ./d"} {
		tests = append(tests, transcript.TestRun{Command: c, Passed: true})
	}
	lines := validationLines(tests)
	if len(lines) != 4 || lines[3] != "… and 1 more" {
		t.Errorf("lines = %q", lines)
	}
}
