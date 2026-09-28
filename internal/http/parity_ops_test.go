package http

import (
	"context"
	"database/sql"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/api"
	"github.com/lleontor705/cortex/v2/internal/domain"
	code "github.com/lleontor705/cortex/v2/internal/domain/code"
	"github.com/lleontor705/cortex/v2/internal/migration"
	"github.com/lleontor705/cortex/v2/internal/store/bundle"
	graphstore "github.com/lleontor705/cortex/v2/internal/store/graph"
	"github.com/lleontor705/cortex/v2/internal/store/session"
	sqlitestore "github.com/lleontor705/cortex/v2/internal/store/sqlite"
	"github.com/lleontor705/cortex/v2/testutil"
)

// newOpsTestStores builds a minimal SQLite bundle with exactly the tables the
// stats/projects surface reads: sessions, observations (and their FTS mirror)
// and graph edges. The code schema is created by NewCodeStore itself.
func newOpsTestStores(t *testing.T) (*bundle.Stores, *sql.DB) {
	t.Helper()

	registry := migration.NewRegistry()
	registry.Register(migration.Migration{
		Version: 1, Name: "init",
		UpSQL: `
			CREATE TABLE sessions (id TEXT PRIMARY KEY, project TEXT NOT NULL, directory TEXT NOT NULL,
				started_at TEXT NOT NULL DEFAULT (datetime('now')), ended_at TEXT, summary TEXT);
			CREATE TABLE observations (id INTEGER PRIMARY KEY AUTOINCREMENT, sync_id TEXT,
				session_id TEXT NOT NULL, type TEXT NOT NULL, title TEXT NOT NULL, content TEXT NOT NULL,
				tool_name TEXT, project TEXT, scope TEXT NOT NULL DEFAULT 'project', topic_key TEXT,
				normalized_hash TEXT, revision_count INTEGER NOT NULL DEFAULT 1,
				duplicate_count INTEGER NOT NULL DEFAULT 1, last_seen_at TEXT,
				confidence REAL NOT NULL DEFAULT 1.0,
				source TEXT NOT NULL DEFAULT 'manual',
				tags TEXT,
				created_at TEXT NOT NULL DEFAULT (datetime('now')),
				updated_at TEXT NOT NULL DEFAULT (datetime('now')), deleted_at TEXT,
				FOREIGN KEY (session_id) REFERENCES sessions(id));`,
		DownSQL: `DROP TABLE IF EXISTS observations; DROP TABLE IF EXISTS sessions;`,
	})
	registry.Register(migration.Migration{
		Version: 2, Name: "fts",
		UpSQL: `
			CREATE VIRTUAL TABLE observations_fts USING fts5(title, content, type, project, content=observations, content_rowid=id);
			CREATE TRIGGER observations_fts_insert AFTER INSERT ON observations BEGIN
				INSERT INTO observations_fts(rowid, title, content, type, project) VALUES (new.id, new.title, new.content, new.type, new.project);
			END;`,
		DownSQL: `DROP TRIGGER IF EXISTS observations_fts_insert; DROP TABLE IF EXISTS observations_fts;`,
	})
	registry.Register(migration.Migration{
		Version: 3, Name: "graph",
		UpSQL: `CREATE TABLE edges (id INTEGER PRIMARY KEY AUTOINCREMENT, from_obs_id INTEGER NOT NULL,
			to_obs_id INTEGER NOT NULL, relation_type TEXT NOT NULL, weight REAL NOT NULL DEFAULT 1.0,
			confidence REAL NOT NULL DEFAULT 1.0, source TEXT, reasoning TEXT,
			valid_from TEXT, invalid_at TEXT,
			evolution_id INTEGER, evolution_type TEXT NOT NULL DEFAULT 'original',
			fact_state TEXT NOT NULL DEFAULT 'current', change_reason TEXT,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (from_obs_id) REFERENCES observations(id) ON DELETE CASCADE,
			FOREIGN KEY (to_obs_id) REFERENCES observations(id) ON DELETE CASCADE,
			UNIQUE(from_obs_id, to_obs_id, relation_type));`,
		DownSQL: `DROP TABLE IF EXISTS edges;`,
	})

	testDB := testutil.NewTestDBWithMigrations(t, registry)
	db := testDB.DB()
	codeStore, err := sqlitestore.NewCodeStore(db)
	if err != nil {
		t.Fatalf("OpsTest: create code store: %v", err)
	}
	return &bundle.Stores{
		Observations: sqlitestore.NewStore(db),
		Sessions:     session.NewStore(db),
		Graph:        graphstore.NewStore(db),
		Metrics:      sqlitestore.NewMetricsRepository(db),
		Code:         codeStore,
	}, db
}

