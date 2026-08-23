package cortex

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/lleontor705/cortex/v2/bench/common"
	"github.com/lleontor705/cortex/v2/internal/domain"
)

func TestEvidenceOrchestrate(t *testing.T) {
	t.Run("runs production search and preserves observed rank order", func(t *testing.T) {
		ctx := context.Background()
		stores, err := common.NewBenchStores()
		if err != nil {
			t.Fatalf("NewBenchStores() error = %v", err)
		}
		t.Cleanup(func() { _ = stores.Close() })

		observations := []domain.Observation{
			{Title: "Primary", Content: "zephyr primary retrieval evidence", Type: domain.TypeDecision, Scope: domain.ScopeProject},
			{Title: "Secondary", Content: "zephyr secondary retrieval evidence", Type: domain.TypeDecision, Scope: domain.ScopeProject},
		}
		if err := stores.IngestSession(ctx, "orchestration", "project-a", observations); err != nil {
			t.Fatalf("IngestSession() error = %v", err)
		}

		collector := &orchestrationCollector{sample: measuredProcessResources()}
		result, err := OrchestrateEvidence(ctx, EvidenceOrchestrationRequest{
			Stores:    stores,
			StableIDs: map[int64]string{observations[0].ID: "stable-primary", observations[1].ID: "stable-secondary"},
			Queries: []Query{{
				ID:   "query-zephyr",
				Text: "zephyr retrieval evidence",
				Options: domain.SearchOptions{
					Project: "project-a",
					Limit:   10,
				},
			}},
			Collector: collector,
			Report:    orchestrationReport(),
		})
		if err != nil {
			t.Fatalf("OrchestrateEvidence() error = %v", err)
		}
		if collector.startCalls != 1 || collector.snapshotCalls != 1 {
			t.Fatalf("collector calls = start:%d snapshot:%d, want 1 each", collector.startCalls, collector.snapshotCalls)
		}
		if len(result.Baseline.Queries) != 1 || len(result.Baseline.Queries[0].Ranked) != 2 {
			t.Fatalf("baseline queries = %+v, want one production trace with two results", result.Baseline.Queries)
		}
		if result.Baseline.Queries[0].Ranked[0].Strategy == "" {
			t.Fatal("production search strategy is empty")
		}

		wantRanked := make([]common.RankedOutput, 0, len(result.Baseline.Queries[0].Ranked))
		for _, ranked := range result.Baseline.Queries[0].Ranked {
			wantRanked = append(wantRanked, common.RankedOutput{StableID: ranked.StableID, Rank: ranked.Position, Score: ranked.Score})
		}
		if got := result.Report.Queries[0].CurrentOutput; !reflect.DeepEqual(got, wantRanked) {
			t.Fatalf("report rank order = %+v, want observed order %+v", got, wantRanked)
		}
		if err := result.Report.Validate(); err != nil {
			t.Fatalf("EvidenceReport.Validate() error = %v", err)
		}
		if result.Report.Resources.CPUAvailable == nil || !*result.Report.Resources.CPUAvailable || result.Report.Resources.CPUUnit != "seconds" {
			t.Fatalf("CPU evidence = %+v, want explicit available seconds", result.Report.Resources)
		}
		if result.Report.Resources.PeakRSSAvailable == nil || !*result.Report.Resources.PeakRSSAvailable || result.Report.Resources.PeakRSSUnit != "bytes" {
			t.Fatalf("peak RSS evidence = %+v, want explicit available bytes", result.Report.Resources)
		}
	})

	t.Run("returns typed unavailable measurements without zero substitution", func(t *testing.T) {
		unavailable := &MeasurementUnavailableError{
			Collector: ResourceCollectorIdentity{Method: "unsupported-platform", Version: "v1"},
			Resource:  "CPU and peak RSS",
		}
		collector := &orchestrationCollector{snapshotErr: unavailable}

		_, err := OrchestrateEvidence(context.Background(), EvidenceOrchestrationRequest{
			Collector: collector,
		})
		if !errors.Is(err, ErrMeasurementUnavailable) {
			t.Fatalf("OrchestrateEvidence() error = %v, want ErrMeasurementUnavailable", err)
		}
	})
}

type orchestrationCollector struct {
	sample        ProcessResources
	startErr      error
	snapshotErr   error
	startCalls    int
	snapshotCalls int
}

func (c *orchestrationCollector) Start(context.Context) error {
	c.startCalls++
	return c.startErr
}

func (c *orchestrationCollector) Snapshot(context.Context) (ProcessResources, error) {
	c.snapshotCalls++
	return c.sample, c.snapshotErr
}

func measuredProcessResources() ProcessResources {
	resources := NewProcessResources(ResourceCollectorIdentity{Method: "test-process", Version: "v1"})
	resources.Availability = ResourceAvailability{Wall: true, CPU: true, PeakRSS: true, HeapAlloc: true, TotalAlloc: true}
	resources.Wall = time.Second
	resources.CPU = 500 * time.Millisecond
	resources.PeakRSSBytes = 4096
	resources.HeapAllocBytes = 2048
	resources.TotalAllocBytes = 8192
	return resources
}

func orchestrationReport() common.EvidenceReport {
	available := true
	return common.EvidenceReport{
		SchemaVersion:   "cortex.retrieval-evidence/v1",
		RunID:           "run-orchestration",
		ReportID:        "report-orchestration",
		CorpusVersion:   "corpus/v1",
		ProtocolVersion: "protocol/v1",
		Build:           common.BuildMetadata{Commit: "test-commit"},
		Hardware: common.HardwareMetadata{
			ProfileID: "test-hardware",
			OS:        "test-os",
			Arch:      "test-arch",
			CPU:       "test-cpu",
			MemoryMB:  1024,
		},
		MetricDefinitions: []common.MetricDefinition{{Name: "retrieved_count", Unit: "count", Direction: "higher_is_better", Description: "Observed production results."}},
		Profiles: []common.ProfileReport{{
			ProfileID:      "current-production",
			ProfileVersion: "v1",
			QueryClass:     "lexical",
			Metrics:        map[string]float64{"retrieved_count": 2},
			Latency:        common.LatencyReport{Unit: "nanoseconds", P50: 1, P95: 1, P99: 1},
			Throughput:     common.ThroughputReport{Unit: "queries_per_second", QueriesPerSecond: 1},
		}},
		Queries: []common.QueryReport{{
			QueryID:         "query-zephyr",
			ProfileID:       "current-production",
			ProfileVersion:  "v1",
			QueryClass:      "lexical",
			Metrics:         map[string]float64{"retrieved_count": 2},
			CurrentOutput:   []common.RankedOutput{},
			CandidateOutput: []common.RankedOutput{},
		}},
		Resources: common.ResourceReport{
			StorageBytes:     1024,
			StorageUnit:      "bytes",
			StorageAvailable: &available,
			IndexBytes:       512,
			IndexUnit:        "bytes",
			IndexAvailable:   &available,
		},
		Uncertainty: common.UncertaintyReport{Method: "single-run", ConfidenceLevel: 0.95, SampleSize: 1, Notes: "Focused orchestration test."},
		Limitations: []string{"Candidate evaluation is outside current-production orchestration."},
	}
}
