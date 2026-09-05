# Video storyboard prototype

Throwaway prototype for one question: **which compact visual structure best
communicates the Claude Code -> Feishu -> same Claude Code session loop?**

It now contains the selected hybrid and its closest comparison:

- **A — Cinematic parallel bridge (selected):** terminal and phone stay visible
  while scale and light move the viewer's attention.
- **B — Cinematic handoff:** focus moves from desktop to phone and back.

Open `storyboard.html` directly, or serve it locally:

```sh
python3 -m http.server 4173 --directory prototype/video-storyboard
```

Then visit <http://localhost:4173/storyboard.html?variant=A>.

Use the bottom switcher or the left/right arrow keys to compare A with B. Use
the play button or click individual beats to walk through a cut. The original
graphic-led variant C was rejected as too frenetic and has been removed.

The terminal shown here is only a timing placeholder. The production video
must replace it with a recording of the real Claude Code harness launched as:

```sh
claude --dangerously-load-development-channels server:claude-companion
```

The Feishu copy follows the current card implementation. The prototype is not
production UI and should not be merged into the product.

When the direction is approved, follow `capture-rehearsal.md`. The
`prepare-take.sh` helper restores and verifies the deterministic failing task
before each real Claude Code take.
