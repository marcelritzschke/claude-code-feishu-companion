# Wirelark

**Wirelark tells you when your coding agent needs you, and lets you continue
the session it is telling you about.**

Wirelark connects the Claude Code sessions running on your computer to
Feishu, so you can give Claude a task, put the laptop aside, and know from
your phone whether it finished, whether it needs you, and what happened -
and then pick a session and send it your next instruction, without the
conversation ever leaving your machine.

The rule behind every message it sends:

> **Notify on attention. Summarize on completion. Continue the existing
> session. Avoid recreating the terminal.**

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

## Continuing a session from Feishu

Message the Wirelark bot and it shows what is running on your computer:

```text
Wirelark
Your local Claude sessions

⚠️ 1. frontend
Upgrade React
Waiting for you · Remote ready

🟢 2. payments-api
Fix token refresh
Working · Remote ready

⚪ wirelark
Idle · Notifications only

[ 1. Upgrade React · frontend ]  [ 2. Fix token refresh · payments-api ]
```

Tap a session, or reply with its number, and Wirelark says which session you
are now talking to. Everything
you send after that goes to that session and no other - plain language, no
command syntax. If the session is mid-turn, Wirelark says the message is
queued rather than pretending it landed; if the session ends, the next
message goes nowhere and you are asked to pick again. Wirelark never
redirects a message to a session you did not choose.

Your terminal stays usable throughout. There is no second copy of the work:
when you get back to the keyboard, the same session is there with your
message in it.

Completion, failure, and progress cards for a reachable session carry a
`[ Continue this session ]` button, so reading the outcome and giving the
next instruction is one gesture.

Say `sessions` at any time to see the overview again.

**Buttons are a convenience, not the contract.** Card callbacks are a
separate Feishu subscription from card delivery, and an app can send
perfectly good cards whose every button is inert. So everything a button
does can be typed: a bare number picks that session from the last overview,
and `y <id>` / `n <id>` answers a permission request using the id printed on
its card. A bare `yes` is deliberately not accepted - it is one autocorrect
away from approving a command you never read, and it is an ordinary thing to
say to Claude.

### Permissions

When a session is reachable, a permission prompt reaches Feishu with the
two answers Claude Code accepts:

```text
⚠️ Permission requested
Fix token refresh · payments-api

Claude wants to run:
Bash
{"command":"npm install"}

In
~/work/payments-api

[ Allow once ]  [ Deny ]

Or reply  y abcde  to allow,  n abcde  to deny.
```

The local dialog stays open the whole time and either answer ends it: the
decision is still on your computer, Wirelark only adds a second place to
make it from. Answer in the terminal instead and the Feishu card settles
itself to "already answered" rather than standing there asking.

An action that cannot easily be undone - `rm -rf`, `sudo`, a force push, a
dropped table, `curl … | sh` - gets a red card, the full command rather than
an excerpt, and no emphasis on Allow.

Remote approval is a real grant of authority: anyone who can message your
Wirelark bot can approve a command in your session. It is a separate setting
from continuation for that reason, and `init` asks about it separately.

### What stays in Claude Code

Multiple-choice questions (`AskUserQuestion`) are a terminal dialog, not a
permission prompt: no channel can answer one, and the session is blocked
until someone answers it where it was asked. Wirelark says so on the card
rather than offering a button that would not work.

