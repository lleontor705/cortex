package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/domain/privacy"
)

type syncMockTx struct {
	privacyCaptureTx
	changes []syncChange
	rowMap  map[string][]any
}

func (m *syncMockTx) Query(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
	return &syncMockRows{changes: m.changes}, nil
}

func (m *syncMockTx) QueryRow(_ context.Context, _ string, args ...any) pgx.Row {
	m.privacyCaptureTx.QueryRow(context.Background(), "", args...)
	for _, arg := range args {
		if id, ok := arg.(string); ok && m.rowMap != nil {
			if vals, exists := m.rowMap[id]; exists {
				return &syncMockValRow{vals: vals}
			}
		}
	}
	return &privacyStubRow{id: 101, publicID: "20000000-0000-0000-0000-000000000001"}
}

type syncMockRows struct {
	pgx.Rows
	changes []syncChange
	idx     int
}

func (r *syncMockRows) Next() bool {
	if r.idx < len(r.changes) {
		r.idx++
		return true
	}
	return false
}

func (r *syncMockRows) Scan(dest ...any) error {
	c := r.changes[r.idx-1]
	*dest[0].(*int64) = c.sequence
	*dest[1].(*string) = c.kind
	*dest[2].(*string) = c.publicID
	*dest[3].(*string) = c.syncID
	*dest[4].(*bool) = c.deleted
	return nil
}
func (*syncMockRows) Close()     {}
func (*syncMockRows) Err() error { return nil }

type syncMockValRow struct{ vals []any }

func (r *syncMockValRow) Scan(dest ...any) error {
	for i, d := range dest {
		if i < len(r.vals) && r.vals[i] != nil {
			switch target := d.(type) {
			case *string:
				if s, ok := r.vals[i].(string); ok {
					*target = s
				}
			case *time.Time:
				if t, ok := r.vals[i].(time.Time); ok {
					*target = t
				}
			case *float64:
				if f, ok := r.vals[i].(float64); ok {
					*target = f
				}
			case **time.Time:
				if t, ok := r.vals[i].(*time.Time); ok {
					*target = t
				}
			case *[]byte:
				if b, ok := r.vals[i].([]byte); ok {
					*target = b
				}
			}
		}
	}
	return nil
}

func TestPostgresPrivacySync_PushBatchRedaction(t *testing.T) {
	store := newPrivacyTestStore(uuid.NewString(), uuid.NewString())
	tx := &syncMockTx{}
	ctx := context.WithValue(context.WithValue(context.Background(), txKey{}, pgx.Tx(tx)), workspaceKey{}, int64(1))
	now := time.Now().UTC()

	batch := &domain.SyncBatch{
		Sessions: []domain.SyncSession{{
			SyncID: "s1", Project: "proj-sync", Summary: "S <private>canary-sess</private> sum",
			StartedAt: now, UpdatedAt: now,
		}},
		Observations: []domain.SyncObservation{{
			SyncID: "o1", SessionSyncID: "s1", Project: "proj-sync", Scope: "project", Type: "manual",
			Title:     "Title <private>canary-obs-title</private> ok",
			Content:   "Obs <private>canary-obs-content</private> body",
			CreatedAt: now, UpdatedAt: now,
		}},
		Prompts: []domain.SyncPrompt{{
			SyncID: "p1", SessionSyncID: "s1", Project: "proj-sync",
			Content:   "Prompt <private>canary-prompt</private> txt",
			CreatedAt: now, UpdatedAt: now,
		}},
		Edges: []domain.SyncEdge{{
			SyncID: "e1", FromSyncID: "o1", ToSyncID: "o2", Relation: "relates_to",
			Reasoning: "Edge <private>canary-edge</private> reason",
			CreatedAt: now, UpdatedAt: now,
		}},
	}

	res, err := store.PushSync(ctx, batch)
	if err != nil {
		t.Fatalf("PushSync failed: %v", err)
	}
	if res.Accepted != 4 {
		t.Fatalf("expected 4 accepted, got %d", res.Accepted)
	}

	canaries := []string{"canary-sess", "canary-obs-title", "canary-obs-content", "canary-prompt", "canary-edge"}
	for _, argList := range tx.args {
		for _, arg := range argList {
			if s, ok := arg.(string); ok {
				for _, c := range canaries {
					if strings.Contains(s, c) {
						t.Fatalf("private canary %q leaked into SQL arg: %s", c, s)
					}
				}
			}
		}
	}
}

