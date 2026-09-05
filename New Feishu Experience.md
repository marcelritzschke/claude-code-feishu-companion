# Wirelark Feishu Experience

## Goal

The Feishu experience should answer one question exceptionally well:

> Is Claude still making progress, or does it need me?

Wirelark should provide enough visibility to give the user confidence without turning Feishu into a terminal feed.

The experience should remain quiet by default.

Working activity is visible when the user looks for it.  
Human attention is pushed when it is required.  
Completed work is summarized.  
The Claude Code terminal remains the source of truth.

---

## Three card types

Wirelark uses only three user-facing card types.

### Session Card

One live representation of a Claude Code session.

The Session Card answers:

- What session is this?
- What is Claude doing?
- Is it still active?
- Does it need me?
- What happened when the current turn finished?

It evolves with the session instead of producing a new message for every state change.

Typical lifecycle:

```text
Working
   ↓
Waiting for permission / Waiting for answer
   ↓
Working
   ↓
Completed
```

or:

```text
Working
   ↓
Failed / Interrupted
```

Normal Session Card updates are quiet and should not create notification fatigue.

---

### Permission Card

Used only when Claude needs an explicit security-sensitive decision.

Example:

```text
Permission required

payments-api

Claude wants to run:

go test ./...

[ Allow once ]
[ Deny ]
```

There is no **Allow for this session**, and there cannot be one. Claude
Code validates a relayed verdict against a closed enum:

```text
notifications/claude/channel/permission
  params: { request_id: string, behavior: "allow" | "deny" }
```

A session-wide grant is not a value the protocol has, so the card offers
the two answers that exist rather than a third that would be silently
rejected. If Claude Code ever adds one, this is the card that should
change first.

The Permission Card generates an attention notification.

It should clearly show:

- which session is requesting permission
- what Claude wants permission to do
- enough detail for an informed decision
- the scope of the approval

After the user responds, the card settles into a clear final state:

```text
✓ Allowed once
```

or:

```text
✕ Denied
```

Only an authorized Wirelark user may make permission decisions.

---

### Question Card

Used when Claude cannot continue without user input.

For discrete choices:

```text
Claude needs your input

payments-api

Which API should remain backwards compatible?

A. v1
B. v2
C. Both

This must currently be answered in Claude Code.
```

The options are shown but not tappable. A permission prompt can be
answered remotely because Claude Code relays it over the channel and
accepts a verdict back; a question has no such path - there is no
equivalent of `permission_request` for `AskUserQuestion`, so nothing a
card did could reach the waiting dialog. Showing the options still earns
its place: the user learns what is being asked, and whether it is worth
walking back to the terminal for.

The Question Card is therefore notification-only until Claude Code offers
a way to answer one from outside the session.

For open-ended questions:

```text
Claude needs your input

payments-api

What should happen when the refresh token has expired?

Reply to this message.
```

The Question Card generates an attention notification.

After an answer is received, the card should show that it has been answered and should no longer appear actionable.

---

## Session Card

The Session Card is the center of the experience.

A working session might look like:

```text
🟢 Working · 4m

payments-api
Fix token refresh

Consolidating refresh validation and
checking the affected callers.

Activity just now

[ Interrupt ]
```

The card should communicate liveness without exposing implementation noise.

### Show

Show only information useful to someone checking progress:

- current phase
- session/project identity
- current task
- latest meaningful progress
- how recently meaningful activity occurred
- final result when the turn completes

### Show, but folded

The card carries every step of the turn, and hides almost all of it.

Plain steps are one line each - and a run of the same kind is one line for
the run, so four reads are `Read 4 files`. The two things worth looking
into keep a panel of their own: whatever is running right now, and whatever
went wrong. A panel is shut until the user opens it.

This is not a reversal of "do not turn Feishu into a terminal feed". A feed
is a stream of messages that keep arriving; this is one card that quietly
gets longer, and the reader sees only as much of it as they ask for. What
made a full step list unacceptable was volume, and folding removes that
objection.

Whatever does not fit folds into a single shut panel that says how much it
holds. The steps are never dropped: "what happened while I was away" is
half of what this product answers.

### Do not show

Do not expose:

- raw shell output, or any other tool output
- protocol events
- token-by-token text
- chain-of-thought or internal reasoning

A step's panel shows its **input** - the command, the path, the pattern -
and never its output. Input is bounded, and it is the question the user
actually has when they open a step: what exactly did it run? Output is
unbounded, and it is where a shell command's secrets would end up printed
in a chat message.

Reasoning stays off the card even though folding would hide it, because
folding solves volume and reasoning has a second problem: it is not a
commitment. Claude reasons its way towards approaches it then discards, and
a user who reads a discarded plan on their phone and walks back to the
terminal to argue with it has been misled by the card.

The goal is not observability for debugging.

