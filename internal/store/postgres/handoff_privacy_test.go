package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/domain/privacy"
)

func TestPostgres_PrivacyHandoff_ValidProseRedacted(t *testing.T) {
	authorized, tx := stubAuthorizedStore(t)
	ctx := ctxWithStubTxAndWorkspace(tx)

	req := domain.HandoffRequest{
		IdempotencyKey: "hand-priv-001",
		Observation: domain.SaveObservationInput{
			Project: "stub", Scope: domain.ScopeProject, Type: domain.TypeDecision,
			Title:   "Decision <private>secret-decision</private> accepted",
			Content: "Details <private>secret-token</private> recorded",
		},
	}

	canonical, payload, _, err := domain.CanonicalizeHandoff(req)
	if err != nil {
		t.Fatalf("CanonicalizeHandoff failed: %v", err)
	}

	if strings.Contains(canonical.Observation.Title, "secret-decision") {
		t.Fatalf("canonical title leaked secret: %q", canonical.Observation.Title)
	}
	if !strings.Contains(canonical.Observation.Title, privacy.RedactedPlaceholder) {
		t.Fatalf("canonical title missing placeholder: %q", canonical.Observation.Title)
	}
	if strings.Contains(string(payload), "secret-decision") || strings.Contains(string(payload), "secret-token") {
		t.Fatal("canonical payload leaked secret canary")
	}

	res, execErr := authorized.ExecuteHandoff(ctx, req)
	if execErr != nil {
		t.Fatalf("ExecuteHandoff failed: %v\nissued=%s", execErr, tx.issuedSQL())
	}
	if res.Status != domain.WriteStatusCreated || res.Ref.PublicID == nil {
		t.Fatalf("unexpected handoff result: %+v", res)
	}
	for _, q := range tx.queries {
		for _, arg := range q.args {
			if s, ok := arg.(string); ok {
				if strings.Contains(s, "secret-decision") || strings.Contains(s, "secret-token") {
					t.Fatalf("private canary leaked into handoff SQL argument: %q", s)
				}
			}
		}
	}
}

func TestPostgres_PrivacyHandoff_MalformedProseRejected(t *testing.T) {
	tenant, ws := uuid.NewString(), uuid.NewString()
	store := newPrivacyTestStore(tenant, ws)

	canary := "unclosed-handoff-secret"
	req := domain.HandoffRequest{
		IdempotencyKey: "hand-priv-002",
		Observation: domain.SaveObservationInput{
			Project: "my-proj", Scope: domain.ScopeProject, Type: domain.TypeDecision,
			Title:   "Decision <private>" + canary,
			Content: "Valid content",
		},
	}

	_, err := store.ExecuteHandoff(context.Background(), req)
	if err == nil {
		t.Fatal("expected error on unclosed marker in handoff")
	}
	if strings.Contains(err.Error(), canary) {
		t.Fatalf("ExecuteHandoff error leaked private canary: %v", err)
	}
	var privErr *privacy.Error
	if !errors.As(err, &privErr) || privErr.Code != privacy.ErrCodeInvalidMarker {
		t.Fatalf("expected ErrCodeInvalidMarker, got %v", err)
	}
}

func TestPostgres_PrivacyHandoff_RequiredEmptyRejected(t *testing.T) {
	tenant, ws := uuid.NewString(), uuid.NewString()
	store := newPrivacyTestStore(tenant, ws)

	req := domain.HandoffRequest{
		IdempotencyKey: "hand-priv-003",
		Observation: domain.SaveObservationInput{
			Project: "my-proj", Scope: domain.ScopeProject, Type: domain.TypeDecision,
			Title:   "Valid Title",
			Content: "<private>all-private-no-residual</private>",
		},
	}

	_, err := store.ExecuteHandoff(context.Background(), req)
	if err == nil {
		t.Fatal("expected error on required-empty handoff content")
	}
	var privErr *privacy.Error
	if !errors.As(err, &privErr) || privErr.Code != privacy.ErrCodeRequiredEmpty {
		t.Fatalf("expected ErrCodeRequiredEmpty, got %v", err)
	}
}

func TestPostgres_PrivacyHandoff_MetadataMarkerRejected(t *testing.T) {
	tenant, ws := uuid.NewString(), uuid.NewString()
	store := newPrivacyTestStore(tenant, ws)

	for _, tc := range []struct {
		name string
		req  domain.HandoffRequest
	}{
		{
			name: "marker in project",
			req: domain.HandoffRequest{
				IdempotencyKey: "k1",
				Observation: domain.SaveObservationInput{
					Project: "p<private>bad</private>", Scope: domain.ScopeProject,
					Title: "T", Content: "C",
				},
			},
		},
		{
			name: "marker in scope",
			req: domain.HandoffRequest{
				IdempotencyKey: "k2",
				Observation: domain.SaveObservationInput{
					Project: "p", Scope: "<private>bad</private>",
					Title: "T", Content: "C",
				},
			},
		},
		{
			name: "marker in idempotency key",
			req: domain.HandoffRequest{
				IdempotencyKey: "key-<private>bad</private>",
				Observation: domain.SaveObservationInput{
					Project: "p", Scope: domain.ScopeProject,
					Title: "T", Content: "C",
				},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := store.ExecuteHandoff(context.Background(), tc.req)
			if err == nil {
				t.Fatalf("%s: expected rejection on marker in metadata", tc.name)
			}
			if strings.Contains(err.Error(), "<private>") {
				t.Fatalf("%s: error leaked marker syntax: %v", tc.name, err)
			}
		})
	}
}
