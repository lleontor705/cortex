package transportpolicy

import (
	"net/http"
	"net/url"
	"testing"
)

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse(%q) = %v", raw, err)
	}
	return u
}

func TestCheckBearerRedirectNilTarget(t *testing.T) {
	tests := []struct {
		name string
		req  *http.Request
	}{
		{name: "nil request", req: nil},
		{name: "request without URL", req: &http.Request{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckBearerRedirect(tt.req, nil)
			policyErr, ok := err.(*Error)
			if !ok {
				t.Fatalf("error is %T (%v), want *transportpolicy.Error", err, err)
			}
			if policyErr.Code != CodeInvalidURL {
				t.Fatalf("code = %q, want %q", policyErr.Code, CodeInvalidURL)
			}
		})
	}
}

func TestCheckBearerRedirectInvalidOrigins(t *testing.T) {
	tests := []struct {
		name   string
		via    []*http.Request
		target *url.URL
	}{
		{
			name:   "malformed original origin rejected",
			via:    []*http.Request{{URL: &url.URL{}}},
			target: mustParseURL(t, "https://cortex.example.com/api"),
		},
		{
			name:   "malformed redirect target origin rejected",
			via:    []*http.Request{{URL: mustParseURL(t, "https://cortex.example.com/api")}},
			target: mustParseURL(t, "https:"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &http.Request{Method: http.MethodGet, URL: tt.target}
			err := CheckBearerRedirect(req, tt.via)
			policyErr, ok := err.(*Error)
			if !ok {
				t.Fatalf("error is %T (%v), want *transportpolicy.Error", err, err)
			}
			if policyErr.Code != CodeInvalidURL {
				t.Fatalf("code = %q, want %q", policyErr.Code, CodeInvalidURL)
			}
		})
	}
}

func TestOriginKeyRejections(t *testing.T) {
	tests := []struct {
		name string
		in   *url.URL
	}{
		{name: "nil URL", in: nil},
		{name: "missing host", in: &url.URL{Scheme: "https"}},
		{name: "missing scheme", in: &url.URL{Host: "cortex.example.com"}},
		{name: "unsupported scheme", in: &url.URL{Scheme: "ftp", Host: "cortex.example.com"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := originKey(tt.in); err == nil {
				t.Fatalf("originKey(%+v) = nil error, want rejection", tt.in)
			}
		})
	}
}

func TestOriginKeyCanonicalizesDefaults(t *testing.T) {
	tests := []struct {
		name string
		in   *url.URL
		want string
	}{
		{
			name: "http default port",
			in:   &url.URL{Scheme: "http", Host: "127.0.0.1"},
			want: "http://127.0.0.1:80",
		},
		{
			name: "https default port and canonical host",
			in:   &url.URL{Scheme: "HTTPS", Host: "Example.COM."},
			want: "https://example.com:443",
		},
		{
			name: "explicit port preserved",
			in:   &url.URL{Scheme: "http", Host: "127.0.0.1:7001"},
			want: "http://127.0.0.1:7001",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := originKey(tt.in)
			if err != nil {
				t.Fatalf("originKey(%+v) = %v, want success", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("originKey(%+v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestDefaultPort(t *testing.T) {
	tests := []struct {
		scheme string
		want   string
	}{
		{scheme: "https", want: "443"},
		{scheme: "http", want: "80"},
		{scheme: "ftp", want: "80"},
	}
	for _, tt := range tests {
		t.Run(tt.scheme, func(t *testing.T) {
			if got := defaultPort(tt.scheme); got != tt.want {
				t.Fatalf("defaultPort(%q) = %q, want %q", tt.scheme, got, tt.want)
			}
		})
	}
}
