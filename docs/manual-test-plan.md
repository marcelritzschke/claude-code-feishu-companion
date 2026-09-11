# Manual test plan

Everything here needs something the unit tests cannot have: a real Feishu
account, a phone, a real install, and a person to look at a card and say
whether it makes sense. Run it before a release, and after changing cards,
message routing, setup, or either installer.

Each step says what to do, what to expect, and what it would catch. A step
that fails is worth writing down in full - what the card said, not just
that it was wrong - because the failures this catches are usually failures
of wording rather than of code.

## Before you start

Build and install:

```sh
go build -o claude-companion .
install -m 755 claude-companion "$(command -v claude-companion)"
```

Then confirm the daemon is the build you just installed:

```sh
claude-companion daemon --status
```

A daemon can outlive the binary it was started from - an install stops it,
and a hook firing in that window starts a fresh one from the file that is
about to be replaced. `--status` says so, and the daemon retires itself
within a minute; wait for it to say plain `claude-companion daemon is
running` before testing anything. **Every "nothing changed" report starts
here.** On Linux the underlying fact is visible directly:

```sh
p=$(pgrep -f "claude-companion daemon")
ls -l /proc/$p/exe          # must not end in "(deleted)"
```

A Claude Code session keeps the `claude-companion channel` process it was
given until it restarts, so **quit and restart any session you plan to
test with**. Sessions left over from before the install are still
registered, and will show up in step 4 as cards you were not expecting.

To go back, keep the binary you replaced and install it over the new one.

## The session card

**1. A card opens by itself.** Start a session and give it real work:

```sh
claude --dangerously-load-development-channels server:claude-companion
```

> read the three biggest files in this repo and summarise each

A 🔵 **Working** card appears within a few seconds, updating in place, with
**Interrupt** and a *Message this session* box. Nothing was tapped or typed
to bring it up, and nothing needs to be to keep it current.

**2. Nothing is a mode.** While the turn runs, send `watch`, `stop
watching`, `unwatch`. Each is delivered into the session as an instruction
and Claude reacts to the word. None of them is a Claude Companion command,
and none opens, closes, or changes a card.

**3. The turn's end is a new message.** Let it finish. The 🔵 card is
**deleted** and ✅ **Completed** arrives as a new message, so the phone
notifies. One turn is one message throughout: never two cards, never a
rewrite the user would not see.

**4. Asking re-posts, and never duplicates.** Start another turn, wait for
the card, scroll up, send `sessions`. The card standing further up is
deleted and an identical one appears at the bottom. One card per session,
where you are looking.

**5. A session between turns.** With nothing running, send `sessions`. The
card shows what the last turn came to, with a reply box and a footer saying
nothing is running. Type in the box: it reaches the terminal.

## A session that cannot be answered

**6. No box, and a reason.** Start a session with plain `claude`, give it a
task, and send `sessions`. Its card is grey, titled **⚪ Working ·
Notifications only**, carries **no reply box and no Interrupt**, and says
in a sentence that the session was started without the Claude Companion
channel and how to start one that was not.

Let the turn finish. The ✅ **Completed** card carries the same sentence and
no box. A missing reply box explains nothing on its own - the card that
has one and the card that does not must be tellable apart by reading, not
by noticing an absence.

Nothing anywhere says "Remote untested".

> On Windows this is the step that used to fail. Claude Code never tells a
> channel whether the session registered it, so the answer comes from the
> session's own command line - procfs on Linux, `ps` on macOS, the
> process's parameter block on Windows. Where that cannot be read, a
> session is "unconfirmed": it keeps its box and the card says the reply
> may not arrive.

## Where a message goes

**7. One session is not a choice.** With a single continuable session
running, type anything in the conversation. It goes there, and the answer
names the session.

**8. Two sessions is a choice, and it is the user's.** Start a second
continuable session and type in the conversation again. The message stays
put. The answer is **one line** saying to reply on the card of the session
you mean - **no cards are posted**, nothing is numbered, and neither
session receives anything.

**9. A number is just a number.** Send `2` with one session running: it
arrives in that session as the text `2`.

## Interrupt

**10. The button.** Start a long turn, tap **Interrupt**, confirm the
dialog. The terminal returns to its prompt, the card settles to ⏹️
**Interrupted**, and the session is still alive - `/status` in the terminal
proves it.

**11. The typed form.** With one turn running, send `interrupt`; it stops.
With two, it stops neither and says to use the card. `interrupt the build
if it hangs` is delivered to the session as an instruction.

> Remote interrupt is macOS and Linux only.

## Permissions

**12. Tapped.** With `remote_permissions = "on"`, have a session ask for
something. The card offers **Allow once** and **Deny**; tapping answers the
prompt in the terminal and the card settles into what was decided.

**13. Typed.** Repeat, and answer with `y <id>` / `n <id>` from the card
instead of tapping. It must work identically - a verdict carries the
request it answers inside itself, so it is the one thing that still works
when card callbacks do not.

**14. Answered in the terminal.** Repeat, and answer in Claude Code. The
card must stop asking rather than sit there waiting for a decision that has
been made.

## Setup

**15. Quitting leaves the terminal usable.** Against a throwaway
configuration, so nothing real is touched:

```sh
CLAUDE_COMPANION_CONFIG=/tmp/probe.toml \
CLAUDE_COMPANION_STATE_DIR=/tmp/probe-state \
claude-companion init
```

Press Ctrl+C at the QR screen: setup says it was cancelled, and `echo
hello` **echoes**. Repeat, and `kill` the process from another terminal
instead - the terminal must still echo. Raw mode outlives the process that
set it, and a shell with no echo and no line editing is the worst thing an
abandoned setup could leave behind. Clean up with `rm -rf /tmp/probe.toml
/tmp/probe-state`.

**16. Both directions are proved.** Run setup through to the end. After the
test card, it asks you to message the bot, and then puts up a card with a
button and asks you to tap it. Both waits accept Ctrl+C and say so. The
probe card is deleted once tapped.

The tap is not decoration: card delivery and card callbacks are separate
subscriptions on a Feishu app, and an app can send perfectly good cards
while every button and reply box on them is inert. To check what a failure
reads like, remove `card.action.trigger` from the app's subscriptions and
run setup again - it must name the callback, and say that notifications
still work and permissions can still be answered by typing.

## Upgrades

**17. A daemon does not outlive its program.** With a daemon running,
rebuild and install over it. `claude-companion daemon --status` says
straight away that it is an older build than the one installed. Wait, touch
nothing, and within a minute it has retired itself and a current one is
serving.

## Windows

The steps above apply, with two exceptions: remote interrupt is
unavailable, and step 6 is the one that can only be proved here.

**18. Installing twice over a running session.** With a Claude Code session
open, run the install line twice:

```powershell
irm https://raw.githubusercontent.com/marcelritzschke/claude-code-feishu-companion/main/install.ps1 | iex
```

Neither run fails. Once nothing holds the old program any more, no
`claude-companion.exe.old*` is left in the install directory. A fixed
sidecar name is what used to make the second install stop with
"claude-companion.exe is in use and could not be replaced" - naming, for
what it is worth, the one file that was not in use.

**19. A session that cannot be answered.** Start a plain `claude` and check
its card, exactly as in step 6. Windows reads the command line out of the
process's own memory; if that ever breaks, this is where it shows.
