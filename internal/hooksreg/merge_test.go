package hooksreg

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// fixture mirrors a real user settings.json: an existing unrelated hook
// plus settings Wirelark must not touch.
const fixture = `{
  "model": "sonnet",
  "hooks": {
    "SessionStart": [
      {
        "matcher": "*",
        "hooks": [
          {
            "type": "command",
            "command": "bash '/home/u/.claude/hooks/herdr-agent-state.sh' session",
            "timeout": 10
          }
        ]
      }
    ]
  },
  "statusLine": {
    "type": "command",
    "command": "bash ~/.claude/statusline-command.sh"
  },
  "theme": "dark"
}`

// legacyFixture is a settings.json written by an older Wirelark: the three
// events V1 no longer notifies on.
const legacyFixture = `{
  "hooks": {
    "Notification": [
      {"matcher": "permission_prompt", "hooks": [{"type": "command", "command": "/bin/wirelark send", "timeout": 5, "async": true}]}
    ],
    "Stop": [
      {"hooks": [{"type": "command", "command": "/bin/wirelark send", "timeout": 5, "async": true}]}
    ],
    "SessionStart": [
      {"matcher": "", "hooks": [{"type": "command", "command": "/bin/wirelark send", "timeout": 5, "async": true}]}
    ],
    "SessionEnd": [
      {"matcher": "", "hooks": [{"type": "command", "command": "/bin/wirelark send", "timeout": 5, "async": true}]}
    ]
  }
}`

func writeFixture(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func load(t *testing.T, p string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("settings not valid JSON: %v", err)
	}
	return m
}

func hooksOf(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	hooks, _ := m["hooks"].(map[string]any)
	return hooks
}

func groupCommands(t *testing.T, m map[string]any, event string) []string {
	t.Helper()
	groups, ok := hooksOf(t, m)[event].([]any)
	if !ok {
		return nil
	}
	var cmds []string
	for _, g := range groups {
		for _, h := range g.(map[string]any)["hooks"].([]any) {
			cmds = append(cmds, h.(map[string]any)["command"].(string))
		}
	}
	return cmds
}

func TestRegisterPreservesExisting(t *testing.T) {
	p := writeFixture(t, fixture)
	changed, err := Register(p, "/usr/local/bin/wirelark send", Settings{Progress: true, Remote: true})
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}

	m := load(t, p)
	if m["model"] != "sonnet" || m["theme"] != "dark" {
		t.Error("unrelated top-level keys lost")
	}
	if m["statusLine"] == nil {
		t.Error("statusLine lost")
	}
	// Wirelark registers on SessionStart too, and another tool's hook on
	// the same event is left exactly where it was.
	cmds := groupCommands(t, m, "SessionStart")
	if len(cmds) != 2 {
		t.Fatalf("SessionStart commands = %v, want the existing hook plus Wirelark's", cmds)
	}
	if cmds[0] != "bash '/home/u/.claude/hooks/herdr-agent-state.sh' session" {
		t.Errorf("SessionStart commands = %v, want the existing hook untouched and first", cmds)
	}
	if _, has := hooksOf(t, m)["PreToolUse"]; !has {
		t.Error("PreToolUse registration missing")
	}
}

func TestRegisterWirelarkEvents(t *testing.T) {
	p := writeFixture(t, "{}")
	if _, err := Register(p, "/bin/wirelark send", Settings{Progress: true, Remote: true}); err != nil {
		t.Fatal(err)
	}
	m := load(t, p)
	hooks := hooksOf(t, m)

	for _, want := range []struct {
		event   string
		matcher string
	}{
		{"PermissionRequest", ""},
		{"PreToolUse", "AskUserQuestion"},
		{"PostToolUse", ""},
		{"Stop", ""},
		{"StopFailure", ""},
		{"SessionStart", ""},
		{"SessionEnd", ""},
		{"UserPromptSubmit", ""},
	} {
		groups, ok := hooks[want.event].([]any)
		if !ok || len(groups) != 1 {
			t.Fatalf("event %s groups = %v", want.event, groups)
		}
		g := groups[0].(map[string]any)
		if got, _ := g["matcher"].(string); got != want.matcher {
			t.Errorf("%s matcher = %q, want %q", want.event, got, want.matcher)
		}
		h := g["hooks"].([]any)[0].(map[string]any)
		if h["command"] != "/bin/wirelark send" {
			t.Errorf("%s command = %v", want.event, h["command"])
		}
		if h["async"] != true {
			t.Errorf("%s hook must be async: %v", want.event, h)
		}
	}
	if _, has := hooks["Notification"]; has {
		t.Error("Notification must not be registered")
	}
}

