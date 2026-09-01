package notify

import (
	"regexp"
	"strings"
)

// Risk is how much weight a permission decision deserves.
//
// A remote Allow button is a tap on a phone, with none of the pause that
// reading a terminal dialog gives. So the actions that cannot be undone are
// marked, shown in full rather than excerpted, and never offered as the
// easy default.
type Risk int

const (
	// RiskNormal covers the great majority of approvals: installing
	// dependencies, running a build, writing a file in the project.
	RiskNormal Risk = iota
	// RiskHigh is an action that destroys work, escalates privilege, or
	// reaches beyond this machine.
	RiskHigh
)

// destructive matches actions worth stopping to read. It errs toward
// flagging: a needless second look costs a moment, an unnoticed "rm -rf"
// costs the work.
var destructive = regexp.MustCompile(`(?i)` + strings.Join([]string{
	// deleting things
	`\brm\s+-[a-z]*[rf][a-z]*\b`,
	`\bgit\s+clean\b`,
	`\bgit\s+reset\s+--hard\b`,
	`\bgit\s+branch\s+-D\b`,
	`\bfind\b[^|]*-delete\b`,
	`\btruncate\b`,
	// rewriting history or publishing it
	`\bgit\s+push\b[^|]*(--force|-f\b)`,
	`\bnpm\s+publish\b`,
	`\bcargo\s+publish\b`,
	// privilege
	`\bsudo\b`, `\bdoas\b`, `\bsu\s`,
	`\bchmod\s+(-[a-zA-Z]+\s+)*777\b`,
	`\bchown\s+-R\b`,
	// devices and filesystems
	`\bdd\s+if=`, `\bmkfs\b`, `\bfdisk\b`, `>\s*/dev/(sd|nvme|disk)`,
	// running what was just downloaded
	`\b(curl|wget)\b[^|]*\|\s*(sudo\s+)?(ba|z|k)?sh\b`,
	// data stores
	`\bDROP\s+(TABLE|DATABASE|SCHEMA)\b`, `\bTRUNCATE\s+TABLE\b`,
	`\bdb\.dropDatabase\b`, `\bFLUSHALL\b`,
	// infrastructure
	`\bterraform\s+destroy\b`, `\bkubectl\s+delete\b`,
	`\bdocker\s+system\s+prune\b`, `\baws\s+\w+\s+delete-`,
	// credentials
	`\.ssh/(id_|authorized_keys)`, `\.aws/credentials\b`,
}, "|"))

// Classify weighs one permission request. Both fields are model-written
// text Claude Code only partly sanitizes, so they are matched against and
// never executed or trusted.
func Classify(description, inputPreview string) Risk {
	if destructive.MatchString(inputPreview) || destructive.MatchString(description) {
		return RiskHigh
	}
	return RiskNormal
}
