# Security and operations

This document describes Wirelark's local processes, data boundary, failure
behavior, and diagnostic controls. For the product overview, start with the
[README](../README.md).

## Local architecture

One Wirelark binary serves three local roles:

- `wirelark daemon` owns the Feishu connection, session registry, and cards. It
  starts automatically when a hook or channel needs it.
- `wirelark channel` is the MCP channel server Claude Code starts for a
  remote-ready session. It holds no Feishu credentials and talks only to the
  daemon through local IPC.
- `wirelark send` is the asynchronous Claude Code hook entrypoint. It passes a
  lifecycle, attention, or completion event to the daemon and exits.

```text
                         Feishu
                            ↕
                  local Wirelark daemon
                       ↗    ↖
              Claude session  Claude session
                hook + channel  hook + channel
```

Wirelark never starts, resumes, or stops Claude Code. The original terminal
stays usable, and each remote message is delivered only to the explicitly
selected running session.

## Data sent to Feishu

Feishu receives the cards and messages Wirelark intentionally sends. Depending
on the event and configured detail level, that can include:

- the session title and project name;
- a completion or failure summary;
- validation results and filenames extracted from the turn;
- an excerpt of Claude's final answer;
- tool and command details needed to understand a permission request.

Wirelark does not upload a terminal stream or a complete session transcript.
The transcript is read locally to produce the selected summary content.

Inbound messages and card actions are accepted only from the configured owner.
Remote permission approval is nevertheless a real grant of authority: anyone
with access to that Feishu identity could approve a command. Remote approvals
can be disabled independently of remote continuation. Potentially destructive
commands receive stronger warning treatment and display the full command.

## Local files and credentials

The paths below are the Linux defaults. On macOS and Windows, Wirelark uses the
platform-equivalent user configuration and cache directories.

Feishu credentials are stored in `~/.config/wirelark/config.toml`, which uses
mode `0600`.

Runtime state lives under `~/.cache/wirelark`, kept private with mode `0700`.
It includes the cached tenant token, progress-card bookkeeping, session
snapshot, daemon endpoint, and debug log.

On Unix, processes communicate over a local socket accessible only to the
user. Windows uses a loopback port guarded by a secret in a `0600` file because
Unix sockets are unavailable there.

## Failure behavior

Hooks and channels are designed not to block or break a Claude Code session.
They avoid writing to the channel's protocol stream and return safely if the
bridge cannot be reached.

If the daemon is unavailable during a hook event, `wirelark send` can deliver
the notification directly. Remote continuation is unavailable until the daemon
returns, but the Claude Code session remains usable.

## Hook registration

Wirelark registers asynchronous hooks for permission requests, Claude
questions, completion, and failure. With remote continuation enabled, it also
registers lifecycle events used to discover sessions and represent their
state. Tool-completion events are registered only when long-running progress
updates are enabled.

Subagent events are skipped. Re-running `wirelark init` preserves unrelated
hooks, removes obsolete Wirelark entries, and writes a backup.

## Diagnostics

Check or stop the daemon:

```sh
wirelark daemon --status
wirelark daemon --stop
```

Render a notification without a configuration file or Feishu connection:

```sh
echo '<hook payload json>' | wirelark send --dry-run
```

Enable diagnostic logging for a hook event:

```sh
echo '<hook payload json>' | WIRELARK_DEBUG=1 wirelark send
```

The default log is `~/.cache/wirelark/debug.log`. `WIRELARK_CONFIG` and
`WIRELARK_STATE_DIR` can isolate a diagnostic installation from the default
one.

## Windows and WSL

WSL and native Windows are separate environments; each needs its own binary
and `wirelark init`. On Windows, Wirelark cannot inspect a session's process
command line, so the session appears as **Remote untested** until its first
message confirms the channel.
