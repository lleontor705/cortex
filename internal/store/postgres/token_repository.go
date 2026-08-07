package postgres

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lleontor705/cortex/internal/identity"
)

// TokenRepository persists API credentials in PostgreSQL. Plaintext secrets
// are returned only by Issue and are never stored.
type TokenRepository struct{ *Store }

func (s *Store) tokens() *TokenRepository { return &TokenRepository{s} }

var _ identity.TokenStore = (*TokenRepository)(nil)

// List returns metadata only. Secret and digest material are never exposed.
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
	prefix, digest := secret[:12], r.digest(secret)
	var rec identity.TokenRecord
	var expiresAt *time.Time
	err := r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `INSERT INTO api_tokens(tenant_id,name,token_prefix,token_digest,subject_user_id,subject_service_account_id,scopes,workspace_ids,expires_at,created_by) SELECT public.cortex_current_tenant(),$1,$2,$3,au.id,sa.id,$5,$6,$7,$8 FROM (SELECT 1) seed LEFT JOIN app_users au ON au.tenant_id=public.cortex_current_tenant() AND au.public_id=$4::uuid AND au.active LEFT JOIN service_accounts sa ON sa.tenant_id=public.cortex_current_tenant() AND sa.public_id=$4::uuid AND sa.active WHERE au.id IS NOT NULL OR sa.id IS NOT NULL RETURNING public_id::text,expires_at`, in.Name, prefix, digest, in.Subject, in.Scopes, in.Workspaces, nullTime(in.ExpiresAt), actorFromContext(ctx)).Scan(&rec.ID, &expiresAt)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.IssuedToken{}, notFound("token subject", in.Subject)
	}
	if err != nil {
		return identity.IssuedToken{}, err
	}
	if expiresAt != nil {
		rec.ExpiresAt = *expiresAt
	}
	rec.Name, rec.Prefix, rec.Digest, rec.Subject, rec.PrincipalType, rec.OrgID = in.Name, prefix, base64.RawURLEncoding.EncodeToString(digest), in.Subject, in.PrincipalType, in.OrgID
	rec.Workspaces, rec.Scopes = append([]string(nil), in.Workspaces...), append([]string(nil), in.Scopes...)
	return identity.IssuedToken{Secret: secret, Record: rec}, nil
}

