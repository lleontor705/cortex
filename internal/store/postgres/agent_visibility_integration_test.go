//go:build postgres_integration

package postgres

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/lleontor705/cortex/v2/internal/authz"
)

func TestAgentVectorHydrationVisibility(t *testing.T) {
	h := newPostgresHarness(t)
	tenant, workspace, siblingWorkspace := uuid.New(), uuid.New(), uuid.New()
	project, siblingProject := uuid.New(), uuid.New()
	fixture := newScopedCodeFixture(t, h, tenant, workspace, project, "shared-label")
	sibling := newScopedCodeFixture(t, h, tenant, siblingWorkspace, siblingProject, "shared-label")
	actor, foreign := uuid.New(), uuid.New()
	store := newReindexPrincipalStore(t, h, fixture, actor, "user", []string{"viewer"}, nil, nil)

	visibleID := insertReindexObservation(t, h, fixture, "visible", "project", foreign)
	restrictedID := insertReindexObservation(t, h, fixture, "restricted", "restricted", foreign)
	confidentialID := insertReindexObservation(t, h, fixture, "confidential", "confidential", foreign)
	personalID := insertReindexObservation(t, h, fixture, "foreign-personal", "personal", foreign)
	ownPersonalID := insertReindexObservation(t, h, fixture, "own-personal", "personal", actor)
	siblingID := insertReindexObservation(t, h, sibling, "sibling", "project", foreign)

	for _, tc := range []struct {
		name string
		id   int64
	}{
		{name: "restricted without clearance", id: restrictedID},
		{name: "confidential without clearance", id: confidentialID},
		{name: "foreign personal", id: personalID},
		{name: "duplicate label in sibling workspace", id: siblingID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			observation, err := store.GetAgentObservationByID(t.Context(), project.String(), "shared-label", tc.id)
			if observation != nil || !errors.Is(err, authz.ErrResourceNotFound) {
				t.Fatalf("hydration returned observation=%v error=%v, want indistinguishable not-found", observation != nil, err)
			}
		})
	}

	for _, tc := range []struct {
		name string
		id   int64
	}{
		{name: "visible project row", id: visibleID},
		{name: "owned personal row", id: ownPersonalID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			observation, err := store.GetAgentObservationByID(t.Context(), project.String(), "shared-label", tc.id)
			if err != nil {
				t.Fatalf("hydrate visible observation: %v", err)
			}
			if observation.ID != tc.id || observation.Project != project.String() {
				t.Fatalf("hydrated observation = id:%d project:%q, want id:%d project:%q", observation.ID, observation.Project, tc.id, project.String())
			}
		})
	}

	cleared := newReindexPrincipalStore(t, h, fixture, uuid.New(), "user", []string{"viewer"}, []string{"restricted", "confidential"}, nil)
	for _, id := range []int64{restrictedID, confidentialID} {
		if observation, err := cleared.GetAgentObservationByID(t.Context(), project.String(), "shared-label", id); err != nil || observation.ID != id {
			t.Fatalf("cleared hydration id=%d returned observation=%v error=%v", id, observation != nil, err)
		}
	}

	if _, err := store.GetAgentObservationByID(t.Context(), siblingProject.String(), "shared-label", siblingID); err == nil || err.Error() != authz.DenyProject {
		t.Fatalf("ungranted duplicate project UUID error = %v, want %s", err, authz.DenyProject)
	}
}
