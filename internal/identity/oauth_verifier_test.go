package identity_test

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lleontor705/cortex/internal/identity"
)

func TestOAuthVerifierValidatesIssuerAudienceAndRotation(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	current := key
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/openid-configuration" {
			json.NewEncoder(w).Encode(map[string]string{"issuer": "https://issuer.test", "jwks_uri": "" + "http://" + r.Host + "/jwks"})
			return
		}
		if r.URL.Path == "/jwks" {
			n := base64.RawURLEncoding.EncodeToString(current.PublicKey.N.Bytes())
			e := base64.RawURLEncoding.EncodeToString([]byte{byte(current.PublicKey.E >> 24), byte(current.PublicKey.E >> 16), byte(current.PublicKey.E >> 8), byte(current.PublicKey.E)})
			json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]string{"kty": "RSA", "kid": "one", "alg": "RS256", "n": n, "e": strings.TrimLeft(e, "\x00")}}})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	// Discovery is exercised with a fixed JWKS URL because test servers cannot advertise an external issuer URL.
	v := identity.NewOAuthVerifier(identity.OAuthConfig{Issuer: "https://issuer.test", JWKSURL: server.URL + "/jwks", Audience: []string{"api://cortex"}, ClockSkew: time.Minute})
	token := signTestJWT(t, key, "one", map[string]any{"iss": "https://issuer.test", "sub": "u1", "aud": "api://cortex", "exp": time.Now().Add(time.Hour).Unix(), "nbf": time.Now().Add(-time.Minute).Unix(), "org_id": "org-1", "roles": []string{"admin"}})
	p, err := v.Verify(context.Background(), token, "api://cortex")
	if err != nil {
		t.Fatal(err)
	}
	if p.Subject != "u1" || p.OrgID != "org-1" {
		t.Fatalf("principal: %+v", p)
	}
	if _, err = v.Verify(context.Background(), token, "other"); err == nil {
		t.Fatal("wrong RFC8707 audience accepted")
	}
	bad := signTestJWT(t, key, "one", map[string]any{"iss": "wrong", "sub": "u1", "aud": "api://cortex", "exp": time.Now().Add(time.Hour).Unix()})
	if _, err = v.Verify(context.Background(), bad, "api://cortex"); err == nil {
		t.Fatal("wrong issuer accepted")
	}
}

func signTestJWT(t *testing.T, key *rsa.PrivateKey, kid string, claims map[string]any) string {
	header := map[string]string{"alg": "RS256", "kid": kid, "typ": "JWT"}
	h, _ := json.Marshal(header)
	c, _ := json.Marshal(claims)
	enc := base64.RawURLEncoding
	input := enc.EncodeToString(h) + "." + enc.EncodeToString(c)
	digest := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return input + "." + enc.EncodeToString(sig)
}

func TestClaimsMapperNormalizesScopeAndDefaults(t *testing.T) {
	p, err := (identity.ClaimsMapper{}).Map(map[string]any{"sub": "svc", "scope": "a b", "scp": []any{"ignored"}}, "oidc")
	if err != nil {
		t.Fatal(err)
	}
	if p.Type != "user" || len(p.Scopes) != 2 {
		t.Fatalf("principal=%+v", p)
	}
	if _, err = (identity.ClaimsMapper{}).Map(map[string]any{}, "oidc"); err == nil {
		t.Fatal("missing subject accepted")
	}
}
