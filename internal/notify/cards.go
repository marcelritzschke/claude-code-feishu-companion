package notify

import (
	"fmt"
	"strings"

	"github.com/marcelritzschke/claude-code-feishu-companion/internal/hook"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/session"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/transcript"
)

// PermissionCard is the highest-priority notification: Claude cannot
// continue without a permission decision.
func PermissionCard(p *hook.Payload, turn *transcript.Turn, opts Options) (string, error) {
	bodies := []string{
		"Claude is waiting for permission to continue.",
		"**Requested action**\n" + describeAction(p.ToolName, p.ToolInput, p.Cwd),
	}
	return card("orange", "⚠️ Permission required", contextLine(p, turn), bodies,
		opts.buttons(), "Open Claude Code to respond.")
}

// QuestionCard fires when Claude asks a multiple-choice question; the
// question itself is the most important content.
func QuestionCard(p *hook.Payload, turn *transcript.Turn, opts Options) (string, error) {
	var bodies []string
	for _, q := range parseQuestions(p.ToolInput) {
		var b strings.Builder
		b.WriteString(truncateRunes(q.Question, questionCap))
		for i, opt := range q.Options {
			line := string(rune('A'+i)) + ". " + opt.Label
			if opt.Description != "" {
				line += " - " + opt.Description
			}
			b.WriteString("\n" + truncateRunes(line, optionCap))
		}
		bodies = append(bodies, b.String())
	}
	if len(bodies) == 0 {
		bodies = append(bodies, "Claude is waiting for an answer.")
	}
	// A question is a terminal dialog, not a permission prompt: Claude Code
	// offers no way to answer one from a channel, and the session stays
	// blocked until someone answers it where it was asked. Saying so is
	// better than a button that would not work.
	return card("blue", "❓ Claude needs your input", contextLine(p, turn), bodies,
		nil, "This must currently be answered in Claude Code.")
}

// QuestionAnsweredCard is what a question card becomes once the session
// moves on: answered, and no longer reading as something to act on.
func QuestionAnsweredCard(s session.Session) (string, error) {
	return card("grey", "✓ Answered", s.Describe(),
		[]string{"This question was answered in Claude Code."}, nil, "")
}

// CompletionCard reports a finished turn: what Claude accomplished, the
// validation results, and (at Normal detail) an excerpt of the final answer.
func CompletionCard(p *hook.Payload, turn *transcript.Turn, opts Options) (string, error) {
	summary, rest := splitFinal(p.LastAssistantMessage)
	if summary == "" {
		summary = accomplishment(turn)
	}

	var bodies []string
	bodies = append(bodies, summary)
	if v := validationLines(turn.Tests); len(v) > 0 {
		bodies = append(bodies, "**Validation**\n"+strings.Join(v, "\n"))
	}
	if rest != "" {
		bodies = append(bodies, "**Claude**\n\""+truncateRunes(rest, quoteCap)+"\"")
	}
	return cardOf("green", "✅ Completed"+elapsedSuffix(turn), contextLine(p, turn),
		withHistory(proseOf(bodies), turn), opts.buttons(), "")
}

// FailureCard reports a turn that needs the user instead of one that
// finished: either the API stopped the turn, or the work itself ended in a
// failing state. A failed turn is different from a temporary tool failure:
// an intermediate command that failed and was recovered from never reaches
// this card.
func FailureCard(p *hook.Payload, turn *transcript.Turn, opts Options) (string, error) {
	bodies := []string{failureText(p, turn)}
	if turn != nil {
		if v := validationLines(turn.Tests); len(v) > 0 {
			bodies = append(bodies, "**Validation**\n"+strings.Join(v, "\n"))
		}
	}
	if detail := lastRelevantError(p, turn); detail != "" {
		bodies = append(bodies, "**Last relevant error**\n"+detail)
	}
	return cardOf("red", "🔴 Failed"+elapsedSuffix(turn), contextLine(p, turn),
		withHistory(proseOf(bodies), turn), opts.buttons(), "Open Claude Code to continue.")
}

