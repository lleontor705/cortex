// Package update provides version checking and self-updating against GitHub releases.
package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	// Owner and repository on GitHub.
	RepoOwner = "lleontor705"
	RepoName  = "cortex"

	// Default HTTP timeout for metadata queries.
	RequestTimeout = 5 * time.Second

	// Timeout for binary downloads.
	DownloadTimeout = 60 * time.Second
)

// githubAsset represents a release binary asset on GitHub.
type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

// githubRelease is the subset of the GitHub release JSON we care about.
type githubRelease struct {
	TagName string        `json:"tag_name"`
	HTMLURL string        `json:"html_url"`
	Body    string        `json:"body"`
	Assets  []githubAsset `json:"assets"`
}

// Result holds the outcome of a version check.
type Result struct {
	Current      string
	Latest       string // latest release tag (e.g. "v0.4.0")
	UpdateURL    string // URL to the release page
	ReleaseNotes string
	IsNewer      bool
	AssetURL     string
	AssetName    string
}

// Check queries the GitHub releases API and returns a Result when a newer
// version is available. Returns nil when the current version is up-to-date
// or when the check cannot be performed (network error, dev build, etc.).
func Check(currentVersion string) *Result {
	// Skip check for dev builds.
	if currentVersion == "dev" || currentVersion == "" {
		return nil
	}

	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", RepoOwner, RepoName)
	res, err := CheckCustom(currentVersion, url, RequestTimeout)
	if err != nil || res == nil || !res.IsNewer {
		return nil
	}
	return res
}

// CheckCustom queries a specific API URL for releases.
func CheckCustom(currentVersion, apiURL string, timeout time.Duration) (*Result, error) {
	client := &http.Client{Timeout: timeout}
	req, err := http.NewRequest(http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "Cortex-Update-Checker")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub release check returned HTTP %d", resp.StatusCode)
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, err
	}

	if release.TagName == "" {
		return nil, errors.New("empty release tag")
	}

	newer := isNewer(release.TagName, currentVersion)
	result := &Result{
		Current:      currentVersion,
		Latest:       release.TagName,
		UpdateURL:    release.HTMLURL,
		ReleaseNotes: release.Body,
		IsNewer:      newer,
	}

	if asset := FindMatchingAsset(release.Assets, runtime.GOOS, runtime.GOARCH); asset != nil {
		result.AssetURL = asset.BrowserDownloadURL
		result.AssetName = asset.Name
	}

	return result, nil
}

// FindMatchingAsset finds the release asset matching the target OS and Architecture.
func FindMatchingAsset(assets []githubAsset, goos, goarch string) *githubAsset {
	osKey := strings.ToLower(goos)
	var archKeys []string

	switch strings.ToLower(goarch) {
	case "amd64", "x86_64":
		archKeys = []string{"amd64", "x86_64"}
	case "arm64", "aarch64":
		archKeys = []string{"arm64", "aarch64"}
	case "386", "i386":
		archKeys = []string{"386", "i386"}
	default:
		archKeys = []string{strings.ToLower(goarch)}
	}

	for _, asset := range assets {
		name := strings.ToLower(asset.Name)

		// Must match OS
		if !strings.Contains(name, osKey) {
			continue
		}

		// Must match Arch
		archMatch := false
		for _, k := range archKeys {
			if strings.Contains(name, k) {
				archMatch = true
				break
			}
		}
		if !archMatch {
			continue
		}

		// Must be an executable archive or binary
		if strings.HasSuffix(name, ".zip") || strings.HasSuffix(name, ".tar.gz") || strings.HasSuffix(name, ".tgz") || strings.HasSuffix(name, ".exe") {
			return &asset
		}
	}

	return nil
}

// SelfUpdate checks for the latest version, downloads the binary asset, and updates the executable.
func SelfUpdate(currentVersion string, progressFn func(string)) (*Result, error) {
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", RepoOwner, RepoName)
	return SelfUpdateWithCustomURL(currentVersion, apiURL, progressFn)
}

