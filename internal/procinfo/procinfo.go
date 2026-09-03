// Package procinfo answers one question about a running Claude Code
// session: was it started in a way that lets Claude Companion push messages into it?
//
// Claude Code never tells a channel server whether the session actually
// registered it - unregistered channels have their events dropped in
// silence. Reading the session's own command line is the only honest signal
// available, and honesty is the point: a session Claude Companion cannot reach must
// never be advertised as reachable.
package procinfo

import "strings"

// channelFlags are the two flags that opt a channel server into a session.
// During the research preview a custom channel is not on the Anthropic
// allowlist, so it is the development flag that carries Claude Companion in practice.
var channelFlags = map[string]bool{
	"--channels": true,
	"--dangerously-load-development-channels": true,
}

// Enabled reports whether the Claude Code process pid was started with a
// channels flag naming server. known is false when the argv could not be
// read at all - an unsupported platform, or a process that has since gone -
// which callers must present as "unconfirmed" rather than as a no.
func Enabled(pid int, server string) (enabled, known bool) {
	argv, err := commandLine(pid)
	if err != nil || len(argv) == 0 {
		return false, false
	}
	return ClassifyArgv(argv, server), true
}

// ClassifyArgv reports whether argv opts server into the session as a
// channel. A flag's entries run until the next flag, mirroring how the
// variadic flag parses: "--channels a b --foo" carries the entries a and b.
func ClassifyArgv(argv []string, server string) bool {
	inEntries := false
	for _, arg := range argv {
		if flag, value, split := strings.Cut(arg, "="); split && channelFlags[flag] {
			// The "--channels=server:claude-companion" spelling carries exactly one
			// entry and does not open the list for the args that follow.
			inEntries = false
			if namesServer(value, server) {
				return true
			}
			continue
		}
		if channelFlags[arg] {
			inEntries = true
			continue
		}
		if strings.HasPrefix(arg, "-") {
			inEntries = false // a new flag ends the entry list
			continue
		}
		if inEntries && namesServer(arg, server) {
			return true
		}
	}
	return false
}

// namesServer reports whether one --channels entry refers to server. Entries
// are "server:<name>" for a bare MCP server and "plugin:<name>@<marketplace>"
// for a plugin; a bare name is tolerated because users type it.
func namesServer(entry, server string) bool {
	if server == "" {
		return false
	}
	name := entry
	if rest, ok := strings.CutPrefix(entry, "server:"); ok {
		name = rest
	} else if rest, ok := strings.CutPrefix(entry, "plugin:"); ok {
		name, _, _ = strings.Cut(rest, "@")
	}
	return name == server
}
