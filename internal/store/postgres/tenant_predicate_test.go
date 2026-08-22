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

// TestWorkspaceScopedReadQueriesKeepExplicitWorkspacePredicate pins SEC-01
// statically: every observation list/search/topic/count read and every
// supporting prompt/graph subquery in the owned repository files must carry
// an explicit workspace_id predicate bound to the transaction-resolved
// workspace, and every multi-row read must fail closed through
// errWorkspaceScopeRequired when that binding is missing.
func TestWorkspaceScopedReadQueriesKeepExplicitWorkspacePredicate(t *testing.T) {
	scopes := []struct {
		file    string
		markers []string
	}{
		{"repositories.go", []string{
			"func (r *ObservationRepository) List(",
			"func (r *ObservationRepository) CountAll(",
			"func (r *ObservationRepository) CountByRoot(",
			"func (r *ObservationRepository) CountEdgesAsObs(",
			"func (r *ObservationRepository) GetByTopicKey(",
		}},
		{"extras.go", []string{
			"func (r *PromptRepository) List(",
			"func (r *SearchRepository) Search(",
			"func (r *GraphRepository) GetEdgesForObservation(",
			"func (r *GraphRepository) populateEdgeEndpoints(",
			"func (r *GraphRepository) GetRelated(",
			"func (r *GraphRepository) GetEvolutionChain(",
			"func (r *GraphRepository) CountEdgesByObservation(",
			"func (r *GraphRepository) CountAllEdges(",
			"func (r *GraphRepository) GetContradictions(",
		}},
	}
	for _, scope := range scopes {
		data, err := os.ReadFile(scope.file)
		if err != nil {
			t.Fatal(err)
		}
		// Splitting at "\nfunc " yields one chunk per top-level function;
		// the chunk beginning with the marker suffix is that function body.
		chunks := strings.Split("\n"+string(data), "\nfunc ")
		for _, marker := range scope.markers {
			body := ""
			for _, chunk := range chunks {
				if strings.HasPrefix(chunk, strings.TrimPrefix(marker, "func ")) {
					body = chunk
					break
				}
			}
			if body == "" {
				t.Fatalf("%s: function %s not found for workspace predicate audit", scope.file, marker)
			}
			if !strings.Contains(body, "workspace_id=$") {
				t.Fatalf("%s %s lacks an explicit workspace_id predicate (SEC-01 requires tenant+workspace on every list/search/topic/count/supporting read)", scope.file, marker)
			}
			// The fail-closed guard is either the direct sentinel or the
			// requireWorkspaceScope resolver that returns it; both prove
			// the read cannot degrade to tenant-wide visibility.
			if !strings.Contains(body, "errWorkspaceScopeRequired") && !strings.Contains(body, "requireWorkspaceScope(ctx)") {
				t.Fatalf("%s %s does not fail closed through errWorkspaceScopeRequired when the workspace binding is missing", scope.file, marker)
			}
		}
	}
}
