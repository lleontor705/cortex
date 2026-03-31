package search

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/lleontor705/cortex/internal/domain"
	_ "modernc.org/sqlite"
)

func setupBenchDB(b *testing.B, numObs int) *sql.DB {
	b.Helper()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = db.Close() })

	// Apply schema
	for _, stmt := range []string{
		getInitMigrationSQL(),
		getFTSMigrationSQL(),
		getImportanceScoresMigrationSQL(),
		getEdgesMigrationSQL(),
	} {
		if _, err := db.Exec(stmt); err != nil {
			b.Fatalf("schema: %v", err)
		}
	}

	// Create session
	now := time.Now().Format(time.RFC3339)
	_, _ = db.Exec(`INSERT INTO sessions(id, project, directory, started_at) VALUES('bench-session', 'bench', '/tmp', ?)`, now)

	topics := []string{"architecture/auth", "bug/token-leak", "decision/jwt", "pattern/middleware", "config/cors"}
	for i := 1; i <= numObs; i++ {
		title := fmt.Sprintf("Observation %d about authentication and security", i)
		content := fmt.Sprintf("Content %d: authentication JWT tokens middleware patterns security best practices for the application.", i)
		topicKey := topics[i%len(topics)]
		createdAt := time.Now().Add(-time.Duration(i) * time.Hour).Format(time.RFC3339)

		_, err := db.Exec(`
			INSERT INTO observations (id, session_id, type, title, content, project, scope, topic_key, created_at, updated_at)
			VALUES (?, 'bench-session', 'decision', ?, ?, 'bench', 'project', ?, ?, ?)
		`, i, title, content, topicKey, createdAt, createdAt)
		if err != nil {
			b.Fatalf("insert observation %d: %v", i, err)
		}

		if i%2 == 0 {
			_, _ = db.Exec(`INSERT INTO importance_scores(observation_id, score, access_count, last_accessed) VALUES(?, ?, ?, datetime('now'))`,
				i, float64(i%5), i%10)
		}
		if i > 1 && i%3 == 0 {
			_, _ = db.Exec(`INSERT OR IGNORE INTO edges(from_obs_id, to_obs_id, relation_type, weight) VALUES(?, ?, 'references', 1.0)`,
				i-1, i)
		}
	}

	return db
}

func BenchmarkSearch_FTS5Only(b *testing.B) {
	db := setupBenchDB(b, 100)
	store := NewStore(db)
	ctx := context.Background()
	opts := domain.SearchOptions{Project: "bench", Limit: 10}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = store.searchKeywords(ctx, "authentication", opts, 10)
	}
}

func BenchmarkSearch_Enhanced(b *testing.B) {
	db := setupBenchDB(b, 100)
	store := NewStore(db)
	ctx := context.Background()
	opts := domain.SearchOptions{Project: "bench", Limit: 10}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = store.Search(ctx, "authentication", opts)
	}
}

func BenchmarkSearch_EnhancedWithGraph(b *testing.B) {
	db := setupBenchDB(b, 100)
	store := NewStore(db)
	ctx := context.Background()
	opts := domain.SearchOptions{Project: "bench", Limit: 10, GraphExpand: true}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = store.Search(ctx, "authentication", opts)
	}
}

func BenchmarkSearch_Scale(b *testing.B) {
	for _, n := range []int{10, 50, 100, 500} {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			db := setupBenchDB(b, n)
			store := NewStore(db)
			ctx := context.Background()
			opts := domain.SearchOptions{Project: "bench", Limit: 10}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, _ = store.Search(ctx, "authentication", opts)
			}
		})
	}
}
