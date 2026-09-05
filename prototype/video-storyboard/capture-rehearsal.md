# Real capture rehearsal

## Before recording

- Use a clean terminal window at 1280x720 or larger.
- Set a readable monospace font around 18–20 px.
- Hide unrelated tabs, paths, account names, notifications, and secrets.
- Confirm `claude` and `claude-companion` are both on `PATH`.
- Confirm the Companion daemon and Feishu owner account are already configured.
- Keep the Feishu phone or desktop client ready on the bot conversation.

This environment currently has Claude Code 2.1.259, but does not currently find
`claude-companion` on `PATH`. Install or link the repository build before the
first integrated rehearsal.

## Prepare one take

From the repository root:

```sh
prototype/video-storyboard/prepare-take.sh
```

This restores the intentional bug and verifies that only the expired-token
case fails. It prints the exact launch command and opening prompt.

## Record the terminal

From `prototype/video-storyboard/demo-task`, start the real harness:

```sh
claude --dangerously-load-development-channels server:claude-companion
```

Paste this prompt without editing it:

```text
Fix the failing expired-token test. Change only the validator, then run exactly go test ./... once. Don't run any other checks.
```

For the keeper take, capture the shell command as its own short shot. After
accepting Claude Code's development-channel warning, wait for startup to
settle and press `Ctrl+L`. Start the continuous TUI shot at the clean prompt.
This keeps the real harness while excluding unrelated startup-hook noise.

Expected sequence:

1. Claude reads `token.go` and `token_test.go`.
2. Claude changes the expired-token branch to return `ErrExpired`.
3. Claude requests permission for exactly `go test ./...`.
4. Tap **Allow once** in Feishu.
5. The tests pass and the completion card arrives.
6. Send this from Feishu:

   ```text
   Give me a three-line issue update: root cause, fix, and verification.
   ```

7. Capture the reply arriving in the same Claude Code terminal.

Do not stop recording during waits. Editing will remove the dead time while
preserving a truthful continuous take.

## Capture framing

For the final composite, record the terminal by itself. Record Feishu by
itself if practical. Two clean sources give us more control than recording an
already-arranged split screen.

Keep all essential terminal text inside the left 57% safe area. Keep Feishu
card content centered vertically so the permission push-in can be cropped
without losing its title or buttons.

## Replaying a raw rehearsal

The helper capture made from this workspace can be replayed with:

```sh
scriptreplay \
  -T prototype/video-storyboard/captures/terminal.timing \
  -O prototype/video-storyboard/captures/terminal.raw
```

That raw take is diagnostic rather than a keeper: it includes startup noise
and the exploratory first prompt. The keeper should be screen-recorded from a
normal graphical terminal so the native font, colour, and cursor rendering are
preserved.
