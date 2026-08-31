# Wirelark Product Experience Spec

## Product idea

Wirelark connects a local coding-agent session to Feishu.

Its purpose is not to reproduce the terminal or coding-agent UI inside chat.

Its purpose is to answer two questions:

1. **Does my coding agent need me?**
2. **What happened while I was away?**

Wirelark should feel quiet when nothing matters and immediate when something does.

The core product principle is:

> **Notify on attention. Summarize on completion. Avoid narrating routine work.**

---

# V1 - Attention Mode

## Goal

V1 makes it safe to leave Claude Code running without repeatedly checking the terminal.

The user starts work locally as usual.

Wirelark sends a Feishu DM only when:

- Claude needs attention
- Claude has finished meaningful work
- Claude encountered a meaningful failure
- Claude has been running long enough that a progress notification is useful

Routine tool calls, file reads, searches, and intermediate reasoning are not sent.

The user should be able to understand every Wirelark notification in a few seconds from their phone.

---

# V1 experience principles

## 1. Quiet by default

Wirelark should not send messages such as:

> Claude read `foo.go`

> Claude ran `git status`

> Claude searched for `RefreshToken`

These events may matter internally, but they do not normally require the user's attention.

A successful Wirelark session may generate only one Feishu message: the completion notification.

---

## 2. Every notification answers "why am I seeing this?"

The first line should immediately communicate the reason for the notification.

Good:

> **Claude needs your attention**

> **Claude finished**

> **Claude hit a problem**

Bad:

> **Wirelark notification**

> **Claude Code event**

> **Hook received**

---

## 3. Show project context prominently

Every notification should identify the project/session without forcing the user to infer it.

Use a small context line such as:

`payments-api · ~/work/payments-api`

If a session name is available, it can be used instead:

`Fix token refresh · payments-api`

Do not show irrelevant technical identifiers.

---

# V1 notification types

## A. Attention required

This is the highest-priority Wirelark notification.

Use it whenever Claude cannot usefully continue without user input.

### Example: permission needed

```text
┌─────────────────────────────────────┐
│ ⚠️ Claude needs your attention      │
│                                     │
│ payments-api                        │
│                                     │
│ Claude is waiting for permission to │
│ continue.                            │
│                                     │
│ Requested action                    │
│ Run:                                │
│ rm -rf node_modules && npm install  │
│                                     │
│ Open Claude Code to respond.        │
└─────────────────────────────────────┘
```

The message should explain the requested action in human-readable form.

Do not dump the complete underlying event payload.

If the requested command is very long, show only the relevant portion and clearly indicate truncation.

---

### Example: Claude asks a question

```text
┌─────────────────────────────────────┐
│ ❓ Claude has a question            │
│                                     │
│ payments-api                        │
│                                     │
│ Which API behavior should I keep?   │
│                                     │
│ A. Return 401 when the refresh      │
│    token is expired                 │
│                                     │
│ B. Attempt a silent refresh first   │
│                                     │
│ Open Claude Code to answer.         │
└─────────────────────────────────────┘
```

The question itself is the most important content.

Do not surround it with agent reasoning.

---

## B. Completion

This will probably be the most common Wirelark message.

It should tell the user:

- what Claude accomplished
- whether the task appears successful
- any important validation result
- enough of Claude's final answer to understand the outcome

### Example: successful coding task

```text
┌─────────────────────────────────────┐
│ ✅ Claude finished                  │
│                                     │
│ payments-api · 4m 18s               │
│                                     │
│ Added refresh-token rotation and    │
│ updated the session middleware.     │
│                                     │
│ Validation                          │
│ ✓ 28 tests passed                   │
│ ✓ go test ./... passed              │
│                                     │
│ Claude                              │
│ "The refresh flow now rotates the   │
│ token after every successful        │
│ refresh and rejects reused tokens." │
└─────────────────────────────────────┘
```

The first summary should ideally fit in roughly 2-4 lines.

The final Claude response may be longer, but Wirelark should prefer a concise excerpt rather than rendering a huge response by default.

---

### Example: informational task

```text
┌─────────────────────────────────────┐
│ ✅ Claude finished                  │
│                                     │
│ wirelark · 1m 42s                   │
│                                     │
│ Investigated how Feishu streaming   │
│ cards are handled in the existing   │
│ bridge project.                     │
│                                     │
│ Key finding                         │
│ The project uses one updating card  │
│ instead of sending every tool call  │
│ as a separate message.              │
└─────────────────────────────────────┘
```

Completion does not always mean "code changed."

The wording should reflect what Claude actually did.

---

## C. Failure

A failure notification should distinguish between:

- Claude encountered a problem
- the task itself failed
- the session stopped unexpectedly

Do not present every failing shell command as a Wirelark failure; coding agents routinely encounter failed commands while solving problems.

Only notify when the overall agent turn ended unsuccessfully or requires intervention.

### Example

```text
┌─────────────────────────────────────┐
│ ❌ Claude couldn't finish           │
│                                     │
│ payments-api · 2m 51s               │
│                                     │
│ The task stopped after the test     │
│ environment failed to start.        │
│                                     │
│ Last relevant error                 │
│ PostgreSQL connection refused on    │
│ localhost:5432                      │
│                                     │
│ Open Claude Code to continue.       │
└─────────────────────────────────────┘
```

