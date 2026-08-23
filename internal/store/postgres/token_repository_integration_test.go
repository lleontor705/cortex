//go:build postgres_integration

package postgres

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lleontor705/cortex/v2/internal/authz"
	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/identity"
)

var provenancePattern = regexp.MustCompile(`^v1:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}:[0-9a-f]{64}$`)

// storeBoundAs builds an AuthorizedStore for an already-verified principal
// whose GrantDigest is verify-minted binding provenance, then proves the
// mediated bind succeeds by opening a transaction.
func bindSucceeds(t *testing.T, h *postgresHarness, tenant, subject uuid.UUID, provenance string, grantVersion int64) {
	t.Helper()
	p := domain.Principal{Subject: subject.String(), Type: "user", OrgID: tenant.String(), GrantDigest: provenance, GrantVersion: grantVersion}
	ac := authz.AuthorizedContext{Principal: p, Tenant: domain.TenantContext{TenantID: tenant.String()}, GrantDigest: provenance}
	store, err := NewAuthorizedStore(h.pool, ac)
	if err != nil {
		t.Fatalf("authorized store: %v", err)
	}
	tx, err := store.store.BeginTx(context.Background())
	if err != nil {
		t.Fatalf("mediated bind rejected: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
}

func bindRejected(t *testing.T, h *postgresHarness, subject uuid.UUID, digest string, version int64) {
	t.Helper()
	tx, err := h.pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if rollbackErr := tx.Rollback(context.Background()); rollbackErr != nil {
			t.Errorf("rollback rejected-bind transaction: %v", rollbackErr)
		}
	}()
	if _, err := tx.Exec(context.Background(), `SELECT public.cortex_bind_principal($1,$2,$3)`, subject, digest, version); err == nil {
		provided := digest != ""
		t.Fatalf("bind with digest input (provided=%t) at version %d unexpectedly accepted", provided, version)
	}
}

func TestPostgresTokenRepositoryLifecycleAndRLS(t *testing.T) {
	h := newPostgresHarness(t)
	ctx := context.Background()
	tenant, sa := uuid.New(), uuid.New()
	if _, err := h.admin.Exec(ctx, `INSERT INTO organizations(tenant_id,name) VALUES($1,$2)`, tenant, "token-org"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.admin.Exec(ctx, `INSERT INTO service_accounts(tenant_id,public_id,name) VALUES($1,$2,$3)`, tenant, sa, "token-service"); err != nil {
		t.Fatal(err)
	}
	// The service-account actor carries a configured (non-empty) stored
	// grant digest; under mediation that digest is integrity metadata only
	// and never authenticates a bind.
	if _, err := h.admin.Exec(ctx, `INSERT INTO actor_subjects(tenant_id,subject,actor_type,public_id,grant_digest,grant_version) VALUES($1,$2,'service_account',$3,'token-test-digest',1)`, tenant, sa.String(), sa); err != nil {
		t.Fatal(err)
	}
	admin := newAuthorizedTestStore(t, h, tenant, uuid.Nil, uuid.New())
	issued, err := admin.tokens().Issue(ctx, identity.TokenIssue{Subject: sa.String(), PrincipalType: "service_account", OrgID: tenant.String(), Scopes: []string{"read"}})
	if err != nil {
		t.Fatal(err)
	}
	if !issued.Record.ExpiresAt.IsZero() {
		t.Fatalf("non-expiring token has expiration %v", issued.Record.ExpiresAt)
	}
	principal, err := admin.tokens().Verify(ctx, issued.Secret, "read")
	if err != nil {
		t.Fatal(err)
	}
	if principal.Subject != sa.String() {
		t.Fatal("verified principal subject mismatch")
	}
	if principal.Type != "service_account" {
		t.Fatal("verified principal type mismatch")
	}
	if principal.OrgID != tenant.String() {
		t.Fatal("verified principal org mismatch")
	}
	// Verify returns ONLY verify-minted binding provenance; the configured
	// stored digest of the actor never reaches the principal.
	if !provenancePattern.MatchString(principal.GrantDigest) {
		t.Fatal("grant digest is not verify-minted provenance")
	}
	if principal.GrantVersion != 1 {
		t.Fatalf("grant version=%d, want 1", principal.GrantVersion)
	}
	verifyTx, err := admin.store.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var lastUsed time.Time
	if err := verifyTx.Handle().(pgx.Tx).QueryRow(ctx, `SELECT last_used_at FROM api_tokens WHERE public_id=$1::uuid`, issued.Record.ID).Scan(&lastUsed); err != nil || lastUsed.IsZero() {
		t.Fatalf("last_used_at not persisted: %v %v", lastUsed, err)
	}
	if err := verifyTx.Rollback(); err != nil {
		t.Fatal(err)
	}
	rotated, err := admin.tokens().Rotate(ctx, issued.Record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !rotated.Record.ExpiresAt.IsZero() {
		t.Fatalf("rotated non-expiring token has expiration %v", rotated.Record.ExpiresAt)
	}
	// The mediated routine returns subject_public_id and principal_type;
	// the repository must copy both into the issued record (pre-mediation
	// behavior for Subject, newly available from SQL for PrincipalType).
	if rotated.Record.Subject != sa.String() {
		t.Fatalf("rotated record subject=%q, want %q", rotated.Record.Subject, sa.String())
	}
	if rotated.Record.PrincipalType != "service_account" {
		t.Fatalf("rotated record principal type=%q, want service_account", rotated.Record.PrincipalType)
	}
	if rotated.Record.Name != issued.Record.Name || rotated.Record.ID == issued.Record.ID {
		t.Fatalf("rotated record name/id=%q/%q", rotated.Record.Name, rotated.Record.ID)
	}
	if _, err := admin.tokens().Verify(ctx, issued.Secret, "read"); !errors.Is(err, identity.ErrTokenRevoked) {
		t.Fatalf("rotated token error=%v, want ErrTokenRevoked", err)
	}
	if _, err := admin.tokens().Verify(ctx, rotated.Secret, "read"); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.tokens().Verify(ctx, rotated.Secret, "admin"); !errors.Is(err, identity.ErrInsufficientScope) {
		t.Fatalf("missing scope error=%v, want ErrInsufficientScope", err)
	}
	if _, err := admin.tokens().Verify(ctx, "ctx_unknown_token", ""); !errors.Is(err, identity.ErrInvalidToken) {
		t.Fatalf("unknown token error=%v, want ErrInvalidToken", err)
	}
	if err := admin.tokens().Revoke(ctx, rotated.Record.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.tokens().Verify(ctx, rotated.Secret, "read"); !errors.Is(err, identity.ErrTokenRevoked) {
		t.Fatalf("revoked token error=%v, want ErrTokenRevoked", err)
	}
	// Rejection keeps the historical error contract for both the
	// already-revoked no-op and the unknown-token SQLSTATE.
	if err := admin.tokens().Revoke(ctx, rotated.Record.ID); !errors.Is(err, identity.ErrInvalidToken) {
		t.Fatalf("already-revoked error=%v, want ErrInvalidToken", err)
	}
	if err := admin.tokens().Revoke(ctx, uuid.NewString()); !errors.Is(err, identity.ErrInvalidToken) {
		t.Fatalf("unknown token error=%v, want ErrInvalidToken", err)
	}
	if _, err := admin.tokens().Issue(ctx, identity.TokenIssue{Subject: uuid.NewString(), OrgID: tenant.String()}); err == nil || !strings.Contains(err.Error(), "token subject") {
		t.Fatalf("unknown subject error=%v, want token subject not found", err)
	}
	other := uuid.New()
	if _, err := h.admin.Exec(ctx, `INSERT INTO organizations(tenant_id,name) VALUES($1,$2)`, other, "other"); err != nil {
		t.Fatal(err)
	}
	otherStore := newAuthorizedTestStore(t, h, other, uuid.Nil, uuid.New())
	if _, err := otherStore.tokens().Verify(ctx, rotated.Secret, "read"); !errors.Is(err, identity.ErrInvalidToken) {
		t.Fatalf("cross-tenant token error=%v, want ErrInvalidToken", err)
	}
}

func TestPostgresTokenExpiryMapsToStableError(t *testing.T) {
	h := newPostgresHarness(t)
	ctx := context.Background()
	tenant, sa := uuid.New(), uuid.New()
	if _, err := h.admin.Exec(ctx, `INSERT INTO organizations(tenant_id,name) VALUES($1,$2)`, tenant, "expired-org"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.admin.Exec(ctx, `INSERT INTO service_accounts(tenant_id,public_id,name) VALUES($1,$2,$3)`, tenant, sa, "expired-service"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.admin.Exec(ctx, `INSERT INTO actor_subjects(tenant_id,subject,actor_type,public_id,grant_digest,grant_version) VALUES($1,$2,'service_account',$3,'',1)`, tenant, sa.String(), sa); err != nil {
		t.Fatal(err)
	}
	admin := newAuthorizedTestStore(t, h, tenant, uuid.Nil, uuid.New())
	issued, err := admin.tokens().Issue(ctx, identity.TokenIssue{Subject: sa.String(), OrgID: tenant.String(), Scopes: []string{"read"}, ExpiresAt: time.Now().UTC().Add(-time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.tokens().Verify(ctx, issued.Secret, "read"); !errors.Is(err, identity.ErrTokenExpired) {
		t.Fatalf("expired token error=%v, want ErrTokenExpired", err)
	}
}

// TestPostgresBindingUsesVerifyMintedProvenanceOnly pins the REQ-IDP-008
// regression surface: an actor whose stored grant digest is empty (legacy)
// and an actor whose stored digest is configured both bind ONLY with the
// provenance minted by a successful active-token verification. Configured
// digests, stale versions, tampered MACs, and cross-actor proofs fail
// closed.
func TestPostgresBindingUsesVerifyMintedProvenanceOnly(t *testing.T) {
	h := newPostgresHarness(t)
	ctx := context.Background()
	tenant := uuid.New()
	if _, err := h.admin.Exec(ctx, `INSERT INTO organizations(tenant_id,name) VALUES($1,$2)`, tenant, "bind-org"); err != nil {
		t.Fatal(err)
	}
	admin := newAuthorizedTestStore(t, h, tenant, uuid.Nil, uuid.New())

	legacyActor := uuid.New()
	if _, err := h.admin.Exec(ctx, `INSERT INTO app_users(tenant_id,public_id,email,display_name) VALUES($1,$2,$3,$4)`, tenant, legacyActor, "legacy@bind.test", "legacy"); err != nil {
		t.Fatal(err)
	}
	legacySecret, legacyProvenance := mintBindingProvenance(t, h, tenant, legacyActor, 1, "")
	// The empty-digest legacy actor verifies through the mediated routine:
	// the provenance minted by SQL must equal the harness construction and
	// the actor's stored empty digest is irrelevant to binding.
	legacyPrincipal, err := admin.tokens().Verify(ctx, legacySecret, "")
	if err != nil {
		t.Fatal(err)
	}
	if legacyPrincipal.GrantDigest != legacyProvenance {
		t.Fatal("verify-minted provenance does not match harness mint")
	}
	bindSucceeds(t, h, tenant, legacyActor, legacyPrincipal.GrantDigest, legacyPrincipal.GrantVersion)

	configuredActor := uuid.New()
	if _, err := h.admin.Exec(ctx, `INSERT INTO app_users(tenant_id,public_id,email,display_name) VALUES($1,$2,$3,$4)`, tenant, configuredActor, "configured@bind.test", "configured"); err != nil {
		t.Fatal(err)
	}
	configuredSecret, configuredProvenance := mintBindingProvenance(t, h, tenant, configuredActor, 3, "configured-digest-value")
	configuredPrincipal, err := admin.tokens().Verify(ctx, configuredSecret, "")
	if err != nil {
		t.Fatal(err)
	}
	if configuredPrincipal.GrantVersion != 3 {
		t.Fatalf("configured actor version=%d, want 3", configuredPrincipal.GrantVersion)
	}
	// A non-empty configured stored digest is integrity metadata only: the
	// bind succeeds with verify-minted provenance at the actor's version.
	bindSucceeds(t, h, tenant, configuredActor, configuredProvenance, 3)
	// The configured digest itself no longer authenticates anything.
	bindRejected(t, h, configuredActor, "configured-digest-value", 3)
	bindRejected(t, h, legacyActor, "", 1)
	bindRejected(t, h, legacyActor, legacyProvenance, 2)
	tampered := legacyProvenance[:len(legacyProvenance)-1]
	if last := legacyProvenance[len(legacyProvenance)-1]; last == '0' {
		tampered += "1"
	} else {
		tampered += "0"
	}
	bindRejected(t, h, legacyActor, tampered, 1)
	// A proof minted for a different actor is foreign and fails closed.
	bindRejected(t, h, configuredActor, legacyProvenance, 3)
}

// TestTokenPrincipalVerifierNarrowSurface pins the pre-composition contract
// without touching PostgreSQL: TokenPrincipalVerifier exposes exactly one
// capability — VerifyToken — and never widens into the mutation-bearing
// identity.TokenStore surface. It runs under the postgres_integration tag
// without CORTEX_TEST_POSTGRES_DSN.
func TestTokenPrincipalVerifierNarrowSurface(t *testing.T) {
	ty := reflect.TypeOf((*TokenPrincipalVerifier)(nil))
	if got := ty.NumMethod(); got != 1 {
		t.Fatalf("TokenPrincipalVerifier exposes %d exported methods, want exactly 1", got)
	}
	if _, ok := ty.MethodByName("VerifyToken"); !ok {
		t.Fatal("TokenPrincipalVerifier must expose VerifyToken")
	}
	if m := ty.Method(0); m.Name != "VerifyToken" {
		t.Fatalf("sole exported method is %q, want VerifyToken", m.Name)
	}
	if ty.Implements(reflect.TypeOf((*identity.TokenStore)(nil)).Elem()) {
		t.Fatal("TokenPrincipalVerifier must not implement the mutation-bearing identity.TokenStore surface")
	}
	var _ interface {
		VerifyToken(context.Context, string, string) (identity.Principal, error)
	} = (*TokenPrincipalVerifier)(nil)
	if _, err := NewTokenPrincipalVerifier(nil, uuid.NewString()); err == nil {
		t.Fatal("nil pool must fail closed")
	}
	if _, err := NewTokenPrincipalVerifier(nil, "not-a-uuid"); err == nil {
		t.Fatal("invalid tenant id must fail closed")
	}
}

// TestTenantDigestKeysOnTenantUUID pins the digest construction the 106 SQL
// contract requires: HMAC-SHA256 keyed by the tenant UUID string over the
// secret. mintBindingProvenance mirrors it byte for byte, so any drift here
// breaks every verification.
func TestTenantDigestKeysOnTenantUUID(t *testing.T) {
	tenant := uuid.NewString()
	secret := "ctx_harness_" + tenant
	mac := hmac.New(sha256.New, []byte(tenant))
	mac.Write([]byte(secret))
	if got := tenantDigest(tenant, secret); !hmac.Equal(got, mac.Sum(nil)) {
		t.Fatal("tenantDigest does not match the tenant-keyed HMAC-SHA256 construction")
	}
}

// TestTokenPrincipalVerifierCanonicalTenantUUID pins the storage/HMAC parity
// the 106 SQL contract requires: uuid.Parse also accepts uppercase, raw,
// URN, and braced spellings, while PostgreSQL derives credential digests
// from the canonical uuid::text form. Every accepted spelling must therefore
// be canonicalized at construction so the stored tenant and its HMAC key
// match the SQL derivation byte for byte.
func TestTokenPrincipalVerifierCanonicalTenantUUID(t *testing.T) {
	secret := "ctx_parity_secret_0123456789"
	canonical := "6f9619ff-8b86-d011-b42d-00c04fc964ff"
	canonicalMac := hmac.New(sha256.New, []byte(canonical))
	canonicalMac.Write([]byte(secret))
	wantDigest := canonicalMac.Sum(nil)

	accepted := map[string]string{
		"canonical": canonical,
		"uppercase": strings.ToUpper(canonical),
		"raw":       "6f9619ff8b86d011b42d00c04fc964ff",
		"urn":       "urn:uuid:" + canonical,
		"braced":    "{" + canonical + "}",
	}
	// The verifier only touches its pool after the pre-pool validation, so
	// an empty pool value keeps this pin DSN-free.
	for name, spelling := range accepted {
		v, err := NewTokenPrincipalVerifier(&pgxpool.Pool{}, spelling)
		if err != nil {
			t.Fatalf("%s spelling %q: constructor rejected an accepted tenant UUID: %v", name, spelling, err)
		}
		if v.tenantID != canonical {
			t.Fatalf("%s spelling %q stored tenant id %q, want canonical %q", name, spelling, v.tenantID, canonical)
		}
		if got := tenantDigest(v.tenantID, secret); !hmac.Equal(got, wantDigest) {
			t.Fatalf("%s spelling %q: digest key drifts from the canonical uuid::text HMAC", name, spelling)
		}
	}

	// Non-UUID input still fails closed with the tenant-context error.
	for _, invalid := range []string{"", "not-a-uuid", "urn:uuid:not-a-uuid", "{6f9619ff-8b86-d011-b42d-00c04fc964ff"} {
		if _, err := NewTokenPrincipalVerifier(&pgxpool.Pool{}, invalid); err == nil {
			t.Fatalf("invalid tenant id %q must fail closed", invalid)
		}
	}
}

// TestPostgresTokenPrincipalVerifierPreComposition proves the narrow
// verifier end-to-end through the real application-role pool without
// constructing any Store: it reproduces the harness-minted provenance byte
// for byte, keeps the stable redacted error taxonomy, never matches a
// foreign tenant's credential, and preserves rotate identity metadata.
func TestPostgresTokenPrincipalVerifierPreComposition(t *testing.T) {
	h := newPostgresHarness(t)
	ctx := context.Background()
	tenant := uuid.New()
	if _, err := h.admin.Exec(ctx, `INSERT INTO organizations(tenant_id,name) VALUES($1,$2)`, tenant, "verifier-org"); err != nil {
		t.Fatal(err)
	}
	if _, err := NewTokenPrincipalVerifier(nil, tenant.String()); err == nil {
		t.Fatal("nil pool must fail closed")
	}
	if _, err := NewTokenPrincipalVerifier(h.pool, "not-a-uuid"); err == nil {
		t.Fatal("invalid tenant id must fail closed")
	}
	verifier, err := NewTokenPrincipalVerifier(h.pool, tenant.String())
	if err != nil {
		t.Fatal(err)
	}

	// Both a legacy empty-digest actor and a configured-digest actor verify
	// through the same narrow surface and receive provenance only.
	legacyActor := uuid.New()
	legacySecret, legacyProvenance := mintBindingProvenance(t, h, tenant, legacyActor, 1, "")
	legacyPrincipal, err := verifier.VerifyToken(ctx, legacySecret, "")
	if err != nil {
		t.Fatal(err)
	}
	if legacyPrincipal.Subject != legacyActor.String() {
		t.Fatal("verifier principal subject mismatch")
	}
	if legacyPrincipal.Type != "user" {
		t.Fatal("verifier principal type mismatch")
	}
	if legacyPrincipal.OrgID != tenant.String() {
		t.Fatal("verifier principal org mismatch")
	}
	if legacyPrincipal.GrantDigest != legacyProvenance {
		t.Fatal("verifier provenance does not match harness mint")
	}
	bindSucceeds(t, h, tenant, legacyActor, legacyPrincipal.GrantDigest, legacyPrincipal.GrantVersion)

	configuredActor := uuid.New()
	configuredSecret, configuredProvenance := mintBindingProvenance(t, h, tenant, configuredActor, 2, "configured-digest-value")
	configuredPrincipal, err := verifier.VerifyToken(ctx, configuredSecret, "")
	if err != nil {
		t.Fatal(err)
	}
	if configuredPrincipal.GrantVersion != 2 {
		t.Fatalf("configured actor version=%d, want 2", configuredPrincipal.GrantVersion)
	}
	if configuredPrincipal.GrantDigest != configuredProvenance {
		t.Fatal("configured actor provenance does not match harness mint")
	}
	// The configured stored digest itself never authenticates a bind.
	bindRejected(t, h, configuredActor, "configured-digest-value", 2)

	// Stable redacted errors through the narrow surface.
	if _, err := verifier.VerifyToken(ctx, configuredSecret, "admin"); !errors.Is(err, identity.ErrInsufficientScope) {
		t.Fatalf("missing scope error=%v, want ErrInsufficientScope", err)
	}
	if _, err := verifier.VerifyToken(ctx, "ctx_unknown_token", ""); !errors.Is(err, identity.ErrInvalidToken) {
		t.Fatalf("unknown token error=%v, want ErrInvalidToken", err)
	}
	if _, err := verifier.VerifyToken(ctx, "short", ""); !errors.Is(err, identity.ErrInvalidToken) {
		t.Fatalf("short secret error=%v, want ErrInvalidToken", err)
	}

	// A verifier pinned to a foreign tenant can never match this tenant's
	// credential: the HMAC key is the fixed tenant UUID.
	otherTenant := uuid.New()
	if _, err := h.admin.Exec(ctx, `INSERT INTO organizations(tenant_id,name) VALUES($1,$2)`, otherTenant, "verifier-other"); err != nil {
		t.Fatal(err)
	}
	otherVerifier, err := NewTokenPrincipalVerifier(h.pool, otherTenant.String())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := otherVerifier.VerifyToken(ctx, configuredSecret, ""); !errors.Is(err, identity.ErrInvalidToken) {
		t.Fatalf("cross-tenant verify error=%v, want ErrInvalidToken", err)
	}

	// Rotate identity metadata is preserved through the narrow surface: the
	// rotated-away credential dies with ErrTokenRevoked and the replacement
	// verifies with the same subject identity and fresh provenance.
	admin := newAuthorizedTestStore(t, h, tenant, uuid.Nil, uuid.New())
	issued, err := admin.tokens().Issue(ctx, identity.TokenIssue{Subject: legacyActor.String(), PrincipalType: "user", OrgID: tenant.String(), Scopes: []string{"read"}})
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := admin.tokens().Rotate(ctx, issued.Record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rotated.Record.Subject != legacyActor.String() || rotated.Record.PrincipalType != "user" {
		t.Fatalf("rotated identity metadata=%q/%q", rotated.Record.Subject, rotated.Record.PrincipalType)
	}
	if _, err := verifier.VerifyToken(ctx, issued.Secret, "read"); !errors.Is(err, identity.ErrTokenRevoked) {
		t.Fatalf("rotated-away secret error=%v, want ErrTokenRevoked", err)
	}
	replacement, err := verifier.VerifyToken(ctx, rotated.Secret, "read")
	if err != nil {
		t.Fatal(err)
	}
	if replacement.Subject != legacyActor.String() || replacement.Type != "user" {
		t.Fatalf("replacement identity=%q/%q", replacement.Subject, replacement.Type)
	}
	if !provenancePattern.MatchString(replacement.GrantDigest) {
		t.Fatal("replacement grant digest is not verify-minted provenance")
	}

	// Expired credentials keep the stable expired error.
	expired, err := admin.tokens().Issue(ctx, identity.TokenIssue{Subject: legacyActor.String(), PrincipalType: "user", OrgID: tenant.String(), Scopes: []string{"read"}, ExpiresAt: time.Now().UTC().Add(-time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifier.VerifyToken(ctx, expired.Secret, "read"); !errors.Is(err, identity.ErrTokenExpired) {
		t.Fatalf("expired token error=%v, want ErrTokenExpired", err)
	}
}

// REQ-BPR-006 redaction for this file and the whole package is enforced by
// the package-wide AST canary TestPackageDiagnosticsRedactProvenance in
// diagnostics_canary_test.go, which superseded the per-file line scanner
// that previously lived here.

// TestPostgresTokenVerifyOverlapsWithoutRowLockWaits proves the PG-02 face of
// migration 108 on the standard harness database: concurrent verifications of
// ONE token fully overlap (the pre-108 same-token FOR UPDATE serialization is
// gone), no verifier waits on a peer verifier's row lock while the telemetry
// winner updates last_used_at, every verification succeeds, and the
// verify-minted provenance is stable across the burst.
func TestPostgresTokenVerifyOverlapsWithoutRowLockWaits(t *testing.T) {
	h := newPostgresHarness(t)
	ctx := context.Background()
	tenant, sa := uuid.New(), uuid.New()
	if _, err := h.admin.Exec(ctx, `INSERT INTO organizations(tenant_id,name) VALUES($1,$2)`, tenant, "overlap-org"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.admin.Exec(ctx, `INSERT INTO service_accounts(tenant_id,public_id,name) VALUES($1,$2,$3)`, tenant, sa, "overlap-service"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.admin.Exec(ctx, `INSERT INTO actor_subjects(tenant_id,subject,actor_type,public_id,grant_digest,grant_version) VALUES($1,$2,'service_account',$3,'overlap-digest',1)`, tenant, sa.String(), sa); err != nil {
		t.Fatal(err)
	}
	admin := newAuthorizedTestStore(t, h, tenant, uuid.Nil, uuid.New())
	issued, err := admin.tokens().Issue(ctx, identity.TokenIssue{Subject: sa.String(), PrincipalType: "service_account", OrgID: tenant.String(), Scopes: []string{"read"}})
	if err != nil {
		t.Fatal(err)
	}
	// Age the telemetry mark past the 30-second throttle so the burst's
	// usage-advisory winner performs a real last_used_at UPDATE while its
	// peers verify the same token.
	if _, err := h.admin.Exec(ctx, `UPDATE api_tokens SET last_used_at = clock_timestamp() - interval '90 seconds' WHERE public_id=$1`, issued.Record.ID); err != nil {
		t.Fatal(err)
	}
	baseline, err := admin.tokens().Verify(ctx, issued.Secret, "read")
	if err != nil {
		t.Fatal(err)
	}

	// Lock-wait sampler: a verifier waiting on any row lock held by a peer
	// verifier is the regression this test exists to catch.
	samplerDone := make(chan struct{})
	lockWaits := make(chan int64, 1)
	go func() {
		defer close(samplerDone)
		var total int64
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			var waiting int64
			if err := h.admin.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%cortex_verify_token_principal%'`).Scan(&waiting); err == nil {
				total += waiting
			}
			time.Sleep(2 * time.Millisecond)
		}
		lockWaits <- total
	}()

	const workers, iters = 8, 5
	var mu sync.Mutex
	failures := 0
	firstErr := ""
	provenanceDrift := false
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				principal, err := admin.tokens().Verify(ctx, issued.Secret, "read")
				if err != nil {
					mu.Lock()
					failures++
					if firstErr == "" {
						firstErr = err.Error()
					}
					mu.Unlock()
					continue
				}
				mu.Lock()
				if principal.GrantDigest != baseline.GrantDigest || principal.GrantVersion != baseline.GrantVersion {
					provenanceDrift = true
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	waits := <-lockWaits
	<-samplerDone
	if failures != 0 {
		t.Fatalf("concurrent same-token verifies failed %d/%d (%s)", failures, workers*iters, firstErr)
	}
	if provenanceDrift {
		t.Fatal("verify-minted provenance or grant version drifted across the concurrent burst")
	}
	if waits != 0 {
		t.Fatalf("same-token verify serialized on row locks: %d lock-wait samples on verifier backends", waits)
	}
	// The telemetry winner advanced the aged mark and authentication kept
	// succeeding afterwards.
	var lastUsed *time.Time
	if err := h.admin.QueryRow(ctx, `SELECT last_used_at FROM api_tokens WHERE public_id=$1`, issued.Record.ID).Scan(&lastUsed); err != nil {
		t.Fatal(err)
	}
	if lastUsed == nil || lastUsed.Before(time.Now().Add(-time.Minute)) {
		t.Fatalf("last_used_at telemetry did not advance from the aged mark: %v", lastUsed)
	}
	if _, err := admin.tokens().Verify(ctx, issued.Secret, "read"); err != nil {
		t.Fatalf("verify after telemetry burst: %v", err)
	}
}

// TestPostgresTokenTelemetryThrottleSkipKeepsAuthentication proves the
// throttled telemetry skip path through the repository: an immediate
// re-verification inside the 30-second window succeeds and leaves the
// monotonic last_used_at mark untouched.
func TestPostgresTokenTelemetryThrottleSkipKeepsAuthentication(t *testing.T) {
	h := newPostgresHarness(t)
	ctx := context.Background()
	tenant, sa := uuid.New(), uuid.New()
	if _, err := h.admin.Exec(ctx, `INSERT INTO organizations(tenant_id,name) VALUES($1,$2)`, tenant, "throttle-org"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.admin.Exec(ctx, `INSERT INTO service_accounts(tenant_id,public_id,name) VALUES($1,$2,$3)`, tenant, sa, "throttle-service"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.admin.Exec(ctx, `INSERT INTO actor_subjects(tenant_id,subject,actor_type,public_id,grant_digest,grant_version) VALUES($1,$2,'service_account',$3,'throttle-digest',1)`, tenant, sa.String(), sa); err != nil {
		t.Fatal(err)
	}
	admin := newAuthorizedTestStore(t, h, tenant, uuid.Nil, uuid.New())
	issued, err := admin.tokens().Issue(ctx, identity.TokenIssue{Subject: sa.String(), PrincipalType: "service_account", OrgID: tenant.String(), Scopes: []string{"read"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.tokens().Verify(ctx, issued.Secret, "read"); err != nil {
		t.Fatal(err)
	}
	var first *time.Time
	if err := h.admin.QueryRow(ctx, `SELECT last_used_at FROM api_tokens WHERE public_id=$1`, issued.Record.ID).Scan(&first); err != nil || first == nil {
		t.Fatalf("first verify did not persist telemetry: %v %v", first, err)
	}
	if _, err := admin.tokens().Verify(ctx, issued.Secret, "read"); err != nil {
		t.Fatalf("throttled re-verify failed authentication: %v", err)
	}
	var second *time.Time
	if err := h.admin.QueryRow(ctx, `SELECT last_used_at FROM api_tokens WHERE public_id=$1`, issued.Record.ID).Scan(&second); err != nil {
		t.Fatal(err)
	}
	if second == nil || first == nil || second.Before(*first) || second.After(first.Add(31*time.Second)) {
		t.Fatalf("throttle skip changed last_used_at outside the monotonic window: first=%v second=%v", first, second)
	}
}
