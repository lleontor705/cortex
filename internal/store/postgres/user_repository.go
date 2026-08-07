package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/lleontor705/cortex/internal/identity"
)

var ErrInvalidUser = errors.New("postgres users: invalid user")

type UserRepository struct{ *Store }

func (s *Store) users() *UserRepository { return &UserRepository{s} }

func (r *UserRepository) Create(ctx context.Context, in identity.UserCreate) (identity.UserRecord, error) {
	in.Email = strings.TrimSpace(strings.ToLower(in.Email))
	in.DisplayName = strings.TrimSpace(in.DisplayName)
	if in.Email == "" || in.DisplayName == "" || len(in.Roles) == 0 {
		return identity.UserRecord{}, ErrInvalidUser
	}
	id := uuid.New()
	grants := userGrants(in)
	digest := grantDigest(grants)
	result := identity.UserRecord{ID: id.String(), Email: in.Email, DisplayName: in.DisplayName, Active: true, Roles: cloneGrant(in.Roles), Workspaces: cloneGrant(in.Workspaces), Projects: cloneGrant(in.Projects), Scopes: cloneGrant(in.Scopes), ClassificationClearance: cloneGrant(in.ClassificationClearance), GrantVersion: 1}
	err := r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO app_users(public_id,tenant_id,email,display_name,created_by,updated_by) VALUES($1,$2,$3,$4,$5,$5)`, id, r.tenant.TenantID, in.Email, in.DisplayName, actorFromContext(ctx)); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO actor_subjects(public_id,tenant_id,subject,actor_type,grant_version,grant_digest) VALUES($1,$2,$3,'user',1,$4)`, id, r.tenant.TenantID, id.String(), digest); err != nil {
			return err
		}
		for _, grant := range grants {
			if _, err := tx.Exec(ctx, `INSERT INTO principal_grants(tenant_id,actor_public_id,grant_type,grant_value,created_by,updated_by) VALUES($1,$2,$3,$4,$5,$5)`, r.tenant.TenantID, id, grant.kind, grant.value, actorFromContext(ctx)); err != nil {
				return err
			}
		}
		return tx.QueryRow(ctx, `SELECT created_at FROM app_users WHERE tenant_id=public.cortex_current_tenant() AND public_id=$1`, id).Scan(&result.CreatedAt)
	})
	return result, err
}

func (r *UserRepository) List(ctx context.Context) ([]identity.UserRecord, error) {
	users := make([]identity.UserRecord, 0)
	err := r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT u.public_id::text,u.email,COALESCE(u.display_name,''),u.active,a.grant_version,u.created_at FROM app_users u JOIN actor_subjects a ON a.tenant_id=u.tenant_id AND a.public_id=u.public_id WHERE u.tenant_id=public.cortex_current_tenant() ORDER BY u.created_at`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var user identity.UserRecord
			if err := rows.Scan(&user.ID, &user.Email, &user.DisplayName, &user.Active, &user.GrantVersion, &user.CreatedAt); err != nil {
				return err
			}
			users = append(users, user)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		for i := range users {
			grantRows, err := tx.Query(ctx, `SELECT grant_type,grant_value FROM principal_grants WHERE tenant_id=public.cortex_current_tenant() AND actor_public_id=$1::uuid ORDER BY grant_type,grant_value`, users[i].ID)
			if err != nil {
				return err
			}
			for grantRows.Next() {
				var kind, value string
				if err := grantRows.Scan(&kind, &value); err != nil {
					grantRows.Close()
					return err
				}
				appendUserGrant(&users[i], kind, value)
			}
			if err := grantRows.Err(); err != nil {
				grantRows.Close()
				return err
			}
			grantRows.Close()
		}
		return nil
	})
	return users, err
}

func (r *UserRepository) SetActive(ctx context.Context, id string, active bool) error {
	return r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE app_users SET active=$1,disabled_at=CASE WHEN $1 THEN NULL ELSE now() END,updated_at=now(),updated_by=$2 WHERE tenant_id=public.cortex_current_tenant() AND public_id=$3::uuid`, active, actorFromContext(ctx), id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return notFound("user", id)
		}
		if _, err := tx.Exec(ctx, `UPDATE actor_subjects SET active=$1,revoked_at=CASE WHEN $1 THEN NULL ELSE now() END,grant_version=grant_version+1 WHERE tenant_id=public.cortex_current_tenant() AND public_id=$2::uuid`, active, id); err != nil {
			return err
		}
		if !active {
			_, err = tx.Exec(ctx, `UPDATE api_tokens SET revoked_at=COALESCE(revoked_at,now()),updated_at=now() WHERE tenant_id=public.cortex_current_tenant() AND subject_user_id=(SELECT id FROM app_users WHERE tenant_id=public.cortex_current_tenant() AND public_id=$1::uuid)`, id)
		}
		return err
	})
}

type persistedGrant struct{ kind, value string }

func userGrants(in identity.UserCreate) []persistedGrant {
	var out []persistedGrant
	for kind, values := range map[string][]string{"role": in.Roles, "workspace": in.Workspaces, "project": in.Projects, "scope": in.Scopes, "classification": in.ClassificationClearance} {
		for _, value := range values {
			if value = strings.TrimSpace(value); value != "" {
				out = append(out, persistedGrant{kind: kind, value: value})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].kind+":"+out[i].value < out[j].kind+":"+out[j].value })
	return out
}

func grantDigest(grants []persistedGrant) string {
	var b strings.Builder
	for _, grant := range grants {
		b.WriteString(grant.kind)
		b.WriteByte(':')
		b.WriteString(grant.value)
		b.WriteByte('\n')
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

func appendUserGrant(user *identity.UserRecord, kind, value string) {
	switch kind {
	case "role":
		user.Roles = append(user.Roles, value)
	case "workspace":
		user.Workspaces = append(user.Workspaces, value)
	case "project":
		user.Projects = append(user.Projects, value)
	case "scope":
		user.Scopes = append(user.Scopes, value)
	case "classification":
		user.ClassificationClearance = append(user.ClassificationClearance, value)
	}
}

func cloneGrant(values []string) []string { return append([]string(nil), values...) }