func opsTestSession(t *testing.T, stores *bundle.Stores, id, project string, ended bool) {
	t.Helper()
	ctx := context.Background()
	if err := stores.Sessions.Create(ctx, &domain.Session{ID: id, Project: project, Directory: "."}); err != nil {
		t.Fatalf("OpsTest: create session %s: %v", id, err)
	}
	if ended {
		if err := stores.Sessions.End(ctx, id, "done"); err != nil {
			t.Fatalf("OpsTest: end session %s: %v", id, err)
		}
	}
}

func opsTestObservation(t *testing.T, stores *bundle.Stores, sessionID, project, title, content string) int64 {
	t.Helper()
	obs := &domain.Observation{
		SessionID: sessionID, Type: "manual", Title: title, Content: content,
		Project: project, Scope: "project",
	}
	if err := stores.Observations.Save(context.Background(), obs); err != nil {
		t.Fatalf("OpsTest: save observation %q: %v", title, err)
	}
	return obs.ID
}

func opsTestEdge(t *testing.T, stores *bundle.Stores, from, to int64) {
	t.Helper()
	edge := &domain.Edge{FromObsID: from, ToObsID: to, RelationType: "references", Weight: 1, Confidence: 1}
	if err := stores.Graph.CreateEdge(context.Background(), edge); err != nil {
		t.Fatalf("OpsTest: create edge: %v", err)
	}
}

func opsTestSymbol(t *testing.T, stores *bundle.Stores, id, project string) {
	t.Helper()
	symbols := []code.Symbol{{ID: id, Project: project, FilePath: "main.go", LineNumber: 1, Kind: "function", Name: "Main"}}
	if err := stores.Code.SaveSymbols(context.Background(), symbols); err != nil {
		t.Fatalf("OpsTest: save symbol %s: %v", id, err)
	}
}

// TestLocalOpsPortServerStatsDerivesCounters proves the recomputed counters
// equal the seeded rows (REQ-SH-012 local stats scenario).
func TestLocalOpsPortServerStatsDerivesCounters(t *testing.T) {
	stores, _ := newOpsTestStores(t)
	opsTestSession(t, stores, "s1", "alpha", false)
	opsTestSession(t, stores, "s2", "beta", true)
	first := opsTestObservation(t, stores, "s1", "alpha", "one", "content one")
	second := opsTestObservation(t, stores, "s1", "alpha", "two", "content two")
	opsTestObservation(t, stores, "s2", "beta", "three", "content three")
	opsTestEdge(t, stores, first, second)

	stats, err := newLocalOpsPort(stores).ServerStats(context.Background())
	if err != nil {
		t.Fatalf("ServerStats() error = %v", err)
	}
	want := domain.ServerStats{Observations: 3, Sessions: 2, ActiveSessions: 1, Edges: 1, Projects: 2}
	if *stats != want {
		t.Fatalf("ServerStats() = %+v, want %+v", *stats, want)
	}
}

// TestLocalOpsPortProjectsSortedUnion proves the project list is the exact
// sorted union of session and observation project identifiers.
func TestLocalOpsPortProjectsSortedUnion(t *testing.T) {
	stores, _ := newOpsTestStores(t)
	opsTestSession(t, stores, "s1", "beta", false)
	opsTestSession(t, stores, "s2", "alpha", false)
	opsTestObservation(t, stores, "s1", "gamma", "g", "content g")
	opsTestObservation(t, stores, "s1", "alpha", "a", "content a")

	projects, err := newLocalOpsPort(stores).Projects(context.Background())
	if err != nil {
		t.Fatalf("Projects() error = %v", err)
	}
	want := []string{"alpha", "beta", "gamma"}
	if len(projects) != len(want) {
		t.Fatalf("Projects() = %v, want %v", projects, want)
	}
	for i := range want {
		if projects[i] != want[i] {
			t.Fatalf("Projects() = %v, want %v", projects, want)
		}
	}
}

// TestLocalOpsPortProjectsEmptyIsNonNil pins the empty result to a JSON array
// (not null), matching the server's make-backed ListProjects.
func TestLocalOpsPortProjectsEmptyIsNonNil(t *testing.T) {
	stores, _ := newOpsTestStores(t)

	projects, err := newLocalOpsPort(stores).Projects(context.Background())
	if err != nil {
		t.Fatalf("Projects() error = %v", err)
	}
	if projects == nil {
		t.Fatal("Projects() = nil, want non-nil empty slice")
	}
	if len(projects) != 0 {
		t.Fatalf("Projects() = %v, want empty", projects)
	}
}

