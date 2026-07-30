package entity

import (
	"strings"
	"testing"

	"github.com/lleontor705/cortex/internal/domain"
)

func TestNormalizeDeterministicCanonicalKey(t *testing.T) {
	if got, want := Normalize(domain.EntityPackage, "  Python/Requests! "), "package:python/requests"; got != want {
		t.Fatalf("Normalize() = %q, want %q", got, want)
	}
	if Normalize(domain.EntityFile, "A.go") != Normalize(domain.EntityFile, "a.go") {
		t.Fatal("case variants must share a canonical blocking key")
	}
}

func TestExtract(t *testing.T) {
	tests := []struct {
		name     string
		obs      *domain.Observation
		wantType map[string][]string // entityType -> expected values
	}{
		{
			name: "file paths",
			obs: &domain.Observation{
				ID:      1,
				Title:   "Updated auth middleware",
				Content: "Changed src/auth/middleware.ts and internal/store/store.go",
			},
			wantType: map[string][]string{
				domain.EntityFile: {"src/auth/middleware.ts", "internal/store/store.go"},
			},
		},
		{
			name: "URLs",
			obs: &domain.Observation{
				ID:      2,
				Title:   "API reference",
				Content: "See https://api.example.com/docs and http://localhost:8080/health",
			},
			wantType: map[string][]string{
				domain.EntityURL: {"https://api.example.com/docs", "http://localhost:8080/health"},
			},
		},
		{
			name: "Go packages",
			obs: &domain.Observation{
				ID:      3,
				Title:   "Dependencies",
				Content: "Using github.com/spf13/viper for config",
			},
			wantType: map[string][]string{
				domain.EntityPackage: {"github.com/spf13/viper"},
			},
		},
		{
			name: "symbols",
			obs: &domain.Observation{
				ID:      4,
				Title:   "New types",
				Content: "Added func HandleSave and type Config struct",
			},
			wantType: map[string][]string{
				domain.EntitySymbol: {"HandleSave", "Config"},
			},
		},
		{
			name: "mixed content",
			obs: &domain.Observation{
				ID:      5,
				Title:   "JWT migration",
				Content: "Changed src/auth/jwt.go. See https://jwt.io for docs. Added func ValidateToken",
			},
			wantType: map[string][]string{
				domain.EntityFile:   {"src/auth/jwt.go"},
				domain.EntityURL:    {"https://jwt.io"},
				domain.EntitySymbol: {"ValidateToken"},
			},
		},
		{
			name: "empty content",
			obs: &domain.Observation{
				ID:      6,
				Title:   "Empty",
				Content: "",
			},
			wantType: map[string][]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			links := Extract(tt.obs)

			// Group by type
			byType := map[string][]string{}
			for _, l := range links {
				byType[l.EntityType] = append(byType[l.EntityType], l.EntityValue)
			}

			for entityType, expected := range tt.wantType {
				actual := byType[entityType]
				for _, exp := range expected {
					found := false
					for _, act := range actual {
						if act == exp {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("expected %s entity %q not found in %v", entityType, exp, actual)
					}
				}
			}
		})
	}
}

func TestExtract_SQLTables(t *testing.T) {
	obs := &domain.Observation{
		ID:      10,
		Title:   "Database query",
		Content: "SELECT * FROM users JOIN orders ON users.id = orders.user_id. INSERT INTO accounts VALUES (...). UPDATE sessions SET ended_at = now()",
	}
	links := Extract(obs)
	byType := groupByType(links)

	expected := []string{"users", "orders", "accounts", "sessions"}
	for _, e := range expected {
		if !contains(byType[domain.EntitySQLTable], e) {
			t.Errorf("expected sql_table %q, got %v", e, byType[domain.EntitySQLTable])
		}
	}

	// Should NOT extract SQL keywords
	for _, kw := range []string{"SELECT", "WHERE", "SET"} {
		if contains(byType[domain.EntitySQLTable], kw) {
			t.Errorf("SQL keyword %q should not be extracted as table", kw)
		}
	}
}

