//go:build postgres_integration

package postgrestest

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestEnsureMigrationRolesSerializesConcurrentBootstrap(t *testing.T) {
	dsn := os.Getenv("CORTEX_TEST_POSTGRES_MIGRATION_DSN")
	if dsn == "" {
		t.Fatal("CORTEX_TEST_POSTGRES_MIGRATION_DSN is required for postgres_integration tests")
	}

	const workers = 8
	start := make(chan struct{})
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			errs <- EnsureMigrationRoles(ctx, dsn)
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent role bootstrap: %v", err)
		}
	}

	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.ConnectConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := conn.Close(context.Background()); closeErr != nil {
			t.Errorf("close role verification connection: %v", closeErr)
		}
	}()
	var count int
	if err := conn.QueryRow(context.Background(), `SELECT count(*) FROM pg_roles WHERE rolname IN ('cortex_app','cortex_admin','cortex_migration')`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("migration roles present=%d want=3", count)
	}
}
