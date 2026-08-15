package transportpolicy

import (
	"net/http"
	"net/url"
	"testing"
)

func TestValidateBearerDestination(t *testing.T) {
	tests := []struct {
		name     string
		rawURL   string
		wantErr  bool
		wantCode string
	}{
		{name: "https remote accepted", rawURL: "https://cortex.example.com"},
		{name: "https remote with port and path accepted", rawURL: "https://cortex.example.com:8443/api/sync/changes?cursor=0"},
		{name: "http strict loopback IPv4 accepted", rawURL: "http://127.0.0.1:7438/api/sync/changes", wantErr: false},
		{name: "http loopback IPv4 127/8 accepted", rawURL: "http://127.0.0.2"},
		{name: "http loopback IPv4 no port accepted", rawURL: "http://127.0.0.1"},
		{name: "http strict loopback IPv6 accepted", rawURL: "http://[::1]:7438/api"},
		{name: "http localhost accepted", rawURL: "http://localhost:7438"},
		{name: "http remote hostname rejected", rawURL: "http://cortex.example.com", wantErr: true, wantCode: CodeInsecureScheme},
		{name: "http private IPv4 rejected", rawURL: "http://192.168.1.10:7438", wantErr: true, wantCode: CodeInsecureScheme},
		{name: "http link-local IPv4 rejected", rawURL: "http://169.254.1.1", wantErr: true, wantCode: CodeInsecureScheme},
		{name: "http public IPv4 rejected", rawURL: "http://8.8.8.8", wantErr: true, wantCode: CodeInsecureScheme},
		{name: "http private IPv6 rejected", rawURL: "http://[fd00::1]", wantErr: true, wantCode: CodeInsecureScheme},
		{name: "http IPv4-mapped IPv6 loopback rejected", rawURL: "http://[::ffff:127.0.0.1]:7438/api", wantErr: true, wantCode: CodeInsecureScheme},
		{name: "http IPv4-mapped IPv6 loopback no port rejected", rawURL: "http://[::ffff:127.0.0.1]", wantErr: true, wantCode: CodeInsecureScheme},
		{name: "http IPv4-mapped 127/8 variant rejected", rawURL: "http://[::ffff:127.0.0.2]", wantErr: true, wantCode: CodeInsecureScheme},
		{name: "http IPv4-mapped uppercase rejected", rawURL: "http://[::FFFF:127.0.0.1]", wantErr: true, wantCode: CodeInsecureScheme},
		{name: "http IPv4-mapped full hex form rejected", rawURL: "http://[0:0:0:0:0:ffff:127.0.0.1]", wantErr: true, wantCode: CodeInsecureScheme},
		{name: "http IPv4-mapped compressed hex form rejected", rawURL: "http://[::ffff:7f00:1]", wantErr: true, wantCode: CodeInsecureScheme},
		{name: "http IPv4-compatible IPv6 loopback rejected", rawURL: "http://[::127.0.0.1]", wantErr: true, wantCode: CodeInsecureScheme},
		{name: "localhost subdomain rejected", rawURL: "http://localhost.evil.example", wantErr: true, wantCode: CodeInsecureScheme},
		{name: "dotless non-loopback name rejected", rawURL: "http://intranet", wantErr: true, wantCode: CodeInsecureScheme},
		{name: "non-HTTP scheme rejected", rawURL: "ftp://cortex.example.com", wantErr: true, wantCode: CodeUnsupportedScheme},
		{name: "empty URL rejected", rawURL: "", wantErr: true, wantCode: CodeInvalidURL},
		{name: "hostless URL rejected", rawURL: "http://", wantErr: true, wantCode: CodeInvalidURL},
		{name: "relative URL rejected", rawURL: "/api/sync/changes", wantErr: true, wantCode: CodeInvalidURL},
		{name: "garbage URL rejected", rawURL: "://not a url%", wantErr: true, wantCode: CodeInvalidURL},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateBearerDestination(tt.rawURL)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ValidateBearerDestination(%q) = nil, want rejection %q", tt.rawURL, tt.wantCode)
				}
				var policyErr *Error
				if e, ok := err.(*Error); !ok {
					t.Fatalf("error is %T, want *transportpolicy.Error", err)
				} else {
					policyErr = e
				}
				if tt.wantCode != "" && policyErr.Code != tt.wantCode {
					t.Fatalf("code = %q, want %q (reason: %s)", policyErr.Code, tt.wantCode, policyErr.Reason)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidateBearerDestination(%q) = %v, want accepted", tt.rawURL, err)
			}
		})
	}
}

