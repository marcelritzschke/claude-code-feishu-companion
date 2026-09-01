package deliver

import (
	"time"

	"github.com/marcelritzschke/wirelark/internal/config"
	"github.com/marcelritzschke/wirelark/internal/notify"
	"github.com/marcelritzschke/wirelark/internal/transcript"
)

// walkAwayTime is how long an answer has to take before the user might not
// be sitting in front of it. It is only ever a reason to notify, never a
// reason to stay quiet: see WithholdChatter.
const walkAwayTime = 2 * time.Minute

// Whether a final card may be withheld when the turn has no live progress
// card to settle. Named so the call sites read as intent.
const (
	AlwaysNotify = false
	LiveCardOnly = true
)

// WithholdChatter decides whether a finished turn may go unreported.
//
// The spec's test is whether Claude "finished meaningful work", not how
// long it took: a task you walk away from has no minimum duration. So the
// only turn withheld is one that did no work at all - no tool ever ran, so
// it was a question answered in conversation, with the user typing and
// reading. Everything else is reported however briefly it ran.
//
// Both escape hatches point the same way, because silence is the one
// failure mode this product cannot afford: a turn Wirelark could not read,
// and a wordless answer long enough to have walked away from, are both
// reported anyway.
func WithholdChatter(turn *transcript.Turn) bool {
	switch {
	case turn.Start.IsZero(): // no readable turn; notifying beats guessing
		return AlwaysNotify
	case turn.LatestTool != nil: // the turn actually did something
		return AlwaysNotify
	case time.Since(turn.Start) >= walkAwayTime:
		return AlwaysNotify
	}
	return LiveCardOnly
}

// DetailOf maps the user's configured completion detail to the card layout.
func DetailOf(cfg *config.Config) notify.Detail {
	if cfg.CompactCompletions() {
		return notify.Compact
	}
	return notify.Normal
}
