**# Claude Companion Product Experience Spec**

**## Product idea**

Claude Companion connects a local coding-agent session to Feishu.

Its purpose is not to reproduce the terminal or coding-agent UI inside chat.

Its purpose is to answer two questions:

1\. **\*\*Does my coding agent need me?\*\***

2\. **\*\*What happened while I was away?\*\***

Claude Companion should feel quiet when nothing matters and immediate when something does.

The core product principle is:

\> **\*\*Notify on attention. Summarize on completion. Avoid narrating routine work.\*\***

**---**

**## A note on version numbers**

The V1 / V2 / V3 numbering below names *product stages*, not card schema
versions. Feishu also numbers its card JSON schema (v1 and 2.0), and the two
axes are unrelated. The code refers to the product stages by name rather than
by number, to keep the collision out of the source:

| Stage | Name used in code |
|---|---|
| V1 | attention mode |
| V2 | remote continuation |
| V3 | live companion |

**---**

**# V1 — Attention Mode**

**## Goal**

V1 makes it safe to leave Claude Code running without repeatedly checking the terminal.

The user starts work locally as usual.

Claude Companion sends a Feishu DM only when:

\- Claude needs attention

\- Claude has finished meaningful work

\- Claude encountered a meaningful failure

\- Claude has been running long enough that a progress notification is useful

Routine tool calls, file reads, searches, and intermediate reasoning are not sent.

The user should be able to understand every Claude Companion notification in a few seconds from their phone.

**---**

**# V1 experience principles**

**## 1. Quiet by default**

Claude Companion should not send messages such as:

