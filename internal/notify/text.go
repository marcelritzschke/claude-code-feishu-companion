package notify

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/marcelritzschke/claude-code-feishu-companion/internal/pathdisp"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/transcript"
)

// Excerpt and truncation caps, in runes. Everything the phone user sees is
// bounded by these so a huge response never becomes a huge card.
const (
	summaryCap  = 160 // completion summary: roughly 2-4 phone lines
	quoteCap    = 400 // excerpt of Claude's final answer
	actionCap   = 120 // permission request action
	questionCap = 300 // question text
	optionCap   = 100 // one answer option
	errorCap    = 200 // failure details
	activityCap = 80  // progress "current activity" line
	commandCap  = 60  // test command shown in validation lines
)

// truncateRunes caps s at max runes total, cutting at a word boundary when
// possible and marking the cut with "... (truncated)".
func truncateRunes(s string, max int) string {
	const marker = " ... (truncated)"
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max <= len(marker) {
		return string(r[:max]) + "…"
	}
	cut := r[:max-len(marker)]
	if i := strings.LastIndex(string(cut), " "); i > len(cut)/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(string(cut), " -\n\t") + marker
}

// capLines caps a list at max entries and states how many were dropped, so
// a shortened list never reads as a complete one.
func capLines(lines []string, max int) []string {
	if len(lines) <= max {
		return lines
	}
	return append(lines[:max:max], fmt.Sprintf("… and %d more", len(lines)-max))
}

// flatten collapses line continuations, newlines, and runs of whitespace
// into single spaces so multi-line commands stay readable on a phone.
func flatten(s string) string {
	s = strings.ReplaceAll(s, "\\\n", " ")
	return strings.Join(strings.Fields(s), " ")
}

// splitFinal splits Claude's final answer into a one-sentence summary and
// the remainder worth quoting. Markdown heading lines are skipped so the
// summary is the first real sentence, not a "## Summary" label.
func splitFinal(msg string) (summary, rest string) {
	s := msg
	for {
		line, remainder, more := strings.Cut(strings.TrimLeft(s, "\n"), "\n")
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			break
		}
		if !more {
			return "", ""
		}
		s = remainder
	}
	s = strings.TrimSpace(s)

	rs := []rune(s)
	end := sentenceEnd(rs)
	if end < 0 {
		return stripBullet(truncateRunes(s, summaryCap)), ""
	}
	summary = stripBullet(string(rs[:end]))
	rest = strings.TrimSpace(string(rs[end:]))
	if summary == "" {
		return truncateRunes(rest, summaryCap), ""
	}
	if utf8.RuneCountInString(summary) > summaryCap {
		return truncateRunes(summary, summaryCap), rest
	}
	return summary, rest
}

// sentenceEnd returns the rune index just past the first sentence
// terminator (., !, ?, or CJK full stop) that ends a sentence, or -1. A
// terminator only counts when followed by whitespace or the end of the
// string, so "v1.2" and "no. 3" do not split.
func sentenceEnd(rs []rune) int {
	for i, r := range rs {
		// CJK terminators end a sentence without a following space.
		if r == '。' || r == '！' || r == '？' {
			return i + 1
		}
		if r != '.' && r != '!' && r != '?' {
			continue
		}
		if i == len(rs)-1 {
			return i + 1
		}
		if next := rs[i+1]; next == ' ' || next == '\n' || next == '\t' {
			return i + 1
		}
	}
	return -1
}

// stripBullet removes leading markdown list/quote markers from a line.
func stripBullet(s string) string {
	s = strings.TrimSpace(s)
	for {
		switch {
		case strings.HasPrefix(s, "- "), strings.HasPrefix(s, "* "), strings.HasPrefix(s, "> "):
			s = strings.TrimSpace(s[2:])
		case strings.HasPrefix(s, "1. "), strings.HasPrefix(s, "1) "):
			s = strings.TrimSpace(s[3:])
		default:
			return s
		}
	}
}

// action describes how one tool's input reads in a permission request: the
// verb the user is approving, and which input field carries its subject.
type action struct {
	verb  string
	field string
	// path shortens the field relative to the project.
	path bool
	// flat collapses a multi-line field onto one line. Commands are never
	// otherwise rewritten here: the user is approving this exact call, so
	// what the card shows must be what Claude asked to run.
	flat bool
}

