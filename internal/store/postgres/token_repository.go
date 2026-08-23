package postgres

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lleontor705/cortex/v2/internal/identity"
)

// TokenRepository persists API credentials in PostgreSQL. Plaintext secrets
// are returned only by Issue and are never stored. Every token lifecycle
// mutation (issue, rotate, revoke) and verification execute exclusively
// through the migration-owned SECURITY DEFINER routines installed by the
// unshipped 106 server migration; the application role holds no direct
// api_tokens write and cannot select token_digest.
type TokenRepository struct{ *Store }

func (s *Store) tokens() *TokenRepository { return &TokenRepository{s} }

var _ identity.TokenStore = (*TokenRepository)(nil)

// List returns metadata only. Secret and digest material are never exposed.
// It reads the non-sensitive api_tokens columns regranted by 106; the digest
// column is not part of the application role's column grant.
func (r *TokenRepository) List(ctx context.Context) (out []identity.TokenRecord, err error) {
	err = r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `SELECT t.public_id::text,t.name,t.token_prefix,COALESCE(au.public_id,sa.public_id)::text,CASE WHEN sa.id IS NOT NULL THEN 'service_account' ELSE 'user' END,t.tenant_id::text,t.scopes,t.workspace_ids,COALESCE(t.expires_at,'epoch'::timestamptz),COALESCE(t.revoked_at,'epoch'::timestamptz),COALESCE(t.last_used_at,'epoch'::timestamptz) FROM api_tokens t LEFT JOIN app_users au ON au.tenant_id=t.tenant_id AND au.id=t.subject_user_id LEFT JOIN service_accounts sa ON sa.tenant_id=t.tenant_id AND sa.id=t.subject_service_account_id WHERE t.tenant_id=public.cortex_current_tenant() ORDER BY t.created_at DESC`)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var r identity.TokenRecord
			if e := rows.Scan(&r.ID, &r.Name, &r.Prefix, &r.Subject, &r.PrincipalType, &r.OrgID, &r.Scopes, &r.Workspaces, &r.ExpiresAt, &r.RevokedAt, &r.LastUsedAt); e != nil {
				return e
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	return
}

