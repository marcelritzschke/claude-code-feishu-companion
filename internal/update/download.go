package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
)

// Downloading a release means running code from the internet as the user,
// which is the most dangerous thing this program does. So every download
// goes through the same two gates, in this order: it comes over HTTPS from
// the release GitHub itself published, and its bytes match the SHA-256 the
// release recorded. Nothing is extracted, and no file is written next to
// the binary, until both hold.

// downloadBase is where a release's assets live. It is the same repository
// releasesAPI reports on, so a release found by the check and an archive
// fetched here can never come from different projects.
const downloadBase = "https://github.com/marcelritzschke/claude-code-feishu-companion/releases/download"

// binaryName is what the archive calls the program, from GoReleaser's
// builds.binary. It is not this process's own name: a renamed binary still
// unpacks the one the archive holds.
const binaryName = "claude-companion"

// projectName is the archive's name prefix, from GoReleaser's project_name.
const projectName = "claude-code-feishu-companion"

// maxArchiveBytes bounds what a download may expand to. The archives are a
// few megabytes; this is loose enough never to be met by an honest release
// and tight enough that a corrupted or hostile one cannot fill the disk.
const maxArchiveBytes = 200 << 20

// AssetName is the archive GoReleaser publishes for one platform, e.g.
// "claude-code-feishu-companion_1.1.2_linux_amd64.tar.gz".
func AssetName(version, goos, goarch string) string {
	ext := "tar.gz"
	if goos == "windows" {
		ext = "zip"
	}
	return fmt.Sprintf("%s_%s_%s_%s.%s", projectName, strings.TrimPrefix(version, "v"), goos, goarch, ext)
}

// Download fetches the release's binary for this platform, verifies it
// against the release's checksums.txt, and writes it to a new file in dir.
//
// It writes into dir - which the caller sets to where the binary being
// replaced lives - so that installing is a rename within one filesystem
// rather than a copy across two, and so that a directory the user cannot
// write to fails here, before anything has been stopped.
func Download(ctx context.Context, version, dir string) (string, error) {
	return downloadFrom(ctx, downloadBase, version, dir, runtime.GOOS, runtime.GOARCH)
}

func downloadFrom(ctx context.Context, base, version, dir, goos, goarch string) (string, error) {
	version = strings.TrimPrefix(version, "v")
	asset := AssetName(version, goos, goarch)
	root := fmt.Sprintf("%s/v%s", base, version)

	archive, err := fetchBytes(ctx, root+"/"+asset)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", asset, err)
	}
	sums, err := fetchBytes(ctx, root+"/checksums.txt")
	if err != nil {
		return "", fmt.Errorf("download checksums: %w", err)
	}
	if err := verifyChecksum(archive, sums, asset); err != nil {
		return "", err
	}
	return extractBinary(archive, asset, dir)
}

// fetchBytes reads one release asset into memory, under the same bound the
// extraction uses.
func fetchBytes(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github returned %s", resp.Status)
	}
	return readCapped(resp.Body)
}

// readCapped reads r, refusing anything past the bound rather than
// truncating it: a partial archive must not look like a whole one.
func readCapped(r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, maxArchiveBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxArchiveBytes {
		return nil, errors.New("release asset is larger than this program is willing to read")
	}
	return b, nil
}

// verifyChecksum matches the archive against the line checksums.txt
// records for it. An asset the file does not mention is refused: a
// checksums.txt that has nothing to say about this download proves
// nothing about it.
func verifyChecksum(archive, sums []byte, asset string) error {
	want, ok := checksumFor(string(sums), asset)
	if !ok {
		return fmt.Errorf("checksums.txt does not list %s", asset)
	}
	sum := sha256.Sum256(archive)
	if got := hex.EncodeToString(sum[:]); got != want {
		return fmt.Errorf("checksum mismatch for %s: release says %s, download is %s", asset, want, got)
	}
	return nil
}

// checksumFor reads GoReleaser's "<sha256>  <file>" lines.
func checksumFor(sums, asset string) (string, bool) {
	for _, line := range strings.Split(sums, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == asset {
			return strings.ToLower(fields[0]), true
		}
	}
	return "", false
}

// extractBinary pulls the program out of a verified archive and writes it
// to a new file in dir, executable and owned by the user alone until it is
// installed.
func extractBinary(archive []byte, asset, dir string) (string, error) {
	var content []byte
	var err error
	if strings.HasSuffix(asset, ".zip") {
		content, err = fromZip(archive)
	} else {
		content, err = fromTarGz(archive)
	}
	if err != nil {
		return "", err
	}

	f, err := os.CreateTemp(dir, ".claude-companion-new-*")
	if err != nil {
		return "", fmt.Errorf("write the new binary next to the old one: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(content); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	if err := f.Chmod(0o755); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// wanted reports whether an archive entry is the program. GoReleaser adds
// a README beside it, and the name carries .exe on Windows.
func wanted(name string) bool {
	base := path.Base(filepath.ToSlash(name))
	return base == binaryName || base == binaryName+".exe"
}

func fromTarGz(archive []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("read archive: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read archive: %w", err)
		}
		if h.Typeflag != tar.TypeReg || !wanted(h.Name) {
			continue
		}
		return readCapped(tr)
	}
	return nil, fmt.Errorf("the archive holds no %s", binaryName)
}

func fromZip(archive []byte) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, fmt.Errorf("read archive: %w", err)
	}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || !wanted(f.Name) {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("read archive: %w", err)
		}
		defer rc.Close()
		return readCapped(rc)
	}
	return nil, fmt.Errorf("the archive holds no %s", binaryName)
}
