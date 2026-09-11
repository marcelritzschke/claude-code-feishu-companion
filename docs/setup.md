# Setup and configuration

This document covers Claude Companion installation, Feishu app setup, and
local configuration. For the product overview, start with the
[README](../README.md).

## Install a release binary

On macOS or Linux:

```sh
curl -fsSL https://raw.githubusercontent.com/marcelritzschke/claude-code-feishu-companion/main/install.sh | sh
```

The installer detects macOS or Linux and amd64 or arm64, downloads the
matching
[GitHub Release](https://github.com/marcelritzschke/claude-code-feishu-companion/releases),
verifies it against the release's `checksums.txt`, and installs it to
`~/.local/bin`. It then hands the terminal to `claude-companion init`, so the
one line both installs and sets up. Piped into a shell with no terminal
attached - a Dockerfile, CI - it installs and prints how to run setup later,
and `SKIP_INIT=1` asks for that on a terminal too.

Set `INSTALL_DIR` to choose another location, `VERSION` to install a specific
tag, or `SKIP_INIT` to install without starting setup:

```sh
curl -fsSL https://raw.githubusercontent.com/marcelritzschke/claude-code-feishu-companion/main/install.sh | INSTALL_DIR=/usr/local/bin SKIP_INIT=1 sh
```

The script above is [`install.sh`](../install.sh) at the repository root, so
it can be inspected before running.

On Windows, in PowerShell:

```powershell
irm https://raw.githubusercontent.com/marcelritzschke/claude-code-feishu-companion/main/install.ps1 | iex
```

[`install.ps1`](../install.ps1) is the same install in PowerShell: it detects
amd64 or arm64, downloads the matching `.zip`, verifies it against the
release's `checksums.txt`, installs `claude-companion.exe` to
`%LOCALAPPDATA%\Programs\claude-companion`, adds that directory to your user
`PATH`, and hands the console to `claude-companion init`. The same
`VERSION`, `INSTALL_DIR`, and `SKIP_INIT` environment variables apply:

```powershell
$env:INSTALL_DIR = "C:\tools\claude-companion"; $env:SKIP_INIT = "1"
irm https://raw.githubusercontent.com/marcelritzschke/claude-code-feishu-companion/main/install.ps1 | iex
```

Two Windows details it takes care of. A running daemon holds the program
open and Windows will not overwrite a running image, so an install over an
existing one stops the daemon and moves the old binary aside rather than
writing through it. And files unpacked from a downloaded archive carry a
mark of the web that Windows warns about on every run, which the installer
clears once the release's own checksum has vouched for the bytes.

Because the binary is not code-signed, SmartScreen may still warn the first
time it is launched from Explorer. Starting it from a terminal, which is how
Claude Companion is used, does not raise that prompt.

If you already have a Go toolchain, this is also supported:

```sh
go install github.com/marcelritzschke/claude-code-feishu-companion@latest
```

## QR onboarding

The installer starts this itself. Run it by hand after a `SKIP_INIT=1`
install, or to redo setup later:

```sh
claude-companion init
```

The default flow is:

```text
claude-companion init
    ↓
scan the Feishu QR code
    ↓
approve the requested app permissions
    ↓
choose notification and remote-control settings
    ↓
receive a test card
    ↓
message the bot, then tap a card button
    ↓
done
```

The QR opens Feishu's app-registration flow with the required capabilities
and subscriptions pre-filled. The account that scans becomes the owner for
this Claude Companion installation: the account Claude Companion messages
and accepts messages from.

After approval, `init` registers the Claude Code hooks and channel, starts
the local daemon, and then proves the connection in both directions: it
sends a test card, waits for a message you send the bot, and puts up a card
with a button for you to tap.

The tap is a separate check because card delivery and card callbacks are
separate subscriptions on a Feishu app. Cards can arrive perfectly while
every button and reply box on them is inert, and that failure is silent -
so setup finds it while you are still at the keyboard rather than the day a
message you sent from a train does not arrive.

## Existing or administrator-managed Feishu app

Press `e` on the QR screen to enter an existing App ID and App Secret. Setup
also asks whether the app uses Feishu (`open.feishu.cn`) or Lark
(`open.larksuite.com`) and how to identify the owner.

The app needs:

- the bot capability and `im:message:send_as_bot`;
- `contact:user.id:readonly` if setup resolves the owner's open ID by email;
- for remote continuation, `im:message` and the `im.message.receive_v1` event
  subscription in **long connection** mode;
- for card buttons, **Interactive Card** and the `card.action.trigger` event
  subscription.

Publish a new app version after changing subscriptions. An administrator may
also need to approve new scopes. If card callbacks are not configured,
notifications still work and typed session and permission replies remain
available.

## Configuration

On Linux, configuration is stored at `~/.config/claude-companion/config.toml`
with mode `0600`; macOS and Windows use their platform-equivalent user
configuration directories:

```toml
app_id = "cli_..."
app_secret = "..."
open_id = "ou_..."           # the configured Claude Companion owner
brand = "feishu"             # use "lark" for open.larksuite.com

notify = "important"            # attention, failures, completion
# notify = "important+progress" # also update long-running progress

remote = "on"                   # "off" makes it notification-only
remote_permissions = "on"       # configured separately from continuation
```

Re-run `claude-companion init` after changing behavior settings so hook
registration matches the new configuration. Existing unrelated Claude Code
hooks are preserved, and Claude Companion's registration is idempotent.

`CLAUDE_COMPANION_CONFIG` points Claude Companion at a different
configuration file. `CLAUDE_COMPANION_STATE_DIR` points it at a different
runtime-state directory. Using both allows a separate local installation
without touching the default one.

## Starting a remote-ready Claude Code session

Claude Code Channels are currently a research preview. Until Claude
Companion is on Anthropic's channel allowlist, start a session you want to
continue from Feishu with:

```sh
claude --dangerously-load-development-channels server:claude-companion
```

A session started with plain `claude` is still discovered and sends
notifications, but it cannot be answered from Feishu. Its cards say so and
carry no reply box, so there is no way to type a message that would vanish.

## The session card

Every session gets one card, and it opens by itself: the first real work in
a turn puts it up, it updates in place with what Claude is broadly doing,
and it settles into the usual completion or failure card when the turn ends.
There is nothing to switch on, nothing to turn off, and no way to end up
with two cards for one turn.

A card also settles on its own when its session ends, when the daemon stops,
and after two hours - a card is a check-in, not a subscription.

Reply `sessions` to bring every session's card back to the bottom of the
conversation: a card standing further up is recalled and posted again, one
per session, with whatever needs you first. A session with nothing running
gets a card showing what its last turn came to.

## Where a message goes

The reply box on a card is the address: what you type there reaches the
session that card names and no other. It is the way to answer that cannot
be wrong, because the session is on the screen while you choose it.

A message typed in the conversation instead, with no card in front of you,
goes to the session it can only have meant:

- one session that can be continued - it goes there, and the answer names
  it;
- more than one - it stays put, and Claude Companion says to answer on the
  card of the one you mean;
- none - it stays put, and Claude Companion says why.

There is no selecting, no numbering, and no remembering which session you
are talking to. Whether there is one session is a fact about your computer
rather than a mode you are in.

Two things are still typed, because neither needs a session named for it:
`interrupt` stops the one turn that is running, and `y <id>` / `n <id>`
answers a permission request - the id is printed on the card that asks, so
the answer carries its own address.