\> Claude read \`foo.go\`

\> Claude ran \`git status\`

\> Claude searched for \`RefreshToken\`

These events may matter internally, but they do not normally require the user's attention.

A successful Claude Companion session may generate only one Feishu message: the completion notification.

**---**

**## 2. Every notification answers "why am I seeing this?"**

The first line should immediately communicate the reason for the notification.

Good:

\> **\*\*Claude needs your attention\*\***

\> **\*\*Claude finished\*\***

\> **\*\*Claude hit a problem\*\***

Bad:

\> **\*\*Claude Companion notification\*\***

\> **\*\*Claude Code event\*\***

\> **\*\*Hook received\*\***

**---**

**## 3. Show project context prominently**

Every notification should identify the project/session without forcing the user to infer it.

Use a small context line such as:

\`payments-api · \~/work/payments-api\`

If a session name is available, it can be used instead:

\`Fix token refresh · payments-api\`

Do not show irrelevant technical identifiers.

**---**

**# V1 notification types**

**## A. Attention required**

This is the highest-priority Claude Companion notification.

Use it whenever Claude cannot usefully continue without user input.

**### Example: permission needed**

\`\`\`text

┌─────────────────────────────────────┐

│ ⚠️ Claude needs your attention      │

│                                     │

│ payments-api                        │

│                                     │

│ Claude is waiting for permission to │

│ continue.                            │

│                                     │

│ Requested action                    │

│ Run:                                │

│ rm -rf node\_modules && npm install  │

│                                     │

│ Open Claude Code to respond.        │

└─────────────────────────────────────┘

\`\`\`

The message should explain the requested action in human-readable form.

Do not dump the complete underlying event payload.

If the requested command is very long, show only the relevant portion and clearly indicate truncation.

**---**

**### Example: Claude asks a question**

\`\`\`text

┌─────────────────────────────────────┐

│ ❓ Claude has a question            │

│                                     │

│ payments-api                        │

│                                     │

│ Which API behavior should I keep?   │

│                                     │

│ A. Return 401 when the refresh      │

│    token is expired                 │

│                                     │

│ B. Attempt a silent refresh first   │

│                                     │

│ Open Claude Code to answer.         │

└─────────────────────────────────────┘

\`\`\`

The question itself is the most important content.

Do not surround it with agent reasoning.

**---**

**## B. Completion**

This will probably be the most common Claude Companion message.

It should tell the user:

\- what Claude accomplished

\- whether the task appears successful

\- any important validation result

\- enough of Claude's final answer to understand the outcome

**### Example: successful coding task**

\`\`\`text

┌─────────────────────────────────────┐

│ ✅ Claude finished                  │

│                                     │

│ payments-api · 4m 18s               │

│                                     │

│ Added refresh-token rotation and    │

│ updated the session middleware.     │

│                                     │

│ Validation                          │

│ ✓ 28 tests passed                   │

│ ✓ go test ./... passed              │

│                                     │

│ Claude                              │

│ "The refresh flow now rotates the   │

│ token after every successful        │

│ refresh and rejects reused tokens." │

└─────────────────────────────────────┘

\`\`\`

The first summary should ideally fit in roughly 2-4 lines.

The final Claude response may be longer, but Claude Companion should prefer a concise excerpt rather than rendering a huge response by default.

**---**

**### Example: informational task**

\`\`\`text

┌─────────────────────────────────────┐

│ ✅ Claude finished                  │

│                                     │

│ my-project · 1m 42s                 │

│                                     │

│ Investigated how Feishu streaming   │

│ cards are handled in the existing   │

│ bridge project.                     │

│                                     │

│ Key finding                         │

│ The project uses one updating card  │

│ instead of sending every tool call  │

│ as a separate message.              │

└─────────────────────────────────────┘

\`\`\`

Completion does not always mean "code changed."

The wording should reflect what Claude actually did.

**---**

**## C. Failure**

A failure notification should distinguish between:

\- Claude encountered a problem

\- the task itself failed

\- the session stopped unexpectedly

Do not present every failing shell command as a Claude Companion failure; coding agents routinely encounter failed commands while solving problems.

Only notify when the overall agent turn ended unsuccessfully or requires intervention.

**### Example**

\`\`\`text

┌─────────────────────────────────────┐

│ ❌ Claude couldn't finish           │

│                                     │

│ payments-api · 2m 51s               │

│                                     │

│ The task stopped after the test     │

│ environment failed to start.        │

│                                     │

│ Last relevant error                 │

│ PostgreSQL connection refused on    │

│ localhost:5432                      │

│                                     │

│ Open Claude Code to continue.       │

└─────────────────────────────────────┘

\`\`\`

Avoid stack traces unless the error itself is short and useful.

**---**

**## D. Long-running task**

This notification should be conservative.

The purpose is to reassure someone who walked away that Claude is still doing useful work.

It should not fire after every few minutes indefinitely.

**### Example**

\`\`\`text

┌─────────────────────────────────────┐

│ 🟡 Claude is still working          │

│                                     │

│ payments-api · 12m                  │

│                                     │

│ Current activity                    │

│ Running the integration test suite. │

│                                     │

│ So far                              │

│ • Updated 4 files                   │

│ • Unit tests passed                 │

│ • Integration tests still running   │

└─────────────────────────────────────┘

\`\`\`

This should only be sent when the task has taken significantly longer than normal.

The information should describe meaningful progress, not individual internal actions.

**---**

**# V1 message lifecycle**

Claude Companion should avoid creating chat clutter.

Whenever possible, one Claude turn should correspond to one logical Feishu notification thread or message lifecycle.

For example:

\`\`\`text

12:01  🟡 Claude is still working

       Running integration tests…

12:06  message becomes:

       ✅ Claude finished

       Integration tests passed.

       Added token rotation and 6 tests.

\`\`\`

Updating an existing status message is preferable to sending several independent updates.

If updating is not appropriate, the final completion notification should still stand on its own without requiring the user to read previous messages.

**---**

**# V1 noise policy**

By default, do NOT notify for:

\- file reads

\- file writes

\- searches

\- grep operations

\- shell commands

\- successful tests during execution

\- intermediate assistant text

\- reasoning/thinking

\- sub-agent activity

\- todo updates

\- individual tool failures that Claude recovered from

Those can become part of a future richer mode.

V1 is about attention, not observability.

**---**

**# V1 user-facing configuration**

Keep the conceptual settings simple.

The user should think in terms of behavior, not hook events.

Suggested settings:

**### Notification level**

**\*\*Important only\*\***

\- questions

\- permission requests

\- failures

\- completion

**\*\*Important + progress\*\***

Same as above, plus long-running task notifications.

The default should be **\*\*Important only\*\***.

**---**

**### Completion detail**

**\*\*Compact\*\***

\`\`\`text

✅ Claude finished

payments-api · 4m

Implemented refresh-token rotation.

28 tests passed.

\`\`\`

**\*\*Normal\*\***

\`\`\`text

✅ Claude finished

payments-api · 4m

Implemented refresh-token rotation and

updated session validation.

✓ 28 tests passed

✓ go test ./... passed

Claude:

"The implementation is complete..."

\`\`\`

Normal should be the default.

**---**

**# V1 success criterion**

A user should be comfortable doing this:

\`\`\`text

1\. Start Claude Code

2\. Give Claude a task

3\. Put laptop aside

4\. Look at phone later

5\. Immediately know:

   - whether Claude finished

   - whether Claude needs help

   - what happened

\`\`\`

If Claude Companion makes the user feel they need to monitor Feishu continuously, V1 has failed.

**---**

**# Post-V1 product direction**

V1 remains unchanged.

V1 answers:

\> **\*\*Does my coding agent need me, and what happened while I was away?\*\***

The next product problem is:

\> **\*\*Can I continue the exact local Claude session I already started, while I am away from my computer?\*\***

The recommended roadmap is:

\`\`\`text
V1 — Attention Mode
     Tell me when Claude needs me or finishes.

V2 — Remote Continuation
     Let me select and continue an existing local session from Feishu.

V3 — Live Companion
     Let me optionally watch a concise live view of that session.
\`\`\`

V2 is the main product step.

V3 adds convenience and visibility, but should not change the basic Claude Companion mental model.

**---**

**# Product architecture boundary**

The product should keep one simple architecture.

\`\`\`text
                         Feishu
                           │
                           ▼
                    Claude Companion daemon
                         (Go)
                           │
              ┌────────────┼────────────┐
              │            │            │
              ▼            ▼            ▼
          Channel       Channel       Channel
              │            │            │
          Claude A      Claude B      Claude C
\`\`\`

The Claude Companion daemon is persistent and owns the Feishu connection.

Claude Code sessions remain normal user-owned sessions.

Each running session may have a Claude Channel attached to it.

The Channel talks only to the local Claude Companion daemon.

It should not independently connect to Feishu.

The exact local IPC mechanism and protocol are implementation choices.

The product only requires that the relationship is local, private, reliable, and supports multiple simultaneous sessions.

The existing V1 hooks continue to provide automatic discovery, lifecycle, attention, and completion information to Claude Companion.

Channels provide the supported path for sending interactive input into an already-running Claude session.

The architecture must preserve this rule:

\> **\*\*Claude Companion connects to sessions. It does not own them.\*\***

**---**

**# V2 — Remote Continuation**

**## Goal**

V2 lets the user continue an existing local Claude Code session from Feishu.

The user still starts Claude Code normally on the computer.

Claude Companion should not create a new Claude session on their behalf.

Claude Companion should not make Feishu the primary session.

The experience should feel like temporarily reaching into the session that is already running on the user's computer.

**---**

**# V2 one-time setup**

V2 should not introduce another product-level setup flow.

The user has already completed the V1 Claude Companion setup.

Conceptually:

\`\`\`text
$ claude-companion init

✓ Feishu connected
✓ Claude Companion running
✓ Claude integration installed
\`\`\`

After that, Claude Companion should stay out of the way.

If current Claude Code limitations require a session to be started with Channels enabled before remote continuation is available, Claude Companion may explain that requirement clearly.

That should be treated as a current platform limitation, not as Claude Companion's long-term interaction model.

Do not make a special Claude Companion launcher part of the permanent product experience.

**---**

**# V2 normal daily use**

The ideal daily workflow remains:

\`\`\`text
$ cd ~/work/payments-api
$ claude

$ cd ~/work/frontend
$ claude
\`\`\`

Claude Companion discovers those sessions automatically.

The user does not register a project manually.

The user does not create a Claude Companion workspace.

The user does not migrate a conversation.

The user does not start Claude from Feishu.

**---**

**# V2 session overview**

Feishu should provide a lightweight overview of the user's current local sessions.

Example:

\`\`\`text
Claude Companion

🟢 payments-api
   Fix token refresh
   Working · Remote ready

⚠️ frontend
   Upgrade React
   Waiting for permission · Remote ready

⚪ claude-companion
   Idle
   Notifications only
\`\`\`

The overview should answer:

\- which local sessions exist

\- what each one is broadly doing

\- whether one needs attention

\- whether remote continuation is available

Do not show implementation identifiers such as PIDs, raw session IDs, socket names, or plugin names.

If a session is visible to Claude Companion but cannot currently receive remote input, show that honestly.

Use user-facing language such as:

\`\`\`text
Notifications only
\`\`\`

rather than presenting it as a broken session.

**---**

**# V2 selecting a session**

When more than one session exists, the user explicitly selects which one they want to continue.

Example:

\`\`\`text
Which session do you want to continue?

[ payments-api · Fix token refresh ]

[ frontend · Upgrade React ]

[ claude-companion · Idle ]
\`\`\`

After selection:

\`\`\`text
payments-api

Fix token refresh
~/work/payments-api

🟢 Remote ready

Send a message here to continue
this Claude session.
\`\`\`

The important UX rule is:

\> **\*\*The user should always know which local session they are talking to.\*\***

Claude Companion must never silently redirect a message to another active session.

**---**

**# V2 sending a message**

The user sends normal language.

No Claude Companion command syntax should be required for ordinary continuation.

Example:

\`\`\`text
You:

Before changing the API, check whether
the mobile client depends on the current
401 behavior.
\`\`\`

Claude Companion sends that message to the selected existing Claude session through its Channel.

The local terminal remains usable.

When the user returns to the computer, the same conversation is there.

There is no session migration and no duplicate Claude instance.

**---**

**# V2 when Claude is already working**

Remote input should not make local use feel unpredictable.

If Claude is already in the middle of work, Claude Companion may hold the remote message until it can be delivered naturally.

Example:

\`\`\`text
Queued for payments-api

Claude is finishing the current turn.
Your message will follow.
\`\`\`

The user does not need to understand how the queue works.

They only need confidence that:

\- the message still targets the selected session

\- it will not interrupt local work unexpectedly

\- it will not be delivered twice

**---**

**# V2 questions**

When Claude needs a decision, the user should be able to answer from Feishu when the current Claude integration supports doing so safely.

Example:

\`\`\`text
❓ Claude needs a decision

payments-api

Which behavior should I keep?

[ Keep existing API behavior ]

[ Use stricter token rotation ]

[ Answer manually ]
\`\`\`

After the answer:

\`\`\`text
You chose:

Use stricter token rotation

Claude resumed working.
\`\`\`

If Claude Companion cannot safely resolve a particular Claude interaction remotely, it should say so rather than pretending to support it.

Example:

\`\`\`text
Claude needs your attention.

This interaction must currently be
handled in Claude Code.
\`\`\`

**---**

**# V2 permissions**

When Claude Channels support trusted remote permission handling for the current session, Claude Companion may expose the decision in Feishu.

Example:

\`\`\`text
┌─────────────────────────────────────┐
│ ⚠️ Permission requested             │
│                                     │
│ payments-api                        │
│                                     │
│ Claude wants to run:                │
│                                     │
│ npm install                         │
│                                     │
│ In                                  │
│ ~/work/payments-api                 │
│                                     │
│ [ Allow once ]      [ Deny ]        │
└─────────────────────────────────────┘
\`\`\`

Permission controls should feel deliberate.

Higher-risk actions should receive stronger visual emphasis.

If remote approval is not available, keep the V1 experience:

\`\`\`text
Claude needs your attention.

Open Claude Code to respond.
\`\`\`

Do not introduce terminal-emulation behavior just to make every permission remotely actionable.

**---**

**# V2 completion**

V2 should keep the V1 notification philosophy.

A turn started from Feishu does not need a live transcript.

When Claude finishes, Claude Companion sends the same kind of concise outcome the user already understands from V1.

Example:

\`\`\`text
✅ Claude finished

payments-api · 2m 14s

Checked the mobile client before changing
the refresh behavior.

Finding

The app depends on the current 401 response,
so Claude did not change the API yet.
\`\`\`

V2 adds the ability to continue the session.

It does not change Claude Companion into a chat transcript viewer.

**---**

**# V2 returning to the computer**

This is the defining experience.

After using Feishu, the user returns to the original terminal.

The same Claude Code session is still there.

They can immediately continue typing.

Conceptually:

\`\`\`text
computer → Feishu → computer → Feishu
\`\`\`

The conversation does not change ownership when the user changes device.

**---**

**# V2 interaction boundaries**

V2 is not a remote IDE.

Do not optimize V2 for:

\- terminal emulation

\- browsing arbitrary files

\- full diffs

\- complete logs

\- every tool call

\- process management

\- recreating Claude Code controls

\- starting bridge-owned Claude sessions

\- making Feishu the canonical conversation

When detailed inspection is needed, the correct destination remains Claude Code on the computer.

V2 is for **\*\*continuing the existing session while away.\*\***

**---**

**# V2 success criterion**

V2 succeeds when this feels natural:

\`\`\`text
1. Start Claude Code locally as usual.

2. Walk away.

3. Open Claude Companion in Feishu.

4. See the local sessions that are running.

5. Select one.

6. Send a follow-up instruction.

7. Receive the result or handle an important decision.

8. Return to the computer.

9. Continue the same Claude session.
\`\`\`

The user should never feel that Claude Companion created another copy of their work.

**---**

**# V3 — Live Companion**

**## Goal**

V3 lets the user briefly check what an active local session is doing.

It adds visibility to V2.

It does not introduce another session model or another setup flow.

The new user action is:

\> **\*\*Watch\*\***

Watching should be optional.

Most sessions should still be quiet unless the user chooses to look.

**---**

**# V3 user experience**

From the session overview, the user can open a session and choose to watch it.

Example:

\`\`\`text
payments-api

Fix token refresh
Working · 6m

[ Continue ]

[ Watch ]
\`\`\`

Choosing **Watch** opens or updates one live card.

**---**

**# V3 live card**

Example:

\`\`\`text
┌─────────────────────────────────────┐
│ 🟢 Claude is working                │
│                                     │
│ Fix token refresh                   │
│ payments-api · 6m 12s               │
│                                     │
│ Current progress                    │
│ Consolidating duplicate refresh     │
│ validation and checking callers.    │
│                                     │
│ Recent activity                     │
│ ✓ Read auth/session.go              │
│ ✓ Updated refresh validation        │
│ ◌ Running go test ./...             │
│                                     │
│ Updated just now                    │
└─────────────────────────────────────┘
\`\`\`

The card should answer:

\> **\*\*What is Claude broadly doing right now?\*\***

It should not answer:

\> **\*\*What events has Claude Code emitted?\*\***

**---**

**# V3 live updates**

The same card updates in place.

Claude Companion should not send a new Feishu message for every action.

Recent activity should remain short.

Three to five meaningful items is enough.

Examples:

\`\`\`text
✓ Read auth/session.go
✓ Updated refresh validation
◌ Running integration tests
\`\`\`

Avoid raw tool payloads or protocol data.

The user should not need to know how Claude Code represents an action internally.

**---**

**# V3 progress**

V3 may show a short human-readable description of the current work.

Good:

\`\`\`text
Found duplicate refresh validation.

Consolidating the logic and checking
whether the mobile client depends on it.
\`\`\`

Do not show raw chain-of-thought or internal reasoning.

V3 exposes progress, not reasoning.

**---**

**# V3 deeper activity**

A meaningful activity item may offer additional detail when that is useful.

Example:

\`\`\`text
▼ ✓ Ran tests

go test ./...

28 passed
0 failed
\`\`\`

For a recovered problem:

\`\`\`text
▼ ⚠ Integration test failed

Database connection refused.

Claude continued investigating.
\`\`\`

The user should always be able to distinguish:

\- a tool encountered a problem

from:

\- the overall task failed

**---**

**# V3 completion**

When the watched task finishes, the same card settles into a completed state.

Example:

\`\`\`text
┌─────────────────────────────────────┐
│ ✅ Claude finished                  │
│                                     │
│ Fix token refresh                   │
│ payments-api · 8m 41s               │
│                                     │
│ Implemented token rotation and      │
│ consolidated refresh validation.    │
│                                     │
│ Validation                          │
│ ✓ 34 tests passed                   │
│ ✓ go test ./... passed              │
│                                     │
│ [ Continue session ]                │
└─────────────────────────────────────┘
\`\`\`

The final state should feel settled.

No stale spinner.

No old "working" state.

No need to read the entire activity history to understand the outcome.

**---**

**# V3 relationship to V2**

V3 should require no additional setup.

A V2-capable session can simply be watched.

The user may move naturally between the two behaviors:

\`\`\`text
Session list
    ↓
Watch progress
    ↓
Send a follow-up
    ↓
Claude continues
    ↓
Watch again
    ↓
Return to terminal
\`\`\`

The session remains the same throughout.

**---**

**# V3 noise policy**

Watching is opt-in.

Without Watch enabled, Claude Companion behaves like V1/V2.

Even while watching, do not show:

\- every file read

\- every search

\- every shell command

\- raw logs

\- raw protocol events

\- internal reasoning

\- every recovered failure

\- every state transition

A good live card should be understandable in a few seconds.

If the user feels compelled to continuously monitor it, V3 has failed.

**---**

**# V1 vs V2 vs V3**

\| Capability | V1 | V2 | V3 |
\|---|---|---|---|
\| Attention notifications | Yes | Yes | Yes |
\| Completion summaries | Yes | Yes | Yes |
\| Automatic local session discovery | Background | User-facing | User-facing |
\| Session overview | No | Yes | Yes |
\| Select an existing local session | No | Yes | Yes |
\| Continue that session from Feishu | No | Yes | Yes |
\| Remote decisions / permissions | No | When supported safely | When supported safely |
\| Live session view | No | No | Yes |
\| Condensed recent activity | No | No | Yes |
\| Full Claude Code UI reproduction | No | No | No |
\| Claude Companion owns Claude sessions | No | No | No |

**---**

**# Recommended positioning**

**## V1**

**\*\*Claude Companion tells you when your coding agent needs you.\*\***

**---**

**## V2**

**\*\*Continue the Claude session already running on your computer, from Feishu.\*\***

The important distinction is not merely remote control.

It is continuity with the user's own local sessions.

**---**

**## V3**

**\*\*See what your local Claude session is doing when you want to check in.\*\***

V3 should feel like a quiet window into the session, not a second Claude Code interface.

**---**

**# The core design rule**

Before adding a post-V1 feature, ask:

\> **\*\*Does this help the user understand, continue, or safely steer an existing local session while away?\*\***

If not, it probably does not belong in Claude Companion.

The enduring product rule is:

\> **\*\*Notify on attention. Summarize on completion. Continue the existing session. Avoid recreating the terminal.\*\***