// SelfUpdateWithCustomURL performs a self update using a specified API URL.
func SelfUpdateWithCustomURL(currentVersion, apiURL string, progressFn func(string)) (*Result, error) {
	if progressFn == nil {
		progressFn = func(string) {}
	}

	progressFn("Checking for latest release...")
	res, err := CheckCustom(currentVersion, apiURL, RequestTimeout)
	if err != nil {
		return nil, fmt.Errorf("failed to check latest release: %w", err)
	}

	if !res.IsNewer && currentVersion != "dev" {
		progressFn(fmt.Sprintf("Cortex is already at the latest version (%s).", currentVersion))
		return res, nil
	}

	if res.AssetURL == "" {
		return res, fmt.Errorf("no release binary found for %s/%s. Please install manually or use: go install github.com/%s/%s/cmd/cortex@latest", runtime.GOOS, runtime.GOARCH, RepoOwner, RepoName)
	}

	progressFn(fmt.Sprintf("Downloading %s (%s)...", res.Latest, res.AssetName))
	client := &http.Client{Timeout: DownloadTimeout}
	resp, err := client.Get(res.AssetURL)
	if err != nil {
		return res, fmt.Errorf("failed to download asset: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return res, fmt.Errorf("download returned HTTP status %d", resp.StatusCode)
	}

	archiveData, err := io.ReadAll(resp.Body)
	if err != nil {
		return res, fmt.Errorf("failed to read download payload: %w", err)
	}

	progressFn("Extracting binary...")
	binaryBytes, err := ExtractBinary(archiveData, res.AssetName)
	if err != nil {
		return res, fmt.Errorf("failed to extract binary: %w", err)
	}

	progressFn("Installing update...")
	if err := ReplaceExecutable(binaryBytes); err != nil {
		return res, fmt.Errorf("failed to update binary in-place: %w", err)
	}

	progressFn(fmt.Sprintf("Successfully updated Cortex to %s!", res.Latest))
	return res, nil
}

// ExtractBinary extracts the cortex executable from a zip, tar.gz, or raw binary payload.
func ExtractBinary(data []byte, filename string) ([]byte, error) {
	lower := strings.ToLower(filename)

	if strings.HasSuffix(lower, ".zip") {
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			base := filepath.Base(f.Name)
			if base == "cortex" || base == "cortex.exe" {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer func() { _ = rc.Close() }()
				return io.ReadAll(rc)
			}
		}
		return nil, errors.New("cortex binary not found in zip archive")
	}

	if strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz") {
		gr, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		defer func() { _ = gr.Close() }()

		tr := tar.NewReader(gr)
		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
			base := filepath.Base(hdr.Name)
			if base == "cortex" || base == "cortex.exe" {
				return io.ReadAll(tr)
			}
		}
		return nil, errors.New("cortex binary not found in tar.gz archive")
	}

	// Raw binary
	return data, nil
}

// ReplaceExecutable safely replaces the current running binary with the new binary.
func ReplaceExecutable(newBinary []byte) error {
	execPath, err := os.Executable()
	if err != nil {
		return err
	}
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return err
	}

	dir := filepath.Dir(execPath)

	if runtime.GOOS == "windows" {
		oldPath := execPath + ".old"
		_ = os.Remove(oldPath) // remove previous backup if exists

		if err := os.Rename(execPath, oldPath); err != nil {
			return fmt.Errorf("could not rename current binary: %w", err)
		}

		if err := os.WriteFile(execPath, newBinary, 0755); err != nil {
			// Try to restore old
			_ = os.Rename(oldPath, execPath)
			return fmt.Errorf("could not write updated binary: %w", err)
		}

		_ = os.Remove(oldPath) // best-effort cleanup
		return nil
	}

	// Unix / macOS: write to temp in same dir then atomic rename
	tmpFile, err := os.CreateTemp(dir, "cortex-update-*")
	if err != nil {
		return fmt.Errorf("could not create temporary update file (check permissions for %s): %w", dir, err)
	}
	tmpPath := tmpFile.Name()

	if _, err := tmpFile.Write(newBinary); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmpFile.Chmod(0755); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	_ = tmpFile.Close()

	if err := os.Rename(tmpPath, execPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("could not replace binary in %s: %w", execPath, err)
	}

	return nil
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
