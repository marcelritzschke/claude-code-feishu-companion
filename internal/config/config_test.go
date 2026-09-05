package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvVar, filepath.Join(dir, "config.toml"))

	c := &Config{AppID: "cli_test", AppSecret: "sec", OpenID: "ou_123",
		Notify: NotifyProgress}
	if err := c.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	info, err := os.Stat(filepath.Join(dir, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	// Windows has no POSIX permission bits to assert on: os.Chmod there
	// only toggles the read-only attribute.
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("config perm = %v, want 0600", info.Mode().Perm())
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if *got != *c {
		t.Errorf("round trip: got %+v want %+v", got, c)
	}
	if !got.ProgressEnabled() {
		t.Errorf("behavior flags: progress=%v", got.ProgressEnabled())
	}
}

func TestLoadAppliesDefaults(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(p, []byte("app_id = 'a'\napp_secret = 's'\nopen_id = 'o'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvVar, p)

	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Notify != NotifyImportant {
		t.Errorf("notify default = %q", got.Notify)
	}
	if got.ProgressEnabled() {
		t.Error("defaults should be quiet and detailed")
	}
}

func TestLoadMissing(t *testing.T) {
	t.Setenv(EnvVar, filepath.Join(t.TempDir(), "nope.toml"))
	if _, err := Load(); err == nil {
		t.Error("expected error for missing config")
	}
}

func TestLoadMalformed(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(p, []byte("app_id = [broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvVar, p)
	if _, err := Load(); err == nil {
		t.Error("expected error for malformed config")
	}
}

func TestBrandChoosesTheDeployment(t *testing.T) {
	feishu := (&Config{Brand: BrandFeishu}).OpenBaseURL()
	lark := (&Config{Brand: BrandLark}).OpenBaseURL()
	if feishu == lark {
		t.Fatalf("both brands resolve to %s; an app exists in one deployment only", feishu)
	}
	// A config written before the field existed must keep reaching the
	// deployment it was set up against.
	if got := (&Config{}).OpenBaseURL(); got != feishu {
		t.Errorf("unset brand resolves to %s, want the Feishu default %s", got, feishu)
	}
}

func TestLoadDefaultsUnknownBrand(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	t.Setenv(EnvVar, path)
	if err := os.WriteFile(path, []byte("app_id = \"cli_x\"\nbrand = \"slack\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Brand != BrandFeishu {
		t.Errorf("brand = %q, want the default %q", got.Brand, BrandFeishu)
	}
}

// configHome points os.UserConfigDir at a temporary directory, so a test
// can exercise the real default paths rather than an override. It reports
// the directory os.UserConfigDir will return, and skips the test on a
// platform where the environment does not steer it.
func configHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	t.Setenv("AppData", dir)
	got, err := os.UserConfigDir()
	if err != nil {
		t.Skipf("no user config dir: %v", err)
	}
	if rel, err := filepath.Rel(dir, got); err != nil || strings.HasPrefix(rel, "..") {
		t.Skipf("os.UserConfigDir() = %s, not steered into %s", got, dir)
	}
	return got
}

// A config written before the project was renamed must survive the
// upgrade: it holds the app secret, and losing it silently reads to the
// user as "Claude Companion forgot everything".
func TestLoadAdoptsPreRenameConfig(t *testing.T) {
	t.Setenv(EnvVar, "")
	home := configHome(t)

	legacy := filepath.Join(home, legacyAppDir, "config.toml")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o700); err != nil {
		t.Fatal(err)
	}
	body := "app_id = 'cli_old'\napp_secret = 'sec'\nopen_id = 'ou_1'\n"
	if err := os.WriteFile(legacy, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.AppID != "cli_old" || got.AppSecret != "sec" || got.OpenID != "ou_1" {
		t.Errorf("adopted config = %+v, want the pre-rename credentials", got)
	}

	// The move must be a move: the config is now where this version looks
	// for it, and the old copy no longer holds a stale secret.
	want := filepath.Join(home, appDir, "config.toml")
	if _, err := os.Stat(want); err != nil {
		t.Errorf("config was not written to %s: %v", want, err)
	}
	if _, err := os.Stat(legacy); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("pre-rename config still present at %s (err %v)", legacy, err)
	}

	// A second load reads the new path and must not depend on the old one.
	again, err := Load()
	if err != nil || again.AppID != "cli_old" {
		t.Errorf("second Load = %+v, %v", again, err)
	}
}

// With no config in either place, Load must still report "not configured"
// rather than anything the migration invented.
func TestLoadMissingWithNoLegacy(t *testing.T) {
	t.Setenv(EnvVar, "")
	configHome(t)
	if _, err := Load(); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Load with nothing on disk = %v, want a not-exist error", err)
	}
}

// An explicit $CLAUDE_COMPANION_CONFIG names one exact file. Filling it
// from the pre-rename location would silently ignore what the caller asked
// for, which matters most in tests and scripted setups.
func TestExplicitPathIgnoresLegacy(t *testing.T) {
	home := configHome(t)
	legacy := filepath.Join(home, legacyAppDir, "config.toml")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte("app_id = 'cli_old'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvVar, filepath.Join(t.TempDir(), "explicit.toml"))
	if _, err := Load(); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Load with an explicit path = %v, want a not-exist error", err)
	}
	if _, err := os.Stat(legacy); err != nil {
		t.Errorf("explicit path consumed the pre-rename config: %v", err)
	}
}
