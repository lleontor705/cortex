package update

import (
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
		json.NewEncoder(w).Encode(githubRelease{
			TagName: "v9.9.9",
			HTMLURL: "https://github.com/test/release",
		})
	}))
	defer ts.Close()

	// Override the check function by calling the logic directly.
	// We test isNewer separately; this verifies the JSON parsing path.
	resp, err := http.Get(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

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
