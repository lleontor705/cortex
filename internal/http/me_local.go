package http

import (
	"github.com/lleontor705/cortex/v2/internal/api"
	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/store/bundle"
	sqlitestore "github.com/lleontor705/cortex/v2/internal/store/sqlite"
)

// localParityPort is the composite api.Port the local serve mux hands to
// internal/api: the bundle-backed operations adapter (principal, stats,
// projects, agent projects) plus the graph-traversal slice. Both halves are
// embedded so their method sets promote without redeclaring ProjectGraph.
type localParityPort struct {
	*localOpsPort
	localProjectGraph
}

// newLocalParityPort assembles the local parity Port over the serve Deps. The
// operations half needs the code store, which Deps does not carry, so its
// bundle view is reconstructed from the shared SQLite handle; the graph half
// binds to Deps directly. The synthetic local principal is injected here so
// /api/me answers with the local identity rather than a null principal.
func newLocalParityPort(deps *Deps) *localParityPort {
	ops := newLocalOpsPort(localBundleStores(deps))
	ops.principal = localOwnerPrincipal()
	return &localParityPort{
		localOpsPort:      ops,
		localProjectGraph: newLocalProjectGraph(deps),
	}
}

// localOwnerPrincipal is the synthetic identity the single-user local serve
// process reports from GET /api/me. Local mode has no credential exchange or
// tenant directory, so the web client receives a fixed service-account
// principal carrying the owner role and wildcard grants (REQ-SH-012).
func localOwnerPrincipal() api.Principal {
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

// localBundleStores reconstructs the bundle view of the serve Deps from the
// shared SQLite handle. Observations are re-wrapped to reach the concrete
// *sqlitestore.Store, and the code store supplies the code-only half of the
// agent-project corpus. A missing handle degrades to nil, and an unusable code
// schema leaves Code nil; the operations adapter tolerates both.
func localBundleStores(deps *Deps) *bundle.Stores {
	if deps == nil || deps.Observations == nil {
		return nil
	}
	db := deps.Observations.DB()
	if db == nil {
		return nil
	}
	stores := &bundle.Stores{
		Observations: sqlitestore.NewStore(db),
		Sessions:     deps.Sessions,
		Graph:        deps.Graph,
	}
	if codeStore, err := sqlitestore.NewCodeStore(db); err == nil {
		stores.Code = codeStore
	}
	return stores
}
