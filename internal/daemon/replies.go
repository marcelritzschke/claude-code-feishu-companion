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
// button on them is inert. Wirelark cannot detect that, and a product whose
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