The goal is confidence.

---

## Liveness

Elapsed time alone is not enough.

These two states are very different:

```text
Working · 8m
Activity just now
```

and:

```text
Working · 8m
No new activity for 3m
```

Wirelark should therefore communicate both:

- how long the turn has been running
- how recently meaningful activity was observed

A lack of recent activity is not automatically an error. It simply gives the user an honest indication of what Wirelark can currently observe.

The card should never fake activity merely because its timer continues increasing.

---

## Working

While Claude is working, the Session Card changes quietly.

Example:

```text
🟢 Working · 2m

payments-api
Fix token refresh

Updating refresh-token validation.

Activity 6s ago
```

Later the same card may become:

```text
🟢 Working · 3m

payments-api
Fix token refresh

Running validation after the changes.

Activity just now
```

These updates should feel live but calm.

The user should not feel compelled to continuously watch the card.

---

## Waiting

If Claude requires the user, the Session Card reflects that state immediately:

```text
🟠 Waiting for permission

payments-api
Fix token refresh

Claude needs approval before continuing.
```

A separate Permission Card or Question Card delivers the actual actionable notification.

This separation is intentional:

**Session Card = state**

**Permission / Question Card = action**

After the user responds, the Session Card returns to Working.

---

## Completed

When the current turn finishes, the Session Card settles into the result:

```text
✅ Completed · 8m

payments-api
Fix token refresh

Implemented token rotation and consolidated
refresh validation.

Validation
✓ 34 tests passed
✓ go test ./... passed

[ Continue ]
```

The completion view should summarize what matters rather than reproduce Claude's full answer.

Starting another turn in the same Claude Code session reactivates the same session experience.

---

## Failed

Failures should be unmistakable:

```text
🔴 Failed · 6m

payments-api
Fix token refresh

Tests still fail in auth/session_test.go.

Validation
✕ 2 tests failed

[ Continue ]
```

A failed turn is different from a temporary tool failure.

Do not mark the entire turn failed merely because an intermediate command failed and Claude recovered.

Failures should be considered important and may notify the user.

---

## Interrupted

The Session Card may allow the owner to interrupt the current Claude turn:

```text
[ Interrupt ]
```

This action means:

> Stop the work Claude is currently performing and return the existing session to an interactive state.

It does **not** mean:

- terminate Claude Code
- delete the session
- close the terminal
- create another session

This preserves Wirelark's product boundary:

> Wirelark can interact with your Claude session, but it does not own its lifecycle.

No confirmation is necessary for interrupting a normal working turn, provided the action is restricted to an authorized user.

---

## Notification-only sessions

Sessions that Wirelark can observe but cannot remotely control must be shown honestly.

Example:

```text
⚪ Working · Notifications only

wirelark
Improve README

Claude is still working locally.

Activity 12s ago

[ Watch ]
```

`Watch` enables a read-only live view when Wirelark can provide one.

The card must continue to make the limitation obvious:

```text
Notifications only
```

Watching a session must never imply that remote continuation or remote permissions are available.

---

## Notification policy

Wirelark distinguishes **activity** from **attention**.

### Quiet

Do not push notifications for:

- normal working progress
- card refreshes
- elapsed-time changes
- routine activity
- successful intermediate tool calls

### Notify

Notify when:

- Claude needs permission
- Claude needs an answer
- the turn fails
- another condition requires human attention

Completion notifications should follow the user's notification preference.

A completed turn may update its Session Card without producing additional noise when completion notifications are disabled.

---

## Interaction and trust

Seeing a card and controlling the local Claude session are different privileges.

Only an authorized Wirelark user should be able to:

- send instructions to a session
- answer Claude questions
- approve or deny permissions
- grant session-wide permission
- interrupt a running turn

This is especially important if the bot or card ever appears in a group chat.

An unauthorized interaction should never control the local machine.

The default personal experience should remain effectively:

> My Feishu identity controls my Claude sessions.

---

## Chat as the dashboard

Wirelark does not need a permanent Overview Card.

Each active Claude session has its own Session Card, and the Feishu conversation naturally becomes the activity surface.

The existing `sessions` action remains useful as navigation when the user wants to find or switch between active sessions, but it should not become another continuously maintained dashboard.

The user should be able to move naturally between:

```text
Session Card
    ↓
Permission / Question
    ↓
Session Card
    ↓
send follow-up
    ↓
Session Card
```

without entering a separate Wirelark interface.

---

## Design principle

The Feishu experience should feel less like watching an agent operate and more like checking in with a colleague.

Most of the time:

```text
🟢 Working
```

Occasionally:

```text
🟠 I need you
```

Finally:

```text
✅ Done
```

Everything else is detail.

Wirelark should expose only enough of that detail to make the user confident that the local Claude Code session is alive, progressing, and still under their control.