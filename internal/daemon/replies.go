package daemon

import (
	"regexp"
	"strconv"
	"strings"
)

// Everything a card button can do, a typed reply can do too.
//
// Card callbacks are a separate subscription in the Feishu console from
// card delivery, and an app can send perfectly good cards while every
// button on them is inert. Claude Companion cannot detect that, and a product whose
// only way to pick a session is a button that silently does nothing is a
// product that does nothing. So the buttons are a convenience and the
// typed forms are the contract.

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

// watchReply matches a request to look inside a session: "watch" on its
// own for the selected one, or "watch 2" for the second session of the
// last overview. The whole message has to be the command, exactly as with
// the overview words: "watch the test output and tell me" is an
// instruction for Claude, not a Claude Companion command.
var watchReply = regexp.MustCompile(`(?i)^\s*/?watch\s*(\d*)\s*$`)

// stopWatchReply matches a request to stop looking.
var stopWatchReply = regexp.MustCompile(`(?i)^\s*(?:/?unwatch|stop\s+watching)\s*$`)

// parseWatch reads a watch request, reporting the session number it names
// (0 when it named none, meaning the selected session).
func parseWatch(text string) (number int, ok bool) {
	m := watchReply.FindStringSubmatch(text)
	if m == nil {
		return 0, false
	}
	if m[1] == "" {
		return 0, true
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}
	return n, true
}

// parseStopWatch reads a request to stop watching.
func parseStopWatch(text string) bool { return stopWatchReply.MatchString(text) }

// parsePick reads a reply that picks the nth session from the last
// overview. A bare number is never an instruction to Claude, which is what
// makes it safe to read as a choice whatever else is going on.
func parsePick(text string, listed int) (index int, ok bool) {
	n, err := strconv.Atoi(strings.TrimSpace(strings.Trim(text, ".)")))
	if err != nil || n < 1 || n > listed {
		return 0, false
	}
	return n - 1, true
}
