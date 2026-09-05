// Package update checks GitHub Releases for a newer claude-companion
// version than the one currently running. It only ever reports; nothing
// here downloads or replaces a binary, so the same check is safe to run
// from a CLI command, from the daemon's background poll, or from anything
// else that wants an honest answer to "am I behind?".
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// releasesAPI is GitHub's "latest release" endpoint. It never returns a
// draft or a prerelease, so nothing here needs to filter those out.
const releasesAPI = "https://api.github.com/repos/marcelritzschke/claude-code-feishu-companion/releases/latest"

// Release is the latest stable release GitHub reports for this project.
type Release struct {
	// Version is the release's semantic version, without a leading "v".
	Version string
	// URL is the release's page on GitHub: release notes, and where the
	// user upgrades from.
	URL string
}

// Announce is the one message shown for a release newer than current,
// whether that is a Feishu DM or a CLI line - so the wording only lives
// in one place.
func Announce(rel Release, current string) string {
	return fmt.Sprintf("claude-companion %s is available (you're on %s).\n%s", rel.Version, current, rel.URL)
}

// Fetch asks GitHub for the project's latest stable release.
func Fetch(ctx context.Context) (Release, error) {
	return fetchFrom(ctx, releasesAPI)
}

func fetchFrom(ctx context.Context, url string) (Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return Release{}, fmt.Errorf("github returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var payload struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return Release{}, fmt.Errorf("decode release: %w", err)
	}
	if payload.TagName == "" {
		return Release{}, fmt.Errorf("release response had no tag_name")
	}
	return Release{Version: strings.TrimPrefix(payload.TagName, "v"), URL: payload.HTMLURL}, nil
}

// IsDevBuild reports whether version identifies a build with nothing to
// compare against: a plain `go build .` or `go run .` that received no
// linker-injected version, and equally a build whose version names a
// commit rather than a release - Go stamps those as a pseudo-version like
// "1.0.2-0.20260903142707-2c23255c532b+dirty", which is not any release
// and sorts below the 1.0.2 it borrows its digits from. Anything IsNewer
// cannot compare belongs here, so a build is never silently reported as
// up to date on a comparison that could not be made.
func IsDevBuild(version string) bool {
	_, ok := parseVersion(version)
	return !ok
}

// IsNewer reports whether candidate is a newer release than current. Both
// may carry a leading "v". A value that doesn't parse as dotted-integer
// semver - including a dev build's "dev", or a prerelease suffix like
// "-rc1" - is treated as not newer, so a malformed or incomparable version
// never produces a false positive.
func IsNewer(candidate, current string) bool {
	c, ok := parseVersion(candidate)
	if !ok {
		return false
	}
	b, ok := parseVersion(current)
	if !ok {
		return false
	}
	for i := range c {
		if c[i] != b[i] {
			return c[i] > b[i]
		}
	}
	return false
}

// parseVersion reads a "vMAJOR.MINOR.PATCH" release tag (the "v" and
// trailing components are optional; missing components default to 0). A
// version carrying anything else - a prerelease suffix, a pseudo-version's
// timestamp and commit, a "+dirty" marker - is not a release and does not
// parse.
func parseVersion(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if v == "" {
		return out, false
	}
	parts := strings.SplitN(v, ".", 3)
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