Wirelark is not a remote IDE. No terminal emulation, no file browsing, no
diffs, no logs, no tool-by-tool transcript. When you need to inspect
something, the right place is still Claude Code on your computer.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/marcelritzschke/wirelark/main/install.sh | sh
```

This detects your OS and architecture (macOS and Linux, amd64 and arm64),
downloads the matching binary from the
[latest release](https://github.com/marcelritzschke/wirelark/releases),
verifies it against the release's `checksums.txt`, and installs it to
`~/.local/bin`. Set `INSTALL_DIR` to install elsewhere, or `VERSION` to
install a specific tag instead of the latest:

```sh
curl -fsSL https://raw.githubusercontent.com/marcelritzschke/wirelark/main/install.sh | INSTALL_DIR=/usr/local/bin sh
```

Then:

```sh
wirelark init
```

Read the script before piping it to `sh` if you'd rather not take that on
faith - `install.sh` at the repo root is exactly what the command above
runs.

On Windows, or to install without running a script, download the archive
for your platform from the
[releases page](https://github.com/marcelritzschke/wirelark/releases),
verify it against that release's `checksums.txt`, and extract the
`wirelark` (or `wirelark.exe`) binary onto your `PATH`. `go install
github.com/marcelritzschke/wirelark@latest` also works if you have a Go
toolchain and prefer that over a release binary; see
[Setup](#setup) below to build from a local checkout instead.

## Single binary, zero dependencies

Wirelark ships as one static binary. No runtime to install, no
dependency tree, no service manager:

- No runtime, no service manager, no container. A handful of Go deps,
  fetched by `go build`.
- Nothing setup needs is paid for by anything else. `wirelark send` runs
  on every hook event and links no terminal-UI machinery it would have to
  initialise: it costs about two milliseconds and writes nothing.
- Same binary on macOS, Linux, Windows. Cross-compile is one `GOOS=`
  away.
- `wirelark send` starts in tens of milliseconds per hook event. Fast
  enough that the runtime is never the slow part.
- The daemon auto-starts on first use. No systemd unit, no Docker, no
  supervisor.

```sh
go build -o wirelark .
./wirelark init
```

That is the whole install. The same binary delivers notifications, runs
the MCP channel per session, owns the Feishu connection, and answers card
callbacks. One process tree, no hidden companions.

## Setup

Requirements: [mise](https://mise.jdx.dev/) (or any Go >= 1.27).

```sh
mise install                # installs the pinned Go toolchain
mise exec -- go build -o wirelark .
./wirelark init   # interactive: scan a QR code, behavior settings, test card, hooks, channel
```

`init` opens straight into a QR code - there is no question to answer
first, because almost nobody arrives holding a Feishu app:

```text
  Wirelark  Claude Code, on your phone

  Connect Wirelark to Feishu

  ████████████████████████████████████████ …
  ████████████████████████████████████████ …
  ████ ▄▄▄▄▄ ██▀▀▀▄▄▄▄█▀█▄ █▀▄ ▄▀▄ ▄▄█ ▄ █ …
  ████ █   █ █▄█▄█▀█▀▄  ▄▀█▄▄█▀▄██▀██▀██▄█ …
  ████ █▄▄▄█ ██▄▄▄▄▀▄▄▄  ▄ ▄▄█ ███ ▀ ▄▄▄   …
  ████▄▄▄▄▄▄▄█ ▀▄█▄▀▄▀ █ ▀▄▀ ▀ ▀ █▄█ █▄█ █ …
                  …

  Scan with Feishu, then approve what Wirelark asks for.
  The account you scan with becomes this computer's Wirelark owner.

  Can't scan? https://open.feishu.cn/page/launcher?…

  ⠹ Waiting for the scan · 9m41s left
  e use an existing Feishu app     ctrl+c cancel
```

Scanning opens Feishu's own app-registration page, pre-filled with the
permissions and subscriptions below. Approve it and Feishu creates the app,
hands Wirelark the credentials, and says who scanned - so there is no
developer console to open, no App Secret to copy, and no email to type. The
account that scanned becomes this computer's Wirelark owner: the only one
the bot messages, and the only one it accepts messages from.

This is Feishu's device-authorization flow (RFC 8628) through the official
Go SDK. Wirelark never sees the approval, only its result.

After that `init` asks four behavior questions, delivers a real test card
before saving anything, then registers the hooks and the channel, starts
the daemon, and checks that Feishu can reach back to your computer while
you are still there to fix it if it cannot.

### Using an app someone else created

Press `e` while the code is on screen - or take the offer `init` makes if
the scan does not work out - and setup asks for an App ID, an App Secret,
which Feishu the app lives on, and who you are. This is the path for a
managed company environment where an administrator pre-creates the app.

That app needs the bot capability, `im:message:send_as_bot` (plus
`contact:user.id:readonly` if you resolve your open_id by email), and - for
continuation - `im:message` and event subscription in **long connection**
mode with `im.message.receive_v1` subscribed.

Card buttons need two more things, and are easy to miss because cards send
fine without them: **Interactive Card** enabled under App Features → Bot,
and the **`card.action.trigger`** event subscribed alongside
`im.message.receive_v1`. Re-publish the app version afterwards, or the
subscription does not take effect. Without these, buttons report that the
callback is not configured and you use the typed replies instead.

The QR flow asks Feishu for all of this during registration, which is the
point of it. If your administrator has to approve the permissions first,
the app is created but stays quiet until they do - `init` says so when the
test card will not send.

Config lives at `~/.config/wirelark/config.toml` (0600):

```toml
app_id = "cli_..."
app_secret = "..."
open_id = "ou_..."           # the account that scanned; Wirelark's owner
brand = "feishu"             # open.feishu.cn; "lark" for open.larksuite.com