func TestExtract_Endpoints(t *testing.T) {
	obs := &domain.Observation{
		ID:      11,
		Title:   "API routes",
		Content: "Added GET /api/users/:id and POST /auth/login endpoints. Also DELETE /api/sessions/{id}",
	}
	links := Extract(obs)
	byType := groupByType(links)

	expected := []string{"/api/users/:id", "/auth/login", "/api/sessions/{id}"}
	for _, e := range expected {
		if !contains(byType[domain.EntityEndpoint], e) {
			t.Errorf("expected endpoint %q, got %v", e, byType[domain.EntityEndpoint])
		}
	}
}

func TestExtract_EnvVars(t *testing.T) {
	obs := &domain.Observation{
		ID:      12,
		Title:   "Config setup",
		Content: "Set $DATABASE_URL and ${REDIS_HOST} for the connection. Also uses $API_KEY",
	}
	links := Extract(obs)
	byType := groupByType(links)

	expected := []string{"DATABASE_URL", "REDIS_HOST", "API_KEY"}
	for _, e := range expected {
		if !contains(byType[domain.EntityEnvVar], e) {
			t.Errorf("expected env_var %q, got %v", e, byType[domain.EntityEnvVar])
		}
	}
}

func TestExtract_Versions(t *testing.T) {
	obs := &domain.Observation{
		ID:      13,
		Title:   "Version updates",
		Content: "Upgraded to v2.3.1 and Node 18.4.0. Also requires Python 3.11",
	}
	links := Extract(obs)
	byType := groupByType(links)

	expected := []string{"2.3.1", "18.4.0", "3.11"}
	for _, e := range expected {
		if !contains(byType[domain.EntityVersion], e) {
			t.Errorf("expected version %q, got %v", e, byType[domain.EntityVersion])
		}
	}
}

func TestExtract_CLIFlags(t *testing.T) {
	obs := &domain.Observation{
		ID:      14,
		Title:   "CLI usage",
		Content: "Run with --verbose and --output=json flags. Use -p for port",
	}
	links := Extract(obs)
	byType := groupByType(links)

	expected := []string{"--verbose", "--output", "-p"}
	for _, e := range expected {
		if !contains(byType[domain.EntityCLIFlag], e) {
			t.Errorf("expected cli_flag %q, got %v", e, byType[domain.EntityCLIFlag])
		}
	}
}

func TestExtract_Errors(t *testing.T) {
	obs := &domain.Observation{
		ID:      15,
		Title:   "Error debugging",
		Content: "Got Error: ECONNREFUSED 127.0.0.1:5432 when connecting to DB. Also saw panic: runtime error: index out of range",
	}
	links := Extract(obs)
	byType := groupByType(links)

	if len(byType[domain.EntityError]) == 0 {
		t.Error("expected at least one error entity extracted")
	}

	// Check that error messages are captured
	foundConn := false
	foundPanic := false
	for _, v := range byType[domain.EntityError] {
		if strings.Contains(v, "ECONNREFUSED") {
			foundConn = true
		}
		if strings.Contains(v, "runtime error") {
			foundPanic = true
		}
	}
	if !foundConn {
		t.Errorf("expected ECONNREFUSED error, got %v", byType[domain.EntityError])
	}
	if !foundPanic {
		t.Errorf("expected runtime error, got %v", byType[domain.EntityError])
	}
}

func TestExtract_SQLKeywordFiltering(t *testing.T) {
	obs := &domain.Observation{
		ID:      16,
		Title:   "Query test",
		Content: "SELECT FROM WHERE GROUP BY HAVING ORDER BY LIMIT",
	}
	links := Extract(obs)
	byType := groupByType(links)

	if len(byType[domain.EntitySQLTable]) > 0 {
		t.Errorf("SQL keywords should not be extracted as tables, got %v", byType[domain.EntitySQLTable])
	}
}

// helpers

func groupByType(links []*domain.EntityLink) map[string][]string {
	byType := map[string][]string{}
	for _, l := range links {
		byType[l.EntityType] = append(byType[l.EntityType], l.EntityValue)
	}
	return byType
}

func contains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

func TestExtract_NoDuplicates(t *testing.T) {
	obs := &domain.Observation{
		ID:      1,
		Title:   "Duplicate test",
		Content: "File src/main.go and again src/main.go referenced twice",
	}

	links := Extract(obs)

	seen := map[string]bool{}
	for _, l := range links {
		key := l.EntityType + ":" + l.EntityValue
		if seen[key] {
			t.Errorf("duplicate entity found: %s", key)
		}
		seen[key] = true
	}
}
