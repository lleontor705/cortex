package identity

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestJWKPolicyMatchesAlgorithmAndUsage(t *testing.T) {
	tests := []struct {
		alg  string
		key  jwk
		want bool
	}{
		{"RS256", jwk{Kty: "RSA", Alg: "RS256", Use: "sig", KeyOps: []string{"verify"}}, true},
		{"RS256", jwk{Kty: "EC", Alg: "RS256"}, false},
		{"ES256", jwk{Kty: "EC", Crv: "P-256", Alg: "ES256"}, true},
		{"EdDSA", jwk{Kty: "OKP", Crv: "Ed25519", Alg: "EdDSA", KeyOps: []string{"sign"}}, false},
	}
	for _, tt := range tests {
		if got := jwkCompatible(tt.key, tt.alg); got != tt.want {
			t.Errorf("jwkCompatible(%q,%+v)=%v want %v", tt.alg, tt.key, got, tt.want)
		}
	}
}

func TestJWTPolicyAlgorithmKeyMatrix(t *testing.T) {
	for _, alg := range []string{"RS256", "RS384", "RS512"} {
		if !jwkCompatible(jwk{Kty: "RSA", Alg: alg}, alg) {
			t.Errorf("RSA %s rejected", alg)
		}
	}
	for _, tt := range []struct{ alg, crv string }{{"ES256", "P-256"}, {"ES384", "P-384"}, {"ES512", "P-521"}} {
		if !jwkCompatible(jwk{Kty: "EC", Crv: tt.crv, Alg: tt.alg}, tt.alg) {
			t.Errorf("EC %s rejected", tt.alg)
		}
	}
	for _, bad := range []jwk{{Kty: "RSA", Alg: "RS256", Use: "enc"}, {Kty: "RSA", Alg: "RS256", KeyOps: []string{"sign"}}, {Kty: "RSA", Alg: "RS384"}} {
		if jwkCompatible(bad, "RS256") {
			t.Errorf("incompatible key accepted: %+v", bad)
		}
	}
}

func TestDiscoveryFallbackForMissingAndInvalidJWKSURI(t *testing.T) {
	for _, invalid := range []string{"", "not-a-url"} {
		t.Run(invalid, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/.well-known/openid-configuration" {
					_ = json.NewEncoder(w).Encode(map[string]string{"jwks_uri": invalid})
					return
				}
				if r.URL.Path == "/.well-known/oauth-authorization-server" {
					_ = json.NewEncoder(w).Encode(map[string]string{"jwks_uri": "http://" + r.Host + "/jwks"})
					return
				}
				if r.URL.Path == "/jwks" {
					_ = json.NewEncoder(w).Encode(map[string]any{"keys": []jwk{{Kty: "RSA", Kid: "k", Alg: "RS256", N: "AA", E: "Aw"}}})
					return
				}
				http.NotFound(w, r)
			}))
			defer s.Close()
			v := NewOAuthVerifier(OAuthConfig{Issuer: s.URL, CacheTTL: time.Millisecond})
			if err := v.loadKeys(context.Background(), true); err != nil {
				t.Fatal(err)
			}
			if _, ok := v.key("k"); !ok {
				t.Fatal("RFC8414 fallback did not load keys")
			}
		})
	}
}

func FuzzDecodeJWTNoPanic(f *testing.F) {
	f.Add("eyJhbGciOiJSUzI1NiJ9", "e30")
	f.Add("", "")
	f.Fuzz(func(t *testing.T, header, claims string) { _, _, _ = decodeJWT(header, claims) })
}

func FuzzPublicKeyNoPanic(f *testing.F) {
	f.Add("RSA", "AA", "Aw", "", "", "")
	f.Fuzz(func(t *testing.T, kty, n, e, x, y, crv string) {
		_, _ = publicKey(jwk{Kty: kty, N: n, E: e, X: x, Y: y, Crv: crv})
	})
}