func (r *TokenRepository) Issue(ctx context.Context, in identity.TokenIssue) (identity.IssuedToken, error) {
	if in.Subject == "" {
		return identity.IssuedToken{}, identity.ErrInvalidToken
	}
	if in.Workspaces == nil {
		in.Workspaces = []string{}
	}
	if in.Scopes == nil {
		in.Scopes = []string{}
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return identity.IssuedToken{}, err
	}
	secret := "ctx_" + base64.RawURLEncoding.EncodeToString(b)
	digest := r.digest(secret)
	var rec identity.TokenRecord
	var expiresAt *time.Time
	// cortex_issue_api_token derives the stored digest inside SQL from the
	// caller-presented one-time secret, authorizes the bound owner/admin
	// caller, and audits atomically. The digest computed here is only the
	// returned record metadata; it never reaches the database.
	err := r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT token_public_id::text,token_prefix,expires_at FROM public.cortex_issue_api_token($1::uuid,$2::text,$3::text,$4::text[],$5::uuid[],$6::timestamptz,$7::text)`, in.Subject, in.Name, secret, in.Scopes, in.Workspaces, nullTime(in.ExpiresAt), "").Scan(&rec.ID, &rec.Prefix, &expiresAt)
	})
	if err != nil {
		return identity.IssuedToken{}, mapMediatedMutationError(err, "token subject", in.Subject)
	}
	if expiresAt != nil {
		rec.ExpiresAt = *expiresAt
	}
	rec.Name, rec.Digest, rec.Subject, rec.PrincipalType, rec.OrgID = in.Name, base64.RawURLEncoding.EncodeToString(digest), in.Subject, in.PrincipalType, in.OrgID
	rec.Workspaces, rec.Scopes = append([]string(nil), in.Workspaces...), append([]string(nil), in.Scopes...)
	return identity.IssuedToken{Secret: secret, Record: rec}, nil
}

// Verify authenticates a presented secret through
// cortex_verify_token_principal. The routine locks the matched token,
// enforces revocation, expiry, subject liveness and the required scope,
// aggregates durable grants, folds last_used_at into the same transaction,
// and mints the one-time binding provenance consumed by
// cortex_bind_principal. The returned Principal.GrantDigest is that
// verify-minted provenance only — it is bearer-equivalent, must stay in
// memory, and is never a configured or recomputable grant digest.
func (r *TokenRepository) Verify(ctx context.Context, secret, requiredScope string) (identity.Principal, error) {
	if err := validatePresentedSecret(secret); err != nil {
		return identity.Principal{}, err
	}
	var principal identity.Principal
	// Verification is authentication: no principal is bound to the
	// transaction yet, so it runs unbound and the definer routine derives
	// the tenant from the credential itself.
	err := r.unboundTransaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var e error
		principal, e = verifyTokenPrincipal(ctx, tx, r.tenant.TenantID, secret, requiredScope)
		return e
	})
	if err != nil {
		return identity.Principal{}, err
	}
	return principal, nil
}

// preCompositionVerifier is the exact capability TokenPrincipalVerifier is
// allowed to expose: token verification, nothing else. Server middleware
// consumes it by structural typing through its own verifier port.
type preCompositionVerifier interface {
	VerifyToken(ctx context.Context, secret, requiredScope string) (identity.Principal, error)
}

// TokenPrincipalVerifier is the narrow, verification-only PostgreSQL
// capability for the pre-composition window: authentication runs before any
// principal exists, and NewStore requires a verified principal, so a
// middleware verifier cannot hold a repository. The verifier pins ONE fixed
// tenant whose UUID keys the credential digest HMAC; the mediated routine
// derives the actual tenant from the credential itself, so a token issued
// under any other tenant never matches. It holds no principal, no grant
// digest, and no mutation surface — VerifyToken is its only method — and it
// never logs or persists the presented secret or the returned provenance.
type TokenPrincipalVerifier struct {
	pool     *pgxpool.Pool
	tenantID string
}

var _ preCompositionVerifier = (*TokenPrincipalVerifier)(nil)

// NewTokenPrincipalVerifier builds the fixed-tenant verifier over an
// application-role pool. The tenant comes from configuration, never from a
// request; pool and tenant id are validated fail-closed before any
// credential is accepted. uuid.Parse accepts uppercase, raw, URN, and braced
// spellings, but migration 106 derives credential digests from the
// canonical uuid::text form, so the parsed UUID is canonicalized before it
// is stored: every accepted spelling keys the same tenant digest HMAC.
func NewTokenPrincipalVerifier(pool *pgxpool.Pool, tenantID string) (*TokenPrincipalVerifier, error) {
	if pool == nil {
		return nil, errors.New("postgres verifier: nil pool")
	}
	parsed, err := uuid.Parse(tenantID)
	if err != nil {
		return nil, fmt.Errorf("%w: tenant id: %v", ErrTenantContextRequired, err)
	}
	return &TokenPrincipalVerifier{pool: pool, tenantID: parsed.String()}, nil
}

// VerifyToken authenticates a presented secret with the same single
// cortex_verify_token_principal call the repository uses, inside its own
// principal-free transaction: nothing is bound before authentication
// succeeds. Errors keep the stable redacted taxonomy
// (ErrInvalidToken/ErrTokenRevoked/ErrTokenExpired/ErrInsufficientScope)
// and the returned Principal.GrantDigest is verify-minted provenance only.
func (v *TokenPrincipalVerifier) VerifyToken(ctx context.Context, secret, requiredScope string) (identity.Principal, error) {
	if err := validatePresentedSecret(secret); err != nil {
		return identity.Principal{}, err
	}
	var principal identity.Principal
	tx, err := v.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return identity.Principal{}, fmt.Errorf("postgres verifier: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	principal, err = verifyTokenPrincipal(ctx, tx, v.tenantID, secret, requiredScope)
	if err != nil {
		return identity.Principal{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return identity.Principal{}, fmt.Errorf("postgres verifier: commit: %w", err)
	}
	return principal, nil
}

// validatePresentedSecret rejects malformed secrets before any connection is
// opened: repositories can be constructed without a reachable pool in
// validation paths, and an obviously-invalid credential must never reach
// PostgreSQL.
func validatePresentedSecret(secret string) error {
	if len(secret) < 12 {
		return identity.ErrInvalidToken
	}
	return nil
}

// verifyTokenPrincipal executes the single cortex_verify_token_principal
// call on an already-open principal-free transaction and assembles the
// provenance-only principal. It is the one shared verification path for
// TokenRepository.Verify and TokenPrincipalVerifier.VerifyToken so the two
// surfaces cannot drift.
func verifyTokenPrincipal(ctx context.Context, tx pgx.Tx, tenantID, secret, requiredScope string) (identity.Principal, error) {
	if len(secret) < 12 {
		return identity.Principal{}, identity.ErrInvalidToken
	}
	digest := tenantDigest(tenantID, secret)
	var rec identity.TokenRecord
	var provenance string
	var grantVersion int64
	var recRoles, recWorkspaces, recProjects, recClearance, recGrantScopes []string
	var expiresAt, revokedAt, lastUsedAt *time.Time
	err := tx.QueryRow(ctx, `SELECT token_public_id::text,token_name,token_prefix,subject_public_id::text,principal_type,tenant_id::text,token_scopes,token_workspace_ids::text[],expires_at,revoked_at,last_used_at,roles,workspaces,projects,classification,grant_scopes,grant_version,binding_provenance FROM public.cortex_verify_token_principal($1,$2::bytea,$3)`, secret[:12], digest, requiredScope).Scan(&rec.ID, &rec.Name, &rec.Prefix, &rec.Subject, &rec.PrincipalType, &rec.OrgID, &rec.Scopes, &rec.Workspaces, &expiresAt, &revokedAt, &lastUsedAt, &recRoles, &recWorkspaces, &recProjects, &recClearance, &recGrantScopes, &grantVersion, &provenance)
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.Principal{}, identity.ErrInvalidToken
	}
	if err != nil {
		return identity.Principal{}, mapVerifyPrincipalError(err)
	}
	if expiresAt != nil {
		rec.ExpiresAt = *expiresAt
	}
	if revokedAt != nil {
		rec.RevokedAt = *revokedAt
	}
	if lastUsedAt != nil {
		rec.LastUsedAt = *lastUsedAt
	}
	if len(recWorkspaces) == 0 {
		recWorkspaces = rec.Workspaces
	}
	scopes := rec.Scopes
	if len(scopes) == 0 {
		scopes = recGrantScopes
	}
	if len(recProjects) == 0 && rec.PrincipalType == "user" {
		recProjects = []string{"*"}
	}
	return identity.Principal{Subject: rec.Subject, Type: rec.PrincipalType, OrgID: rec.OrgID, WorkspaceIDs: recWorkspaces, Roles: recRoles, Scopes: scopes, AuthMethod: "api_key", GrantDigest: provenance, GrantVersion: grantVersion, ProjectIDs: recProjects, ClassificationClearance: recClearance}, nil
}

func (r *TokenRepository) Revoke(ctx context.Context, id string) error {
	return r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// cortex_revoke_api_token revokes idempotently under the bound
		// owner/admin caller and audits only real transitions. Both the
		// unknown-token SQLSTATE and the already-revoked no-op keep the
		// repository's historical identity.ErrInvalidToken contract.
		var transitioned bool
		if err := tx.QueryRow(ctx, `SELECT public.cortex_revoke_api_token($1::uuid,$2::text)`, id, "").Scan(&transitioned); err != nil {
			if isMediatedNotFound(err) {
				return identity.ErrInvalidToken
			}
			return err
		}
		if !transitioned {
			return identity.ErrInvalidToken
		}
		return nil
	})
}

func (r *TokenRepository) Rotate(ctx context.Context, id string) (identity.IssuedToken, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return identity.IssuedToken{}, err
	}
	secret := "ctx_" + base64.RawURLEncoding.EncodeToString(b)
	digest := r.digest(secret)
	var scanned identity.TokenIssue
	var rotatedID, rotatedPrefix string
	var expiresAt *time.Time
	// cortex_rotate_api_token revokes the locked live token and re-issues an
	// exact copy with a fresh in-SQL digest in one audited transaction.
	err := r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT token_public_id::text,token_prefix,token_name,subject_public_id::text,principal_type,token_scopes,token_workspace_ids::text[],expires_at FROM public.cortex_rotate_api_token($1::uuid,$2::text,$3::text)`, id, secret, "").Scan(&rotatedID, &rotatedPrefix, &scanned.Name, &scanned.Subject, &scanned.PrincipalType, &scanned.Scopes, &scanned.Workspaces, &expiresAt)
	})
	if err != nil {
		return identity.IssuedToken{}, mapMediatedMutationError(err, "token", id)
	}
	if expiresAt != nil {
		scanned.ExpiresAt = *expiresAt
	}
	rec := assembleRotatedRecord(scanned, rotatedID, rotatedPrefix, digest, r.tenant.TenantID)
	return identity.IssuedToken{Secret: secret, Record: rec}, nil
}