// TestLocalOpsPortAgentProjectsMergesMemoryAndCode covers the corpus rule: a
// project qualifies with memory alone or with code alone, and a code-only
// project is discovered even though it has no session or observation.
func TestLocalOpsPortAgentProjectsMergesMemoryAndCode(t *testing.T) {
	stores, _ := newOpsTestStores(t)
	opsTestSession(t, stores, "s1", "memory-proj", false)
	opsTestObservation(t, stores, "s1", "memory-proj", "m", "content m")
	opsTestSymbol(t, stores, "sym-code-only", "code-only")
	opsTestSymbol(t, stores, "sym-shared", "memory-proj")

	projects, err := newLocalOpsPort(stores).AgentProjects(context.Background())
	if err != nil {
		t.Fatalf("AgentProjects() error = %v", err)
	}
	want := map[string]string{"memory-proj": "memory-proj", "code-only": "code-only"}
	if len(projects) != len(want) {
		t.Fatalf("AgentProjects() = %v, want %v", projects, want)
	}
	for id, label := range want {
		if projects[id] != label {
			t.Fatalf("AgentProjects()[%q] = %q, want %q", id, projects[id], label)
		}
	}
}

// TestLocalOpsPortNilStoresDegrade proves the zero-embedding/minimal bundle is
// a supported composition: every method returns an empty result, no panic.
func TestLocalOpsPortNilStoresDegrade(t *testing.T) {
	ctx := context.Background()
	for name, port := range map[string]*localOpsPort{
		"nil bundle":   newLocalOpsPort(nil),
		"empty bundle": newLocalOpsPort(&bundle.Stores{}),
		"nil adapter":  nil,
	} {
		t.Run(name, func(t *testing.T) {
			stats, err := port.ServerStats(ctx)
			if err != nil {
				t.Fatalf("ServerStats() error = %v", err)
			}
			if *stats != (domain.ServerStats{}) {
				t.Fatalf("ServerStats() = %+v, want zero", *stats)
			}
			projects, err := port.Projects(ctx)
			if err != nil {
				t.Fatalf("Projects() error = %v", err)
			}
			if projects == nil || len(projects) != 0 {
				t.Fatalf("Projects() = %v, want non-nil empty", projects)
			}
			agents, err := port.AgentProjects(ctx)
			if err != nil {
				t.Fatalf("AgentProjects() error = %v", err)
			}
			if agents == nil || len(agents) != 0 {
				t.Fatalf("AgentProjects() = %v, want non-nil empty", agents)
			}
			principal, err := port.CurrentPrincipal(ctx)
			if err != nil {
				t.Fatalf("CurrentPrincipal() error = %v", err)
			}
			if len(principal.Identity.Roles) != 1 || principal.Identity.Roles[0] != "owner" {
				t.Fatalf("CurrentPrincipal() roles = %v, want [owner]", principal.Identity.Roles)
			}
		})
	}
}

// TestLocalOpsPortCurrentPrincipalOwnerDefault pins the synthetic local
// identity and the injected-principal override.
func TestLocalOpsPortCurrentPrincipalOwnerDefault(t *testing.T) {
	ctx := context.Background()

	principal, err := newLocalOpsPort(nil).CurrentPrincipal(ctx)
	if err != nil {
		t.Fatalf("CurrentPrincipal() error = %v", err)
	}
	if principal.Identity.Subject == "" {
		t.Fatal("CurrentPrincipal() subject is empty")
	}
	if principal.Identity.Type == "" {
		t.Fatal("CurrentPrincipal() type is empty")
	}
	if principal.WorkspaceID == "" {
		t.Fatal("CurrentPrincipal() workspace id is empty")
	}
	if len(principal.Identity.ProjectIDs) != 1 || principal.Identity.ProjectIDs[0] != "*" {
		t.Fatalf("CurrentPrincipal() project grants = %v, want [*]", principal.Identity.ProjectIDs)
	}

	custom := newLocalOpsPort(nil)
	custom.principal = api.Principal{Identity: domain.Principal{Subject: "configured"}}
	overridden, err := custom.CurrentPrincipal(ctx)
	if err != nil {
		t.Fatalf("CurrentPrincipal() error = %v", err)
	}
	if overridden.Identity.Subject != "configured" {
		t.Fatalf("CurrentPrincipal() subject = %q, want configured", overridden.Identity.Subject)
	}
}

// TestLocalOpsPortPropagatesStoreErrors proves a failing store surfaces its
// error instead of being swallowed into an empty result.
func TestLocalOpsPortPropagatesStoreErrors(t *testing.T) {
	ctx := context.Background()
	empty := testutil.NewTestDB(t).DB() // no tables: every read fails

	statsPort := &localOpsPort{stores: &bundle.Stores{Sessions: session.NewStore(empty)}}
	if _, err := statsPort.ServerStats(ctx); err == nil {
		t.Fatal("ServerStats() error = nil, want store failure")
	}

	projectsPort := &localOpsPort{stores: &bundle.Stores{Observations: sqlitestore.NewStore(empty)}}
	if _, err := projectsPort.Projects(ctx); err == nil {
		t.Fatal("Projects() error = nil, want store failure")
	}

	agentPort := &localOpsPort{stores: &bundle.Stores{Sessions: session.NewStore(empty)}}
	if _, err := agentPort.AgentProjects(ctx); err == nil {
		t.Fatal("AgentProjects() error = nil, want store failure")
	}
}
