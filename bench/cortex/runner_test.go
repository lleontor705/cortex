package cortex

import (
	"context"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/bench/common"
	"github.com/lleontor705/cortex/internal/domain"
)

func TestRunCurrentProductionBaselineUsesBenchAppSearchPath(t *testing.T) {
	ctx := context.Background()
	stores, err := common.NewBenchStores()
	if err != nil {
		t.Fatalf("NewBenchStores() error = %v", err)
	}
	t.Cleanup(func() { _ = stores.Close() })

	projectA := []domain.Observation{
		{Title: "Isolation policy", Content: "Authorize retrieval only after project isolation checks.", Type: domain.TypeDecision, Scope: domain.ScopeProject},
		{Title: "Unrelated note", Content: "A benchmark note without the query terms.", Type: domain.TypeDiscovery, Scope: domain.ScopeProject},
	}
	projectB := []domain.Observation{
		{Title: "Isolation policy", Content: "Authorize retrieval only after project isolation checks.", Type: domain.TypeDecision, Scope: domain.ScopeProject},
	}
	if err := stores.IngestSession(ctx, "session-a", "project-a", projectA); err != nil {
		t.Fatalf("IngestSession(project-a) error = %v", err)
	}
	if err := stores.IngestSession(ctx, "session-b", "project-b", projectB); err != nil {
		t.Fatalf("IngestSession(project-b) error = %v", err)
	}

	stableIDs := map[int64]string{
		projectA[0].ID: "project-a-decision",
		projectA[1].ID: "project-a-discovery",
		projectB[0].ID: "project-b-decision",
	}
	run, err := RunCurrentProductionBaseline(ctx, stores, stableIDs, []Query{
		{
			ID:   "project-isolation",
			Text: "project isolation",
			Options: domain.SearchOptions{
				Project: "project-a",
				Type:    domain.TypeDecision,
				Scope:   domain.ScopeProject,
				Limit:   10,
			},
		},
	})
	if err != nil {
		t.Fatalf("RunCurrentProductionBaseline() error = %v", err)
	}
	if len(run.Queries) != 1 {
		t.Fatalf("len(run.Queries) = %d, want 1", len(run.Queries))
	}

	trace := run.Queries[0]
	if trace.QueryID != "project-isolation" {
		t.Errorf("QueryID = %q, want project-isolation", trace.QueryID)
	}
	if trace.EffectiveInput.Project != "project-a" || trace.EffectiveInput.Type != domain.TypeDecision || trace.EffectiveInput.Scope != domain.ScopeProject || trace.EffectiveInput.Limit != 10 {
		t.Errorf("EffectiveInput = %+v, want project/type/scope/limit trace", trace.EffectiveInput)
	}
	if trace.Error != "" {
		t.Errorf("trace.Error = %q, want empty", trace.Error)
	}
	if len(trace.Ranked) != 1 {
		t.Fatalf("len(trace.Ranked) = %d, want 1", len(trace.Ranked))
	}
	result := trace.Ranked[0]
	if result.StableID != "project-a-decision" || result.CurrentID != projectA[0].ID || result.Position != 1 {
		t.Errorf("ranked result = %+v, want stable/current IDs and position", result)
	}
	if result.Project != "project-a" {
		t.Errorf("ranked result project = %q, want project-a", result.Project)
	}
	if result.Strategy == "" {
		t.Error("ranked result strategy is empty; production search evidence was not captured")
	}
	if trace.Latency.Unit != "nanoseconds" || trace.Latency.Nanoseconds < 0 {
		t.Errorf("latency = %+v, want a non-negative nanosecond sample", trace.Latency)
	}
	if trace.Resources.HeapAllocBytes == 0 {
		t.Error("heap allocation sample is zero")
	}
	if len(run.BlockingFailures) != 0 {
		t.Fatalf("BlockingFailures = %v, want none", run.BlockingFailures)
	}
}

func TestDetectProjectLeakageIsBlocking(t *testing.T) {
	trace := QueryTrace{
		QueryID:        "collision-query",
		EffectiveInput: EffectiveInput{Project: "project-a"},
		Ranked: []RankedResult{
			{StableID: "project-a-decision", CurrentID: 1, Project: "project-a", Position: 1},
			{StableID: "project-b-decision", CurrentID: 2, Project: "project-b", Position: 2},
		},
	}

	failures := detectProjectLeakage(trace)
	if len(failures) != 1 {
		t.Fatalf("len(failures) = %d, want 1", len(failures))
	}
	if !strings.Contains(failures[0], "project-b-decision") || !strings.Contains(failures[0], "project-b") {
		t.Fatalf("failure = %q, want traceable leaked stable ID and project", failures[0])
	}
}

func TestDetectMissingStableIDsIsBlocking(t *testing.T) {
	trace := QueryTrace{
		QueryID: "missing-label-query",
		Ranked: []RankedResult{
			{StableID: "current:42", CurrentID: 42, Project: "project-a", Position: 1},
		},
	}

	failures := detectMissingStableIDs(trace)
	if len(failures) != 1 {
		t.Fatalf("len(failures) = %d, want 1", len(failures))
	}
	if !strings.Contains(failures[0], "current ID 42") {
		t.Fatalf("failure = %q, want traceable current ID", failures[0])
	}
}
