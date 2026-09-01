package main

import (
	"bytes"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/marcelritzschke/wirelark/internal/config"
	"github.com/marcelritzschke/wirelark/internal/daemon"
	"github.com/marcelritzschke/wirelark/internal/deliver"
	"github.com/marcelritzschke/wirelark/internal/feishu"
	"github.com/marcelritzschke/wirelark/internal/hook"
	"github.com/marcelritzschke/wirelark/internal/ipc"
	"github.com/marcelritzschke/wirelark/internal/transcript"
)

// maxPayloadBytes bounds stdin so a runaway hook payload cannot OOM the
// process. Claude Code payloads are a few KB at most.
const maxPayloadBytes = 1 << 20

// handoffTimeout bounds the whole exchange with the daemon. A hook runs
// inside the Claude Code session, so it gives up quickly and does the work
// itself rather than making the session wait on a daemon in trouble.
const handoffTimeout = 3 * time.Second

// daemonProbeTimeout bounds asking whether a daemon is there.
const daemonProbeTimeout = 2 * time.Second

// runSend always returns 0: it runs inside Claude Code hooks and must never
// disturb the session, whatever happens.
func runSend(args []string) int {
	dryRun := false
	for _, a := range args {
		if a == "--dry-run" || a == "-dry-run" {
			dryRun = true
		}
	}

	raw, err := io.ReadAll(io.LimitReader(os.Stdin, maxPayloadBytes))
	if err != nil {
		debugLog("read payload: %v", err)
		return 0
	}
	p, err := hook.Decode(bytes.NewReader(raw))
	if err != nil {
		debugLog("decode payload: %v", err)
		return 0
	}
	if p.Subagent() {
		debugLog("skip subagent event %s", p.HookEventName)
		return 0
	}
	if !p.Handled() {
		debugLog("skip unhandled event %s", p.HookEventName)
		return 0
	}
	debugLog("event %s from %s", p.HookEventName, p.ProjectLabel())

	cfg, err := config.Load()
	if err != nil {
		if !dryRun {
			debugLog("load config: %v", err)
			return 0
		}
		// Dry-run needs no credentials; exercise card building with defaults.
		cfg = &config.Config{Notify: config.NotifyImportant, Detail: config.DetailNormal}
	}

	// The daemon is the one role that can see more than this single moment,
	// so it gets the event whenever it can be reached. Everything below is
	// the fallback: a stopped daemon costs remote continuation, never a
	// notification.
	if !dryRun && cfg.RemoteEnabled() && handOff(raw) {
		return 0
	}

	var client *feishu.Client
	if !dryRun {
		client, err = feishu.New(cfg)
		if err != nil {
			debugLog("build client: %v", err)
			return 0
		}
	}

	// A failure to read the transcript degrades to an empty turn
	// (project-only context) rather than dropping the notification.
	turn := transcript.Load(p.TranscriptPath, p.PromptID)
	(&deliver.Deliverer{Payload: p, Sender: senderOrNil(client), DryRun: dryRun}).Event(turn, cfg)
	return 0
}

// handOff gives the event to the daemon and reports whether it took it. The
// payload travels untouched, along with the two facts only a hook process
// can see: which project directory Claude Code named, and which claude
// process this session is.
func handOff(raw []byte) bool {
	if err := daemon.EnsureRunning(); err != nil {
		debugLog("no daemon: %v", err)
		return false
	}
	pid, _ := strconv.Atoi(os.Getenv("CLAUDE_PID"))
	env, err := ipc.Request(ipc.TypeHook, ipc.Hook{
		Payload:    raw,
		ProjectDir: os.Getenv("CLAUDE_PROJECT_DIR"),
		PID:        pid,
	}, handoffTimeout)
	if err != nil {
		debugLog("hand off to daemon: %v", err)
		return false
	}
	var ack ipc.Ack
	if err := env.Into(&ack); err != nil || !ack.OK {
		debugLog("daemon declined the event: %v %s", err, ack.Err)
		return false
	}
	return true
}

// senderOrNil keeps a nil *feishu.Client from becoming a non-nil
// deliver.Sender holding a nil pointer, which would panic on the first send
// instead of staying quiet.
func senderOrNil(c *feishu.Client) deliver.Sender {
	if c == nil {
		return nil
	}
	return c
}
