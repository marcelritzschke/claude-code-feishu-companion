package main

import (
	"testing"
	"time"

	"github.com/marcelritzschke/wirelark/internal/config"
	"github.com/marcelritzschke/wirelark/internal/notify"
	"github.com/marcelritzschke/wirelark/internal/transcript"
)

// The spec's test is meaningful work, not duration: a real task is
// reported however briefly it ran, and only a wordless conversational
// exchange is withheld.
func TestWithholdChatter(t *testing.T) {
	tool := &transcript.ToolCall{Name: "Bash", Input: map[string]any{"command": "go test ./..."}}
	cases := []struct {
		name string
		turn *transcript.Turn
		want bool
	}{
		{
			name: "short turn that did work is reported",
			turn: &transcript.Turn{Start: time.Now().Add(-8 * time.Second), LatestTool: tool},
			want: alwaysNotify,
		},
		{
			name: "long turn that did work is reported",
			turn: &transcript.Turn{Start: time.Now().Add(-10 * time.Minute), LatestTool: tool},
			want: alwaysNotify,
		},
		{
			name: "brief conversational answer is withheld",
			turn: &transcript.Turn{Start: time.Now().Add(-8 * time.Second)},
			want: liveCardOnly,
		},
		{
			name: "long wordless answer is reported anyway",
			turn: &transcript.Turn{Start: time.Now().Add(-walkAwayTime - time.Second)},
			want: alwaysNotify,
		},
		{
			name: "unreadable turn is reported anyway",
			turn: &transcript.Turn{},
			want: alwaysNotify,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := withholdChatter(tc.turn); got != tc.want {
				t.Errorf("withholdChatter = %v, want %v", got, tc.want)
			}
		})
	}
}

// The regression that silenced a real install: a turn that edited files and
// ran tests in under a minute must still reach the phone.
func TestWithholdChatterReportsShortRealWork(t *testing.T) {
	turn := &transcript.Turn{
		Start:      time.Now().Add(-20 * time.Second),
		Files:      []string{"session.go"},
		Tests:      []transcript.TestRun{{Command: "go test ./...", Passed: true}},
		LatestTool: &transcript.ToolCall{Name: "Bash", Input: map[string]any{"command": "go test ./..."}},
	}
	if withholdChatter(turn) != alwaysNotify {
		t.Error("a 20-second turn that changed files and ran tests must be reported")
	}
}

func TestDetailOf(t *testing.T) {
	cases := []struct {
		level config.DetailLevel
		want  notify.Detail
	}{
		{config.DetailNormal, notify.Normal},
		{config.DetailCompact, notify.Compact},
		{"", notify.Normal}, // anything unrecognised falls back to the default
	}
	for _, tc := range cases {
		if got := detailOf(&config.Config{Detail: tc.level}); got != tc.want {
			t.Errorf("detailOf(%q) = %v, want %v", tc.level, got, tc.want)
		}
	}
}
