package daemon

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/marcelritzschke/claude-code-feishu-companion/internal/config"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/feishu"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/ipc"
)

// fakeInbound stands in for the Feishu return path, so a test can decide
// what reaches this machine and when.
type fakeInbound struct {
	messages  chan feishu.Message
	actions   chan feishu.CardAction
	strangers chan string
}

func newFakeInbound() *fakeInbound {
	return &fakeInbound{
		messages:  make(chan feishu.Message, 1),
		actions:   make(chan feishu.CardAction, 1),
		strangers: make(chan string, 1),
	}
}

func (f *fakeInbound) Run(ctx context.Context) error     { <-ctx.Done(); return ctx.Err() }
func (f *fakeInbound) Messages() <-chan feishu.Message   { return f.messages }
func (f *fakeInbound) Actions() <-chan feishu.CardAction { return f.actions }
func (f *fakeInbound) Strangers() <-chan string          { return f.strangers }

// serving runs a daemon on a real socket, the way setup meets it: over the
// same request the init flow makes, not by calling into the type.
func serving(t *testing.T, in inbound) *Daemon {
	t.Helper()
	private(t)
	d := New(&config.Config{
		OpenID: "ou_owner",
		Notify: config.NotifyImportant,
		Remote: config.On, RemotePermissions: config.On,
	}, newRecorder(), in, "1.0.0")

	l, err := ipc.Listen()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); l.Close() })
	go d.acceptLoop(ctx, l)
	if in != nil {
		go d.readInbound(ctx)
	}
	return d
}

// probe makes the request setup makes, and hands back what came of it.
func probe(t *testing.T) <-chan ipc.InboundProof {
	t.Helper()
	out := make(chan ipc.InboundProof, 1)
	go func() {
		env, err := ipc.Request(ipc.TypeAwaitInbound, nil, 10*time.Second)
		if err != nil {
			t.Errorf("await_inbound: %v", err)
			close(out)
			return
		}
		var proof ipc.InboundProof
		if err := env.Into(&proof); err != nil {
			t.Errorf("decode proof: %v", err)
			close(out)
			return
		}
		out <- proof
	}()
	return out
}

func waitForProof(t *testing.T, out <-chan ipc.InboundProof) ipc.InboundProof {
	t.Helper()
	select {
	case proof, ok := <-out:
		if !ok {
			t.Fatal("the probe failed")
		}
		return proof
	case <-time.After(10 * time.Second):
		t.Fatal("the probe never answered")
	}
	return ipc.InboundProof{}
}

// waitForWaiter keeps the test from racing the probe: the message must not
// arrive before the daemon is waiting for it.
func waitForWaiter(t *testing.T, d *Daemon) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		d.mu.Lock()
		waiting := len(d.inboundWaiters)
		d.mu.Unlock()
		if waiting > 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("the daemon never started waiting for inbound")
}

func TestReturnPathProofIsAMessageThatArrives(t *testing.T) {
	in := newFakeInbound()
	d := serving(t, in)
	out := probe(t)
	waitForWaiter(t, d)

	in.messages <- feishu.Message{Text: "hello", MessageID: "om_1"}

	proof := waitForProof(t, out)
	if !proof.OK || proof.Stranger {
		t.Fatalf("want proof of a working return path, got %+v", proof)
	}
}

// A message from somebody else proves the same connection works, and it
// must not be reported as silence: the two need opposite advice.
func TestReturnPathTellsAStrangerFromSilence(t *testing.T) {
	in := newFakeInbound()
	d := serving(t, in)
	out := probe(t)
	waitForWaiter(t, d)

	in.strangers <- "ou_somebody_else"

	proof := waitForProof(t, out)
	if proof.OK {
		t.Fatalf("a stranger's message is not proof the setup works: %+v", proof)
	}
	if !proof.Stranger {
		t.Fatalf("want the stranger reported, got %+v", proof)
	}
	for _, want := range []string{"ou_somebody_else", "ou_owner"} {
		if !strings.Contains(proof.Err, want) {
			t.Fatalf("want %q named in %q", want, proof.Err)
		}
	}
}

func TestReturnPathSaysWhenRemoteIsOff(t *testing.T) {
	serving(t, nil)
	proof := waitForProof(t, probe(t))
	if proof.OK || proof.Stranger || proof.Err == "" {
		t.Fatalf("want a plain refusal, got %+v", proof)
	}
}

// A daemon reports the configuration it read, which is how a caller holding
// a newer one knows this daemon predates it.
func TestStatusReportsTheConfigTheDaemonRead(t *testing.T) {
	d := serving(t, nil)
	stamp := time.Now().Add(-time.Hour).Round(time.Second)
	d.configStamp = stamp

	env, err := ipc.Request(ipc.TypeStatus, nil, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var st ipc.Status
	if err := env.Into(&st); err != nil {
		t.Fatal(err)
	}
	if !st.OK {
		t.Fatal("want a daemon that says it is running")
	}
	if !st.ConfigStamp.Equal(stamp) {
		t.Fatalf("want the config stamp %s, got %s", stamp, st.ConfigStamp)
	}
}
