package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lleontor705/cortex/v2/internal/authz"
	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/domain/privacy"
)

type privacyCaptureTx struct {
	pgx.Tx
	queries []string
	args    [][]any
}

func (c *privacyCaptureTx) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	c.queries = append(c.queries, sql)
	c.args = append(c.args, append([]any(nil), args...))
	return &privacyStubRow{id: 101, publicID: "20000000-0000-0000-0000-000000000001"}
}

func (c *privacyCaptureTx) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	c.queries = append(c.queries, sql)
	c.args = append(c.args, append([]any(nil), args...))
	return pgconn.NewCommandTag("UPDATE 1"), nil
}

type privacyStubRow struct {
	id       int64
	publicID string
}

func (r *privacyStubRow) Scan(dest ...any) error {
	for _, d := range dest {
		switch v := d.(type) {
		case *int64:
			*v = r.id
		case *string:
			*v = r.publicID
		case *time.Time:
			*v = time.Now().UTC()
		case *int:
			*v = 1
		}
	}
	return nil
}

func newPrivacyTestStore(tenant, workspace string) *AuthorizedStore {
	p := domain.Principal{Subject: uuid.NewString(), OrgID: tenant, Roles: []string{"owner"}, WorkspaceIDs: []string{workspace}, ProjectIDs: []string{"*"}, ClassificationClearance: []string{"*"}}
	return &AuthorizedStore{store: &Store{tenant: &domain.TenantContext{TenantID: tenant, WorkspaceID: workspace}, principal: p, authorized: true, authorizer: authz.NewPolicy()}}
}

func TestAuthorizedOperations_PrivacyObservation_SaveRedaction(t *testing.T) {
	tenant, ws := uuid.NewString(), uuid.NewString()
	store := newPrivacyTestStore(tenant, ws)
	tx := &privacyCaptureTx{}
	ctx := context.WithValue(context.WithValue(context.Background(), txKey{}, pgx.Tx(tx)), workspaceKey{}, int64(1))

	obs := &domain.Observation{
		Project: "my-proj", Scope: domain.ScopeProject,
		Title:   "Test <private>canary-title</private> public",
		Content: "Obs <private>canary-secret</private> remaining text",
	}
	if err := store.SaveObservation(ctx, obs); err != nil {
		t.Fatalf("SaveObservation failed: %v", err)
	}

	if obs.ID != 101 {
		t.Fatalf("expected obs.ID = 101, got %d", obs.ID)
	}
	if strings.Contains(obs.Title, "canary-title") || !strings.Contains(obs.Title, privacy.RedactedPlaceholder) {
		t.Fatalf("obs.Title not redacted: %q", obs.Title)
	}
	if strings.Contains(obs.Content, "canary-secret") || !strings.Contains(obs.Content, privacy.RedactedPlaceholder) {
		t.Fatalf("obs.Content not redacted: %q", obs.Content)
	}

	foundInsert := false
	for _, argList := range tx.args {
		for _, arg := range argList {
			if s, ok := arg.(string); ok {
				if strings.Contains(s, "canary-title") || strings.Contains(s, "canary-secret") {
					t.Fatalf("private canary leaked into SQL argument: %q", s)
				}
				if strings.Contains(s, privacy.RedactedPlaceholder) {
					foundInsert = true
				}
			}
		}
	}
	if !foundInsert {
		t.Fatal("expected redacted placeholder in captured SQL arguments")
	}
}

func TestAuthorizedOperations_PrivacyObservation_SaveWithEffectRedaction(t *testing.T) {
	tenant, ws := uuid.NewString(), uuid.NewString()
	store := newPrivacyTestStore(tenant, ws)
	tx := &privacyCaptureTx{}
	ctx := context.WithValue(context.WithValue(context.Background(), txKey{}, pgx.Tx(tx)), workspaceKey{}, int64(1))

	obs := &domain.Observation{
		Project: "my-proj", Scope: domain.ScopeProject,
		Title:   "Effect <private>canary-title</private> public",
		Content: "Effect <private>canary-body</private> content",
	}
	eff, err := store.SaveObservationWithEffect(ctx, obs)
	if err != nil {
		t.Fatalf("SaveObservationWithEffect failed: %v", err)
	}
	if eff.Status != domain.WriteStatusCreated {
		t.Fatalf("expected WriteStatusCreated, got %v", eff.Status)
	}
	if strings.Contains(obs.Title, "canary-title") || !strings.Contains(obs.Title, privacy.RedactedPlaceholder) {
		t.Fatalf("obs.Title not redacted: %q", obs.Title)
	}
}

