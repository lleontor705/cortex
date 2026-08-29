package postgres

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/lleontor705/cortex/v2/internal/authz"
	"github.com/lleontor705/cortex/v2/internal/domain"
)

func TestAgentCodeQueryPatternsCanonicalAndBounded(t *testing.T) {
	patterns := agentCodeQueryPatterns("zeta Alpha SeedSymbol beta gamma delta epsilon eta theta iota")
	if len(patterns) != 8 {
		t.Fatalf("patterns=%v, want fixed budget", patterns)
	}
	for i := 1; i < len(patterns); i++ {
		if patterns[i-1] >= patterns[i] {
			t.Fatalf("patterns not canonical: %v", patterns)
		}
	}
}

type agentGraphTestTx struct {
	pgx.Tx
	queries           []stubQuery
	seedRows, hopRows [][]any
}

func (t *agentGraphTestTx) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	t.queries = append(t.queries, stubQuery{sql: sql, args: append([]any(nil), args...)})
	return agentProjectLabelRow{projectInternalID: 73, label: "shared-label"}
}

func (t *agentGraphTestTx) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	t.queries = append(t.queries, stubQuery{sql: sql, args: append([]any(nil), args...)})
	if strings.Contains(sql, "FROM edges e") {
		return &agentGraphRows{values: t.hopRows, index: -1}, nil
	}
	return &agentGraphRows{values: t.seedRows, index: -1}, nil
}

type agentGraphRows struct {
	pgx.Rows
	values [][]any
	index  int
}

func (r *agentGraphRows) Next() bool { r.index++; return r.index < len(r.values) }
func (r *agentGraphRows) Close()     {}
func (r *agentGraphRows) Err() error { return nil }
func (r *agentGraphRows) Scan(dest ...any) error {
	if r.index < 0 || r.index >= len(r.values) || (len(dest) != len(r.values[r.index]) && len(dest) != len(r.values[r.index])+1) {
		return fmt.Errorf("invalid graph row")
	}
	for i, value := range r.values[r.index] {
		switch out := dest[i].(type) {
		case *string:
			*out = value.(string)
		case *int:
			*out = value.(int)
		case *int64:
			*out = value.(int64)
		case *float64:
			*out = value.(float64)
		default:
			return fmt.Errorf("unsupported graph scan %T", out)
		}
	}
	if len(dest) == len(r.values[r.index])+1 {
		*dest[len(dest)-1].(*int) = 0
	}
	return nil
}

func newAgentGraphStore(t *testing.T, tx pgx.Tx) (*AuthorizedStore, context.Context, string) {
	t.Helper()
	tenant, workspace, project, subject := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	store := &AuthorizedStore{store: &Store{
		tenant:     &domain.TenantContext{TenantID: tenant, WorkspaceID: workspace},
		principal:  domain.Principal{Subject: subject, OrgID: tenant, Roles: []string{"viewer"}, WorkspaceIDs: []string{workspace}, ProjectIDs: []string{project}},
		authorizer: authz.NewPolicy(),
	}}
	ctx := context.WithValue(context.WithValue(context.Background(), txKey{}, tx), workspaceKey{}, int64(42))
	return store, ctx, project
}

func TestAgentGraphSnapshotReservesBudgetsBeforeFanout(t *testing.T) {
	tx := &agentGraphTestTx{}
	store, ctx, project := newAgentGraphStore(t, pgx.Tx(tx))
	result, err := store.GetAgentGraphSnapshot(ctx, project, "shared-label", []string{uuid.NewString()}, 2, 32, 64)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || len(tx.queries) != 2 {
		t.Fatalf("result=%#v queries=%d", result, len(tx.queries))
	}
	seedSQL := tx.queries[1].sql
	for _, forbidden := range []string{"WITH RECURSIVE", "MATERIALIZED"} {
		if strings.Contains(seedSQL, forbidden) {
			t.Fatalf("budget applied after %s: %s", forbidden, seedSQL)
		}
	}
	for _, required := range []string{"o.tenant_id=public.cortex_current_tenant()", "o.workspace_id=$1", "o.project_id=$2", "o.classification NOT IN ('restricted','confidential')", "o.classification <> 'personal' OR o.owner_subject=$3", "ORDER BY o.public_id", "LIMIT"} {
		if !strings.Contains(seedSQL, required) {
			t.Fatalf("seed query omitted %q: %s", required, seedSQL)
		}
	}
}