# what to notify about
notify = "important"            # attention, failures, completion
# notify = "important+progress" # ... plus long-running progress updates

# how much a completion says
detail = "normal"               # summary, validation, and Claude's answer
# detail = "compact"            # one-glance summary

# continuing sessions from Feishu
remote = "on"                   # off makes Wirelark a one-way notifier again
remote_permissions = "on"       # off keeps permission cards informational
```

Everything else lives in one private directory, `~/.cache/wirelark` (0700):
the cached tenant token, the progress-card bookkeeping, the session
snapshot, the daemon's socket, and the debug log. `WIRELARK_STATE_DIR`
points a whole install somewhere else.

### Starting sessions with the channel enabled

Claude Code channels are a research preview, and a channel that is not on
Anthropic's allowlist has to be opted into per session. Until Wirelark is on
that list, start sessions you want to continue from Feishu with:

```sh
claude --dangerously-load-development-channels server:wirelark
```

Sessions started with a plain `claude` still send you notifications; Feishu
shows them as **Notifications only** rather than as broken. This is a
limitation of the preview, not how Wirelark expects to work, which is why
there is no `wirelark claude` wrapper to hide it.

## How it fits together

```text
                    Feishu
                      │
              wirelark daemon        one persistent process,
                      │              the only one that talks to Feishu
      ┌───────────────┼───────────────┐
      │               │               │
 wirelark channel  channel        wirelark send
 (MCP, per session)                (per hook event)
      │
   Claude A        Claude B        Claude C
```

- **`wirelark send`** is the hook entrypoint, run per event by Claude Code.
  It hands the event to the daemon and exits. If no daemon answers it
  delivers the notification itself, so a stopped daemon costs remote
  continuation and never a message.
- **`wirelark channel`** is the MCP channel server Claude Code spawns with a
  session. It holds no credentials and opens no network connection: it talks
  only to the local daemon, over a socket only you can open.
- **`wirelark daemon`** owns the Feishu connection, the session registry, and
  every card. It starts itself when a hook or a channel needs it;
  `wirelark daemon --status` and `--stop` are there for when you want to
  look or intervene.

Wirelark connects to sessions. It does not own them: it never spawns,
resumes, or stops a Claude Code session, and the conversation does not
change hands when you change device.

## Hook events

Wirelark registers async hooks for `PermissionRequest`, `PreToolUse`
(matcher `AskUserQuestion`), `Stop`, and `StopFailure`. `SessionStart`,
`SessionEnd`, and `UserPromptSubmit` are added when remote continuation is
on - they carry no notification, and exist so the session overview knows
what is running and what each session is doing. `PostToolUse` is added only
at `notify = "important+progress"`, since it fires on every tool call and
has nothing to say otherwise. Re-running `init` after changing a setting
removes the hooks it no longer needs. Subagent events are skipped -
subagent activity is routine work.

Registration is idempotent: existing hooks are preserved, every other
Wirelark entry is removed - including one left by an install at a different
path - and a backup is written.

## Debugging

```sh
echo '<hook payload json>' | ./wirelark send --dry-run   # prints the card JSON
<payload> | WIRELARK_DEBUG=1 ./wirelark send              # traces to the debug log
./wirelark daemon --status
cat ~/.cache/wirelark/debug.log
```

`send --dry-run` works without a config file and never reaches the daemon.
`WIRELARK_CONFIG` points the binary at a different config file, and
`WIRELARK_STATE_DIR` at a different private directory - together they run a
whole second install without touching yours.

## Notes

- WSL and native Windows are both supported runtimes (each environment needs
  its own `init` and binary; cross-compile with `GOOS=windows`). On Windows
  the daemon's socket is a loopback port guarded by a secret in a 0600 file,
  since Windows has no unix sockets; on Windows Wirelark also cannot read a
  session's command line, so sessions show as **Remote untested** until the
  first message proves the link one way or the other.
- Channels require Anthropic authentication (claude.ai or a Console API key)
  and are unavailable on Bedrock, Vertex, and Foundry. Team and Enterprise
  organizations must enable them centrally. Without them, everything in
  "Notifications" still works.
