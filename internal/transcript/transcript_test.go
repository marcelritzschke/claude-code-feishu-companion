package transcript

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fixture is one session: an earlier turn, then the current turn with a
// file edit, a passing test, a failing test that was fixed by a rerun, and
// a sidechain (subagent) call that must not count.
const fixture = `{"type":"ai-title","aiTitle":"Old title"}
{"type":"user","promptId":"p-1","timestamp":"2026-08-31T07:00:00.000Z","message":{"role":"user","content":"earlier prompt"}}
{"type":"assistant","timestamp":"2026-08-31T07:00:05.000Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t0","name":"Bash","input":{"command":"go test ./..."}}]}}
{"type":"user","timestamp":"2026-08-31T07:00:06.000Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t0","is_error":true,"content":"FAIL"}]}}
{"type":"ai-title","aiTitle":"Fix token refresh"}
{"type":"user","promptId":"p-2","timestamp":"2026-08-31T07:10:00.000Z","message":{"role":"user","content":"fix the token refresh"}}
{"type":"assistant","timestamp":"2026-08-31T07:10:01.000Z","message":{"role":"assistant","content":[{"type":"text","text":"on it"}]}}
{"type":"assistant","isSidechain":true,"timestamp":"2026-08-31T07:10:02.000Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t-side","name":"Edit","input":{"file_path":"/x/sidechain.go"}}]}}
{"type":"assistant","timestamp":"2026-08-31T07:10:03.000Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Edit","input":{"file_path":"/home/u/repo/auth/session.go"}}]}}
{"type":"user","timestamp":"2026-08-31T07:10:04.000Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","is_error":false,"content":"ok"}]}}
{"type":"assistant","timestamp":"2026-08-31T07:10:05.000Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t2","name":"Bash","input":{"command":"go test ./...","description":"Run tests"}}]}}
{"type":"user","timestamp":"2026-08-31T07:11:00.000Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t2","is_error":false,"content":"ok"}]}}
{"type":"assistant","timestamp":"2026-08-31T07:11:01.000Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t3","name":"Bash","input":{"command":"pytest -q tests/"}}]}}
{"type":"user","timestamp":"2026-08-31T07:11:02.000Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t3","is_error":true,"content":"1 failed"}]}}
{"type":"assistant","timestamp":"2026-08-31T07:11:03.000Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t4","name":"Write","input":{"file_path":"/home/u/repo/auth/token.go"}}]}}
{"type":"user","timestamp":"2026-08-31T07:11:04.000Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t4","is_error":false,"content":"ok"}]}}
{"type":"assistant","timestamp":"2026-08-31T07:11:05.000Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t5","name":"Bash","input":{"command":"pytest -q tests/"}}]}}
{"type":"user","timestamp":"2026-08-31T07:12:00.000Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t5","is_error":false,"content":"ok"}]}}
{"type":"assistant","timestamp":"2026-08-31T07:12:01.000Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t6","name":"Bash","input":{"command":"go build ./..."}}]}}
{"type":"user","timestamp":"2026-08-31T07:12:02.000Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t6","is_error":false,"content":"ok"}]}}
{"type":"type":"mode","mode":"normal"}
`