func (r *TokenRepository) Verify(ctx context.Context, secret, requiredScope string) (identity.Principal, error) {
	if len(secret) < 12 {
		return identity.Principal{}, identity.ErrInvalidToken
	}
	digest := r.digest(secret)
	var rec identity.TokenRecord
	var grantDigest string
	var grantVersion int64
	var recRoles, recWorkspaces, recProjects, recClearance, recGrantScopes []string
	err := r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// Lock and validate in the SAME transaction as last_used_at. A revoke
		// that wins this row lock is never followed by an accepted verification.
		if err := tx.QueryRow(ctx, `SELECT t.public_id::text,t.name,t.token_prefix,encode(t.token_digest,'base64'),COALESCE(au.public_id,sa.public_id)::text,CASE WHEN sa.id IS NOT NULL THEN 'service_account' ELSE 'user' END,t.tenant_id::text,COALESCE(t.expires_at,'epoch'::timestamptz),COALESCE(t.revoked_at,'epoch'::timestamptz),COALESCE(t.last_used_at,'epoch'::timestamptz),t.scopes,t.workspace_ids::text[],a.grant_digest,a.grant_version FROM api_tokens t LEFT JOIN app_users au ON au.tenant_id=t.tenant_id AND au.id=t.subject_user_id LEFT JOIN service_accounts sa ON sa.tenant_id=t.tenant_id AND sa.id=t.subject_service_account_id JOIN actor_subjects a ON a.tenant_id=t.tenant_id AND a.public_id=COALESCE(au.public_id,sa.public_id) WHERE t.tenant_id=public.cortex_current_tenant() AND t.token_prefix=$1 AND t.token_digest=$2 AND a.active AND a.revoked_at IS NULL AND (au.id IS NULL OR au.active) AND (sa.id IS NULL OR sa.active) FOR UPDATE OF t`, secret[:12], digest).Scan(&rec.ID, &rec.Name, &rec.Prefix, &rec.Digest, &rec.Subject, &rec.PrincipalType, &rec.OrgID, &rec.ExpiresAt, &rec.RevokedAt, &rec.LastUsedAt, &rec.Scopes, &rec.Workspaces, &grantDigest, &grantVersion); err != nil {
			return err
		}
		now := time.Now().UTC()
		if !rec.RevokedAt.IsZero() && rec.RevokedAt.Unix() > 0 {
			return identity.ErrTokenRevoked
		}
		if rec.ExpiresAt.Unix() > 0 && !now.Before(rec.ExpiresAt) {
			return identity.ErrTokenExpired
		}
		if requiredScope != "" && !contains(rec.Scopes, requiredScope) {
			return identity.ErrInsufficientScope
		}
		grantRows, err := tx.Query(ctx, `SELECT grant_type,grant_value FROM principal_grants WHERE tenant_id=public.cortex_current_tenant() AND actor_public_id=$1::uuid ORDER BY grant_type,grant_value`, rec.Subject)
		if err != nil {
			return err
		}
		defer grantRows.Close()
		for grantRows.Next() {
			var kind, value string
			if err := grantRows.Scan(&kind, &value); err != nil {
				return err
			}
			switch kind {
			case "role":
				recRoles = append(recRoles, value)
			case "workspace":
				recWorkspaces = append(recWorkspaces, value)
			case "project":
				recProjects = append(recProjects, value)
			case "classification":
				recClearance = append(recClearance, value)
			case "scope":
				recGrantScopes = append(recGrantScopes, value)
			}
		}
		if err := grantRows.Err(); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE api_tokens SET last_used_at=now(),updated_at=now() WHERE public_id=$1::uuid AND revoked_at IS NULL`, rec.ID)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.Principal{}, identity.ErrInvalidToken
	}
	if err != nil {
		return identity.Principal{}, err
	}
	if len(recWorkspaces) == 0 {
		recWorkspaces = rec.Workspaces
	}
	scopes := rec.Scopes
	if len(scopes) == 0 {
		scopes = recGrantScopes
	}
	if grantDigest == "" {
		grantDigest = rec.ID
	}
	if grantVersion <= 0 {
		grantVersion = 1
	}
	return identity.Principal{Subject: rec.Subject, Type: rec.PrincipalType, OrgID: rec.OrgID, WorkspaceIDs: recWorkspaces, Roles: recRoles, Scopes: scopes, AuthMethod: "api_key", GrantDigest: grantDigest, GrantVersion: grantVersion, ProjectIDs: recProjects, ClassificationClearance: recClearance}, nil
}

func (r *TokenRepository) Revoke(ctx context.Context, id string) error {
	return r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		tag, e := tx.Exec(ctx, `UPDATE api_tokens SET revoked_at=COALESCE(revoked_at,now()),updated_at=now() WHERE public_id=$1::uuid AND revoked_at IS NULL`, id)
		if e != nil {
			return e
		}
		if tag.RowsAffected() == 0 {
			return identity.ErrInvalidToken
		}
		return nil
	})
}
func (r *TokenRepository) Rotate(ctx context.Context, id string) (identity.IssuedToken, error) {
	var in identity.TokenIssue
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return identity.IssuedToken{}, err
	}
	secret, prefix := "ctx_"+base64.RawURLEncoding.EncodeToString(b), ""
	prefix = secret[:12]
	digest := r.digest(secret)
	var rec identity.TokenRecord
	var expiresAt *time.Time
	err := r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT COALESCE(sa.public_id,au.public_id)::text,t.name,t.scopes,t.workspace_ids::text[],t.expires_at FROM api_tokens t LEFT JOIN app_users au ON au.tenant_id=t.tenant_id AND au.id=t.subject_user_id LEFT JOIN service_accounts sa ON sa.tenant_id=t.tenant_id AND sa.id=t.subject_service_account_id WHERE t.public_id=$1::uuid AND t.revoked_at IS NULL`, id).Scan(&in.Subject, &in.Name, &in.Scopes, &in.Workspaces, &expiresAt); err != nil {
			return err
		}
		if expiresAt != nil {
			in.ExpiresAt = *expiresAt
		}
		if _, err := tx.Exec(ctx, `UPDATE api_tokens SET revoked_at=now(),updated_at=now() WHERE public_id=$1::uuid AND revoked_at IS NULL`, id); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `INSERT INTO api_tokens(tenant_id,name,token_prefix,token_digest,subject_user_id,subject_service_account_id,scopes,workspace_ids,expires_at,created_by) VALUES(public.cortex_current_tenant(),$1,$2,$3,(SELECT id FROM app_users WHERE tenant_id=public.cortex_current_tenant() AND public_id=$4::uuid),(SELECT id FROM service_accounts WHERE tenant_id=public.cortex_current_tenant() AND public_id=$4::uuid),$5,$6,$7,$8) RETURNING public_id::text`, in.Name, prefix, digest, in.Subject, in.Scopes, in.Workspaces, nullTime(in.ExpiresAt), actorFromContext(ctx)).Scan(&rec.ID)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.IssuedToken{}, notFound("token", id)
	}
	if err != nil {
		return identity.IssuedToken{}, err
	}
	rec.Name, rec.Prefix, rec.Digest, rec.Subject, rec.Scopes, rec.Workspaces, rec.ExpiresAt = in.Name, prefix, base64.RawURLEncoding.EncodeToString(digest), in.Subject, append([]string(nil), in.Scopes...), append([]string(nil), in.Workspaces...), in.ExpiresAt
	return identity.IssuedToken{Secret: secret, Record: rec}, nil
}

func (r *TokenRepository) digest(s string) []byte {
	mac := hmac.New(sha256.New, []byte(r.tenant.TenantID))
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