// assembleRotatedRecord builds the issued record for a mediated rotation
// from the attributes the definer routine returned. subject_public_id and
// principal_type are copied — the caller's presentation of the rotated
// credential must identify its subject exactly like Issue does — and the
// slice fields are cloned so the record never aliases the scan targets.
func assembleRotatedRecord(scanned identity.TokenIssue, id, prefix string, digest []byte, orgID string) identity.TokenRecord {
	return identity.TokenRecord{
		ID:            id,
		Name:          scanned.Name,
		Prefix:        prefix,
		Digest:        base64.RawURLEncoding.EncodeToString(digest),
		Subject:       scanned.Subject,
		PrincipalType: scanned.PrincipalType,
		OrgID:         orgID,
		Workspaces:    append([]string(nil), scanned.Workspaces...),
		Scopes:        append([]string(nil), scanned.Scopes...),
		ExpiresAt:     scanned.ExpiresAt,
	}
}

func (r *TokenRepository) digest(s string) []byte {
	return tenantDigest(r.tenant.TenantID, s)
}

// tenantDigest derives the credential digest exactly as the 106 SQL
// contract does: HMAC-SHA256 keyed by the tenant UUID string over the
// secret. The key is why a credential can only ever match in its own
// tenant.
func tenantDigest(tenantID, s string) []byte {
	mac := hmac.New(sha256.New, []byte(tenantID))
	_, _ = mac.Write([]byte(s))
	return mac.Sum(nil)
}
func contains(v []string, want string) bool {
	for _, x := range v {
		if x == want {
			return true
		}
	}
	return false
}
func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

