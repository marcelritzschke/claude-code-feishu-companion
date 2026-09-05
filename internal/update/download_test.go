package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// theBinary is what a release archive is supposed to hold, standing in for
// the program itself.
const theBinary = "#!/bin/sh\necho claude-companion\n"

func tarGz(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range entries {
		if err := tw.WriteHeader(&tar.Header{
			Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func zipped(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// releaseServer serves one release's assets the way GitHub does. checksums
// is what it will claim the archive hashes to, so a test can publish a
// truthful release or a lying one.
func releaseServer(t *testing.T, asset string, archive []byte, checksums string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/"+asset):
			w.Write(archive)
		case strings.HasSuffix(r.URL.Path, "/checksums.txt"):
			w.Write([]byte(checksums))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestAssetNameMatchesWhatGoReleaserPublishes(t *testing.T) {
	cases := []struct{ version, goos, goarch, want string }{
		{"1.1.2", "linux", "amd64", "claude-code-feishu-companion_1.1.2_linux_amd64.tar.gz"},
		{"v1.1.2", "darwin", "arm64", "claude-code-feishu-companion_1.1.2_darwin_arm64.tar.gz"},
		{"1.1.2", "windows", "amd64", "claude-code-feishu-companion_1.1.2_windows_amd64.zip"},
	}
	for _, c := range cases {
		if got := AssetName(c.version, c.goos, c.goarch); got != c.want {
			t.Errorf("AssetName(%q, %q, %q) = %q, want %q", c.version, c.goos, c.goarch, got, c.want)
		}
	}
}

func TestDownloadVerifiesAndUnpacksTheBinary(t *testing.T) {
	asset := AssetName("1.1.2", runtime.GOOS, runtime.GOARCH)
	archive := tarGz(t, map[string]string{binaryName: theBinary, "README.md": "not the program"})
	if strings.HasSuffix(asset, ".zip") {
		archive = zipped(t, map[string]string{binaryName + ".exe": theBinary, "README.md": "not the program"})
	}
	srv := releaseServer(t, asset, archive, fmt.Sprintf("%s  %s\n", sum(archive), asset))

	dir := t.TempDir()
	got, err := downloadFrom(context.Background(), srv.URL, "1.1.2", dir, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(got) != dir {
		t.Errorf("staged at %s, want it inside %s so the install is a rename", got, dir)
	}
	body, err := os.ReadFile(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != theBinary {
		t.Errorf("staged file holds %q, want the program", body)
	}
	info, err := os.Stat(got)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		t.Errorf("staged mode = %v, want it executable", info.Mode().Perm())
	}
}

// An archive whose bytes do not match the release's own checksums.txt is
// the case this whole path exists to catch.
func TestDownloadRefusesAnArchiveThatFailsItsChecksum(t *testing.T) {
	asset := AssetName("1.1.2", "linux", "amd64")
	archive := tarGz(t, map[string]string{binaryName: "not what the release published"})
	honest := tarGz(t, map[string]string{binaryName: theBinary})
	srv := releaseServer(t, asset, archive, fmt.Sprintf("%s  %s\n", sum(honest), asset))

	dir := t.TempDir()
	if _, err := downloadFrom(context.Background(), srv.URL, "1.1.2", dir, "linux", "amd64"); err == nil {
		t.Fatal("want a tampered archive refused")
	} else if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("error = %v, want it to name the mismatch", err)
	}
	left, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Errorf("a refused download left %d files behind, want none", len(left))
	}
}

// A checksums.txt that says nothing about this asset proves nothing about
// it, so it is refused rather than accepted unverified.
func TestDownloadRefusesAnUnlistedAsset(t *testing.T) {
	asset := AssetName("1.1.2", "linux", "amd64")
	archive := tarGz(t, map[string]string{binaryName: theBinary})
	srv := releaseServer(t, asset, archive, "deadbeef  some-other-project_1.1.2_linux_amd64.tar.gz\n")

	if _, err := downloadFrom(context.Background(), srv.URL, "1.1.2", t.TempDir(), "linux", "amd64"); err == nil {
		t.Fatal("want an unlisted asset refused")
	} else if !strings.Contains(err.Error(), "does not list") {
		t.Errorf("error = %v, want it to say the asset is unlisted", err)
	}
}

func TestDownloadReportsAnArchiveWithoutTheProgram(t *testing.T) {
	asset := AssetName("1.1.2", "linux", "amd64")
	archive := tarGz(t, map[string]string{"README.md": "only the readme"})
	srv := releaseServer(t, asset, archive, fmt.Sprintf("%s  %s\n", sum(archive), asset))

	if _, err := downloadFrom(context.Background(), srv.URL, "1.1.2", t.TempDir(), "linux", "amd64"); err == nil {
		t.Fatal("want an archive without the program refused")
	} else if !strings.Contains(err.Error(), binaryName) {
		t.Errorf("error = %v, want it to name what is missing", err)
	}
}

func TestDownloadReportsAMissingRelease(t *testing.T) {
	srv := releaseServer(t, "nothing-served", nil, "")
	if _, err := downloadFrom(context.Background(), srv.URL, "9.9.9", t.TempDir(), "linux", "amd64"); err == nil {
		t.Fatal("want a missing asset reported")
	}
}
