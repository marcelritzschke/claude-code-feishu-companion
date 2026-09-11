package daemon

import (
	"regexp"
	"strings"
)

// What can be typed, and why these and not others.
//
// Card callbacks are a separate subscription in the Feishu console from
// card delivery: an app can send perfectly good cards while every button
// and every reply box on them is inert, and it fails by doing nothing at
// all. That used to mean every card action needed a typed twin, including
// a numbered way to pick a session out of a list.
//
// It cost more than it bought. The numbers needed a list to resolve
// against, the list needed a remembered selection, and the selection was
// state the user could not see - so a message went wherever the last
// numbered reply had pointed, which was nowhere on their screen. The way
// to know where a message is going is to have typed it into the card of
// the session it names.
//
// What is left typed is what needs no list to make sense of: a verdict,
// which carries the request it answers inside itself, and a command that
// is about the machine rather than about one session. Setup now proves the
// card path instead of routing around it - see checkCardCallback - so an
// app whose callbacks are missing is found while the user is at the
// keyboard rather than weeks later.

// verdictReply matches an answer to a permission request: "y abcde",
// "yes abcde", "n abcde", "no abcde".
//
// The id is required. A bare "yes" would be one autocorrect away from
// approving a command the user never read, and it is also a perfectly
// ordinary thing to say to Claude. The alphabet is the one Claude Code
// draws request ids from - lowercase, never the letter l, so it cannot be
// misread as a 1 - and the match is case-insensitive because phones
// capitalize the first word of a message.
var verdictReply = regexp.MustCompile(`(?i)^\s*(y|yes|n|no)\s+([a-km-z]{5})\s*$`)

// parseVerdict reads a permission answer, reporting the request it answers
// and whether it allows.
func parseVerdict(text string) (requestID string, allow, ok bool) {
	m := verdictReply.FindStringSubmatch(text)
	if m == nil {
		return "", false, false
	}
	return strings.ToLower(m[2]), strings.HasPrefix(strings.ToLower(m[1]), "y"), true
}

// interruptReply matches a request to stop the running turn. As with every
// command, the whole message has to be it: "interrupt the build if it
// hangs" is an instruction for Claude, not a Claude Companion command.
var interruptReply = regexp.MustCompile(`(?i)^\s*/?interrupt\s*$`)

// parseInterrupt reads a request to stop the running turn.
func parseInterrupt(text string) bool { return interruptReply.MatchString(text) }
