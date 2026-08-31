# Wirelark

**Wirelark tells you when your coding agent needs you.**

Wirelark connects a local Claude Code session to Feishu so you can give
Claude a task, put the laptop aside, and know from your phone whether it
finished, whether it needs you, and what happened.

The rule behind every message it sends:

> **Notify on attention. Summarize on completion. Avoid narrating routine work.**

No message is ever triggered by a file read, a search, a shell command, or
intermediate reasoning. Commands do appear as content where they are the
point - the permission prompt you are approving, a validation result, the
current activity on a long-running card - but never as a notification of
their own. A successful session usually produces exactly one Feishu
message, the completion notification, and a short one produces none.

## Notifications

| Message | Feishu card | When |
|---|---|---|
| ⚠️ Claude needs your attention | orange | a tool call is blocked on a permission decision |
| ❓ Claude has a question | blue | Claude asks a multiple-choice question (AskUserQuestion) |
| ✅ Claude finished | green | the turn completed successfully |
| ❌ Claude couldn't finish | red | the turn ended on an API error (rate limit, billing, outage, ...), or ended with a validation command still failing |
| 🟡 Claude is still working | yellow | a turn has clearly outlasted a normal one (10+ minutes) - optional |

Every card leads with why you are seeing it, then identifies the session
(`Fix token refresh · payments-api · 4m 18s`) using the session's title and
project. Completion cards state what was accomplished, show validation
results extracted from the turn (`✓ go test ./... passed`), and quote an
excerpt of Claude's final answer.

Finishing is not the same as succeeding: a turn whose validation was still
failing at the end gets the red card and the error worth reading, not a
green tick.

Long turns keep one message, not many: the progress card updates in place
and becomes the completion card when the turn ends.

A turn in which no tool ever ran is not reported: that was a question
answered in conversation, with you sitting there reading the reply, and a
card for it would be clutter. Work of any length is reported, however
quickly it finished. A turn that already has a progress card standing is
always settled, so you never see a stale "still working".

## Setup

Requirements: [mise](https://mise.jdx.dev/) (or any Go >= 1.27).

```sh
mise install                # installs the pinned Go toolchain
mise exec -- go build -o wirelark .
./wirelark init   # interactive: credentials, behavior settings, test card, hook registration
```

`init` asks for the Feishu app_id/app_secret of a self-built app with the
bot capability and `im:message:send_as_bot` (plus
`contact:user.id:readonly` if you resolve your open_id by email), asks two
behavior questions, delivers a real test card before saving anything, then
registers the hooks in `~/.claude/settings.json` (idempotent; existing
hooks are preserved, every other Wirelark entry is removed - including one
left by an install at a different path - and a backup is written).

Config lives at `~/.config/wirelark/config.toml` (0600):

```toml
app_id = "cli_..."
app_secret = "..."
open_id = "ou_..."

# what to notify about
notify = "important"            # attention, failures, completion
# notify = "important+progress" # ... plus long-running progress updates

# how much a completion says
detail = "normal"               # summary, validation, and Claude's answer
# detail = "compact"            # one-glance summary
```

The tenant access token is cached at `~/.cache/wirelark/token.json` so each
hook invocation skips the token round trip; the progress-card bookkeeping
lives in `~/.cache/wirelark/state.json`.

## Hook events

Wirelark registers async hooks for `PermissionRequest`, `PreToolUse`
(matcher `AskUserQuestion`), `Stop`, and `StopFailure`. `PostToolUse` is
added only at `notify = "important+progress"`, since it fires on every tool
call and has nothing to say otherwise; re-running `init` after switching
back removes it. Subagent events are skipped - subagent activity is
routine work.

It is a strictly read-only tap on Claude Code: it never spawns, resumes,
stops, or feeds the harness. Each hook invocation reads the payload from
stdin, sends at most one card, and exits within seconds. Every failure is
silent - a broken bridge must be invisible to the session that spawned it.

## Debugging

```sh
echo '<hook payload json>' | ./wirelark send --dry-run   # prints the card JSON
<payload> | WIRELARK_DEBUG=1 ./wirelark send              # traces to the debug log
cat ~/.cache/wirelark/debug.log
```

`send --dry-run` works without a config file. `WIRELARK_CONFIG` points the
binary at a different one.

## Notes

- WSL and native Windows are both supported runtimes (each environment needs
  its own `init` and binary; cross-compile with `GOOS=windows`).
- Remote interaction (answering questions or approving permissions from
  Feishu) is deliberately out of scope for v1 - the notification tells you
  to open Claude Code on your computer.