var actions = map[string]action{
	"Bash":         {verb: "Run", field: "command", flat: true},
	"Edit":         {verb: "Edit file", field: "file_path", path: true},
	"Write":        {verb: "Write file", field: "file_path", path: true},
	"NotebookEdit": {verb: "Edit notebook", field: "notebook_path", path: true},
	"Read":         {verb: "Read file", field: "file_path", path: true},
	"WebFetch":     {verb: "Fetch URL", field: "url"},
	"WebSearch":    {verb: "Search the web", field: "query"},
	"Grep":         {verb: "Search code", field: "pattern"},
	"Glob":         {verb: "Find files", field: "pattern"},
	"Agent":        {verb: "Start subagent", field: "description"},
	"ExitPlanMode": {verb: "Approve plan", field: "plan", flat: true},
}

// describeAction renders a permission request as a human-readable action,
// e.g. "Run:\ngo test ./...". A tool Claude Companion does not know is named, not
// dumped: the raw event payload is never the message.
func describeAction(tool string, input map[string]any, cwd string) string {
	a, known := actions[tool]
	if !known {
		return "Use " + readableTool(tool) + "."
	}
	subject, _ := input[a.field].(string)
	switch {
	case a.path:
		subject = pathdisp.Short(subject, cwd)
	case a.flat:
		subject = flatten(subject)
	}
	if subject == "" {
		return a.verb + "."
	}
	return a.verb + ":\n" + truncateRunes(subject, actionCap)
}

// readableTool turns a tool identifier into something a phone user can
// read. MCP tools arrive as "mcp__<server>__<tool>", which is Claude Companion's
// internal event representation leaking, not a name.
func readableTool(tool string) string {
	rest, ok := strings.CutPrefix(tool, "mcp__")
	if !ok {
		return "the " + tool + " tool"
	}
	server, name, found := strings.Cut(rest, "__")
	if !found {
		return "the " + rest + " tool"
	}
	return strings.ReplaceAll(name, "_", " ") + " (" + server + ")"
}

// describeActivity says what the turn is doing now, for the progress card.
//
// It deliberately narrates almost nothing. The attention-mode noise policy rules out
// reads, searches, and subagent runs, and a progress card must show
// "meaningful progress, not individual internal actions" - so a shell
// command (the spec's own example of current activity) is named, edits are
// reported in aggregate, and everything else is simply work in progress.
func describeActivity(tool *transcript.ToolCall, cwd string) string {
	if tool == nil {
		return "Working."
	}
	switch tool.Name {
	case "Bash":
		cmd, _ := tool.Input["command"].(string)
		if cmd = cleanCommand(flatten(cmd)); cmd != "" {
			return "Running " + truncateRunes(cmd, activityCap)
		}
	case "Edit", "Write", "NotebookEdit":
		return "Editing files."
	}
	return "Working."
}

// latestRuns collapses the turn's test commands to one entry each, with
// the display form of the command and the outcome of its latest run.
// Variants of one command (same command behind different pipes or filters)
// collapse together.
func latestRuns(tests []transcript.TestRun) []transcript.TestRun {
	var out []transcript.TestRun
	indexOf := map[string]int{}
	for _, t := range tests {
		cmd := truncateRunes(cleanCommand(flatten(t.Command)), commandCap)
		run := transcript.TestRun{Command: cmd, Passed: t.Passed}
		if i, ok := indexOf[cmd]; ok {
			out[i] = run // rerun of the same command: latest outcome wins
			continue
		}
		indexOf[cmd] = len(out)
		out = append(out, run)
	}
	return out
}

// validationLines renders test outcomes as check/cross lines, capped so a
// test-heavy turn does not flood the card.
func validationLines(tests []transcript.TestRun) []string {
	var lines []string
	for _, t := range latestRuns(tests) {
		mark, outcome := "✓", "passed"
		if !t.Passed {
			mark, outcome = "✗", "failed"
		}
		lines = append(lines, fmt.Sprintf("%s %s %s", mark, t.Command, outcome))
	}
	return capLines(lines, 3)
}

// cleanCommand drops output plumbing (pipes, redirects like 2>&1) so a
// validation line reads as the command itself.
func cleanCommand(cmd string) string {
	if i := strings.IndexByte(cmd, '|'); i > 0 {
		cmd = cmd[:i]
	}
	if i := strings.IndexByte(cmd, '>'); i > 0 {
		if cmd[i-1] >= '0' && cmd[i-1] <= '9' {
			i-- // the fd prefix of a redirect, e.g. the 2 in 2>&1
		}
		cmd = cmd[:i]
	}
	return strings.TrimSpace(cmd)
}
