// Package hooksreg registers Claude Companion's command hooks in a Claude
// Code settings.json, merging with (never replacing) existing entries and
// removing registrations left by older or relocated Claude Companion
// installs.
package hooksreg

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/marcelritzschke/claude-code-feishu-companion/internal/secfile"
)

// registration pairs a hook event with the matcher Claude Companion needs.
// Empty matcher means every occurrence of the event.
type registration struct {
	event   string
	matcher string
}

// Settings are the parts of the user's configuration that change which
// hooks Claude Companion needs.
type Settings struct {
	// Progress adds the checkpoints long-running progress updates are
	// built from.
	Progress bool
	// Remote adds the lifecycle events the Feishu session overview is made
	// of. They say nothing on their own, so without remote continuation
	// they would only spawn processes with nothing to report.
	Remote bool
}

// registrationsFor lists the hooks Claude Companion needs under the given settings:
//   - PermissionRequest: Claude is blocked on a permission decision
//   - PreToolUse (AskUserQuestion): Claude is blocked on a question
//   - Stop: the turn finished
//   - StopFailure: the turn ended on an API error
//   - PostToolUse: checkpoints for long-running progress updates, and only
//     when those are switched on - at the default level it would spawn a
//     Claude Companion process per tool call with nothing to say.
//   - SessionStart, SessionEnd, UserPromptSubmit: which sessions exist and
//     what each is doing, for the Feishu session overview.
func registrationsFor(s Settings) []registration {
	regs := []registration{
		{event: "PermissionRequest"},
		{event: "PreToolUse", matcher: "AskUserQuestion"},
		{event: "Stop"},
		{event: "StopFailure"},
	}
	if s.Progress {
		regs = append(regs, registration{event: "PostToolUse"})
	}
	if s.Remote {
		regs = append(regs,
			registration{event: "SessionStart"},
			registration{event: "SessionEnd"},
			registration{event: "UserPromptSubmit"})
	}
	return regs
}

// managedEvents is every event Claude Companion registers on now or has
// registered in the past. A Claude Companion hook found on one of these
// that the current settings do not call for is removed, so re-running init
// after moving the binary, or after turning progress off, leaves exactly
// one correct set of hooks behind.
var managedEvents = []string{
	"PermissionRequest", "PreToolUse", "PostToolUse", "Stop", "StopFailure",
	"Notification", "SessionStart", "SessionEnd", "UserPromptSubmit",
}

// companionCommand matches a hook command that runs a Claude Companion
// binary, whichever path it was installed at, under either the current
// binary name or the project's former name (wirelark), so an upgrade from
// an older install still cleans up after its predecessor instead of
// double-registering.
var companionCommand = regexp.MustCompile(`(?i)(?:^|[/\\"'])(?:claude-companion|wirelark)(?:\.exe)?["']?\s+send\b`)

// SettingsPath returns the user-level Claude Code settings.json path.
func SettingsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".claude", "settings.json"), nil
}

// Register merges the hook entries invoking command into settingsPath and
// removes every other Claude Companion entry, including those left by an
// older or relocated install and those for events the current settings no
// longer use. It is idempotent and preserves all non-Claude-Companion
// content. Returns whether anything changed. A backup of the previous file
// is written next to it before modifying.
func Register(settingsPath, command string, opts Settings) (bool, error) {
	raw, err := os.ReadFile(settingsPath)
	var settings map[string]any
	if err == nil {
		if err := json.Unmarshal(raw, &settings); err != nil {
			return false, fmt.Errorf("parse %s: %w", settingsPath, err)
		}
	} else if os.IsNotExist(err) {
		settings = map[string]any{}
	} else {
		return false, err
	}

	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
		settings["hooks"] = hooks
	}

	regs := registrationsFor(opts)
	wanted := map[string]bool{}
	for _, reg := range regs {
		wanted[reg.event] = true
	}

	changed := false
	for _, event := range managedEvents {
		keep := ""
		if wanted[event] {
			keep = command // this event's current registration survives
		}
		if pruneCompanion(hooks, event, keep) {
			changed = true
		}
	}
	for _, reg := range regs {
		groups, _ := hooks[reg.event].([]any)
		if !containsCommand(groups, command) {
			hooks[reg.event] = append(groups, newGroup(reg, command))
			changed = true
		}
	}
	if !changed {
		return false, nil
	}

	if len(raw) > 0 {
		backup := fmt.Sprintf("%s.bak.%d", settingsPath, time.Now().Unix())
		if err := os.WriteFile(backup, raw, 0o600); err != nil {
			return false, fmt.Errorf("write backup: %w", err)
		}
	}

	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return false, err
	}
	out = append(out, '\n')
	// Atomic: a crash mid-write must not leave the user with a truncated
	// settings.json and no working Claude Code.
	if err := secfile.WriteAtomic(settingsPath, out, 0o600); err != nil {
		return false, fmt.Errorf("write settings: %w", err)
	}
	return true, nil
}

// newGroup builds the matcher group for one registration. All Claude
// Companion hooks are async: they deliver a notification and never hold up
// the session.
func newGroup(reg registration, command string) map[string]any {
	hook := map[string]any{
		"type":    "command",
		"command": command,
		"async":   true,
	}
	group := map[string]any{"hooks": []any{hook}}
	if reg.matcher != "" {
		group["matcher"] = reg.matcher
	}
	return group
}

// containsCommand reports whether any hook in the event's groups already
// runs exactly this command.
func containsCommand(groups []any, command string) bool {
	for _, g := range groups {
		group, ok := g.(map[string]any)
		if !ok {
			continue
		}
		hs, ok := group["hooks"].([]any)
		if !ok {
			continue
		}
		for _, h := range hs {
			hook, ok := h.(map[string]any)
			if !ok {
				continue
			}
			if c, _ := hook["command"].(string); c == command {
				return true
			}
		}
	}
	return false
}

// pruneCompanion removes every Claude Companion hook entry from one event
// except the command named by keep, drops groups left empty, and reports
// whether anything changed. Hooks belonging to other tools are never
// touched.
func pruneCompanion(hooks map[string]any, event, keep string) bool {
	groups, ok := hooks[event].([]any)
	if !ok {
		return false
	}
	var kept []any
	changed := false
	for _, g := range groups {
		group, ok := g.(map[string]any)
		if !ok {
			kept = append(kept, g)
			continue
		}
		hs, _ := group["hooks"].([]any)
		var keptHooks []any
		for _, h := range hs {
			if hook, ok := h.(map[string]any); ok {
				c, _ := hook["command"].(string)
				if c != keep && companionCommand.MatchString(c) {
					changed = true
					continue
				}
			}
			keptHooks = append(keptHooks, h)
		}
		if len(keptHooks) == 0 {
			changed = true // drop the emptied group entirely
			continue
		}
		group["hooks"] = keptHooks
		kept = append(kept, group)
	}
	if len(kept) == 0 {
		delete(hooks, event)
	} else {
		hooks[event] = kept
	}
	return changed
}