func TestRegisterPrunesLegacyEvents(t *testing.T) {
	p := writeFixture(t, legacyFixture)
	if _, err := Register(p, "/bin/wirelark send", Settings{Progress: true, Remote: true}); err != nil {
		t.Fatal(err)
	}
	m := load(t, p)
	hooks := hooksOf(t, m)
	if _, has := hooks["Notification"]; has {
		t.Errorf("legacy event Notification not pruned: %v", hooks["Notification"])
	}
	// Stop stays, exactly once.
	if cmds := groupCommands(t, m, "Stop"); len(cmds) != 1 {
		t.Errorf("Stop commands = %v", cmds)
	}
}

func TestRegisterIdempotent(t *testing.T) {
	p := writeFixture(t, fixture)
	cmd := "/bin/wirelark send"
	if _, err := Register(p, cmd, Settings{Progress: true, Remote: true}); err != nil {
		t.Fatal(err)
	}
	changed, err := Register(p, cmd, Settings{Progress: true, Remote: true})
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("second Register should be a no-op")
	}
	m := load(t, p)
	for _, event := range []string{"PermissionRequest", "PostToolUse", "Stop", "StopFailure", "PreToolUse"} {
		if cmds := groupCommands(t, m, event); len(cmds) != 1 {
			t.Errorf("%s commands = %v, want exactly one", event, cmds)
		}
	}
}

func TestRegisterCreatesMissingFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	changed, err := Register(p, "/bin/wirelark send", Settings{Progress: true, Remote: true})
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	m := load(t, p)
	hooks := hooksOf(t, m)
	if want := len(registrationsFor(Settings{Progress: true, Remote: true})); len(hooks) != want {
		t.Errorf("registered %d events, want %d", len(hooks), want)
	}
}

func TestRegisterBackupWritten(t *testing.T) {
	p := writeFixture(t, fixture)
	if _, err := Register(p, "/bin/wirelark send", Settings{Progress: true, Remote: true}); err != nil {
		t.Fatal(err)
	}
	matches, _ := filepath.Glob(p + ".bak.*")
	if len(matches) != 1 {
		t.Fatalf("backups = %v, want exactly one", matches)
	}
	bak, _ := os.ReadFile(matches[0])
	if string(bak) != fixture {
		t.Error("backup must contain the pre-register content")
	}
}

func TestRegisterMalformedRejected(t *testing.T) {
	p := writeFixture(t, "{broken")
	if _, err := Register(p, "/bin/wirelark send", Settings{Progress: true, Remote: true}); err == nil {
		t.Error("malformed settings must be rejected")
	}
	// And the file must be untouched.
	data, _ := os.ReadFile(p)
	if string(data) != "{broken" {
		t.Error("malformed file was modified")
	}
}

// relocatedFixture is a settings.json left by a Wirelark that lived at a
// different path, next to an unrelated hook that must survive.
const relocatedFixture = `{
  "hooks": {
    "Stop": [
      {"hooks": [{"type": "command", "command": "\"/old/place/wirelark\" send", "async": true}]},
      {"hooks": [{"type": "command", "command": "/usr/bin/other-tool run"}]}
    ],
    "PermissionRequest": [
      {"hooks": [{"type": "command", "command": "C:\\tools\\wirelark.exe send", "async": true}]}
    ]
  }
}`

