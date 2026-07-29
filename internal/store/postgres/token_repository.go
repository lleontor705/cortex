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

func (s *Store) Tokens() *TokenRepository { return &TokenRepository{s} }

var _ identity.TokenStore = (*TokenRepository)(nil)

// List returns metadata only. Secret and digest material are never exposed.
func (r *TokenRepository) List(ctx context.Context) (out []identity.TokenRecord, err error) {
	err = r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `SELECT public_id::text,token_prefix,subject_user_id,subject_service_account_id,tenant_id::text,scopes,workspace_ids,COALESCE(expires_at,'epoch'::timestamptz),COALESCE(revoked_at,'epoch'::timestamptz),COALESCE(last_used_at,'epoch'::timestamptz) FROM api_tokens WHERE tenant_id=public.cortex_current_tenant() ORDER BY created_at DESC`)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var r identity.TokenRecord
			var userID, serviceID any
			if e := rows.Scan(&r.ID, &r.Prefix, &userID, &serviceID, &r.OrgID, &r.Scopes, &r.Workspaces, &r.ExpiresAt, &r.RevokedAt, &r.LastUsedAt); e != nil {
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
	err := r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `INSERT INTO api_tokens(tenant_id,token_prefix,token_digest,subject_user_id,subject_service_account_id,scopes,workspace_ids,expires_at) VALUES(public.cortex_current_tenant(),$1,$2,(SELECT id FROM app_users WHERE tenant_id=public.cortex_current_tenant() AND public_id=$3::uuid),(SELECT id FROM service_accounts WHERE tenant_id=public.cortex_current_tenant() AND public_id=$3::uuid),$4,$5,$6) RETURNING public_id::text,expires_at`, prefix, digest, in.Subject, in.Scopes, in.Workspaces, nullTime(in.ExpiresAt)).Scan(&rec.ID, &rec.ExpiresAt)
	})
	if err != nil {
		return identity.IssuedToken{}, err
	}
	rec.Prefix, rec.Digest, rec.Subject, rec.PrincipalType, rec.OrgID = prefix, base64.RawURLEncoding.EncodeToString(digest), in.Subject, in.PrincipalType, in.OrgID
	rec.Workspaces, rec.Scopes = append([]string(nil), in.Workspaces...), append([]string(nil), in.Scopes...)
	return identity.IssuedToken{Secret: secret, Record: rec}, nil
}

func (r *TokenRepository) Verify(ctx context.Context, secret, requiredScope string) (identity.Principal, error) {
	if len(secret) < 12 {
		return identity.Principal{}, identity.ErrInvalidToken
	}
	digest := r.digest(secret)
	var rec identity.TokenRecord
	err := r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// Lock and validate in the SAME transaction as last_used_at. A revoke
		// that wins this row lock is never followed by an accepted verification.
		if err := tx.QueryRow(ctx, `SELECT t.public_id::text,t.token_prefix,encode(t.token_digest,'base64'),COALESCE(au.public_id,sa.public_id)::text,CASE WHEN sa.id IS NOT NULL THEN 'service_account' ELSE 'user' END,t.tenant_id::text,COALESCE(t.expires_at,'epoch'::timestamptz),COALESCE(t.revoked_at,'epoch'::timestamptz),COALESCE(t.last_used_at,'epoch'::timestamptz),t.scopes,t.workspace_ids::text[] FROM api_tokens t LEFT JOIN app_users au ON au.tenant_id=t.tenant_id AND au.id=t.subject_user_id LEFT JOIN service_accounts sa ON sa.tenant_id=t.tenant_id AND sa.id=t.subject_service_account_id WHERE t.tenant_id=public.cortex_current_tenant() AND t.token_prefix=$1 AND t.token_digest=$2 FOR UPDATE OF t`, secret[:12], digest).Scan(&rec.ID, &rec.Prefix, &rec.Digest, &rec.Subject, &rec.PrincipalType, &rec.OrgID, &rec.ExpiresAt, &rec.RevokedAt, &rec.LastUsedAt, &rec.Scopes, &rec.Workspaces); err != nil {
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
		_, err := tx.Exec(ctx, `UPDATE api_tokens SET last_used_at=now(),updated_at=now() WHERE public_id=$1::uuid AND revoked_at IS NULL`, rec.ID)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.Principal{}, identity.ErrInvalidToken
	}
	if err != nil {
		return identity.Principal{}, err
	}
	return identity.NewPrincipal(rec.Subject, rec.PrincipalType, rec.OrgID, rec.Workspaces, nil, rec.Scopes, "api_key", rec.ID), nil
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
	err := r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT COALESCE(sa.public_id,au.public_id)::text,t.scopes,t.expires_at FROM api_tokens t LEFT JOIN app_users au ON au.tenant_id=t.tenant_id AND au.id=t.subject_user_id LEFT JOIN service_accounts sa ON sa.tenant_id=t.tenant_id AND sa.id=t.subject_service_account_id WHERE t.public_id=$1::uuid AND t.revoked_at IS NULL`, id).Scan(&in.Subject, &in.Scopes, &in.ExpiresAt); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE api_tokens SET revoked_at=now(),updated_at=now() WHERE public_id=$1::uuid AND revoked_at IS NULL`, id); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `INSERT INTO api_tokens(tenant_id,token_prefix,token_digest,subject_user_id,subject_service_account_id,scopes,expires_at) VALUES(public.cortex_current_tenant(),$1,$2,(SELECT id FROM app_users WHERE tenant_id=public.cortex_current_tenant() AND public_id=$3::uuid),(SELECT id FROM service_accounts WHERE tenant_id=public.cortex_current_tenant() AND public_id=$3::uuid),$4,$5) RETURNING public_id::text`, prefix, digest, in.Subject, in.Scopes, nullTime(in.ExpiresAt)).Scan(&rec.ID)
	})
	if err != nil {
		return identity.IssuedToken{}, err
	}
	rec.Prefix, rec.Digest, rec.Subject, rec.Scopes, rec.ExpiresAt = prefix, base64.RawURLEncoding.EncodeToString(digest), in.Subject, append([]string(nil), in.Scopes...), in.ExpiresAt
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
