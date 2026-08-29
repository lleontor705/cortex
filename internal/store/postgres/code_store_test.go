package postgres

import (
	"os"
	"strings"
	"testing"
)

func TestPostgresCodeStoreUsesOnlyMigratedScopedTables(t *testing.T) {
	source, err := os.ReadFile("code_store.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	for _, required := range []string{"scoped_code_symbols", "scoped_code_relations", "scoped_code_index_state"} {
		if !strings.Contains(text, required) {
			t.Errorf("code store does not reference %s", required)
		}
	}
	for _, forbidden := range []string{"CREATE TABLE", "ALTER TABLE", "FROM code_symbols", "INTO code_symbols", "FROM code_relations", "INTO code_relations"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("runtime code store contains forbidden schema/legacy token %q", forbidden)
		}
	}
}

func TestNormalizeCodeProjectRejectsMissingOrNonUUIDProject(t *testing.T) {
	for _, project := range []string{"", "project-name", "   "} {
		if _, err := normalizeCodeProject(project); err == nil {
			t.Fatalf("normalizeCodeProject(%q) expected error", project)
		}
	}
}
