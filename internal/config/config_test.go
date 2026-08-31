package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvVar, filepath.Join(dir, "config.toml"))

	c := &Config{AppID: "cli_test", AppSecret: "sec", OpenID: "ou_123",
		Notify: NotifyProgress, Detail: DetailCompact}
	if err := c.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	info, err := os.Stat(filepath.Join(dir, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("config perm = %v, want 0600", info.Mode().Perm())
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if *got != *c {
		t.Errorf("round trip: got %+v want %+v", got, c)
	}
	if !got.ProgressEnabled() || !got.CompactCompletions() {
		t.Errorf("behavior flags: progress=%v compact=%v", got.ProgressEnabled(), got.CompactCompletions())
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
	if got.Detail != DetailNormal {
		t.Errorf("detail default = %q", got.Detail)
	}
	if got.ProgressEnabled() || got.CompactCompletions() {
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
