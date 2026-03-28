// Package update provides passive version checking against GitHub releases.
//
// On startup Cortex can query the GitHub API for the latest release tag
// and print a one-line notice when a newer version is available. The check
// runs in a goroutine with a short timeout so it never blocks the CLI.
package update

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	// owner/repo on GitHub.
	repoOwner = "lleontor705"
	repoName  = "cortex"

	// Maximum time to wait for the GitHub API.
	requestTimeout = 3 * time.Second
)

// githubRelease is the subset of the GitHub release JSON we care about.
type githubRelease struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
}

// Result holds the outcome of a version check.
type Result struct {
	Latest    string // latest release tag (e.g. "v0.4.0")
	UpdateURL string // URL to the release page
}

// Check queries the GitHub releases API and returns a Result when a newer
// version is available. Returns nil when the current version is up-to-date
// or when the check cannot be performed (network error, dev build, etc.).
func Check(currentVersion string) *Result {
	// Skip check for dev builds.
	if currentVersion == "dev" || currentVersion == "" {
		return nil
	}

	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", repoOwner, repoName)

	client := &http.Client{Timeout: requestTimeout}
	resp, err := client.Get(url)
	if err != nil || resp.StatusCode != http.StatusOK {
		return nil
	}
	defer func() { _ = resp.Body.Close() }()

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil
	}

	if release.TagName == "" {
		return nil
	}

	if !isNewer(release.TagName, currentVersion) {
		return nil
	}

	return &Result{
		Latest:    release.TagName,
		UpdateURL: release.HTMLURL,
	}
}

// isNewer returns true when remote is a higher semver than current.
// Both values may optionally have a "v" prefix.
func isNewer(remote, current string) bool {
	r := parseSemver(remote)
	c := parseSemver(current)
	if r == nil || c == nil {
		return false
	}

	if r[0] != c[0] {
		return r[0] > c[0]
	}
	if r[1] != c[1] {
		return r[1] > c[1]
	}
	return r[2] > c[2]
}

// parseSemver extracts major.minor.patch from a version string.
// Returns nil if the string is not a valid semver.
func parseSemver(v string) []int {
	v = strings.TrimPrefix(v, "v")

	parts := strings.SplitN(v, ".", 3)
	if len(parts) != 3 {
		return nil
	}

	nums := make([]int, 3)
	for i, p := range parts {
		// Strip pre-release suffix (e.g. "0-rc1").
		if idx := strings.IndexByte(p, '-'); idx >= 0 {
			p = p[:idx]
		}
		n := 0
		for _, ch := range p {
			if ch < '0' || ch > '9' {
				return nil
			}
			n = n*10 + int(ch-'0')
		}
		nums[i] = n
	}
	return nums
}