func writeTranscript(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadCurrentTurn(t *testing.T) {
	turn := Load(writeTranscript(t, fixture), "p-2")

	if want := "Fix token refresh"; turn.Title != want {
		t.Errorf("title = %q, want %q", turn.Title, want)
	}
	start, err := time.Parse(time.RFC3339, "2026-08-31T07:10:00.000Z")
	if err != nil {
		t.Fatal(err)
	}
	if !turn.Start.Equal(start) {
		t.Errorf("start = %v, want %v", turn.Start, start)
	}
	if got := turn.Files; len(got) != 2 || got[0] != "session.go" || got[1] != "token.go" {
		t.Errorf("files = %v", got)
	}
	if got := turn.Tests; len(got) != 2 {
		t.Fatalf("tests = %+v, want go test and pytest", got)
	}
	for _, tc := range []struct {
		cmd    string
		passed bool
	}{
		{"go test ./...", true},
		{"pytest -q tests/", true}, // failed first, latest run passed
	} {
		found := false
		for _, tr := range turn.Tests {
			if tr.Command == tc.cmd {
				found = true
				if tr.Passed != tc.passed {
					t.Errorf("%s passed = %v, want %v", tc.cmd, tr.Passed, tc.passed)
				}
			}
		}
		if !found {
			t.Errorf("test %q missing from %+v", tc.cmd, turn.Tests)
		}
	}
	if turn.LatestTool == nil || turn.LatestTool.Name != "Bash" {
		t.Errorf("latest tool = %+v", turn.LatestTool)
	}
	if cmd, _ := turn.LatestTool.Input["command"].(string); cmd != "go build ./..." {
		t.Errorf("latest tool command = %q", cmd)
	}
}

func TestLoadFallsBackToLastPrompt(t *testing.T) {
	turn := Load(writeTranscript(t, fixture), "")
	if start, _ := time.Parse(time.RFC3339, "2026-08-31T07:10:00.000Z"); !turn.Start.Equal(start) {
		t.Errorf("start = %v, want the last prompt's timestamp", turn.Start)
	}
}

func TestLoadUnknownPromptID(t *testing.T) {
	// A prompt id absent from the transcript (async write lag) falls back
	// to the last prompt rather than reporting nothing.
	turn := Load(writeTranscript(t, fixture), "p-99")
	if turn.Start.IsZero() {
		t.Error("start should fall back to the last prompt")
	}
}

func TestLoadMissingFile(t *testing.T) {
	turn := Load(filepath.Join(t.TempDir(), "nope.jsonl"), "p-1")
	if turn == nil || !turn.Start.IsZero() || turn.Title != "" || len(turn.Files) != 0 {
		t.Errorf("missing transcript should yield an empty turn, got %+v", turn)
	}
}

func TestLoadIgnoresNonTestAndPendingCommands(t *testing.T) {
	fixture := `{"type":"user","promptId":"p-1","timestamp":"2026-08-31T07:00:00.000Z","message":{"role":"user","content":"go"}}
{"type":"assistant","timestamp":"2026-08-31T07:00:01.000Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls -la"}}]}}
{"type":"user","timestamp":"2026-08-31T07:00:02.000Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","is_error":false,"content":""}]}}
{"type":"assistant","timestamp":"2026-08-31T07:00:03.000Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t2","name":"Bash","input":{"command":"go test ./..."}}]}}
`
	turn := Load(writeTranscript(t, fixture), "p-1")
	if len(turn.Tests) != 0 {
		t.Errorf("pending test and non-test commands should not count, got %+v", turn.Tests)
	}
}

func TestLoadEmptyTranscript(t *testing.T) {
	turn := Load(writeTranscript(t, ""), "p-1")
	if !turn.Start.IsZero() || turn.LatestTool != nil {
		t.Errorf("empty transcript should yield an empty turn, got %+v", turn)
	}
}

// failedFixture ends with a validation command that never recovered, plus
// an earlier tool error that Claude worked past.
const failedFixture = `{"type":"user","promptId":"p-1","timestamp":"2026-08-31T07:00:00.000Z","message":{"role":"user","content":"make the tests pass"}}
{"type":"assistant","timestamp":"2026-08-31T07:00:01.000Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"docker compose up -d"}}]}}
{"type":"user","timestamp":"2026-08-31T07:00:02.000Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","is_error":true,"content":"transient hiccup"}]}}
{"type":"assistant","timestamp":"2026-08-31T07:00:03.000Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t2","name":"Bash","input":{"command":"go test ./..."}}]}}
{"type":"user","timestamp":"2026-08-31T07:00:04.000Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t2","is_error":true,"content":[{"type":"text","text":"PostgreSQL connection refused on localhost:5432"}]}]}}
`

// A command that failed and was then fixed is not a failed turn: agents
// routinely hit failing commands on the way to a working result.
func TestLoadRecoveredFailureIsNotAFailedTurn(t *testing.T) {
	turn := Load(writeTranscript(t, fixture), "p-2")
	if turn.Failed {
		t.Errorf("turn with a recovered test failure must not be Failed: %+v", turn.Tests)
	}
	if turn.LastError != "1 failed" {
		t.Errorf("last error = %q, want the recovered failure's text", turn.LastError)
	}
}

func TestLoadFailedTurn(t *testing.T) {
	turn := Load(writeTranscript(t, failedFixture), "p-1")
	if !turn.Failed {
		t.Errorf("validation still failing at the end must mark the turn failed: %+v", turn.Tests)
	}
	// The last error wins, and content blocks decode as readable text.
	if want := "PostgreSQL connection refused on localhost:5432"; turn.LastError != want {
		t.Errorf("last error = %q, want %q", turn.LastError, want)
	}
}

func TestLoadNoErrorsMeansNoLastError(t *testing.T) {
	const clean = `{"type":"user","promptId":"p-1","timestamp":"2026-08-31T07:00:00.000Z","message":{"role":"user","content":"go"}}
{"type":"assistant","timestamp":"2026-08-31T07:00:01.000Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"go test ./..."}}]}}
{"type":"user","timestamp":"2026-08-31T07:00:02.000Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","is_error":false,"content":"ok"}]}}
`
	turn := Load(writeTranscript(t, clean), "p-1")
	if turn.Failed || turn.LastError != "" {
		t.Errorf("clean turn: failed=%v lastError=%q", turn.Failed, turn.LastError)
	}
}
