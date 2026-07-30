package postgres

import (
	"os"
	"strings"
	"testing"
)

// Keep tenant predicates explicit even when PostgreSQL RLS is enabled. This
// protects future migrations, query rewrites, and non-RLS test databases.
func TestObservationReadQueriesKeepExplicitTenantPredicate(t *testing.T) {
	for _, file := range []string{"repositories.go", "extras.go", "scoring.go"} {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		src := string(data)
		for _, marker := range []string{"GetByTopicKey", "CountAll", "Search", "GetTopByScore"} {
			idx := strings.Index(src, marker)
			if idx < 0 {
				continue
			}
			end := idx + 2200
			if end > len(src) {
				end = len(src)
			}
			window := src[idx:end]
			if !strings.Contains(window, "tenant_id=public.cortex_current_tenant()") && !strings.Contains(window, "o.tenant_id=public.cortex_current_tenant()") && !strings.Contains(window, "s.tenant_id=public.cortex_current_tenant()") {
				t.Fatalf("%s %s query lacks explicit tenant predicate", file, marker)
			}
		}
	}
}