func TestAuthorizedOperations_PrivacyObservation_UpdateRedaction(t *testing.T) {
	tenant, ws := uuid.NewString(), uuid.NewString()
	store := newPrivacyTestStore(tenant, ws)
	tx := &privacyCaptureTx{}
	ctx := context.WithValue(context.WithValue(context.Background(), txKey{}, pgx.Tx(tx)), workspaceKey{}, int64(1))

	obs := &domain.Observation{
		ID: 101, Project: "my-proj", Scope: domain.ScopeProject,
		Title:   "Update <private>canary-upd-title</private> public",
		Content: "Update <private>canary-upd-content</private> public",
	}
	if err := store.UpdateObservation(ctx, obs); err != nil {
		t.Fatalf("UpdateObservation failed: %v", err)
	}
	if strings.Contains(obs.Title, "canary-upd-title") || !strings.Contains(obs.Title, privacy.RedactedPlaceholder) {
		t.Fatalf("obs.Title not redacted: %q", obs.Title)
	}
}

func TestAuthorizedOperations_PrivacyObservation_BulkSaveAtomic(t *testing.T) {
	tenant, ws := uuid.NewString(), uuid.NewString()
	store := newPrivacyTestStore(tenant, ws)
	tx := &privacyCaptureTx{}
	ctx := context.WithValue(context.WithValue(context.Background(), txKey{}, pgx.Tx(tx)), workspaceKey{}, int64(1))

	obs1 := &domain.Observation{Project: "proj", Title: "Valid 1 <private>secret1</private> text", Content: "Body 1 text"}
	obs2 := &domain.Observation{Project: "proj", Title: "Invalid <private>bad", Content: "Body 2"}
	batch := []*domain.Observation{obs1, obs2}

	err := store.BulkSaveObservations(ctx, batch)
	if err == nil {
		t.Fatal("expected error on malformed marker in batch item 2")
	}
	var privErr *privacy.Error
	if !errors.As(err, &privErr) {
		t.Fatalf("expected privacy.Error, got %T: %v", err, err)
	}
	if len(tx.queries) > 0 {
		t.Fatalf("expected 0 SQL queries on preflight batch failure, got %d", len(tx.queries))
	}
	if strings.Contains(obs1.Title, privacy.RedactedPlaceholder) {
		t.Fatalf("obs1 was mutated in-place prior to atomic validation")
	}
}

func TestAuthorizedOperations_PrivacyObservation_MalformedMarkerRejected(t *testing.T) {
	tenant, ws := uuid.NewString(), uuid.NewString()
	store := newPrivacyTestStore(tenant, ws)
	tx := &privacyCaptureTx{}
	ctx := context.WithValue(context.WithValue(context.Background(), txKey{}, pgx.Tx(tx)), workspaceKey{}, int64(1))

	canary := "unclosed-canary-secret"
	obs := &domain.Observation{
		Project: "my-proj", Title: "Title <private>" + canary,
		Content: "Valid content",
	}
	err := store.SaveObservation(ctx, obs)
	if err == nil {
		t.Fatal("expected error on unclosed marker")
	}
	if strings.Contains(err.Error(), canary) {
		t.Fatalf("error diagnostic leaked private canary: %v", err)
	}
	if len(tx.queries) > 0 {
		t.Fatalf("expected 0 DB queries, got %d", len(tx.queries))
	}
}

func TestAuthorizedOperations_PrivacyObservation_RequiredEmptyRejected(t *testing.T) {
	tenant, ws := uuid.NewString(), uuid.NewString()
	store := newPrivacyTestStore(tenant, ws)
	tx := &privacyCaptureTx{}
	ctx := context.WithValue(context.WithValue(context.Background(), txKey{}, pgx.Tx(tx)), workspaceKey{}, int64(1))

	obs := &domain.Observation{
		Project: "my-proj", Title: "Valid title",
		Content: "<private>all-private-no-residual</private>",
	}
	err := store.SaveObservation(ctx, obs)
	if err == nil {
		t.Fatal("expected error on required-empty content")
	}
	var privErr *privacy.Error
	if !errors.As(err, &privErr) || privErr.Code != privacy.ErrCodeRequiredEmpty {
		t.Fatalf("expected ErrCodeRequiredEmpty, got %v", err)
	}
}

func TestAuthorizedOperations_PrivacyObservation_MetadataMarkerRejected(t *testing.T) {
	tenant, ws := uuid.NewString(), uuid.NewString()
	store := newPrivacyTestStore(tenant, ws)
	tx := &privacyCaptureTx{}
	ctx := context.WithValue(context.WithValue(context.Background(), txKey{}, pgx.Tx(tx)), workspaceKey{}, int64(1))

	for _, tc := range []struct {
		name string
		obs  *domain.Observation
	}{
		{"marker in project", &domain.Observation{Project: "proj<private>bad</private>", Title: "T", Content: "C"}},
		{"marker in topic_key", &domain.Observation{Project: "proj", TopicKey: "top<private>bad</private>", Title: "T", Content: "C"}},
		{"marker in tag", &domain.Observation{Project: "proj", Tags: []string{"<private>bad</private>"}, Title: "T", Content: "C"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := store.SaveObservation(ctx, tc.obs)
			if err == nil {
				t.Fatalf("%s: expected rejection on marker in metadata", tc.name)
			}
			if len(tx.queries) > 0 {
				t.Fatalf("%s: expected 0 DB queries, got %d", tc.name, len(tx.queries))
			}
		})
	}
}
