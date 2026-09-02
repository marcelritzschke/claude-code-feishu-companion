package tui

import (
	"strings"
	"testing"
	"time"
)

func TestCompactDuration(t *testing.T) {
	cases := map[time.Duration]string{
		10 * time.Minute:              "10m00s",
		9*time.Minute + 5*time.Second: "9m05s",
		59 * time.Second:              "59s",
		time.Minute:                   "1m00s",
		time.Duration(0):              "0s",
	}
	for d, want := range cases {
		if got := compactDuration(d); got != want {
			t.Errorf("compactDuration(%v) = %q, want %q", d, got, want)
		}
	}
}

// TestExpiredCodeSaysSo guards the one thing the countdown must never do:
// show a nonsense negative time to someone wondering whether they still
// have time to find their phone.
func TestExpiredCodeSaysSo(t *testing.T) {
	if got := waitLine(time.Now().Add(-time.Second)); !strings.Contains(got, "expired") {
		t.Errorf("waitLine on a dead code = %q, want it to say so", got)
	}
}