Avoid stack traces unless the error itself is short and useful.

---

## D. Long-running task

This notification should be conservative.

The purpose is to reassure someone who walked away that Claude is still doing useful work.

It should not fire after every few minutes indefinitely.

### Example

```text
┌─────────────────────────────────────┐
│ 🟡 Claude is still working          │
│                                     │
│ payments-api · 12m                  │
│                                     │
│ Current activity                    │
│ Running the integration test suite. │
│                                     │
│ So far                              │
│ • Updated 4 files                   │
│ • Unit tests passed                 │
│ • Integration tests still running   │
└─────────────────────────────────────┘
```

This should only be sent when the task has taken significantly longer than normal.

The information should describe meaningful progress, not individual internal actions.

---

# V1 message lifecycle

Wirelark should avoid creating chat clutter.

Whenever possible, one Claude turn should correspond to one logical Feishu notification thread or message lifecycle.

For example:

```text
12:01  🟡 Claude is still working
       Running integration tests…

12:06  message becomes:

       ✅ Claude finished
       Integration tests passed.
       Added token rotation and 6 tests.
```

Updating an existing status message is preferable to sending several independent updates.

If updating is not appropriate, the final completion notification should still stand on its own without requiring the user to read previous messages.

---

# V1 noise policy

By default, do NOT notify for:

- file reads
- file writes
- searches
- grep operations
- shell commands
- successful tests during execution
- intermediate assistant text
- reasoning/thinking
- sub-agent activity
- todo updates
- individual tool failures that Claude recovered from

Those can become part of a future richer mode.

V1 is about attention, not observability.

---

# V1 user-facing configuration

Keep the conceptual settings simple.

The user should think in terms of behavior, not hook events.

Suggested settings:

### Notification level

**Important only**

- questions
- permission requests
- failures
- completion

**Important + progress**

Same as above, plus long-running task notifications.

The default should be **Important only**.

---

### Completion detail

**Compact**

```text
✅ Claude finished

payments-api · 4m

Implemented refresh-token rotation.
28 tests passed.
```

**Normal**

```text
✅ Claude finished

payments-api · 4m

Implemented refresh-token rotation and
updated session validation.

✓ 28 tests passed
✓ go test ./... passed

Claude:
"The implementation is complete..."
```

Normal should be the default.

---

# V1 success criterion

A user should be comfortable doing this:

```text
1. Start Claude Code
2. Give Claude a task
3. Put laptop aside
4. Look at phone later
5. Immediately know:
   - whether Claude finished
   - whether Claude needs help
   - what happened
```

If Wirelark makes the user feel they need to monitor Feishu continuously, V1 has failed.

---

# V2 - Remote Companion

## Goal

V2 lets the user follow and interact with an active coding-agent session from Feishu without turning Feishu into a terminal emulator.

V2 introduces two new concepts:

1. **Watch**
2. **Respond**

The user can see what Claude is broadly doing and handle important interactions remotely.

---

# V2 principle

V1 says:

> Tell me when something matters.

V2 says:

> Let me briefly check in and intervene when necessary.

It still does NOT say:

> Reproduce every line of Claude Code in Feishu.

---

# V2: live session card

When the user chooses to watch a session, Wirelark maintains one live card.

Example:

```text
┌─────────────────────────────────────┐
│ 🟢 Claude is working                │
│                                     │
│ Fix token refresh                   │
│ payments-api · 6m 12s               │
│                                     │
│ I found that refresh tokens are     │
│ validated in two separate places.   │
│ I'm consolidating that logic now.   │
│                                     │
│ Recent activity                     │
│ ✓ Read auth/session.go              │
│ ✓ Search RefreshToken               │
│ ✓ Edit auth/session.go              │
│ ◌ Running go test ./...             │
│                                     │
│ Updated just now                    │
└─────────────────────────────────────┘
```

Important distinction:

The activity list is a **summary of recent work**, not a complete event log.

Keep only a small number of recent meaningful actions visible.

For example, 3-5 items.

---

# V2 tool activity

Tool calls should be condensed.

Good:

```text
✓ Read auth/session.go
✓ Edited refresh-token validation
◌ Running integration tests
```

Avoid:

```text
Read({"file_path":"/Users/foo/work/api/src/auth/session.go","offset":0,...})

Bash({"command":"go test ./...","timeout":120000,...})
```

The user should understand the activity without understanding Wirelark's internal event representation.

---

# V2 expanded activity

A user may optionally expand an activity item.

Example:

```text
▼ ✓ Ran tests

Command
go test ./...

Result
28 passed
0 failed
```

For a failed action:

```text
▼ ⚠ Integration test failed

Command
go test ./integration/...

Result
Database connection refused.

Claude continued investigating.
```

The last line matters.

A failing tool invocation is not necessarily a failed task.

---

# V2: reasoning

Do not show raw chain-of-thought.

Instead, show short progress statements that describe what Claude is doing.

Good:

```text
Investigating where refresh tokens are validated.

Found duplicate validation logic.

Updating the session middleware and adding a regression test.
```