func TestIsStrictLoopbackHostNativeExact(t *testing.T) {
	// Strict loopback sanctions only the exact native representations of the
	// loopback address: dotted-quad 127/8, the IPv6 loopback address ::1 (in
	// any valid IPv6 spelling of that one address), and the exact dotless
	// RFC 6761 name. IPv4-mapped IPv6 spellings must be rejected.
	accept := []string{
		"127.0.0.1",
		"127.0.0.2",
		"127.200.3.4",
		"::1",
		"0:0:0:0:0:0:0:1",
		"localhost",
		"LOCALHOST",
		" 127.0.0.1 ",
	}
	reject := []string{
		"::ffff:127.0.0.1",
		"::FFFF:127.0.0.1",
		"0:0:0:0:0:ffff:127.0.0.1",
		"::ffff:7f00:1",
		"::127.0.0.1",
		"::ffff:192.168.1.10",
		"::ffff:8.8.8.8",
		"192.168.1.10",
		"8.8.8.8",
		"localhost.",
		"localhost.evil.example",
		"intranet",
		"",
	}
	for _, host := range accept {
		if !IsStrictLoopbackHost(host) {
			t.Errorf("IsStrictLoopbackHost(%q) = false, want true (native loopback form)", host)
		}
	}
	for _, host := range reject {
		if IsStrictLoopbackHost(host) {
			t.Errorf("IsStrictLoopbackHost(%q) = true, want false (non-native or non-loopback)", host)
		}
	}
}

func TestCheckBearerRedirect(t *testing.T) {
	newReq := func(raw string) *http.Request {
		t.Helper()
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("parse %q: %v", raw, err)
		}
		return &http.Request{Method: http.MethodGet, URL: u, Header: make(http.Header)}
	}
	tests := []struct {
		name     string
		original string
		target   string
		wantCode string
	}{
		{name: "same origin HTTPS allowed", original: "https://cortex.example.com/api", target: "https://cortex.example.com/api/v2"},
		{name: "same origin with default port normalization allowed", original: "https://cortex.example.com/api", target: "https://cortex.example.com:443/api/v2"},
		{name: "same origin HTTP loopback allowed", original: "http://127.0.0.1:7001/api", target: "http://127.0.0.1:7001/other"},
		{name: "scheme downgrade rejected", original: "https://cortex.example.com/api", target: "http://cortex.example.com/api", wantCode: CodeSchemeDowngrade},
		{name: "downgrade to loopback rejected", original: "https://cortex.example.com/api", target: "http://127.0.0.1:8080/api", wantCode: CodeSchemeDowngrade},
		{name: "cross-origin HTTPS rejected", original: "https://cortex.example.com/api", target: "https://evil.example.com/api", wantCode: CodeOriginChange},
		{name: "cross-origin hostname case and trailing dot still same host", original: "https://cortex.example.com/api", target: "https://Cortex.Example.com./api"},
		{name: "cross-port HTTP loopback rejected", original: "http://127.0.0.1:7001/api", target: "http://127.0.0.1:7002/api", wantCode: CodeOriginChange},
		{name: "cross-origin IPv6 rejected", original: "http://[::1]:7001/api", target: "http://[::1]:7002/api", wantCode: CodeOriginChange},
		{name: "non-HTTP redirect scheme rejected", original: "https://cortex.example.com/api", target: "file:///etc/passwd", wantCode: CodeUnsupportedScheme},
		{name: "insecure redirect destination rejected", original: "http://127.0.0.1:7001/api", target: "http://192.0.2.5/api", wantCode: CodeInsecureScheme},
		{name: "empty via allowed (initial request)", original: "https://cortex.example.com/api", target: "https://cortex.example.com/api"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			via := []*http.Request{newReq(tt.original)}
			if tt.name == "empty via allowed (initial request)" {
				via = nil
			}
			err := CheckBearerRedirect(newReq(tt.target), via)
			if tt.wantCode == "" {
				if err != nil {
					t.Fatalf("CheckBearerRedirect(%q -> %q) = %v, want allowed", tt.original, tt.target, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("CheckBearerRedirect(%q -> %q) = nil, want rejection %q", tt.original, tt.target, tt.wantCode)
			}
			var policyErr *Error
			if e, ok := err.(*Error); !ok {
				t.Fatalf("error is %T, want *transportpolicy.Error", err)
			} else {
				policyErr = e
			}
			if policyErr.Code != tt.wantCode {
				t.Fatalf("code = %q, want %q (reason: %s)", policyErr.Code, tt.wantCode, policyErr.Reason)
			}
		})
	}
}

func TestPolicyErrorNeverEchoesUserInfo(t *testing.T) {
	raw := "https://user:supersecret%40x@cortex.example.com"
	_ = raw
	// A rejected insecure variant with embedded credentials must not leak them.
	insecure := "http://user:supersecret%40x@cortex.example.com"
	err := ValidateBearerDestination(insecure)
	if err == nil {
		t.Fatal("insecure destination with userinfo must be rejected")
	}
	if got := err.Error(); contains(got, "supersecret") {
		t.Fatalf("error message leaks userinfo: %s", got)
	}
}

func contains(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
