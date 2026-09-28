package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"
)

func runtimeAssetName() string {
	return "cortex_2.5.0_" + runtime.GOOS + "_" + runtime.GOARCH + ".zip"
}

func writeRelease(t *testing.T, w http.ResponseWriter, rel githubRelease) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(rel); err != nil {
		t.Errorf("encode release: %v", err)
	}
}

func TestCheckCustom_ErrorPaths(t *testing.T) {
	t.Run("invalid url", func(t *testing.T) {
		if _, err := CheckCustom("v1.0.0", "://bad", time.Second); err == nil {
			t.Fatal("expected request construction error")
		}
	})

	cases := []struct {
		name string
		h    http.HandlerFunc
		want string
	}{
		{"non-200", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusInternalServerError) }, "HTTP 500"},
		{"malformed json", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("{not json")) }, ""},
		{"empty tag", func(w http.ResponseWriter, r *http.Request) { writeRelease(t, w, githubRelease{}) }, "empty release tag"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(tc.h)
			defer srv.Close()

			_, err := CheckCustom("v1.0.0", srv.URL, time.Second)
			if err == nil {
				t.Fatal("expected error")
			}
			if tc.want != "" && !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
		})
	}

	t.Run("connection failure", func(t *testing.T) {
		if _, err := CheckCustom("v1.0.0", "http://127.0.0.1:1/", time.Second); err == nil {
			t.Fatal("expected transport error")
		}
	})
}

func TestCheckCustom_MatchesRuntimeAsset(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeRelease(t, w, githubRelease{
			TagName: "v2.5.0",
			Assets:  []githubAsset{{Name: runtimeAssetName(), BrowserDownloadURL: "https://example.invalid/cortex.zip"}},
		})
	}))
	defer srv.Close()
	res, err := CheckCustom("v2.0.0", srv.URL, RequestTimeout)
	if err != nil {
		t.Fatal(err)
	}
	if res.AssetName != runtimeAssetName() || res.AssetURL == "" {
		t.Fatalf("asset not populated: name=%q url=%q", res.AssetName, res.AssetURL)
	}
}

func TestFindMatchingAsset_AliasesAndSuffixes(t *testing.T) {
	assets := []githubAsset{
		{Name: "cortex_2.0.0_linux_x86_64.tar.gz"},
		{Name: "cortex_2.0.0_linux_aarch64.tgz"},
		{Name: "cortex_2.0.0_linux_386"},
		{Name: "cortex_2.0.0_linux_i386.tgz"},
		{Name: "cortex_2.0.0_linux_amd64.txt"},
		{Name: "cortex_2.0.0_linux_riscv64.zip"},
		{Name: "cortex_2.0.0_windows_amd64.exe"},
	}

	tests := []struct {
		goos, goarch, want string
	}{
		{"linux", "amd64", "cortex_2.0.0_linux_x86_64.tar.gz"},
		{"linux", "arm64", "cortex_2.0.0_linux_aarch64.tgz"},
		{"linux", "386", "cortex_2.0.0_linux_i386.tgz"},
		{"linux", "riscv64", "cortex_2.0.0_linux_riscv64.zip"},
		{"windows", "amd64", "cortex_2.0.0_windows_amd64.exe"},
	}
	for _, tt := range tests {
		got := FindMatchingAsset(assets, tt.goos, tt.goarch)
		if got == nil || got.Name != tt.want {
			t.Fatalf("FindMatchingAsset(%s,%s) = %v, want %s", tt.goos, tt.goarch, got, tt.want)
		}
	}

	if got := FindMatchingAsset(assets, "freebsd", "amd64"); got != nil {
		t.Fatalf("expected nil for unsupported OS, got %v", got)
	}
}

func TestExtractBinary_ErrorPaths(t *testing.T) {
	if _, err := ExtractBinary([]byte("not a zip"), "cortex.zip"); err == nil {
		t.Fatal("expected zip open error")
	}
	if _, err := ExtractBinary([]byte("not gzip"), "cortex.tgz"); err == nil {
		t.Fatal("expected gzip open error")
	}

	var zbuf bytes.Buffer
	zw := zip.NewWriter(&zbuf)
	if _, err := zw.Create("readme.txt"); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ExtractBinary(zbuf.Bytes(), "cortex.zip"); err == nil || !strings.Contains(err.Error(), "not found in zip") {
		t.Fatalf("got %v, want zip entry error", err)
	}

	var tbuf bytes.Buffer
	gz := gzip.NewWriter(&tbuf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "readme.txt", Mode: 0o644, Size: 0}); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ExtractBinary(tbuf.Bytes(), "cortex.tar.gz"); err == nil || !strings.Contains(err.Error(), "not found in tar.gz") {
		t.Fatalf("got %v, want tar entry error", err)
	}
}

func TestExtractBinary_TarGzSuccessAndRaw(t *testing.T) {
	payload := []byte("#!cortex")
	var tbuf bytes.Buffer
	gz := gzip.NewWriter(&tbuf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "bin/cortex", Mode: 0o755, Size: int64(len(payload))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}

	got, err := ExtractBinary(tbuf.Bytes(), "cortex.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("tar.gz payload = %q, want %q", got, payload)
	}

	raw, err := ExtractBinary([]byte("rawbits"), "cortex.exe")
	if err != nil || string(raw) != "rawbits" {
		t.Fatalf("raw = %q, err = %v", raw, err)
	}
}

func TestSelfUpdate_EarlyExits(t *testing.T) {
	res, err := SelfUpdateWithCustomURL("v1.0.0", "://bad", nil)
	if err == nil || res != nil || !strings.Contains(err.Error(), "failed to check latest release") {
		t.Fatalf("check failure: res=%+v err=%v", res, err)
	}

	srvLatest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeRelease(t, w, githubRelease{TagName: "v2.0.0"})
	}))
	defer srvLatest.Close()
	if res, err := SelfUpdateWithCustomURL("v2.0.0", srvLatest.URL, nil); err != nil || res == nil || res.IsNewer {
		t.Fatalf("already latest: res=%+v err=%v", res, err)
	}

	srvNoAsset := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeRelease(t, w, githubRelease{TagName: "v9.9.9"})
	}))
	defer srvNoAsset.Close()
	if _, err := SelfUpdateWithCustomURL("dev", srvNoAsset.URL, nil); err == nil || !strings.Contains(err.Error(), "no release binary found") {
		t.Fatalf("missing asset: %v", err)
	}
}

func TestSelfUpdate_DownloadErrors(t *testing.T) {
	cases := []struct {
		name string
		h    http.HandlerFunc
		want string
	}{
		{"http status", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) }, "download returned HTTP status"},
		{"transport", nil, "failed to download asset"},
		{"extract", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("not a zip")) }, "failed to extract binary"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var srv *httptest.Server
			srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/download" && tc.h != nil {
					tc.h(w, r)
					return
				}
				assetURL := srv.URL + "/download"
				if tc.h == nil {
					assetURL = "http://127.0.0.1:1/missing"
				}
				writeRelease(t, w, githubRelease{
					TagName: "v9.9.9",
					Assets:  []githubAsset{{Name: runtimeAssetName(), BrowserDownloadURL: assetURL}},
				})
			}))
			defer srv.Close()

			_, err := SelfUpdateWithCustomURL("v1.0.0", srv.URL, nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
		})
	}
}
