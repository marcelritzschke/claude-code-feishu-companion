// Package config loads and saves the Claude Companion configuration. The settings
// are behavioral, not technical: what to notify about and how much detail
// completions carry.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	"github.com/pelletier/go-toml/v2"

	"github.com/marcelritzschke/claude-code-feishu-companion/internal/secfile"
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

// Switch is a plain on/off setting. It is spelled out rather than left a
// bool because a missing value in a config file is indistinguishable from
// false, and the two remote-continuation settings default to on.
type Switch string

const (
	// On is the default for both remote-continuation settings.
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

// Brand names which Feishu environment the app lives in. The two are
// separate deployments with separate hostnames, and an app created in one
// does not exist in the other, so every call - REST and WebSocket alike -
// has to be aimed at the right one.
type Brand string

const (
	// BrandFeishu is the mainland Chinese deployment, open.feishu.cn. It
	// is the default, so a config written before this field existed keeps
	// working.
	BrandFeishu Brand = "feishu"
	// BrandLark is the international deployment, open.larksuite.com.
	BrandLark Brand = "lark"
)

// OpenBaseURL is the open-platform host for the configured brand. The SDK
// derives its OAuth host from this one, so it is the only address that has
// to be configured.
func (c *Config) OpenBaseURL() string {
	if c.Brand == BrandLark {
		return lark.LarkBaseUrl
	}
	return lark.FeishuBaseUrl
}

// EnvVar lets tests (and users) point the binary at another config file.
const EnvVar = "CLAUDE_COMPANION_CONFIG"

// Config is the on-disk configuration.
type Config struct {
	AppID      string     `toml:"app_id"`
	AppSecret  string     `toml:"app_secret"`
	OpenID     string     `toml:"open_id"`
	OpenIDKind OpenIDType `toml:"open_id_type"`
	// Brand is which Feishu deployment the app belongs to. Setup records
	// what the registration reported rather than asking, and an empty
	// value means the Feishu default.
	Brand  Brand       `toml:"brand"`
	Notify NotifyLevel `toml:"notify"`

	// Remote is whether a session may be continued from Feishu at all.
	// With it off, Claude Companion is exactly the attention-mode one-way notifier.
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

// Path returns the config file path: $CLAUDE_COMPANION_CONFIG if set,
// otherwise <user config dir>/claude-companion/config.toml.
func Path() (string, error) {
	if p := os.Getenv(EnvVar); p != "" {
		return p, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config dir: %w", err)
	}
	return filepath.Join(dir, appDir, "config.toml"), nil
}

// appDir is the directory this program keeps its configuration in.
const appDir = "claude-companion"

// Stamp reports when the config file was last written. A running daemon
// holds the credentials it read at startup, so this is what tells a caller
// whether the daemon it is talking to predates the configuration on disk.
// A config file that is not there yet has no stamp, and no error either:
// the caller's question is whether the daemon is behind, and it cannot be
// behind a file that does not exist.
func Stamp() (time.Time, error) {
	p, err := Path()
	if err != nil {
		return time.Time{}, err
	}
	info, err := os.Stat(p)
	if errors.Is(err, os.ErrNotExist) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("stat config: %w", err)
	}
	return info.ModTime(), nil
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
	if c.Brand != BrandFeishu && c.Brand != BrandLark {
		c.Brand = BrandFeishu
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
