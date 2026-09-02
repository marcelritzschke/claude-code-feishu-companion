# Setup and configuration

This document covers Wirelark installation, Feishu app setup, and local
configuration. For the product overview, start with the
[README](../README.md).

## Install a release binary

On macOS or Linux:

```sh
curl -fsSL https://raw.githubusercontent.com/marcelritzschke/wirelark/main/install.sh | sh
```

The installer detects macOS or Linux and amd64 or arm64, downloads the matching
[GitHub Release](https://github.com/marcelritzschke/wirelark/releases), verifies
it against the release's `checksums.txt`, and installs it to `~/.local/bin`.

Set `INSTALL_DIR` to choose another location or `VERSION` to install a specific
tag:

```sh
curl -fsSL https://raw.githubusercontent.com/marcelritzschke/wirelark/main/install.sh | INSTALL_DIR=/usr/local/bin sh
```

The script above is [`install.sh`](../install.sh) at the repository root, so it
can be inspected before running. On Windows, download the `.zip` from the
releases page, verify it against the release checksums, and put `wirelark.exe`
on your `PATH`.

If you already have a Go toolchain, this is also supported:

```sh
go install github.com/marcelritzschke/wirelark@latest
```

## QR onboarding

Run:

```sh
wirelark init
```

The default flow is:

```text
wirelark init
    ↓
scan the Feishu QR code
    ↓
approve the requested app permissions
    ↓
choose notification and remote-control settings
    ↓
receive a test card
    ↓
done
```

The QR opens Feishu's app-registration flow with the required capabilities and
subscriptions pre-filled. The account that scans becomes the owner for this
Wirelark installation: the account Wirelark messages and accepts messages from.

After approval, `init` registers the Claude Code hooks and channel, starts the
local daemon, sends a test card, and verifies that Feishu can reach the local
bridge.

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

On Linux, configuration is stored at
`~/.config/wirelark/config.toml` with mode `0600`; macOS and Windows use their
platform-equivalent user configuration directories:

```toml
app_id = "cli_..."
app_secret = "..."
open_id = "ou_..."           # the configured Wirelark owner
brand = "feishu"             # use "lark" for open.larksuite.com

notify = "important"            # attention, failures, completion
# notify = "important+progress" # also update long-running progress

detail = "normal"               # summary, validation, answer excerpt
# detail = "compact"            # shorter completion cards

remote = "on"                   # "off" makes Wirelark notification-only
remote_permissions = "on"       # configured separately from continuation
```

Re-run `wirelark init` after changing behavior settings so hook registration
matches the new configuration. Existing unrelated Claude Code hooks are
preserved, and Wirelark's registration is idempotent.

`WIRELARK_CONFIG` points Wirelark at a different configuration file.
`WIRELARK_STATE_DIR` points it at a different runtime-state directory. Using
both allows a separate local installation without touching the default one.

## Starting a remote-ready Claude Code session

Claude Code Channels are currently a research preview. Until Wirelark is on
Anthropic's channel allowlist, start a session you want to continue from
Feishu with:

```sh
claude --dangerously-load-development-channels server:wirelark
```

A session started with plain `claude` is still discovered and sends
notifications, but it appears in Feishu as **Notifications only**.
