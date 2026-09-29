package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/authz"
	"github.com/lleontor705/cortex/v2/internal/config"
	"github.com/lleontor705/cortex/v2/internal/domain"
)

func TestStaticBearerVerifierExactMatchYieldsSyntheticPrincipal(t *testing.T) {
	cfg := validBootstrapConfig()
	verifier, err := newStaticBearerVerifier(cfg)
	if err != nil {
		t.Fatalf("newStaticBearerVerifier() = %v", err)
	}

	got, err := verifier.VerifyToken(context.Background(), cfg.HTTP.Token, "")
	if err != nil {
		t.Fatalf("VerifyToken(exact bearer) = %v", err)
	}
	want := domain.Principal{
		Subject:                 cfg.Server.PrincipalSubject,
		Type:                    "service_account",
		OrgID:                   cfg.Server.TenantID,
		WorkspaceIDs:            []string{cfg.Server.WorkspaceID},
		Roles:                   []string{string(authz.RoleOwner)},
		AuthMethod:              "static",
		GrantDigest:             staticGrantDigest,
		GrantVersion:            1,
		RateLimitTier:           "standard",
		ProjectIDs:              []string{"*"},
		ClassificationClearance: []string{"*"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("synthetic principal = %+v, want %+v", got, want)
	}
	if got.ProjectIDs[0] != "*" || got.ClassificationClearance[0] != "*" {
		t.Fatalf("wildcard grants missing: %+v", got)
	}
}

// TestStaticBearerVerifierHonorsConfiguredGrantDigest pins the provenance
// source: a configured grant_digest wins, while an absent one falls back to
// the non-empty constant so the store/audit contracts stay satisfiable.
func TestStaticBearerVerifierHonorsConfiguredGrantDigest(t *testing.T) {
	cfg := validBootstrapConfig()
	cfg.Server.GrantDigest = "configured-provenance-digest"
	verifier, err := newStaticBearerVerifier(cfg)
	if err != nil {
		t.Fatalf("newStaticBearerVerifier() = %v", err)
	}
	got, err := verifier.VerifyToken(context.Background(), cfg.HTTP.Token, "")
	if err != nil {
		t.Fatalf("VerifyToken() = %v", err)
	}
	if got.GrantDigest != "configured-provenance-digest" || got.GrantVersion != 1 {
		t.Fatalf("grant provenance = %q/%d, want configured digest at version 1", got.GrantDigest, got.GrantVersion)
	}
}

// TestStaticBearerVerifierPropagatesResolvedGrantVersion is the HIGH-1
// regression guard. After a canonical-grant reconcile bumps the sticky
// grant_version, the composition overwrites the deprecated configured value
// with the actor row's real version; the synthetic principal MUST carry that
// propagated version so migration 112's FOR SHARE revalidation matches.
// Re-hardcoding the assembled version to the historical 1 turns this red.
func TestStaticBearerVerifierPropagatesResolvedGrantVersion(t *testing.T) {
	cfg := validBootstrapConfig()
	cfg.Server.GrantDigest = "static:" + strings.Repeat("a", 64)
	cfg.Server.GrantVersion = 2
	verifier, err := newStaticBearerVerifier(cfg)
	if err != nil {
		t.Fatalf("newStaticBearerVerifier() = %v", err)
	}
	got, err := verifier.VerifyToken(context.Background(), cfg.HTTP.Token, "")
	if err != nil {
		t.Fatalf("VerifyToken() = %v", err)
	}
	if got.GrantVersion != 2 {
		t.Fatalf("grant version = %d, want the propagated row version 2", got.GrantVersion)
	}
	if got.GrantDigest != cfg.Server.GrantDigest {
		t.Fatalf("grant digest = %q, want the resolved provenance %q", got.GrantDigest, cfg.Server.GrantDigest)
	}
}

// TestStaticBearerVerifierRejectsNegativeGrantVersion pins the fail-closed
// startup contract: a negative resolved version can never yield a principal.
// A zero version remains the deprecated configured default and keeps the
// historical version-1 shape covered by the exact-match test above.
func TestStaticBearerVerifierRejectsNegativeGrantVersion(t *testing.T) {
	cfg := validBootstrapConfig()
	cfg.Server.GrantVersion = -1
	if _, err := newStaticBearerVerifier(cfg); err == nil {
		t.Fatal("newStaticBearerVerifier(negative grant_version) = nil error, want fail-closed refusal")
	}
}

// TestStaticBearerVerifierFailsClosedForAnyByteDifference covers the byte-exact
// contract: a secret that differs in any byte, is empty, or carries
// surrounding whitespace must be rejected without leaking a principal.
func TestStaticBearerVerifierFailsClosedForAnyByteDifference(t *testing.T) {
	cfg := validBootstrapConfig()
	verifier, err := newStaticBearerVerifier(cfg)
	if err != nil {
		t.Fatalf("newStaticBearerVerifier() = %v", err)
	}
	token := cfg.HTTP.Token

	cases := []struct {
		name   string
		secret string
	}{
		{"empty", ""},
		{"single byte changed", "configured-bootstrap-beareR"},
		{"case difference", "Configured-bootstrap-bearer"},
		{"truncated prefix", "configured-bootstrap-bear"},
		{"extended", token + "x"},
		{"leading whitespace", " " + token},
		{"trailing whitespace", token + " "},
		{"surrounding newline", "\n" + token + "\n"},
		{"unrelated secret", "ctx_user_token"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			principal, err := verifier.VerifyToken(context.Background(), tc.secret, "")
			if !errors.Is(err, errInvalidBearer) {
				t.Fatalf("VerifyToken(%q) error = %v, want errInvalidBearer", tc.secret, err)
			}
			if !reflect.DeepEqual(principal, domain.Principal{}) {
				t.Fatalf("rejected bearer returned a principal: %+v", principal)
			}
		})
	}
}

