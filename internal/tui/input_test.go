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
