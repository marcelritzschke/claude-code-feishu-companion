package ipc

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/marcelritzschke/wirelark/internal/session"
)

// private points every path this package uses at a directory of the test's
// own, so a test never disturbs the user's running daemon.
//
// It uses a short-named MkdirTemp rather than t.TempDir(): t.TempDir()
// nests the full test name under the OS temp dir, and on macOS that
// combination routinely exceeds the ~104-byte length unix domain sockets
// allow for sun_path, so Listen fails with "bind: invalid argument".
func private(t *testing.T) {
	t.Helper()
	dir, err := os.MkdirTemp("", "wl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Setenv("WIRELARK_STATE_DIR", dir)
}

func TestRoundTrip(t *testing.T) {
	private(t)
	l, err := Listen()
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	served := make(chan error, 1)
	go func() {
		c, err := l.Accept()
		if err != nil {
			served <- err
			return
		}
		defer c.Close()
		env, err := c.Read()
		if err != nil {
			served <- err
			return
		}
		var reg Register
		if err := env.Into(&reg); err != nil {
			served <- err
			return
		}
		served <- c.Write(TypeAck, Ack{OK: reg.SessionID == "sess-1"})
	}()

	env, err := Request(TypeRegister, Register{SessionID: "sess-1", PID: 42, Remote: session.Ready}, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-served; err != nil {
		t.Fatal(err)
	}
	if env.Type != TypeAck {
		t.Fatalf("reply type = %q, want %q", env.Type, TypeAck)
	}
	var ack Ack
	if err := env.Into(&ack); err != nil {
		t.Fatal(err)
	}
	if !ack.OK {
		t.Errorf("ack = %+v, want the registration to have arrived intact", ack)
	}
}

// A hook must be able to tell "no daemon" apart from "the daemon refused",
// because only the first means it should deliver the card itself.
func TestDialWithoutDaemon(t *testing.T) {
	private(t)
	if _, err := Dial(time.Second); err == nil {
		t.Fatal("Dial succeeded with no daemon listening")
	}
	if Ping(time.Second) {
		t.Error("Ping reported a daemon that is not running")
	}
}

// A channel's connection ending is how the daemon learns its Claude Code
// session is gone, so the reader must report a clean EOF rather than noise.
func TestReadReportsEOFWhenPeerLeaves(t *testing.T) {
	private(t)
	l, err := Listen()
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	accepted := make(chan *Conn, 1)
	go func() {
		c, err := l.Accept()
		if err != nil {
			close(accepted)
			return
		}
		accepted <- c
	}()

	client, err := Dial(5 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Write(TypeStatus, nil); err != nil {
		t.Fatal(err)
	}
	server := <-accepted
	if server == nil {
		t.Fatal("no connection was accepted")
	}
	defer server.Close()
	if _, err := server.Read(); err != nil {
		t.Fatal(err)
	}

	client.Close()
	if _, err := server.Read(); !errors.Is(err, io.EOF) {
		t.Errorf("Read after the peer left = %v, want io.EOF", err)
	}
}

func TestEnvelopeIntoRejectsEmptyBody(t *testing.T) {
	err := Envelope{Type: TypeInject}.Into(&Inject{})
	if err == nil || !strings.Contains(err.Error(), TypeInject) {
		t.Errorf("Into on a bodiless envelope = %v, want an error naming the type", err)
	}
}

// The hook forwards the payload untouched, so whatever Claude Code wrote
// must survive the trip byte for byte.
func TestHookPayloadSurvivesUnchanged(t *testing.T) {
	raw := json.RawMessage(`{"hook_event_name":"Stop","tool_input":{"command":"go test ./..."}}`)
	line, err := json.Marshal(Envelope{Type: TypeHook, Body: mustJSON(t, Hook{Payload: raw, PID: 7})})
	if err != nil {
		t.Fatal(err)
	}
	var env Envelope
	if err := json.Unmarshal(line, &env); err != nil {
		t.Fatal(err)
	}
	var got Hook
	if err := env.Into(&got); err != nil {
		t.Fatal(err)
	}
	if string(got.Payload) != string(raw) {
		t.Errorf("payload = %s, want %s", got.Payload, raw)
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// A hook payload can be far larger than the read buffer - a long final
// answer, a big tool input - and losing one to a buffer size would silence
// exactly the notification most worth having.
func TestLargeMessageSurvives(t *testing.T) {
	private(t)
	l, err := Listen()
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	body := strings.Repeat("x", 200*1024)
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		env, rerr := c.Read()
		if rerr != nil {
			c.Write(TypeAck, Ack{Err: rerr.Error()}) //nolint:errcheck
			return
		}
		var h Hook
		if err := env.Into(&h); err != nil {
			c.Write(TypeAck, Ack{Err: err.Error()}) //nolint:errcheck
			return
		}
		var payload struct {
			Message string `json:"last_assistant_message"`
		}
		json.Unmarshal(h.Payload, &payload)                //nolint:errcheck
		c.Write(TypeAck, Ack{OK: payload.Message == body}) //nolint:errcheck
	}()

	raw, err := json.Marshal(map[string]string{"last_assistant_message": body})
	if err != nil {
		t.Fatal(err)
	}
	env, err := Request(TypeHook, Hook{Payload: raw}, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var ack Ack
	if err := env.Into(&ack); err != nil {
		t.Fatal(err)
	}
	if !ack.OK {
		t.Errorf("a %d byte payload did not survive: %s", len(body), ack.Err)
	}
}
