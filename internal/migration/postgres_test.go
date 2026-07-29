package migration

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

func TestPostgresServerMigrationMetadata(t *testing.T) {
	m, err := NewPostgresServerMigration()
	if err != nil {
		t.Fatalf("NewPostgresServerMigration: %v", err)
	}
	if m.Version() != 100 || m.Name() != "server" {
		t.Fatalf("migration identity = %d/%q, want 100/server", m.Version(), m.Name())
	}
	if len(m.SQL()) < 1000 || m.Checksum() == "" {
		t.Fatal("embedded server migration or checksum is empty")
	}
}

func TestPostgresServerMigrationIsServerOnly(t *testing.T) {
	m, err := NewPostgresServerMigration()
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"organizations", "workspaces", "projects", "app_users", "service_accounts", "api_tokens", "memberships", "sessions", "observations", "prompts", "edges", "entities", "index_outbox", "audit_events"} {
		if !strings.Contains(m.SQL(), "CREATE TABLE IF NOT EXISTS "+table) {
			t.Errorf("server migration does not define %s", table)
		}
	}
	if strings.Contains(strings.ToLower(m.SQL()), "sqlite") {
		t.Fatal("server migration contains SQLite-only SQL")
	}
	if !strings.Contains(m.SQL(), "FORCE ROW LEVEL SECURITY") || !strings.Contains(m.SQL(), "SECURITY DEFINER") {
		t.Fatal("server migration is missing forced RLS or SECURITY DEFINER context binding")
	}
}

func TestPostgresServerMigratorRejectsNilDB(t *testing.T) {
	m, err := NewPostgresServerMigration()
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Apply(context.Background(), (*sql.DB)(nil)); err == nil {
		t.Fatal("Apply(nil) unexpectedly succeeded")
	}
}

func TestPostgresServerMigrationHasRecoveryAndIsolationSchema(t *testing.T) {
	m, err := NewPostgresServerMigration()
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"actor_subjects", "lease_owner", "leased_until", "affected_rows", "completed_at", "project_key", "scope", "source", "edges_valid_range"} {
		if !strings.Contains(m.SQL(), token) {
			t.Errorf("server migration missing %q", token)
		}
	}
}
