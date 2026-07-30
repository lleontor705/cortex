package authz

import (
	"context"
	"testing"

	"github.com/lleontor705/cortex/internal/domain"
)

func FuzzPolicyNeverPanics(f *testing.F) {
	f.Add("tenant", "workspace", "project", "read")
	f.Add("", "", "", "")
	f.Fuzz(func(t *testing.T, tenant, workspace, project, action string) {
		p := domain.Principal{Subject: "subject", OrgID: tenant, Roles: []string{string(RoleMember)}, WorkspaceIDs: []string{workspace}, Scopes: []string{"project:" + project}}
		_ = NewPolicy().Authorize(context.Background(), Request{
			Principal:    p,
			Tenant:       Tenant{ID: tenant, WorkspaceID: workspace, ProjectID: project},
			Resource:     ResourceRef{TenantID: tenant, WorkspaceID: workspace, ProjectID: project},
			ResourceType: ResourceMemory,
			Action:       Action(action),
		})
	})
}
