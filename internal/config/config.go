// Package config loads and saves the Wirelark configuration. The settings
// are behavioral, not technical: what to notify about and how much detail
// completions carry.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/marcelritzschke/wirelark/internal/secfile"
)

// NotifyLevel is what the user wants to hear about.
type NotifyLevel string

const (
	// NotifyImportant sends attention, failure, and completion
	// notifications. The default.
	NotifyImportant NotifyLevel = "important"
	// NotifyProgress additionally sends long-running progress updates.
	NotifyProgress NotifyLevel = "important+progress"
)

// DetailLevel is how much a completion notification says.
type DetailLevel string

const (
	// DetailNormal includes validation results and an excerpt of Claude's
	// final answer. The default.
	DetailNormal DetailLevel = "normal"
	// DetailCompact keeps completions to a one-glance summary.
	DetailCompact DetailLevel = "compact"
)

// Switch is a plain on/off setting. It is spelled out rather than left a
// bool because a missing value in a config file is indistinguishable from
// false, and the two V2 settings default to on.
type Switch string

const (
	// On is the default for both V2 settings.
	On Switch = "on"
	// Off disables the setting.
	Off Switch = "off"
)

// Enabled reports whether the switch is on.
func (s Switch) Enabled() bool { return s != Off }

// OpenIDType names which kind of Feishu user identifier OpenID holds. The
// SDK sends to any of them; we record the kind so the right ReceiveIdType
// is used and the user can paste whichever id the admin console hands them.
type OpenIDType string

const (
	OpenIDTypeOpenID  OpenIDType = "open_id"
	OpenIDTypeUserID  OpenIDType = "user_id"
	OpenIDTypeUnionID OpenIDType = "union_id"
)

// DetectOpenIDType picks the id kind from its surface form: an "ou_..." or
// "on_..." prefix is unambiguous, anything else (the short alphanumeric
// user_id form, e.g. "7257282b") is treated as a tenant user_id.
func DetectOpenIDType(s string) OpenIDType {
	switch {
	case strings.HasPrefix(s, "ou_"):
		return OpenIDTypeOpenID
	case strings.HasPrefix(s, "on_"):
		return OpenIDTypeUnionID
	default:
		return OpenIDTypeUserID
	}
}

// EnvVar lets tests (and users) point the binary at another config file.
const EnvVar = "WIRELARK_CONFIG"

// Config is the on-disk configuration.
type Config struct {
	AppID      string      `toml:"app_id"`
	AppSecret  string      `toml:"app_secret"`
	OpenID     string      `toml:"open_id"`
	OpenIDKind OpenIDType  `toml:"open_id_type"`
	Notify     NotifyLevel `toml:"notify"`
	Detail     DetailLevel `toml:"detail"`

	// Remote is whether a session may be continued from Feishu at all.
	// With it off, Wirelark is exactly the V1 one-way notifier.
	Remote Switch `toml:"remote"`
	// RemotePermissions is whether tool approvals may be answered from
	// Feishu. It is separate from Remote because it is a different kind of
	// trust: anyone who can message the bot can approve a command with it,
	// where continuation only lets them ask for work.
	RemotePermissions Switch `toml:"remote_permissions"`
}

// RemoteEnabled reports whether sessions may be continued from Feishu.
func (c *Config) RemoteEnabled() bool { return c.Remote.Enabled() }

// RemotePermissionsEnabled reports whether tool approvals may be answered
// from Feishu. It requires remote continuation: without the return path
// there is nothing to carry a verdict back on.
func (c *Config) RemotePermissionsEnabled() bool {
	return c.RemoteEnabled() && c.RemotePermissions.Enabled()
}

// ProgressEnabled reports whether long-running progress updates are on.
func (c *Config) ProgressEnabled() bool {
	return c.Notify == NotifyProgress
}

// CompactCompletions reports whether completions use the compact layout.
func (c *Config) CompactCompletions() bool {
	return c.Detail == DetailCompact
}

// Path returns the config file path: $WIRELARK_CONFIG if set,
// otherwise <user config dir>/wirelark/config.toml.
func Path() (string, error) {
	if p := os.Getenv(EnvVar); p != "" {
		return p, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config dir: %w", err)
	}
	return filepath.Join(dir, "wirelark", "config.toml"), nil
}

// Load reads the config from Path, applying defaults for unset behavior
// settings.
func Load() (*Config, error) {
	p, err := Path()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var c Config
	if err := toml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", p, err)
	}
	c.applyDefaults()
	return &c, nil
}

// applyDefaults fills in unset behavior settings and falls back to the
// default for any value the user mistyped: an unrecognised level must not
// silently mean something other than what it says.
func (c *Config) applyDefaults() {
	if c.Notify != NotifyImportant && c.Notify != NotifyProgress {
		c.Notify = NotifyImportant
	}
	if c.Detail != DetailNormal && c.Detail != DetailCompact {
		c.Detail = DetailNormal
	}
	if c.Remote != On && c.Remote != Off {
		c.Remote = On
	}
	if c.RemotePermissions != On && c.RemotePermissions != Off {
		c.RemotePermissions = On
	}
	switch c.OpenIDKind {
	case OpenIDTypeOpenID, OpenIDTypeUserID, OpenIDTypeUnionID:
	default:
		c.OpenIDKind = OpenIDTypeOpenID
	}
}

// Save writes the config to Path with restrictive permissions (dir 0700,
// file 0600) since it holds the app secret.
func (c *Config) Save() error {
	c.applyDefaults()
	p, err := Path()
	if err != nil {
		return err
	}
	data, err := toml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := secfile.WriteAtomic(p, data, 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}
