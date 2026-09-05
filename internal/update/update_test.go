package update

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsNewer(t *testing.T) {
	cases := []struct {
		candidate, current string
		want               bool
	}{
		{"1.2.3", "1.2.2", true},
		{"v1.2.3", "v1.2.2", true}, // leading "v" on either side
		{"1.2.3", "1.2.3", false},  // equal is not newer
		{"1.2.2", "1.2.3", false},
		{"2.0.0", "1.9.9", true},
		{"1.10.0", "1.9.0", true}, // numeric, not lexical, comparison
		{"1.2", "1.1.9", true},    // missing components default to 0
		{"1.2.3", "dev", false},   // unparseable current: never claim newer
		{"", "1.2.3", false},
		{"1.2.3-rc1", "1.2.2", false}, // unparseable candidate: never a false positive
	}
	for _, c := range cases {
		if got := IsNewer(c.candidate, c.current); got != c.want {
			t.Errorf("IsNewer(%q, %q) = %v, want %v", c.candidate, c.current, got, c.want)
		}
	}
}

func TestIsDevBuild(t *testing.T) {
	for _, v := range []string{"", "dev", "1.0.2-0.20260903142707-2c23255c532b+dirty", "1.4.0-rc1", "1.4.0+dirty"} {
		if !IsDevBuild(v) {
			t.Errorf("IsDevBuild(%q) = false, want true", v)
		}
	}
	for _, v := range []string{"1.2.3", "v1.2.3"} {
		if IsDevBuild(v) {
			t.Errorf("IsDevBuild(%q) = true, want false", v)
		}
	}
}

func TestFetchFromParsesLatestRelease(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Accept"); got != "application/vnd.github+json" {
			t.Errorf("Accept header = %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"tag_name": "v1.4.0",
			"html_url": "https://github.com/marcelritzschke/claude-code-feishu-companion/releases/tag/v1.4.0",
		})
	}))
	defer srv.Close()

	rel, err := fetchFrom(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if rel.Version != "1.4.0" {
		t.Errorf("Version = %q, want %q", rel.Version, "1.4.0")
	}
	if rel.URL != "https://github.com/marcelritzschke/claude-code-feishu-companion/releases/tag/v1.4.0" {
		t.Errorf("URL = %q", rel.URL)
	}
}

func TestFetchFromRejectsNonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	if _, err := fetchFrom(context.Background(), srv.URL); err == nil {
		t.Fatal("want an error for a non-200 response")
	}
}

func TestFetchFromRejectsMalformedBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()

	if _, err := fetchFrom(context.Background(), srv.URL); err == nil {
		t.Fatal("want an error for a malformed body")
	}
}
