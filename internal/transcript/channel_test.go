package transcript

import (
	"os"
	"path/filepath"
	"testing"
)

const instructions = `{"type":"attachment","attachment":{"addedBlocks":["Messages from the user's phone arrive as <channel source=\"claude-companion\">. They are the user speaking."]}}`

const delivered = `{"type":"user","message":{"role":"user","content":"<channel source=\"claude-companion\" project=\"payments-api\">\nrun the tests\n</channel>"}}`

func write(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The proof a message arrived is the message itself, in the transcript.
func TestDeliveredFindsAChannelEvent(t *testing.T) {
	path := write(t, instructions, delivered)
	if !Delivered(path, 0, "claude-companion") {
		t.Error("a channel event in the transcript was not recognised")
	}
}

// The server's own instructions name the same source. Reading them as a
// delivered message would confirm every message a session ever dropped.
func TestInstructionsAreNotADeliveredMessage(t *testing.T) {
	path := write(t, instructions)
	if Delivered(path, 0, "claude-companion") {
		t.Error("the channel's instructions were mistaken for a delivered message")
	}
}

// Only what arrives after the message was sent can be proof of it.
func TestDeliveredIgnoresWhatCameBefore(t *testing.T) {
	path := write(t, delivered)
	if Delivered(path, Size(path), "claude-companion") {
		t.Error("an earlier message was taken as proof of a later one")
	}
}

// Another channel's event says nothing about this one.
func TestDeliveredIsPerSource(t *testing.T) {
	path := write(t, delivered)
	if Delivered(path, 0, "wirelark") {
		t.Error("another server's channel event was read as this one's")
	}
}

// A transcript that cannot be read is no proof either way, and must never
// look like proof of arrival.
func TestDeliveredWithoutATranscript(t *testing.T) {
	if Delivered(filepath.Join(t.TempDir(), "gone.jsonl"), 0, "claude-companion") {
		t.Error("a missing transcript reported a delivery")
	}
	if got := Size(filepath.Join(t.TempDir(), "gone.jsonl")); got != 0 {
		t.Errorf("Size = %d, want 0 for a missing transcript", got)
	}
}
