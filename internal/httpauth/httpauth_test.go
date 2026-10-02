package httpauth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func requestWith(headers map[string]string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/stats", nil)
	for name, value := range headers {
		r.Header.Set(name, value)
	}
	return r
}

func TestExtractSecretBearerScheme(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts Options
	}{
		{"local", LocalOptions()},
		{"server", ServerOptions()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := requestWith(map[string]string{"Authorization": "Bearer s3cret"})
			got, ok := ExtractSecret(r, tc.opts)
			if !ok || got != "s3cret" {
				t.Fatalf("ExtractSecret = (%q, %v), want (%q, true)", got, ok, "s3cret")
			}
		})
	}
}

func TestExtractSecretAPIKeyAcceptance(t *testing.T) {
	local := requestWith(map[string]string{"X-API-Key": "k3y"})
	if got, ok := ExtractSecret(local, LocalOptions()); !ok || got != "k3y" {
		t.Fatalf("local ExtractSecret(X-API-Key) = (%q, %v), want (%q, true)", got, ok, "k3y")
	}

	server := requestWith(map[string]string{"X-API-Key": "k3y"})
	if got, ok := ExtractSecret(server, ServerOptions()); ok {
		t.Fatalf("server ExtractSecret(X-API-Key) = (%q, %v), want (\"\", false): the server plane ignores X-API-Key today", got, ok)
	}
}

func TestExtractSecretBearerTakesPrecedenceOverAPIKey(t *testing.T) {
	r := requestWith(map[string]string{
		"Authorization": "Bearer primary",
		"X-API-Key":     "secondary",
	})
	got, ok := ExtractSecret(r, LocalOptions())
	if !ok || got != "primary" {
		t.Fatalf("ExtractSecret = (%q, %v), want (%q, true)", got, ok, "primary")
	}
}

func TestExtractSecretBlankBearerFallsThroughToAPIKey(t *testing.T) {
	r := requestWith(map[string]string{
		"Authorization": "Bearer    ",
		"X-API-Key":     "fallback",
	})
	got, ok := ExtractSecret(r, LocalOptions())
	if !ok || got != "fallback" {
		t.Fatalf("ExtractSecret = (%q, %v), want (%q, true): local falls through a blank bearer", got, ok, "fallback")
	}

	server := requestWith(map[string]string{"Authorization": "Bearer    "})
	if got, ok := ExtractSecret(server, ServerOptions()); ok {
		t.Fatalf("server ExtractSecret(blank bearer) = (%q, %v), want (\"\", false)", got, ok)
	}
}

func TestExtractSecretSchemeIsCaseSensitive(t *testing.T) {
	for _, scheme := range []string{"bearer x", "BEARER x", "Token x", "Bearerx"} {
		r := requestWith(map[string]string{"Authorization": scheme})
		if got, ok := ExtractSecret(r, LocalOptions()); ok {
			t.Fatalf("ExtractSecret(%q) accepted with %q, want rejection", scheme, got)
		}
		if got, ok := ExtractSecret(r, ServerOptions()); ok {
			t.Fatalf("server ExtractSecret(%q) accepted with %q, want rejection", scheme, got)
		}
	}
}

func TestExtractSecretWhitespacePolicy(t *testing.T) {
	padded := requestWith(map[string]string{"Authorization": "Bearer  spaced "})

	if got, ok := ExtractSecret(padded, ServerOptions()); !ok || got != " spaced " {
		t.Fatalf("server ExtractSecret = (%q, %v), want (%q, true): byte-exact secret", got, ok, " spaced ")
	}

	leading := requestWith(map[string]string{"Authorization": "  Bearer spaced"})
	if got, ok := ExtractSecret(leading, ServerOptions()); ok {
		t.Fatalf("server ExtractSecret(leading space) = (%q, %v), want (\"\", false)", got, ok)
	}

	trimmed := requestWith(map[string]string{"Authorization": "  Bearer  spaced  "})
	if got, ok := ExtractSecret(trimmed, LocalOptions()); !ok || got != "spaced" {
		t.Fatalf("local ExtractSecret = (%q, %v), want (%q, true)", got, ok, "spaced")
	}
}

