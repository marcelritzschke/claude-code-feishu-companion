# Security and operations

This document describes Claude Companion's local processes, data boundary,
failure behavior, and diagnostic controls. For the product overview, start
with the [README](../README.md).

## Local architecture

One Claude Companion binary serves three local roles:

- `claude-companion daemon` owns the Feishu connection, session registry, and
  cards. It starts automatically when a hook or channel needs it.
- `claude-companion channel` is the MCP channel server Claude Code starts for
  a remote-ready session. It holds no Feishu credentials and talks only to
  the daemon through local IPC.
- `claude-companion send` is the asynchronous Claude Code hook entrypoint. It
  passes a lifecycle, attention, or completion event to the daemon and
  exits.

```text
                         Feishu
                            ↕
                  local Claude Companion daemon
                       ↗    ↖
              Claude session  Claude session
                hook + channel  hook + channel
```

Claude Companion never starts, resumes, or stops Claude Code. The original
terminal stays usable, and each remote message is delivered only to the
explicitly selected running session.

## Data sent to Feishu

Feishu receives the cards and messages Claude Companion intentionally
sends. Depending on the event and configured detail level, that can
include:

- the session title and project name;
- a completion or failure summary;
- validation results and filenames extracted from the turn;
- an excerpt of Claude's final answer;
- tool and command details needed to understand a permission request;
- while a session is being watched, a short description of the current work and
  a few condensed recent actions.

Claude Companion does not upload a terminal stream or a complete session
transcript. The transcript is read locally to produce the selected summary
content.

Watching is opt-in and per session. It is started explicitly from Feishu,
sends no additional Claude Code events, and ends when the turn ends. While a
session is watched, Claude Companion re-reads its transcript locally every
few seconds and rewrites one existing card; it does not send a message per
action, and it never sends model reasoning.

The daemon also checks GitHub for a newer stable release at startup and
every 24 hours, and sends one plain-text Feishu message the first time it
finds a version newer than the one running - never more than once per
version. This check is outbound-only: the request carries no session data,
just an anonymous read of the project's public release list.

Inbound messages and card actions are accepted only from the configured owner.
Remote permission approval is nevertheless a real grant of authority: anyone
with access to that Feishu identity could approve a command. Remote approvals
can be disabled independently of remote continuation. Potentially destructive
commands receive stronger warning treatment and display the full command.

## Local files and credentials

The paths below are the Linux defaults. On macOS and Windows, Claude
Companion uses the platform-equivalent user configuration and cache
directories.

Feishu credentials are stored in `~/.config/claude-companion/config.toml`,
which uses mode `0600`.

Runtime state lives under `~/.cache/claude-companion`, kept private with
mode `0700`. It includes the cached tenant token, progress-card bookkeeping,
session snapshot, daemon endpoint, debug log, and the update check's cache
(the latest version last seen on GitHub, and the version last announced).

On Unix, processes communicate over a local socket accessible only to the
user. Windows uses a loopback port guarded by a secret in a `0600` file because
Unix sockets are unavailable there.

## Failure behavior

Hooks and channels are designed not to block or break a Claude Code session.
They avoid writing to the channel's protocol stream and return safely if the
bridge cannot be reached.

If the daemon is unavailable during a hook event, `claude-companion send`
can deliver the notification directly. Remote continuation is unavailable
until the daemon returns, but the Claude Code session remains usable.

## Hook registration

Claude Companion registers asynchronous hooks for permission requests, Claude
questions, completion, and failure. With remote continuation enabled, it also
registers lifecycle events used to discover sessions and represent their
state. Tool-completion events are registered only when long-running progress
updates are enabled.

Subagent events are skipped. Re-running `claude-companion init` preserves
unrelated hooks, removes obsolete Claude Companion entries, and writes a
backup.

## Diagnostics

Check or stop the daemon:

```sh
claude-companion daemon --status
claude-companion daemon --stop
```

Force a live update check, independent of the daemon's cache or schedule:

```sh
claude-companion update
```

Render a notification without a configuration file or Feishu connection:

```sh
echo '<hook payload json>' | claude-companion send --dry-run
```

Enable diagnostic logging for a hook event:

```sh
echo '<hook payload json>' | CLAUDE_COMPANION_DEBUG=1 claude-companion send
```

The default log is `~/.cache/claude-companion/debug.log`.
`CLAUDE_COMPANION_CONFIG` and `CLAUDE_COMPANION_STATE_DIR` can isolate a
diagnostic installation from the default one.

## Windows and WSL

WSL and native Windows are separate environments; each needs its own
binary and `claude-companion init`. On Windows, Claude Companion cannot
inspect a session's process command line, so the session appears as
**Remote untested** until its first message confirms the channel.
