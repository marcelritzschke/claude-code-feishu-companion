// Package transcript reads Claude Code session transcripts (JSONL) and
// distills the current turn into the facts notifications are built from:
// when it started, what session it belongs to, which files changed, which
// test commands ran and how they ended, and what Claude is doing right now.
package transcript

import (
	"bufio"
	"encoding/json"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/marcelritzschke/wirelark/internal/pathdisp"
)

// Turn is the distilled view of one Claude Code turn: one user prompt and
// everything Claude did in response.
type Turn struct {
	// Start is when the turn's user prompt was recorded. Zero when it
	// could not be determined.
	Start time.Time
	// Title is the session's AI-generated title, e.g. "Fix token refresh".
	Title string
	// Files lists basenames of files edited or written this turn, in
	// order of first change, deduplicated.
	Files []string
	// Tests lists test commands run this turn with the outcome of their
	// latest completed run, in order of most recent run.
	Tests []TestRun
	// LatestTool is the last tool call of the turn, nil when Claude has
	// only talked so far.
	LatestTool *ToolCall
	// Failed reports whether the turn ended in a state that needs the
	// user: a validation command it ran was still failing at the end.
	// A command that failed and later passed is not a failure - agents
	// routinely hit failing commands on the way to a working result.
	Failed bool
	// LastError is the error text of the turn's last errored tool result,
	// the "last relevant error" a failure notification quotes. Empty when
	// nothing errored.
	LastError string
}

// TestRun is one test command and how its latest completed run ended.
type TestRun struct {
	Command string
	Passed  bool
}

// ToolCall is a tool invocation, kept as presentation-neutral facts.
type ToolCall struct {
	Name  string
	Input map[string]any
}

// fileTools maps tool names that modify files to their path input field.
var fileTools = map[string]string{
	"Edit":         "file_path",
	"Write":        "file_path",
	"NotebookEdit": "notebook_path",
}

// testCommandRe matches shell commands that run a test suite.
var testCommandRe = regexp.MustCompile(`(?:^|&&|\|\||;|\s)(?:go test|pytest|python -m pytest|python -m unittest|npm test|npm run test|yarn test|pnpm test|pnpm run test|npx jest|npx vitest|cargo test|mvn test|gradle test|make test|make check|jest|vitest|rspec|dotnet test|ctest|bats)\b`)

// record is one transcript line; unknown record types carry only Type.
type record struct {
	Type        string   `json:"type"`
	Timestamp   string   `json:"timestamp"`
	PromptID    string   `json:"promptId"`
	IsSidechain bool     `json:"isSidechain"`
	AITitle     string   `json:"aiTitle"`
	Message     *message `json:"message"`
}

type message struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type contentItem struct {
	Type string `json:"type"`
	// tool_use
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Input map[string]any `json:"input"`
	// tool_result
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
	Content   json.RawMessage `json:"content"`
}

// toolResult is how one tool call ended.
type toolResult struct {
	errored bool
	text    string
}

type toolUse struct {
	index int // record index, orders calls within the session
	item  contentItem
}

