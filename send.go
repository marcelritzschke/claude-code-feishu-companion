package main

import (
	"os"
	"time"

	"github.com/marcelritzschke/wirelark/internal/config"
	"github.com/marcelritzschke/wirelark/internal/feishu"
	"github.com/marcelritzschke/wirelark/internal/hook"
	"github.com/marcelritzschke/wirelark/internal/notify"
	"github.com/marcelritzschke/wirelark/internal/transcript"
)

// walkAwayTime is how long an answer has to take before the user might not
// be sitting in front of it. It is only ever a reason to notify, never a
// reason to stay quiet: see withholdChatter.
const walkAwayTime = 2 * time.Minute

// runSend always returns 0: it runs inside Claude Code hooks and must never
// disturb the session, whatever happens.
func runSend(args []string) int {
	dryRun := false
	for _, a := range args {
		if a == "--dry-run" || a == "-dry-run" {
			dryRun = true
		}
	}

	p, err := hook.Decode(os.Stdin)
	if err != nil {
		debugLog("decode payload: %v", err)
		return 0
	}
	if p.Subagent() {
		debugLog("skip subagent event %s", p.HookEventName)
		return 0
	}
	if !p.Handled() {
		debugLog("skip unhandled event %s", p.HookEventName)
		return 0
	}
	debugLog("event %s from %s", p.HookEventName, p.ProjectLabel())

	cfg, err := config.Load()
	if err != nil {
		if !dryRun {
			debugLog("load config: %v", err)
			return 0
		}
		// Dry-run needs no credentials; exercise card building with defaults.
		cfg = &config.Config{Notify: config.NotifyImportant, Detail: config.DetailNormal}
	}

	var client *feishu.Client
	if !dryRun {
		client, err = feishu.New(cfg)
		if err != nil {
			debugLog("build client: %v", err)
			return 0
		}
	}

	// A failure to read the transcript degrades to an empty turn
	// (project-only context) rather than dropping the notification.
	turn := transcript.Load(p.TranscriptPath, p.PromptID)
	d := &deliverer{payload: p, client: client, dryRun: dryRun}

	switch p.HookEventName {
	case hook.EventPermissionRequest:
		card, err := notify.PermissionCard(p, turn)
		d.fresh(card, err)
	case hook.EventPreToolUse:
		card, err := notify.QuestionCard(p, turn)
		d.fresh(card, err)
	case hook.EventStop:
		// A turn can finish without succeeding: if the work it validated
		// was still failing at the end, that needs the user, not a ✅.
		if turn.Failed {
			card, err := notify.FailureCard(p, turn)
			d.settle(card, err, alwaysNotify)
			break
		}
		card, err := notify.CompletionCard(p, turn, detailOf(cfg))
		d.settle(card, err, withholdChatter(turn))
	case hook.EventStopFailure:
		card, err := notify.FailureCard(p, turn)
		d.settle(card, err, alwaysNotify)
	case hook.EventPostToolUse:
		if cfg.ProgressEnabled() {
			d.progress(turn)
		}
	}
	return 0
}

// Whether a final card may be withheld when the turn has no live progress
// card to settle. Named so the call sites read as intent.
const (
	alwaysNotify = false
	liveCardOnly = true
)

// withholdChatter decides whether a finished turn may go unreported.
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
func withholdChatter(turn *transcript.Turn) bool {
	switch {
	case turn.Start.IsZero(): // no readable turn; notifying beats guessing
		return alwaysNotify
	case turn.LatestTool != nil: // the turn actually did something
		return alwaysNotify
	case time.Since(turn.Start) >= walkAwayTime:
		return alwaysNotify
	}
	return liveCardOnly
}

func detailOf(cfg *config.Config) notify.Detail {
	if cfg.CompactCompletions() {
		return notify.Compact
	}
	return notify.Normal
}