func TestExtractSecretAbsent(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts Options
	}{
		{"local", LocalOptions()},
		{"server", ServerOptions()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, ok := ExtractSecret(httptest.NewRequest(http.MethodGet, "/api/stats", nil), tc.opts); ok {
				t.Fatalf("ExtractSecret(no headers) = (%q, %v), want (\"\", false)", got, ok)
			}
			basic := requestWith(map[string]string{"Authorization": "Basic dXNlcjpwYXNz"})
			if got, ok := ExtractSecret(basic, tc.opts); ok {
				t.Fatalf("ExtractSecret(Basic) = (%q, %v), want (\"\", false)", got, ok)
			}
		})
	}
}

func TestOptionsMatrixDocumentsBothPlanes(t *testing.T) {
	local := LocalOptions()
	if !local.AcceptAPIKey || !local.Trim {
		t.Fatalf("LocalOptions() = %+v, want {AcceptAPIKey:true Trim:true}", local)
	}
	server := ServerOptions()
	if server.AcceptAPIKey || server.Trim {
		t.Fatalf("ServerOptions() = %+v, want {AcceptAPIKey:false Trim:false}", server)
	}
}

func TestEqualSecret(t *testing.T) {
	for _, tc := range []struct {
		name       string
		presented  string
		configured string
		want       bool
	}{
		{"equal", "token", "token", true},
		{"same length differs", "token", "tokem", false},
		{"longer presented", "token", "token-extended", false},
		{"shorter presented", "tok", "token", false},
		{"empty configured", "token", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := EqualSecret(tc.presented, tc.configured); got != tc.want {
				t.Fatalf("EqualSecret(%q, %q) = %v, want %v", tc.presented, tc.configured, got, tc.want)
			}
		})
	}
}

func TestSecretDigestIsFixedWidth(t *testing.T) {
	for _, secret := range []string{"", "a", "token", strings.Repeat("x", 4096)} {
		if got := len(SecretDigest(secret)); got != sha256.Size {
			t.Fatalf("len(SecretDigest(len %d)) = %d, want %d", len(secret), got, sha256.Size)
		}
	}
}

// TestEqualSecretComparesFixedWidthDigests is the LOW-9 oracle: it observes the
// operand widths handed to the constant-time compare and fails if the raw
// secret lengths ever reach it (a raw subtle.ConstantTimeCompare is a length
// oracle because it short-circuits on len(x) != len(y)).
func TestEqualSecretComparesFixedWidthDigests(t *testing.T) {
	var widths [][2]int
	original := constantTimeCompare
	constantTimeCompare = func(a, b []byte) int {
		widths = append(widths, [2]int{len(a), len(b)})
		return subtle.ConstantTimeCompare(a, b)
	}
	t.Cleanup(func() { constantTimeCompare = original })

	for _, tc := range []struct {
		name       string
		presented  string
		configured string
	}{
		{"equal short", "short", "short"},
		{"unequal long", "a", strings.Repeat("b", 4096)},
	} {
		widths = nil
		EqualSecret(tc.presented, tc.configured)
		if len(widths) != 1 {
			t.Fatalf("%s: constant-time compare invoked %d times, want 1 (digests bypassed)", tc.name, len(widths))
		}
		if widths[0] != [2]int{sha256.Size, sha256.Size} {
			t.Fatalf("%s: compared operand widths %v, want [%d %d]: raw-length oracle is open", tc.name, widths[0], sha256.Size, sha256.Size)
		}
	}
}

func TestWriteUnauthorized(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteUnauthorized(rec)

	res := rec.Result()
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", res.StatusCode, http.StatusUnauthorized)
	}
	if got := res.Header.Get("WWW-Authenticate"); got != `Bearer realm="cortex"` {
		t.Fatalf("WWW-Authenticate = %q, want %q", got, `Bearer realm="cortex"`)
	}
	if got := res.Header.Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want application/json; charset=utf-8", got)
	}

	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("decode envelope %q: %v", raw, err)
	}
	if envelope.Error.Code != "unauthorized" {
		t.Fatalf("error.code = %q, want %q", envelope.Error.Code, "unauthorized")
	}
	if envelope.Error.Message != "valid bearer token required" {
		t.Fatalf("error.message = %q, want %q", envelope.Error.Message, "valid bearer token required")
	}

	var flat map[string]any
	if err := json.Unmarshal(raw, &flat); err != nil {
		t.Fatalf("decode flat map %q: %v", raw, err)
	}
	if _, ok := flat["code"]; ok {
		t.Fatalf("401 envelope %q is flat; want the nested {\"error\":{\"code\",\"message\"}} shape", raw)
	}
	if _, ok := flat["error"].(map[string]any); !ok {
		t.Fatalf("401 envelope %q has no nested error object", raw)
	}
}