func TestAgentGraphSnapshotTruncatesMaliciousFanoutDeterministically(t *testing.T) {
	seed := uuid.NewString()
	tx := &agentGraphTestTx{
		seedRows: [][]any{{int64(1), seed, "seed", "decision"}},
		hopRows: [][]any{
			{"edge-a", int64(1), int64(2), "references", float64(1), float64(1), int64(2), uuid.NewString(), "a", "decision"},
			{"edge-b", int64(1), int64(3), "references", float64(1), float64(1), int64(3), uuid.NewString(), "b", "decision"},
			{"edge-c", int64(1), int64(4), "references", float64(1), float64(1), int64(4), uuid.NewString(), "c", "decision"},
		},
	}
	store, ctx, project := newAgentGraphStore(t, pgx.Tx(tx))
	result, err := store.GetAgentGraphSnapshot(ctx, project, "shared-label", []string{seed}, 2, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Nodes) > 2 || len(result.Edges) > 2 || !result.Truncated {
		t.Fatalf("unbounded snapshot: %#v", result)
	}
	if len(tx.queries) < 3 || !strings.Contains(tx.queries[2].sql, "ORDER BY e.public_id") || !strings.Contains(tx.queries[2].sql, "LIMIT") {
		t.Fatalf("fanout query is not canonically budgeted: %#v", tx.queries)
	}
}

func TestAgentGraphSnapshotTruncatedOnlyWhenEvidenceExceedsBudget(t *testing.T) {
	seedA := uuid.NewString()
	seedB := uuid.NewString()
	neighborB := uuid.NewString()
	neighborC := uuid.NewString()
	edgeAB := []any{"edge-a", int64(1), int64(2), "references", float64(1), float64(1), int64(2), neighborB, "b", "decision"}
	edgeAC := []any{"edge-b", int64(1), int64(3), "references", float64(1), float64(1), int64(3), neighborC, "c", "decision"}

	tests := []struct {
		name      string
		seedRows  [][]any
		hopRows   [][]any
		seeds     []string
		maxHops   int
		maxNodes  int
		maxEdges  int
		truncated bool
	}{
		{
			name:      "complete A to B at one hop",
			seedRows:  [][]any{{int64(1), seedA, "a", "decision"}},
			hopRows:   [][]any{edgeAB},
			seeds:     []string{seedA},
			maxHops:   1,
			maxNodes:  2,
			maxEdges:  2,
			truncated: false,
		},
		{
			name: "exactly max nodes without extra",
			seedRows: [][]any{
				{int64(1), seedA, "a", "decision"},
				{int64(2), seedB, "b", "decision"},
			},
			seeds:     []string{seedA, seedB},
			maxHops:   1,
			maxNodes:  2,
			maxEdges:  2,
			truncated: false,
		},
		{
			name:      "exactly max edges without extra",
			seedRows:  [][]any{{int64(1), seedA, "a", "decision"}},
			hopRows:   [][]any{edgeAB},
			seeds:     []string{seedA},
			maxHops:   2,
			maxNodes:  3,
			maxEdges:  1,
			truncated: false,
		},
		{
			name:      "cap plus one",
			seedRows:  [][]any{{int64(1), seedA, "a", "decision"}},
			hopRows:   [][]any{edgeAB, edgeAC},
			seeds:     []string{seedA},
			maxHops:   2,
			maxNodes:  3,
			maxEdges:  1,
			truncated: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx := &agentGraphTestTx{seedRows: tt.seedRows, hopRows: tt.hopRows}
			store, ctx, project := newAgentGraphStore(t, pgx.Tx(tx))
			result, err := store.GetAgentGraphSnapshot(ctx, project, "shared-label", tt.seeds, tt.maxHops, tt.maxNodes, tt.maxEdges)
			if err != nil {
				t.Fatal(err)
			}
			if result.Truncated != tt.truncated {
				t.Fatalf("Truncated=%v, want %v: %#v", result.Truncated, tt.truncated, result)
			}
		})
	}
}