func TestRegisterReplacesRelocatedInstall(t *testing.T) {
	p := writeFixture(t, relocatedFixture)
	cmd := `"/new/place/wirelark" send`
	if _, err := Register(p, cmd, Settings{Progress: true, Remote: true}); err != nil {
		t.Fatal(err)
	}
	m := load(t, p)

	// Exactly one Wirelark hook per event, at the new path, and the
	// unrelated hook untouched.
	stop := groupCommands(t, m, "Stop")
	if len(stop) != 2 {
		t.Fatalf("Stop commands = %q", stop)
	}
	var wirelark, other int
	for _, c := range stop {
		switch {
		case c == cmd:
			wirelark++
		case c == "/usr/bin/other-tool run":
			other++
		default:
			t.Errorf("unexpected Stop command %q", c)
		}
	}
	if wirelark != 1 || other != 1 {
		t.Errorf("Stop = %q, want one wirelark and one other-tool hook", stop)
	}
	if cmds := groupCommands(t, m, "PermissionRequest"); len(cmds) != 1 || cmds[0] != cmd {
		t.Errorf("PermissionRequest = %q, want only %q", cmds, cmd)
	}
}

// PostToolUse fires on every tool call. Registering it at the default
// notification level would spawn a Wirelark process per call with nothing
// to say, so it is only registered when progress updates are switched on.
func TestRegisterPostToolUseOnlyWithProgress(t *testing.T) {
	p := writeFixture(t, "{}")
	if _, err := Register(p, "/bin/wirelark send", Settings{Progress: false, Remote: true}); err != nil {
		t.Fatal(err)
	}
	hooks := hooksOf(t, load(t, p))
	if _, has := hooks["PostToolUse"]; has {
		t.Error("PostToolUse must not be registered without progress notifications")
	}
	for _, event := range []string{"PermissionRequest", "PreToolUse", "Stop", "StopFailure"} {
		if _, has := hooks[event]; !has {
			t.Errorf("%s registration missing", event)
		}
	}
}

func TestRegisterTurningProgressOffRemovesPostToolUse(t *testing.T) {
	p := writeFixture(t, "{}")
	cmd := "/bin/wirelark send"
	if _, err := Register(p, cmd, Settings{Progress: true, Remote: true}); err != nil {
		t.Fatal(err)
	}
	if _, has := hooksOf(t, load(t, p))["PostToolUse"]; !has {
		t.Fatal("PostToolUse should be registered with progress on")
	}
	changed, err := Register(p, cmd, Settings{Progress: false, Remote: true})
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("turning progress off must change the settings")
	}
	if _, has := hooksOf(t, load(t, p))["PostToolUse"]; has {
		t.Error("PostToolUse must be removed when progress is turned off")
	}
}

func TestWirelarkCommandRecognition(t *testing.T) {
	yes := []string{
		"/bin/wirelark send",
		`"/home/u/go/bin/wirelark" send`,
		`C:	ools\wirelark.exe send`,
		"wirelark send",
		"/usr/local/bin/wirelark send --dry-run",
	}
	for _, c := range yes {
		if !wirelarkCommand.MatchString(c) {
			t.Errorf("should be recognised as Wirelark: %q", c)
		}
	}
	no := []string{
		"bash '/home/u/.claude/hooks/herdr-agent-state.sh' session",
		"/opt/not-wirelark send",
		"/bin/wirelark init",
		"echo wirelark",
	}
	for _, c := range no {
		if wirelarkCommand.MatchString(c) {
			t.Errorf("should not be recognised as Wirelark: %q", c)
		}
	}
}

// The lifecycle events exist for the Feishu session overview. With remote
// continuation off there is no overview, so they must not be registered -
// and re-running init after switching it off must take them away again.
func TestRemoteOffLeavesNoLifecycleHooks(t *testing.T) {
	p := writeFixture(t, "{}")
	cmd := "/bin/wirelark send"
	if _, err := Register(p, cmd, Settings{Remote: true}); err != nil {
		t.Fatal(err)
	}

	changed, err := Register(p, cmd, Settings{Remote: false})
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	hooks := hooksOf(t, load(t, p))
	for _, event := range []string{"SessionStart", "SessionEnd", "UserPromptSubmit"} {
		if _, has := hooks[event]; has {
			t.Errorf("%s survived switching remote continuation off: %v", event, hooks[event])
		}
	}
	if _, has := hooks["Stop"]; !has {
		t.Error("Stop must stay registered whatever the remote setting is")
	}
}