// mapVerifyPrincipalError maps the stable authentication errors raised by
// cortex_verify_token_principal onto the repository's historical identity
// error values. SQLSTATE 28000 carries both revoked and expired, so the
// mapping keys on the routine's exact message; any other failure passes
// through unchanged.
func mapVerifyPrincipalError(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}
	switch pgErr.Message {
	case "token is revoked":
		return identity.ErrTokenRevoked
	case "token is expired":
		return identity.ErrTokenExpired
	case "token is missing required scope":
		return identity.ErrInsufficientScope
	default:
		return err
	}
}

// isMediatedNotFound reports whether err is a mediated routine's
// does-not-exist-in-tenant rejection (SQLSTATE 23503).
func isMediatedNotFound(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}

// mapMediatedMutationError translates a mediated identity mutation's
// SQLSTATE 23503 into the repository's historical not-found taxonomy for the
// given resource kind; every other error is returned unchanged so unique
// violations and authorization failures keep their causes.
func mapMediatedMutationError(err error, kind string, id any) error {
	if isMediatedNotFound(err) {
		return notFound(kind, id)
	}
	return err
}

// provisionGrantPayload serializes grants into the JSON array contract of
// cortex_provision_actor. The payload is canonical type/value objects; the
// SQL routine re-validates against the 101 allowlist and recomputes the
// digest inside the database.
func provisionGrantPayload(grants []persistedGrant) ([]byte, error) {
	if len(grants) == 0 {
		return nil, fmt.Errorf("postgres users: provisioning requires at least one grant")
	}
	type grantObject struct {
		Type  string `json:"type"`
		Value string `json:"value"`
	}
	payload := make([]grantObject, 0, len(grants))
	for _, grant := range grants {
		payload = append(payload, grantObject{Type: grant.kind, Value: grant.value})
	}
	return json.Marshal(payload)
}
