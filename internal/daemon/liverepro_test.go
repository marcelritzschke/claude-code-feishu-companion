package daemon

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/marcelritzschke/claude-code-feishu-companion/internal/config"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/feishu"
)

// TestLiveInjectionReachesARealSession drives the whole remote-continuation
// path against a real Claude Code session: a real channel process, the real
// daemon loop, and a real session started the way the product asks for.
// Only Feishu is stood in for.
//
// It exists because everything this path can get wrong lives outside the
// code - Claude Code's channel registration, the environment a channel is
// spawned with, whether an idle session takes an injected message at all -
// and none of it can be reproduced against a fake.
//
// It is off by default: it spawns Claude Code, costs a turn, and needs a
// Unix pty to hold a session open.
func TestLiveInjectionReachesARealSession(t *testing.T) {
	if os.Getenv("CLAUDE_COMPANION_LIVE_REPRO") != "1" {
		t.Skip("set CLAUDE_COMPANION_LIVE_REPRO=1 to run the live reproduction")
	}
	if runtime.GOOS == "windows" {
		t.Skip("the harness holds a session open with a Unix pty")
	}
	dir := t.TempDir()
	t.Setenv("CLAUDE_COMPANION_STATE_DIR", dir)

	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "claude-companion")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	mcp := filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(mcp, []byte(`{"mcpServers":{"claude-companion":{"command":"`+bin+`","args":["channel"]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	rec := newRecorder()
	d := New(&config.Config{Notify: config.NotifyImportant, Remote: config.On, RemotePermissions: config.On}, rec, nil, "live")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		if err := d.Serve(ctx); err != nil {
			t.Logf("daemon: %v", err)
		}
	}()
	time.Sleep(time.Second)

	fifo := filepath.Join(dir, "in.fifo")
	if out, err := exec.Command("mkfifo", fifo).CombinedOutput(); err != nil {
		t.Fatalf("mkfifo: %v %s", err, out)
	}
	log := filepath.Join(dir, "pty.log")
	runner := filepath.Join(dir, "run.sh")
	channels := "claude-companion"
	if os.Getenv("CLAUDE_COMPANION_LIVE_WRONG_CHANNEL") == "1" {
		// The failure this harness exists to catch: a session that names a
		// channel server it does not have. Claude Code loads ours as an
		// ordinary MCP server, registers no channel, and drops every event
		// it is sent without a word.
		channels = "wirelark"
	}
	script := "#!/bin/sh\nexec 3<>" + fifo + "\ncd " + root +
		"\nexec script -q -f " + log + " -c \"claude --mcp-config " + mcp +
		" --dangerously-load-development-channels server:" + channels + " --model sonnet\" /dev/null < " + fifo + " >/dev/null 2>&1\n"
	if err := os.WriteFile(runner, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	claude := exec.Command(runner)
	claude.Env = append(os.Environ(), "CLAUDE_COMPANION_STATE_DIR="+dir, "CLAUDE_COMPANION_DEBUG=1")
	if err := claude.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		// The temp directory is unique to this run, so it names every
		// process this harness started - the session, its channel, and any
		// daemon the channel started when this test's own died - and no
		// session the user is sitting in. A daemon left behind would hold
		// a second Feishu connection open under the same credentials.
		exec.Command("pkill", "-f", dir).Run()
		claude.Wait()
	}()

	// Confirm the development-channels warning, then wait for the channel.
	time.Sleep(4 * time.Second)
	if f, err := os.OpenFile(fifo, os.O_WRONLY, 0); err == nil {
		f.WriteString("\r")
		f.Close()
	}

	var id string
	for deadline := time.Now().Add(60 * time.Second); time.Now().Before(deadline); {
		for _, s := range d.reg.List() {
			if s.Attached() {
				id = s.ID
			}
		}
		if id != "" {
			break
		}
		time.Sleep(time.Second)
	}
	if id == "" {
		t.Fatalf("no channel ever attached; pty:\n%s", tail(t, log))
	}
	s, _ := d.reg.Get(id)
	t.Logf("channel attached: id=%s pid=%d remote=%s state=%s", s.ID, s.PID, s.Remote, s.State)

	if trace, err := os.ReadFile(filepath.Join(dir, "debug.log")); err == nil {
		t.Logf("companion trace:\n%s", trace)
	}
	d.onMessage(ctx, feishu.Message{Text: "Reply with exactly the single word PONGLIVE and nothing else. Do not use any tools."})
	t.Logf("daemon said: %v", rec.texts)

	answered := false
	for deadline := time.Now().Add(90 * time.Second); time.Now().Before(deadline); {
		if strings.Contains(tail(t, log), "PONGLIVE") {
			answered = true
			break
		}
		if len(rec.texts) > 1 {
			break // the daemon has already given up on it
		}
		time.Sleep(2 * time.Second)
	}
	t.Logf("daemon said: %v", rec.texts)
	if channels != "claude-companion" {
		if answered {
			t.Fatal("a session that registered no channel answered anyway")
		}
		if len(rec.texts) < 2 || !strings.Contains(rec.texts[1], "not delivered") {
			t.Fatalf("answers = %v, want the daemon to report the message as undelivered", rec.texts)
		}
		t.Log("a message into a session with no channel was reported, not assumed")
		return
	}
	if !answered {
		t.Fatalf("the message never reached the session; pty:\n%s", tail(t, log))
	}
	t.Log("the session answered: the message arrived")
}

func tail(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return "(no pty log: " + err.Error() + ")"
	}
	if len(data) > 3000 {
		data = data[len(data)-3000:]
	}
	return string(data)
}