func TestPostgresPrivacySync_PushMalformedRejectionAtomic(t *testing.T) {
	store := newPrivacyTestStore(uuid.NewString(), uuid.NewString())
	tx := &syncMockTx{}
	ctx := context.WithValue(context.WithValue(context.Background(), txKey{}, pgx.Tx(tx)), workspaceKey{}, int64(1))
	now := time.Now().UTC()

	batch := &domain.SyncBatch{
		Observations: []domain.SyncObservation{{
			SyncID: "o1", SessionSyncID: "s1", Project: "p", Scope: "project", Type: "manual",
			Title: "Valid Title", Content: "Bad <private>unclosed-canary",
			CreatedAt: now, UpdatedAt: now,
		}},
	}

	_, err := store.PushSync(ctx, batch)
	if err == nil || !errors.Is(err, privacy.ErrInvalidMarker) {
		t.Fatalf("expected ErrInvalidMarker, got %v", err)
	}
	if len(tx.queries) > 0 {
		t.Fatalf("expected 0 queries on rejection, got %d", len(tx.queries))
	}
	if strings.Contains(err.Error(), "unclosed-canary") {
		t.Fatalf("error leaked private canary: %s", err.Error())
	}
}

func TestPostgresPrivacySync_PushMetadataRejection(t *testing.T) {
	store := newPrivacyTestStore(uuid.NewString(), uuid.NewString())
	tx := &syncMockTx{}
	ctx := context.WithValue(context.WithValue(context.Background(), txKey{}, pgx.Tx(tx)), workspaceKey{}, int64(1))
	now := time.Now().UTC()

	batch := &domain.SyncBatch{
		Sessions: []domain.SyncSession{{
			SyncID: "s1", Project: "proj/<private>canary</private>",
			StartedAt: now, UpdatedAt: now,
		}},
	}

	_, err := store.PushSync(ctx, batch)
	if err == nil || (!errors.Is(err, privacy.ErrMetadataRejected) && !errors.Is(err, privacy.ErrInvalidMarker)) {
		t.Fatalf("expected metadata rejection, got %v", err)
	}
	if len(tx.queries) > 0 {
		t.Fatalf("expected 0 queries on metadata rejection, got %d", len(tx.queries))
	}
}

func TestPostgresPrivacySync_PullRedactsLegacyValidMarkers(t *testing.T) {
	store := newPrivacyTestStore(uuid.NewString(), uuid.NewString())
	now := time.Now().UTC()
	pubID := "10000000-0000-0000-0000-000000000001"
	tx := &syncMockTx{
		changes: []syncChange{
			{sequence: 1, kind: "observations", publicID: pubID, syncID: "o1"},
		},
		rowMap: map[string][]any{
			pubID: {"o1", "s1", "Title", "Legacy <private>legacy-canary</private> text", "manual", "p", "project", "", 1.0, "manual", []byte("[]"), now, now, nil},
		},
	}
	ctx := context.WithValue(context.WithValue(context.Background(), txKey{}, pgx.Tx(tx)), workspaceKey{}, int64(1))

	page, err := store.PullSync(ctx, 0, 10)
	if err != nil {
		t.Fatalf("PullSync failed: %v", err)
	}
	if page.Cursor != 1 || len(page.Observations) != 1 {
		t.Fatalf("unexpected page: cursor=%d, len=%d", page.Cursor, len(page.Observations))
	}
	obs := page.Observations[0]
	if strings.Contains(obs.Content, "legacy-canary") {
		t.Fatalf("legacy canary not redacted: %q", obs.Content)
	}
	if !strings.Contains(obs.Content, privacy.RedactedPlaceholder) {
		t.Fatalf("expected placeholder in content: %q", obs.Content)
	}
}

func TestPostgresPrivacySync_PullMalformedRejectionNoCursorAdvance(t *testing.T) {
	store := newPrivacyTestStore(uuid.NewString(), uuid.NewString())
	now := time.Now().UTC()
	pubID := "10000000-0000-0000-0000-000000000002"
	tx := &syncMockTx{
		changes: []syncChange{
			{sequence: 2, kind: "observations", publicID: pubID, syncID: "o2"},
		},
		rowMap: map[string][]any{
			pubID: {"o2", "s1", "Title", "Bad <private>unclosed-legacy", "manual", "p", "project", "", 1.0, "manual", []byte("[]"), now, now, nil},
		},
	}
	ctx := context.WithValue(context.WithValue(context.Background(), txKey{}, pgx.Tx(tx)), workspaceKey{}, int64(1))

	page, err := store.PullSync(ctx, 0, 10)
	if err == nil || !errors.Is(err, privacy.ErrInvalidMarker) {
		t.Fatalf("expected ErrInvalidMarker on malformed pull, got %v", err)
	}
	if page != nil {
		t.Fatalf("expected nil page on malformed pull, got %+v", page)
	}
	if strings.Contains(err.Error(), "unclosed-legacy") {
		t.Fatalf("pull error leaked private canary: %s", err.Error())
	}
}