func TestStaticBearerVerifierRequiresSingleTenantIdentity(t *testing.T) {
	cases := []struct {
		name  string
		key   string
		apply func(*config.Config)
	}{
		{"missing tenant", "tenant_id", func(c *config.Config) { c.Server.TenantID = "" }},
		{"missing workspace", "workspace_id", func(c *config.Config) { c.Server.WorkspaceID = "" }},
		{"missing subject", "principal_subject", func(c *config.Config) { c.Server.PrincipalSubject = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validBootstrapConfig()
			tc.apply(&cfg)
			if _, err := newStaticBearerVerifier(cfg); err == nil || !strings.Contains(err.Error(), tc.key) {
				t.Fatalf("newStaticBearerVerifier() error = %v, want key %q", err, tc.key)
			}
		})
	}
}

// TestStaticBearerVerifierDrivesStandardUnauthorizedEnvelope exercises the
// constant-time verification path through the request middleware: the exact
// bearer yields the synthetic principal, and any failure yields the standard
// 401 envelope with no factory call.
func TestStaticBearerVerifierDrivesStandardUnauthorizedEnvelope(t *testing.T) {
	cfg := validBootstrapConfig()
	verifier, err := newStaticBearerVerifier(cfg)
	if err != nil {
		t.Fatalf("newStaticBearerVerifier() = %v", err)
	}
	factoryCalls := 0
	auth := requestAuthenticator{
		verifier: verifier,
		factory: operationsFactoryFunc(func(_ context.Context, principal domain.Principal) (Operations, error) {
			factoryCalls++
			if principal.Subject != cfg.Server.PrincipalSubject {
				t.Fatalf("factory principal = %+v", principal)
			}
			return newFakeOperations(), nil
		}),
		workspace: workspaceSelector{defaultWorkspace: cfg.Server.WorkspaceID},
	}
	handler := auth.middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	serve := func(bearer string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
		req.Header.Set("Authorization", "Bearer "+bearer)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	if rec := serve(cfg.HTTP.Token); rec.Code != http.StatusNoContent {
		t.Fatalf("exact bearer status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if factoryCalls != 1 {
		t.Fatalf("factory calls = %d, want 1", factoryCalls)
	}

	for _, rejected := range []string{" " + cfg.HTTP.Token, cfg.HTTP.Token + " ", "wrong-bearer"} {
		rec := serve(rejected)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("secret %q status = %d, want %d", rejected, rec.Code, http.StatusUnauthorized)
		}
		if got := rec.Header().Get("WWW-Authenticate"); got != `Bearer realm="cortex"` {
			t.Fatalf("WWW-Authenticate = %q", got)
		}
	}
	if factoryCalls != 1 {
		t.Fatalf("factory calls after rejections = %d, want 1", factoryCalls)
	}
}
