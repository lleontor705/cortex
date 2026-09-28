package http

import (
	"context"
	"sort"

	"github.com/lleontor705/cortex/v2/internal/api"
	"github.com/lleontor705/cortex/v2/internal/domain"
	code "github.com/lleontor705/cortex/v2/internal/domain/code"
	"github.com/lleontor705/cortex/v2/internal/store/bundle"
)

// localOpsCodeProjectScanLimit asks CodeStore.ListSymbols for every indexed
// symbol so code-only projects (no memory rows) are still discovered. 100000
// is CodeStore's own clamp ceiling; a larger value would be silently reduced.
const localOpsCodeProjectScanLimit = 100000

// localOpsPort is the bundle-backed half of the local parity Port (REQ-SH-012):
// principal, stats, projects and agent projects. The graph slice lives in
// localProjectGraph (parity_graph.go) and the serve mux composes both into the
// api.Port handed to internal/api.New.
//
// Every method tolerates a nil bundle or nil optional stores (zero-embedding /
// minimal local composition) by returning an empty result instead of panicking;
// a non-nil store that fails propagates its error so the api handler maps it to
// the shared envelope.
type localOpsPort struct {
	stores *bundle.Stores
	// principal is the synthetic single-user local identity. When empty,
	// principalOrDefault supplies the fixed owner principal.
	principal api.Principal
}

// newLocalOpsPort binds the local parity operations to the SQLite bundle.
func newLocalOpsPort(stores *bundle.Stores) *localOpsPort {
	return &localOpsPort{stores: stores}
}

// CurrentPrincipal returns the synthetic local principal so the web client no
// longer observes a null principal (REQ-SH-012). Local mode is single-user, so
// the identity carries the owner role and wildcard grants.
func (p *localOpsPort) CurrentPrincipal(context.Context) (api.Principal, error) {
	return p.principalOrDefault(), nil
}

// principalOrDefault returns the injected principal when set, otherwise the
// fixed local owner identity. A nil adapter still yields the default.
func (p *localOpsPort) principalOrDefault() api.Principal {
	if p != nil && p.principal.Identity.Subject != "" {
		return p.principal
	}
	return api.Principal{
		Identity: domain.Principal{
			Subject:                 "local-owner",
			Type:                    "service_account",
			OrgID:                   "local",
			WorkspaceIDs:            []string{"local"},
			Roles:                   []string{"owner"},
			AuthMethod:              "static",
			GrantDigest:             "static-single-tenant",
			GrantVersion:            1,
			RateLimitTier:           "standard",
			ProjectIDs:              []string{"*"},
			ClassificationClearance: []string{"*"},
		},
		WorkspaceID: "local",
	}
}

// ServerStats recomputes the dashboard counters because the local SQLite
// bundle exposes no GetServerStats. Observations and projects come from the
// observation store, sessions and active sessions from the session store, and
// edges from the graph store; bundle.Metrics carries only time-range aggregates
// (averages), not totals, so it contributes no counter.
func (p *localOpsPort) ServerStats(ctx context.Context) (*domain.ServerStats, error) {
	stats := &domain.ServerStats{}
	if p == nil || p.stores == nil {
		return stats, nil
	}

	projects := make(map[string]struct{})
	if p.stores.Sessions != nil {
		sessions, err := p.stores.Sessions.GetStats(ctx)
		if err != nil {
			return nil, err
		}
		if sessions != nil {
			stats.Sessions = sessions.TotalSessions
			stats.ActiveSessions = sessions.ActiveSessions
			opsAddProjectIDs(projects, sessions.Projects)
		}
	}
	if p.stores.Observations != nil {
		observations, err := p.stores.Observations.Stats(ctx)
		if err != nil {
			return nil, err
		}
		if observations != nil {
			stats.Observations = observations.TotalObservations
			opsAddProjectIDs(projects, observations.Projects)
		}
	}
	stats.Projects = len(projects)

	if p.stores.Graph != nil {
		edges, err := p.stores.Graph.CountAllEdges(ctx)
		if err != nil {
			return nil, err
		}
		stats.Edges = edges
	}
	return stats, nil
}

// Projects returns the sorted union of the non-empty project identifiers
// recorded on sessions and observations. The result is always a non-nil slice,
// matching the server's make-backed ListProjects list ([] rather than null).
func (p *localOpsPort) Projects(ctx context.Context) ([]string, error) {
	projectSet, err := p.memoryProjectIDs(ctx)
	if err != nil {
		return nil, err
	}
	projects := make([]string, 0, len(projectSet))
	for id := range projectSet {
		projects = append(projects, id)
	}
	sort.Strings(projects)
	return projects, nil
}

// AgentProjects returns the id/label pairs the agent project picker consumes.
// It mirrors the server's corpus rule: a project qualifies when it has memory
// (a session or observation) or indexed code. Local mode has no separate
// public identifier table, so each project's identifier is also its label.
func (p *localOpsPort) AgentProjects(ctx context.Context) (map[string]string, error) {
	memory, err := p.memoryProjectIDs(ctx)
	if err != nil {
		return nil, err
	}
	codeProjects, err := p.codeProjectIDs(ctx)
	if err != nil {
		return nil, err
	}

	projects := make(map[string]string, len(memory)+len(codeProjects))
	for id := range memory {
		projects[id] = id
	}
	for id := range codeProjects {
		projects[id] = id
	}
	return projects, nil
}

// memoryProjectIDs unions the non-empty project identifiers on sessions and
// observations. A nil adapter or nil stores yield an empty set.
func (p *localOpsPort) memoryProjectIDs(ctx context.Context) (map[string]struct{}, error) {
	projectSet := make(map[string]struct{})
	if p == nil || p.stores == nil {
		return projectSet, nil
	}
	if p.stores.Sessions != nil {
		sessions, err := p.stores.Sessions.GetStats(ctx)
		if err != nil {
			return nil, err
		}
		if sessions != nil {
			opsAddProjectIDs(projectSet, sessions.Projects)
		}
	}
	if p.stores.Observations != nil {
		observations, err := p.stores.Observations.Stats(ctx)
		if err != nil {
			return nil, err
		}
		if observations != nil {
			opsAddProjectIDs(projectSet, observations.Projects)
		}
	}
	return projectSet, nil
}

// codeProjectIDs returns the projects with at least one indexed code symbol.
// A nil adapter or an unwired code store yields an empty set.
func (p *localOpsPort) codeProjectIDs(ctx context.Context) (map[string]struct{}, error) {
	projectSet := make(map[string]struct{})
	if p == nil || p.stores == nil || p.stores.Code == nil {
		return projectSet, nil
	}
	symbols, err := p.stores.Code.ListSymbols(ctx, code.SymbolFilter{Limit: localOpsCodeProjectScanLimit})
	if err != nil {
		return nil, err
	}
	for _, symbol := range symbols {
		if symbol.Project != "" {
			projectSet[symbol.Project] = struct{}{}
		}
	}
	return projectSet, nil
}

// opsAddProjectIDs inserts every non-empty identifier into set.
func opsAddProjectIDs(set map[string]struct{}, ids []string) {
	for _, id := range ids {
		if id != "" {
			set[id] = struct{}{}
		}
	}
}
