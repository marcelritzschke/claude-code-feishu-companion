// Package debuglog is Claude Companion's only output channel outside
// Feishu.
//
// Every Claude Companion role must stay invisible to the Claude Code
// session it belongs to: hooks and the channel share the session's stdio,
// so a stray write would land in the user's terminal or corrupt the MCP
// stream. So nothing is ever printed - traces go to a file, and only when
// the user asks for them with CLAUDE_COMPANION_DEBUG=1.
package debuglog

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/marcelritzschke/claude-code-feishu-companion/internal/paths"
)

const envVar = "CLAUDE_COMPANION_DEBUG"

// mu serializes writes: the daemon traces from several goroutines at once.
var mu sync.Mutex

// Printf appends one line to the debug log when CLAUDE_COMPANION_DEBUG=1.
// It never logs secrets: only event names, project labels, and errors.
func Printf(format string, args ...any) {
	if os.Getenv(envVar) != "1" {
		return
	}
	dir, err := paths.Dir()
	if err != nil {
		return
	}
	p := filepath.Join(dir, "debug.log")

	mu.Lock()
	defer mu.Unlock()
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, time.Now().Format(time.RFC3339)+" "+format+"\n", args...)
}