Bad:

```text
We need inspect this carefully. Maybe the function...
Actually perhaps auth.go. Let's reason...
```

The Feishu experience should expose **progress**, not internal reasoning.

---

# V2: remote questions

This is where V2 becomes materially more useful than V1.

If Claude needs a choice, the user can answer inside Feishu.

Example:

```text
┌─────────────────────────────────────┐
│ ❓ Claude needs a decision          │
│                                     │
│ I found two reasonable ways to fix  │
│ the refresh behavior.               │
│                                     │
│ Which should I use?                 │
│                                     │
│ [ Keep existing API behavior ]      │
│                                     │
│ [ Use stricter token rotation ]     │
│                                     │
│ [ I'll answer manually ]            │
└─────────────────────────────────────┘
```

After selection:

```text
You chose:
Use stricter token rotation

Claude resumed working.
```

The live session card then continues updating.

---

# V2: remote permission

Permissions should be rendered as explicit decisions, separate from general session activity.

Example:

```text
┌─────────────────────────────────────┐
│ ⚠️ Permission requested             │
│                                     │
│ Claude wants to run:                │
│                                     │
│ npm install                         │
│                                     │
│ In                                  │
│ ~/work/payments-api                 │
│                                     │
│ [ Allow ]        [ Deny ]           │
└─────────────────────────────────────┘
```

For higher-risk operations, make the action more prominent:

```text
┌─────────────────────────────────────┐
│ ⚠️ Review this action               │
│                                     │
│ Claude wants to delete:             │
│                                     │
│ ./tmp/generated/*                   │
│                                     │
│ [ Allow once ]                      │
│ [ Deny ]                            │
└─────────────────────────────────────┘
```

Do not make approval buttons casual or easy to confuse with informational buttons.

---

# V2: completion after watching

When the task finishes, the same live card should become a completed card.

Before:

```text
🟢 Claude is working
◌ Running go test ./...
```

After:

```text
┌─────────────────────────────────────┐
│ ✅ Claude finished                  │
│                                     │
│ Fix token refresh                   │
│ payments-api · 8m 41s               │
│                                     │
│ Implemented token rotation and      │
│ consolidated refresh validation.    │
│                                     │
│ Changed                             │
│ • auth/session.go                   │
│ • auth/token.go                     │
│ • auth/session_test.go              │
│                                     │
│ Validation                          │
│ ✓ 34 tests passed                   │
│ ✓ go test ./... passed              │
│                                     │
│ [ View summary ]                    │
└─────────────────────────────────────┘
```

The final state should feel settled.

There should no longer be spinners, "working" text, or stale progress indicators.

---

# V2: session list

Once users have multiple Claude sessions, Wirelark needs a lightweight overview.

Example:

```text
Wirelark

🟢 payments-api
   Fix token refresh
   Working · 6m

⚠️ frontend
   Upgrade React
   Waiting for permission

✅ wirelark
   Improve Feishu notifications
   Finished 18m ago
```

This is useful in Feishu without trying to recreate a full process manager.

---

# V2: sending a message to Claude

Once remote input is supported, keep the mental model simple:

The user is talking to **the existing local Claude session**.

Example:

```text
You:
Before changing the API, check whether
the mobile client depends on the current
401 behavior.
```

Then the existing session card changes to:

```text
🟢 Claude is working

Checking mobile-client usage before
changing the API behavior.

Recent activity
✓ Search refresh endpoint
✓ Read mobile auth client
◌ Comparing error handling
```

Avoid adding chat commands or control syntax when natural language works.

---

# V2 interaction boundaries

Even in V2, Wirelark should avoid becoming a remote IDE.

Do NOT optimize for:

- browsing arbitrary files
- showing full diffs inline
- terminal emulation
- scrolling through every tool call
- displaying complete logs
- showing raw agent protocol messages
- reproducing Claude Code menus/settings
- exposing every internal agent state

When the user needs that level of detail, the correct destination remains Claude Code on the computer.

Wirelark is for understanding and steering the session while away.

---

# V1 vs V2

| Capability | V1 | V2 |
|---|---|---|
| Completion notifications | Yes | Yes |
| Permission notifications | Yes | Yes |
| Failure notifications | Yes | Yes |
| Long-running progress | Optional | Yes |
| Tool-call timeline | No | Condensed |
| Live updating session card | No | Yes |
| Progress narration | No | Yes |
| Answer Claude questions remotely | No | Yes |
| Approve/deny remotely | No | Yes |
| Send follow-up instructions | No | Yes |
| Multiple session overview | No | Yes |
| Full Claude UI reproduction | No | No |

---

# Recommended product positioning

## V1

**Wirelark tells you when your coding agent needs you.**

Alternative:

**Walk away from Claude Code. Wirelark will ping you when it matters.**

---

## V2

**Your coding agent, within reach.**

Or:

**Follow and steer your local coding sessions from Feishu.**

---

# The core design rule

Before adding any Feishu message, ask:

> If the user is away from their computer, does seeing or acting on this information help them?

If not, don't send it.

That rule should keep Wirelark useful instead of noisy.
