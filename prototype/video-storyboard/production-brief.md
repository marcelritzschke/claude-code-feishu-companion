# Claude Code Feishu Companion — 22-second demo

## Purpose

Show one complete promise: start in the real Claude Code terminal, leave the
desk, make a meaningful decision and continue from Feishu, then return to the
same terminal session.

## Selected cut: A+ — Cinematic parallel bridge

Both surfaces remain visible from the moment Feishu enters until the return to
the terminal. Attention moves without breaking continuity:

```text
terminal emphasis -> balanced split -> Feishu emphasis -> balanced split -> terminal emphasis
```

Use a gentle 4–6% scale change, a small exposure shift, and eased lateral
movement. Do not use hard zooms, whip transitions, or a full-screen phone.

| Time | Picture | Exact content |
| --- | --- | --- |
| 0:00–0:02 | Real terminal, full frame | `$ claude --dangerously-load-development-channels server:claude-companion` |
| 0:02–0:04 | Real Claude Code harness | `> Fix the failing expired-token test. Change only the validator, then run exactly go test ./... once. Don't run any other checks.` |
| 0:04–0:07 | Terminal shifts left; Feishu phone enters right | `🟢 Working · 8s` / `refresh-token-demo` / `Checking refresh-token validation and the failing tests.` |
| 0:07–0:11 | Phone takes emphasis; terminal remains visible behind it | `⚠️ Permission requested` / `Claude wants to run:` / `go test ./...` / buttons: `Allow once`, `Deny` |
| 0:11–0:14 | Permission card leaves; session card updates | `✅ Completed · 42s` / `Fixed expired-token validation.` / `✓ go test ./... passed` |
| 0:14–0:18 | Feishu conversation | User sends: `Give me a three-line issue update: root cause, fix, and verification.` Confirmation: `Sent to refresh-token-demo.` |
| 0:18–0:22 | Return to the real terminal; same Claude Code session | Claude's three-line issue update appears in the same terminal. End line: `Leave the desk. Keep the session.` |

### Motion treatment

| Beat | Terminal | Feishu |
| --- | --- | --- |
| Launch and prompt | Full emphasis | Not yet visible |
| Working | 57% width, fully legible | 43% width, enters softly |
| Permission | 47% width, dimmed but legible | 53% width, subtle push-in |
| Completion and follow-up | 52% width | 48% width |
| Return | Expands to full frame | Slides away only after delivery is established |

## Accuracy constraints

- The terminal footage is a real Claude Code session, not a recreated shell.
- Show the Channels preview flag in full; do not hide it behind an alias.
- The permission decision is `Allow once` or `Deny`; do not show a fictional
  session-wide approval button.
- The detailed Feishu message is a follow-up sent to the selected session. It
  is not presented as an answer to `AskUserQuestion`.
- The permission card may be visually composited, but its title, action,
  command, and buttons should match the actual product.
- Use **Claude Code Feishu Companion** throughout. Do not use Wirelark.

## Capture plan

1. Reset the demo task to its intentionally failing state.
2. Start the real harness from `prototype/video-storyboard/demo-task`.
3. Accept the development-channel warning, wait for startup to settle, and
   press `Ctrl+L` once. Capture the clean TUI from this point separately from
   the shell-command shot.
4. Paste the exact opening prompt from the table.
5. Capture the entire terminal session at normal speed.
6. Capture the short explanatory follow-up using the exact Feishu message.
7. Edit out waiting time; never accelerate typing or cursor motion so much
   that it looks synthetic.
8. Composite the Feishu cards using the real captured cards when available;
   otherwise use the faithful storyboard treatment for the first animatic.

## Export targets

- Primary: 1920x1080 MP4, 22 seconds, 30 fps.
- Social crop: 1080x1080 MP4 with the same center-safe composition.
- Documentation: 960px-wide looping GIF, compressed after the MP4 is locked.
