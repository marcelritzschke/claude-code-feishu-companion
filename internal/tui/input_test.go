package tui

import (
	"testing"
)

// TestCRLF covers the thing that goes wrong everywhere at once if it is
// missed: with the terminal in raw mode, a bare newline drops a line
// without returning to the margin, and the whole of setup walks
// diagonally off the screen.
func TestCRLF(t *testing.T) {
	cases := map[string]string{
		"a\nb":     "a\r\nb",
		"a\r\nb":   "a\r\nb",
		"":         "",
		"\n":       "\r\n",
		"a\n\nb":   "a\r\n\r\nb",
		"no break": "no break",
	}
	for in, want := range cases {
		if got := crlf(in); got != want {
			t.Errorf("crlf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSplitFrameIgnoresTheTrailingNewline(t *testing.T) {
	cases := map[string][]string{
		"a\nb\n": {"a", "b"},
		"a\nb":   {"a", "b"},
		"":       nil,
		"\n":     nil,
		"one\n":  {"one"},
	}
	for in, want := range cases {
		got := splitFrame(in)
		if len(got) != len(want) {
			t.Errorf("splitFrame(%q) = %q, want %q", in, got, want)
			continue
		}
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("splitFrame(%q) = %q, want %q", in, got, want)
				break
			}
		}
	}
}

// Ctrl+c has to reach the waits between the questions, not only the
// questions. Setup sends a card, registers hooks and then waits two
// minutes for Feishu to reach back; with the terminal in raw mode there is
// no signal to interrupt any of them, and the keystroke that would is a
// byte nobody is reading.
func TestCtrlCReleasesAWaitWithNoQuestion(t *testing.T) {
	in := &input{keys: make(chan key, 16), quit: make(chan struct{})}

	select {
	case <-in.quit:
		t.Fatal("the wait was released before anything was pressed")
	default:
	}

	in.abort()
	in.abort() // a second ctrl+c must not close a closed channel

	select {
	case <-in.quit:
	default:
		t.Error("ctrl+c did not release the wait")
	}
}
