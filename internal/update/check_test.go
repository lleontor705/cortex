package update

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsNewer(t *testing.T) {
	tests := []struct {
		remote, current string
		want            bool
	}{
		{"v0.4.0", "v0.3.0", true},
		{"v1.0.0", "v0.9.9", true},
		{"v0.3.1", "v0.3.0", true},
		{"v0.3.0", "v0.3.0", false},
		{"v0.2.0", "v0.3.0", false},
		{"0.4.0", "0.3.0", true},
		{"v1.0.0-rc1", "v0.9.0", true},
		{"invalid", "v0.3.0", false},
		{"v0.3.0", "invalid", false},
	}

	for _, tt := range tests {
		t.Run(tt.remote+"_vs_"+tt.current, func(t *testing.T) {
			got := isNewer(tt.remote, tt.current)
			if got != tt.want {
				t.Errorf("isNewer(%q, %q) = %v, want %v", tt.remote, tt.current, got, tt.want)
			}
		})
	}
}

func TestParseSemver(t *testing.T) {
	tests := []struct {
		input string
		want  []int
	}{
		{"v1.2.3", []int{1, 2, 3}},
		{"0.4.0", []int{0, 4, 0}},
		{"v1.0.0-rc1", []int{1, 0, 0}},
		{"invalid", nil},
		{"v1.2", nil},
		{"v1.2.abc", nil},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseSemver(tt.input)
			if tt.want == nil {
				if got != nil {
					t.Errorf("parseSemver(%q) = %v, want nil", tt.input, got)
				}
				return
			}
			if got == nil {
				t.Fatalf("parseSemver(%q) = nil, want %v", tt.input, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("parseSemver(%q)[%d] = %d, want %d", tt.input, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestCheckDev(t *testing.T) {
	if r := Check("dev"); r != nil {
		t.Error("Check(dev) should return nil")
	}
	if r := Check(""); r != nil {
		t.Error("Check('') should return nil")
	}
}

func TestCheckWithServer(t *testing.T) {
	// Mock GitHub API.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(githubRelease{ //nolint:errcheck
			TagName: "v9.9.9",
			HTMLURL: "https://github.com/test/release",
		})
	}))
	defer ts.Close() //nolint:errcheck

	// Override the check function by calling the logic directly.
	// We test isNewer separately; this verifies the JSON parsing path.
	resp, err := http.Get(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		t.Fatal(err)
	}

	if release.TagName != "v9.9.9" {
		t.Errorf("got tag %q, want v9.9.9", release.TagName)
	}

	if !isNewer(release.TagName, "v0.1.0") {
		t.Error("v9.9.9 should be newer than v0.1.0")
	}
}

func TestFindMatchingAsset(t *testing.T) {
	assets := []githubAsset{
		{Name: "cortex_2.0.0_linux_amd64.tar.gz", BrowserDownloadURL: "https://example.com/linux_amd64.tar.gz"},
		{Name: "cortex_2.0.0_windows_amd64.zip", BrowserDownloadURL: "https://example.com/windows_amd64.zip"},
		{Name: "cortex_2.0.0_darwin_arm64.tar.gz", BrowserDownloadURL: "https://example.com/darwin_arm64.tar.gz"},
	}

	win := FindMatchingAsset(assets, "windows", "amd64")
	if win == nil || win.Name != "cortex_2.0.0_windows_amd64.zip" {
		t.Fatalf("expected windows asset, got %v", win)
	}

	lin := FindMatchingAsset(assets, "linux", "amd64")
	if lin == nil || lin.Name != "cortex_2.0.0_linux_amd64.tar.gz" {
		t.Fatalf("expected linux asset, got %v", lin)
	}

	dar := FindMatchingAsset(assets, "darwin", "arm64")
	if dar == nil || dar.Name != "cortex_2.0.0_darwin_arm64.tar.gz" {
		t.Fatalf("expected darwin asset, got %v", dar)
	}

	none := FindMatchingAsset(assets, "freebsd", "riscv64")
	if none != nil {
		t.Fatalf("expected nil for unsupported platform, got %v", none)
	}
}

func TestExtractBinaryZip(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	fw, err := zw.Create("cortex.exe")
	if err != nil {
		t.Fatal(err)
	}
	expected := []byte("#!cortex-test-binary")
	if _, err := fw.Write(expected); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	extracted, err := ExtractBinary(buf.Bytes(), "cortex_windows_amd64.zip")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(extracted, expected) {
		t.Fatalf("extracted binary = %q, want %q", extracted, expected)
	}
}

func TestCheckCustomWithServer(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(githubRelease{ //nolint:errcheck
			TagName: "v2.5.0",
			HTMLURL: "https://github.com/lleontor705/cortex/releases/tag/v2.5.0",
			Body:    "Awesome release with AI and AST",
			Assets: []githubAsset{
				{Name: "cortex_2.5.0_windows_amd64.zip", BrowserDownloadURL: "https://example.com/cortex.zip"},
			},
		})
	}))
	defer ts.Close()

	res, err := CheckCustom("v2.0.0", ts.URL, RequestTimeout)
	if err != nil {
		t.Fatal(err)
	}
	if res == nil {
		t.Fatal("expected non-nil result")
	}
	if !res.IsNewer {
		t.Error("expected IsNewer to be true")
	}
	if res.Latest != "v2.5.0" {
		t.Errorf("got latest %s, want v2.5.0", res.Latest)
	}
	if res.ReleaseNotes != "Awesome release with AI and AST" {
		t.Errorf("got release notes %s", res.ReleaseNotes)
	}
}
