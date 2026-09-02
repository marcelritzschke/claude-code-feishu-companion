**English** | [简体中文](README.zh-CN.md)

# Wirelark

**Keep working in Claude Code. Reach the same session from Feishu when you walk away.**

Wirelark is a quiet remote companion for Claude Code. It does not run Claude
Code for you or replace the Claude Code TUI. It connects Feishu to the sessions
you started on your own computer and still own.

```text
Claude Code TUI  ←────  Wirelark  ────→  Feishu
     primary          quiet bridge          remote companion
```

Wirelark stays in the background: Claude Code remains your workspace, and
Feishu becomes a temporary remote window into it. Walk away, respond when the
session needs attention, and return to the same session in the same terminal.

## Why Wirelark?

### Claude Code stays native

Your normal workflow remains the center of the experience:

```sh
cd my-project
claude
```

There is no Wirelark workspace, agent runtime, web UI, terminal mirror, or
bridge-owned session. The original terminal remains usable throughout.

### Attention instead of noise

> **Notify on attention. Summarize on completion. Continue the existing
> session.**

Wirelark does not send every file read, search, shell command, or tool call to
Feishu. It surfaces permission requests and questions, summarizes completed
work, and can optionally update one card for a long-running turn.

The goal is to answer two questions while you are away:

- Does Claude need me?
- What happened while I was away?

### Remote continuation, not a second conversation

Ask the Feishu bot for `sessions` and choose one of the Claude Code sessions
running on your computer:

```text
payments-api
Working · Remote ready

frontend
Waiting for permission · Remote ready

wirelark
Idle · Notifications only
```

Tap a session or reply with its number. Following messages go to that specific
session and no other. If it ends, Wirelark clears the selection instead of
silently redirecting you elsewhere.

## The workflow

```text
Start Claude normally
        ↓
Wirelark discovers the session
        ↓
Walk away from the computer
        ↓
Feishu tells you when Claude needs you
        ↓
Select the exact running session
        ↓
Continue it remotely
        ↓
Return to the terminal
        ↓
Continue the same native Claude Code session
```

Wirelark provides focused attention and completion cards, a local session
overview, exact-session follow-ups, and optional remote permission decisions.
Buttons are convenient but not required: typed session numbers and explicit
permission replies work too.

## How it differs from agent gateways

Many useful agent gateways make the bridge or platform the place where work
begins:

```text
Feishu
   ↓
bridge / agent platform
   ↓
bridge starts or resumes Claude
```

Wirelark starts from a different product boundary:

```text
Claude Code TUI ← Wirelark → Feishu
```

A gateway is a natural front door when chat is the primary workspace. Wirelark
is for people who want Claude Code itself to remain the workspace and only need
a quiet way to reach it while away.

Wirelark does not own, launch, or resume your Claude Code sessions. It is not a
general agent platform, a Feishu-first coding environment, or an alternative
Claude UI.

## Install and set up

On macOS or Linux:

```sh
curl -fsSL https://raw.githubusercontent.com/marcelritzschke/wirelark/main/install.sh | sh
wirelark init
```

The installer downloads the matching self-contained binary from
[GitHub Releases](https://github.com/marcelritzschke/wirelark/releases), verifies
its checksum, and installs it to `~/.local/bin`. There is no runtime, container,
or service manager to maintain.

`wirelark init` starts QR-based Feishu onboarding:

```text
wirelark init
    ↓
scan Feishu QR
    ↓
approve
    ↓
done
```

The account that scans becomes the owner for that Wirelark installation. An
existing App ID and App Secret can be used instead in administrator-managed
environments.

Windows release archives, manual Feishu app setup, required scopes,
configuration options, and alternate install paths are covered in
[Setup and configuration](docs/setup.md).

## How it works

Wirelark runs a lightweight local daemon that maintains the Feishu connection
and knows about local Claude Code sessions. Claude Code hooks provide lifecycle,
attention, and completion events. Claude Channels provide the supported path
for sending a Feishu message into an already-running session.

```text
                         Feishu
                            ↕
                  local Wirelark daemon
                       ↗    ↖
              Claude session  Claude session
                hook + channel  hook + channel
```

The important consequence is simple:

> **Your Claude sessions remain local and user-owned. Wirelark connects to
> them; it does not own them.**

Wirelark is not a terminal emulator or a cloud-hosted Claude runtime. For
process details, local storage, hook behavior, and operational controls, see
[Security and operations](docs/security-and-operations.md).

## Security and transparency

Only the local Wirelark bridge talks to Feishu. It sends the cards and messages
needed for the configured experience—such as session identity, completion
excerpts, validation results, and permission details—not a terminal stream or
complete transcript.

Inbound messages are accepted only from the configured owner. Remote messages
go to the explicitly selected session, and remote permission approval is a
separate setting because it grants real authority to that Feishu identity.

Wirelark does not start or stop Claude Code, take over the terminal, edit files
itself, or control Claude authentication. The full trust boundary and local data
handling are documented in [Security and operations](docs/security-and-operations.md).

## Current limitations

- **Remote continuation currently needs a preview flag.** Claude Code Channels
  are a research preview, and Wirelark is not yet on Anthropic's channel
  allowlist. Start a session you want to continue remotely with:

  ```sh
  claude --dangerously-load-development-channels server:wirelark
  ```

  Plain `claude` sessions are still discovered and send notifications, but they
  appear in Feishu as **Notifications only**.

- Channels require Anthropic authentication through claude.ai or a Console API
  key. They are unavailable on Bedrock, Vertex, and Foundry. Team and Enterprise
  organizations must enable Channels centrally.
- Claude Code multiple-choice `AskUserQuestion` prompts cannot currently be
  answered through a channel. Wirelark notifies you, but the answer must be
  given in the original terminal.
- The computer, Claude Code session, Wirelark daemon, and network connection
  must remain running for remote continuation.
- WSL and native Windows are separate installations. On Windows, remote status
  remains untested until the first message confirms a session's channel.

## Project status

Focused notifications, completion summaries, local session discovery,
exact-session follow-ups, optional permission decisions, release-binary
installation, and QR onboarding are implemented today.

The next product direction is an optional concise live companion for a selected
session—not terminal streaming and not a second Claude Code interface. See the
[product experience specification](docs/product-experience-spec.md) for the
longer design rationale.

## Contributing

```sh
mise install
mise exec -- go test ./...
mise exec -- go build -o wirelark .
```

The repository pins its Go toolchain with [mise](https://mise.jdx.dev/). Setup
and diagnostic commands are documented in [Setup and configuration](docs/setup.md)
and [Security and operations](docs/security-and-operations.md).
