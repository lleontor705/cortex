package domain_test

import (
	"reflect"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

// serverShapeParams mirrors the identity inputs the PostgreSQL request plane
// feeds newStaticBearerVerifier from administrator configuration.
func serverShapeParams() domain.SyntheticPrincipalParams {
	return domain.SyntheticPrincipalParams{
		Subject:      "svc-tenant-a",
		OrgID:        "tenant-a",
		WorkspaceID:  "ws-a",
		AuthMethod:   "static",
		GrantDigest:  "rack-1-digest",
		GrantVersion: 7,
	}
}

// localShapeParams mirrors the fixed identity the SQLite single-user plane
// reports from GET /api/me (internal/http/me_local.go, parity_ops.go).
func localShapeParams() domain.SyntheticPrincipalParams {
	return domain.SyntheticPrincipalParams{
		Subject:      "local-owner",
		OrgID:        "local",
		WorkspaceID:  "local",
		AuthMethod:   "static",
		GrantDigest:  "static-single-tenant",
		GrantVersion: 1,
	}
}

// TestNewSyntheticPrincipalServerLiteralShape pins the constructor output to the
// exact domain.Principal literal internal/platform/server/static_verifier.go
// assembles today, field for field. Any grant drift (role, wildcards, tier,
// type) breaks this test.
func TestNewSyntheticPrincipalServerLiteralShape(t *testing.T) {
	got := domain.NewSyntheticPrincipal(serverShapeParams())
	want := domain.Principal{
		Subject:                 "svc-tenant-a",
		Type:                    "service_account",
		OrgID:                   "tenant-a",
		WorkspaceIDs:            []string{"ws-a"},
		Roles:                   []string{"owner"},
		AuthMethod:              "static",
		GrantDigest:             "rack-1-digest",
		GrantVersion:            7,
		RateLimitTier:           "standard",
		ProjectIDs:              []string{"*"},
		ClassificationClearance: []string{"*"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("synthetic principal shape mismatch (server literal):\n got %+v\nwant %+v", got, want)
	}
}

// TestNewSyntheticPrincipalLocalLiteralShape pins the constructor output to the
// exact domain.Principal nested inside the api.Principal literals at
// internal/http/me_local.go and parity_ops.go. The adopters supply only the
// identity, so the canonical grants must come out identical.
func TestNewSyntheticPrincipalLocalLiteralShape(t *testing.T) {
	got := domain.NewSyntheticPrincipal(localShapeParams())
	want := domain.Principal{
		Subject:                 "local-owner",
		Type:                    "service_account",
		OrgID:                   "local",
		WorkspaceIDs:            []string{"local"},
		Roles:                   []string{"owner"},
		AuthMethod:              "static",
		GrantDigest:             "static-single-tenant",
		GrantVersion:            1,
		RateLimitTier:           "standard",
		ProjectIDs:              []string{"*"},
		ClassificationClearance: []string{"*"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("synthetic principal shape mismatch (local literal):\n got %+v\nwant %+v", got, want)
	}
}

// TestNewSyntheticPrincipalCentralizesGrants proves the authorization grants are
// constructor-owned constants shared by every site: varying only identity inputs
// must never vary roles, wildcards, tier, or type.
func TestNewSyntheticPrincipalCentralizesGrants(t *testing.T) {
	for name, params := range map[string]domain.SyntheticPrincipalParams{
		"server": serverShapeParams(),
		"local":  localShapeParams(),
	} {
		got := domain.NewSyntheticPrincipal(params)
		if !reflect.DeepEqual(got.Roles, []string{domain.SyntheticPrincipalRole}) {
			t.Errorf("%s: Roles = %v, want [%s]", name, got.Roles, domain.SyntheticPrincipalRole)
		}
		if !reflect.DeepEqual(got.ProjectIDs, []string{domain.SyntheticPrincipalGrantWildcard}) {
			t.Errorf("%s: ProjectIDs = %v, want [%s]", name, got.ProjectIDs, domain.SyntheticPrincipalGrantWildcard)
		}
		if !reflect.DeepEqual(got.ClassificationClearance, []string{domain.SyntheticPrincipalGrantWildcard}) {
			t.Errorf("%s: ClassificationClearance = %v, want [%s]", name, got.ClassificationClearance, domain.SyntheticPrincipalGrantWildcard)
		}
		if got.RateLimitTier != domain.SyntheticPrincipalRateLimitTier {
			t.Errorf("%s: RateLimitTier = %q, want %q", name, got.RateLimitTier, domain.SyntheticPrincipalRateLimitTier)
		}
		if got.Type != domain.SyntheticPrincipalType {
			t.Errorf("%s: Type = %q, want %q", name, got.Type, domain.SyntheticPrincipalType)
		}
		if got.AuthMethod != params.AuthMethod {
			t.Errorf("%s: AuthMethod = %q, want %q", name, got.AuthMethod, params.AuthMethod)
		}
		if !reflect.DeepEqual(got.WorkspaceIDs, []string{params.WorkspaceID}) {
			t.Errorf("%s: WorkspaceIDs = %v, want [%s]", name, got.WorkspaceIDs, params.WorkspaceID)
		}
	}
}

// TestNewSyntheticPrincipalAuthMethodParameterized proves the caller controls the
// authentication method while the grants stay canonical.
func TestNewSyntheticPrincipalAuthMethodParameterized(t *testing.T) {
	for _, method := range []string{"static", "api_key", "client_credentials"} {
		params := serverShapeParams()
		params.AuthMethod = method
		got := domain.NewSyntheticPrincipal(params)
		if got.AuthMethod != method {
			t.Fatalf("AuthMethod = %q, want %q", got.AuthMethod, method)
		}
		if !reflect.DeepEqual(got.Roles, []string{"owner"}) {
			t.Fatalf("AuthMethod %q changed canonical roles: %v", method, got.Roles)
		}
	}
}