// failureText says why the turn needs the user, in one sentence.
func failureText(p *hook.Payload, turn *transcript.Turn) string {
	if p.Error != "" {
		return apiFailureText(p.Error)
	}
	if turn == nil {
		return "The turn ended without finishing its work."
	}
	for _, t := range latestRuns(turn.Tests) {
		if !t.Passed {
			return "The task stopped with " + t.Command + " still failing."
		}
	}
	return "The turn ended without finishing its work."
}

// lastRelevantError is the one error worth quoting: the API's own detail
// when the API stopped the turn, otherwise the last error the work hit.
func lastRelevantError(p *hook.Payload, turn *transcript.Turn) string {
	detail := p.ErrorDetails
	if detail == "" && turn != nil {
		detail = turn.LastError
	}
	if detail == "" {
		return ""
	}
	return truncateRunes(flatten(detail), errorCap)
}

// ProgressCard reassures a user who walked away that long-running work is
// still moving. It updates in place until the turn ends.
func ProgressCard(p *hook.Payload, turn *transcript.Turn, opts Options) (string, error) {
	bodies := []string{"**Current activity**\n" + describeActivity(turn.LatestTool, p.Cwd)}
	if facts := soFar(turn); facts != "" {
		bodies = append(bodies, "**So far**\n"+facts)
	}
	return card("yellow", "🟡 Claude is still working", contextWithDuration(p, turn), bodies, opts.buttons(), "")
}

// apiFailureText turns a StopFailure error type into a sentence a phone
// user can act on.
func apiFailureText(errorType string) string {
	switch errorType {
	case "rate_limit":
		return "Claude hit a rate limit and the turn stopped."
	case "overloaded":
		return "The API was overloaded and the turn stopped."
	case "authentication_failed":
		return "Authentication failed. Check the Claude Code login."
	case "oauth_org_not_allowed":
		return "Your organization is not allowed to use the API."
	case "billing_error":
		return "There is a billing problem with the account."
	case "invalid_request":
		return "The API rejected the request and the turn stopped."
	case "model_not_found":
		return "The requested model is not available."
	case "server_error":
		return "An Anthropic server error stopped the turn."
	case "max_output_tokens":
		return "The response hit the output token limit."
	}
	return "The turn ended with an API error."
}

// accomplishment is the completion summary when Claude's final answer is
// empty: it states what the turn verifiably did.
func accomplishment(turn *transcript.Turn) string {
	if n := len(turn.Files); n > 0 {
		return fmt.Sprintf("Updated %d %s.", n, plural(n, "file", "files"))
	}
	if len(turn.Tests) > 0 {
		return "Ran the test suite."
	}
	return "Turn finished."
}

// soFar lists the turn's meaningful progress for the progress card: files
// changed and how the test runs ended.
func soFar(turn *transcript.Turn) string {
	var facts []string
	if n := len(turn.Files); n > 0 {
		facts = append(facts, fmt.Sprintf("• Updated %d %s", n, plural(n, "file", "files")))
	}
	for _, t := range latestRuns(turn.Tests) {
		outcome := "passed"
		if !t.Passed {
			outcome = "failed"
		}
		facts = append(facts, "• "+t.Command+" "+outcome)
	}
	return strings.Join(capLines(facts, 3), "\n")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// question is one parsed AskUserQuestion entry.
type question struct {
	Question string
	Options  []option
}

type option struct {
	Label       string
	Description string
}

// parseQuestions extracts the questions from an AskUserQuestion tool input.
func parseQuestions(input map[string]any) []question {
	raw, _ := input["questions"].([]any)
	var qs []question
	for _, rq := range raw {
		m, ok := rq.(map[string]any)
		if !ok {
			continue
		}
		q := question{Question: stringField(m, "question")}
		if q.Question == "" {
			continue
		}
		if rawOpts, ok := m["options"].([]any); ok {
			for _, ro := range rawOpts {
				if om, ok := ro.(map[string]any); ok {
					label := stringField(om, "label")
					if label == "" {
						continue
					}
					q.Options = append(q.Options, option{Label: label, Description: stringField(om, "description")})
				}
			}
		}
		qs = append(qs, q)
	}
	return qs
}

func stringField(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}
