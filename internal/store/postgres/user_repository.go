package postgres

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/lleontor705/cortex/internal/identity"
)

var ErrInvalidUser = errors.New("postgres users: invalid user")

// UserRepository persists application users. Identity provisioning, grant
// reads, and activation state changes execute exclusively through the
// migration-owned SECURITY DEFINER routines installed by the unshipped 106
// server migration; the application role holds no direct DML on
// actor_subjects or principal_grants and cannot read grant digests or
// versions.
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
	payload, err := provisionGrantPayload(grants)
	if err != nil {
		return identity.UserRecord{}, err
	}
	result := identity.UserRecord{ID: id.String(), Email: in.Email, DisplayName: in.DisplayName, Active: true, Roles: cloneGrant(in.Roles), Workspaces: cloneGrant(in.Workspaces), Projects: cloneGrant(in.Projects), Scopes: cloneGrant(in.Scopes), ClassificationClearance: cloneGrant(in.ClassificationClearance), GrantVersion: 1}
	err = r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO app_users(public_id,tenant_id,email,display_name,created_by,updated_by) VALUES($1,$2,$3,$4,$5,$5)`, id, r.tenant.TenantID, in.Email, in.DisplayName, actorFromContext(ctx)); err != nil {
			return err
		}
		// cortex_provision_actor validates the grants against the 101
		// allowlist, computes the digest inside SQL, inserts the actor and
		// grant rows exactly once, and audits — atomically with the user
		// insert above. The returned digest is integrity metadata only and
		// is never used as an authenticator by this process.
		var grantVersion int64
		var grantDigest string
		if err := tx.QueryRow(ctx, `SELECT grant_version,grant_digest FROM public.cortex_provision_actor($1::uuid,$2::text,'user',$3::jsonb,$4::text)`, id, id.String(), payload, "").Scan(&grantVersion, &grantDigest); err != nil {
			return err
		}
		result.GrantVersion = grantVersion
		return tx.QueryRow(ctx, `SELECT created_at FROM app_users WHERE tenant_id=public.cortex_current_tenant() AND public_id=$1`, id).Scan(&result.CreatedAt)
	})
	return result, err
}

func (r *UserRepository) List(ctx context.Context) ([]identity.UserRecord, error) {
	users := make([]identity.UserRecord, 0)
	err := r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT u.public_id::text,u.email,COALESCE(u.display_name,''),u.active,u.created_at FROM app_users u WHERE u.tenant_id=public.cortex_current_tenant() ORDER BY u.created_at`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var user identity.UserRecord
			if err := rows.Scan(&user.ID, &user.Email, &user.DisplayName, &user.Active, &user.CreatedAt); err != nil {
				return err
			}
			users = append(users, user)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		for i := 0; i < len(users); i++ {
			// Grant versions and grant rows are read back through the
			// owner/admin-authorized definer helpers; the application role
			// selects neither actor state nor principal_grants directly.
			// A user with no provisioned actor is skipped, preserving the
			// historical INNER JOIN actor_subjects listing semantics.
			if err := tx.QueryRow(ctx, `SELECT public.cortex_actor_grant_version($1::uuid)`, users[i].ID).Scan(&users[i].GrantVersion); err != nil {
				if isMediatedNotFound(err) {
					users = append(users[:i], users[i+1:]...)
					i--
					continue
				}
				return err
			}
			grantRows, err := tx.Query(ctx, `SELECT grant_type,grant_value FROM public.cortex_read_actor_grants($1::uuid)`, users[i].ID)
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
	if err != nil {
		return nil, err
	}
	return users, nil
}

func (r *UserRepository) SetActive(ctx context.Context, id string, active bool) error {
	return r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// cortex_set_actor_active authorizes the bound owner/admin caller,
		// synchronizes actor_subjects, app_users and live tokens atomically,
		// bumps the grant version only on a real transition, and audits.
		var grantVersion int64
		if err := tx.QueryRow(ctx, `SELECT public.cortex_set_actor_active($1::uuid,$2,$3::text)`, id, active, "").Scan(&grantVersion); err != nil {
			if isMediatedNotFound(err) {
				return notFound("user", id)
			}
			return err
		}
		return nil
	})
}

func (r *UserRepository) GetByPublicID(ctx context.Context, id string) (*identity.UserRecord, error) {
	var user identity.UserRecord
	err := r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT u.public_id::text,u.email,COALESCE(u.display_name,''),u.active,u.created_at FROM app_users u WHERE u.tenant_id=public.cortex_current_tenant() AND u.public_id=$1::uuid`, id).Scan(&user.ID, &user.Email, &user.DisplayName, &user.Active, &user.CreatedAt)
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, notFound("user", id)
		}
		return nil, err
	}
	return &user, nil
}

type persistedGrant struct{ kind, value string }

// userGrants canonicalizes the administrator-supplied grants. Duplicates are
// collapsed so the mediated provisioning routine's uniqueness validation
// accepts every input the historical direct inserts accepted.
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
	var deduped []persistedGrant
	for _, grant := range out {
		if len(deduped) == 0 || grant != deduped[len(deduped)-1] {
			deduped = append(deduped, grant)
		}
	}
	return deduped
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