// Load reads the transcript at path and returns the turn started by the
// prompt with the given promptID (falling back to the last prompt in the
// file). A missing or unreadable transcript yields an empty Turn: the
// notification degrades to project-only context instead of failing.
func Load(path, promptID string) *Turn {
	t := &Turn{}
	f, err := os.Open(path)
	if err != nil {
		return t
	}
	defer f.Close()

	var (
		sc       = bufio.NewScanner(f)
		recs     []record
		prompts  []int // record indices of real user prompts
		toolUses []toolUse
		results  = map[string]toolResult{} // tool_use id -> outcome
	)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		var r record
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			continue // tolerate malformed lines
		}
		if r.IsSidechain {
			continue // subagent activity is not this turn's work
		}
		idx := len(recs)
		recs = append(recs, r)

		switch {
		case r.Type == "ai-title" && r.AITitle != "":
			t.Title = r.AITitle
		case r.Type == "user" && r.Message != nil:
			if userIsPrompt(r.Message) {
				prompts = append(prompts, idx)
			}
			for _, item := range contentItems(r.Message) {
				if item.Type == "tool_result" {
					results[item.ToolUseID] = toolResult{errored: item.IsError, text: resultText(item.Content)}
				}
			}
		case r.Type == "assistant" && r.Message != nil:
			for _, item := range contentItems(r.Message) {
				if item.Type == "tool_use" {
					toolUses = append(toolUses, toolUse{index: idx, item: item})
				}
			}
		}
	}

	// The turn is everything after its prompt record; without a prompt
	// record the transcript says nothing about this turn.
	from := -1
	for i := len(prompts) - 1; i >= 0; i-- {
		if promptID == "" || recs[prompts[i]].PromptID == promptID {
			from = prompts[i]
			break
		}
	}
	if from < 0 && len(prompts) > 0 {
		from = prompts[len(prompts)-1]
	}
	if from < 0 {
		return t
	}

	t.Start = parseTime(recs[from].Timestamp)
	t.collect(toolUsesAfter(toolUses, from), results)
	return t
}

// collect fills the turn's activity facts from the turn's tool calls,
// resolving outcomes from results (tool_use id -> outcome).
func (t *Turn) collect(uses []toolUse, results map[string]toolResult) {
	files := map[string]bool{}
	tests := map[string]TestRun{}
	lastRun := map[string]int{}
	for _, u := range uses {
		switch {
		case fileTools[u.item.Name] != "":
			if p, ok := u.item.Input[fileTools[u.item.Name]].(string); ok && p != "" && !files[p] {
				files[p] = true
				t.Files = append(t.Files, pathdisp.Base(p))
			}
		case u.item.Name == "Bash":
			cmd, _ := u.item.Input["command"].(string)
			if !testCommandRe.MatchString(cmd) {
				break
			}
			res, done := results[u.item.ID]
			if !done {
				break // still running; its outcome is unknown
			}
			tests[cmd] = TestRun{Command: cmd, Passed: !res.errored}
			lastRun[cmd] = u.index
		}
	}
	for _, u := range uses {
		if res, ok := results[u.item.ID]; ok && res.errored {
			t.LastError = res.text // later errors overwrite earlier ones
		}
	}
	if len(uses) > 0 {
		t.LatestTool = &ToolCall{Name: uses[len(uses)-1].item.Name, Input: uses[len(uses)-1].item.Input}
	}

	cmds := make([]string, 0, len(tests))
	for cmd := range tests {
		cmds = append(cmds, cmd)
	}
	sort.Slice(cmds, func(i, j int) bool { return lastRun[cmds[i]] < lastRun[cmds[j]] })
	for _, cmd := range cmds {
		t.Tests = append(t.Tests, tests[cmd])
		if !tests[cmd].Passed {
			t.Failed = true
		}
	}
}

// resultText extracts readable text from a tool_result content field,
// which Claude Code writes either as a bare string or as an array of
// content blocks.
func resultText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// toolUsesAfter returns the tool calls recorded after record index from.
func toolUsesAfter(uses []toolUse, from int) []toolUse {
	var out []toolUse
	for _, u := range uses {
		if u.index > from {
			out = append(out, u)
		}
	}
	return out
}

// userIsPrompt reports whether a user message is a real prompt (plain text)
// rather than tool results.
func userIsPrompt(m *message) bool {
	var s string
	return json.Unmarshal(m.Content, &s) == nil && s != ""
}

// contentItems decodes a message content array; non-array content yields nil.
func contentItems(m *message) []contentItem {
	var items []contentItem
	if err := json.Unmarshal(m.Content, &items); err != nil {
		return nil
	}
	return items
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	if ts, err := time.Parse(time.RFC3339, s); err == nil {
		return ts
	}
	return time.Time{}
}
