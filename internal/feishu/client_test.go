package feishu

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTestCache(t *testing.T) *diskTokenCache {
	t.Helper()
	return &diskTokenCache{path: filepath.Join(t.TempDir(), "token.json")}
}

func TestDiskTokenCacheRoundTrip(t *testing.T) {
	c := newTestCache(t)
	ctx := context.Background()

	if v, err := c.Get(ctx, "tenant_access_token_internal"); err != nil || v != "" {
		t.Fatalf("empty cache should miss: %q %v", v, err)
	}
	if err := c.Set(ctx, "tenant_access_token_internal", "t-abc", time.Hour); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if v, err := c.Get(ctx, "tenant_access_token_internal"); err != nil || v != "t-abc" {
		t.Fatalf("Get after Set: %q %v", v, err)
	}

	info, err := os.Stat(c.path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("perm = %v, want 0600", info.Mode().Perm())
	}
}

func TestDiskTokenCacheExpiry(t *testing.T) {
	c := newTestCache(t)
	ctx := context.Background()
	if err := c.Set(ctx, "tenant_access_token_internal", "t-abc", time.Second); err != nil {
		t.Fatal(err)
	}
	// Backdate the stored expiry by rewriting the file.
	data, _ := os.ReadFile(c.path)
	var m map[string]cacheEntry
	_ = json.Unmarshal(data, &m)
	for k := range m {
		m[k] = cacheEntry{Value: m[k].Value, ExpireAt: time.Now().Add(-time.Minute)}
	}
	data, _ = json.Marshal(m)
	_ = os.WriteFile(c.path, data, 0o600)

	if v, err := c.Get(ctx, "tenant_access_token_internal"); err != nil || v != "" {
		t.Fatalf("expired token should miss: %q %v", v, err)
	}
}

func TestDiskTokenCacheCorruptFile(t *testing.T) {
	c := newTestCache(t)
	ctx := context.Background()
	if err := os.WriteFile(c.path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if v, err := c.Get(ctx, "tenant_access_token_internal"); err != nil || v != "" {
		t.Fatalf("corrupt cache should miss: %q %v", v, err)
	}
	if err := c.Set(ctx, "tenant_access_token_internal", "t-ok", time.Hour); err != nil {
		t.Fatalf("Set over corrupt file: %v", err)
	}
	if v, err := c.Get(ctx, "tenant_access_token_internal"); err != nil || v != "t-ok" {
		t.Fatalf("Get after repair: %q %v", v, err)
	}
}

func TestDiskTokenCacheIgnoresOtherKeys(t *testing.T) {
	c := newTestCache(t)
	ctx := context.Background()
	if err := c.Set(ctx, "app_ticket_x", "secret-ticket", time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(c.path); !os.IsNotExist(err) {
		t.Error("non-token keys must not be written to disk")
	}
	if v, err := c.Get(ctx, "app_ticket_x"); err != nil || v != "" {
		t.Errorf("non-token keys must miss: %q %v", v, err)
	}
}
