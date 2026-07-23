package cortex

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/lleontor705/cortex/bench/common"
	"github.com/lleontor705/cortex/internal/domain"
)

// RunEvidence composes identity validation, fresh ingestion, the production
// runner, resource capture, report construction, and atomic output publishing.
func RunEvidence(ctx context.Context, request EvidenceRunRequest) (common.IndependentRun, error) {
	if err := RefuseExternalProviders(); err != nil {
		return common.IndependentRun{}, err
	}
	if err := ValidateEvidenceIdentity(request); err != nil {
		return common.IndependentRun{}, err
	}
	if err := refuseExistingEvidenceOutput(request.OutputDir); err != nil {
		return common.IndependentRun{}, err
	}

	workRoot := request.WorkDir
	if strings.TrimSpace(workRoot) == "" {
		workRoot = os.TempDir()
	}
	workDir, err := os.MkdirTemp(workRoot, "cortex-evidence-")
	if err != nil {
		return common.IndependentRun{}, fmt.Errorf("create evidence work directory: %w", err)
	}
	defer os.RemoveAll(workDir)

	stores, err := NewFreshBenchStores(ctx, workDir)
	if err != nil {
		return common.IndependentRun{}, err
	}
	defer stores.Close()

	stableIDs, err := IngestEvidenceCorpus(ctx, stores, request.Corpus)
	if err != nil {
		return common.IndependentRun{}, err
	}
	queries, err := evidenceQueries(request.Corpus)
	if err != nil {
		return common.IndependentRun{}, err
	}
	collector, err := NewResourceCollector()
	if err != nil {
		return common.IndependentRun{}, fmt.Errorf("create resource collector: %w", err)
	}
	report, err := evidenceReportTemplate(request, filepath.Join(workDir, "cortex.db"))
	if err != nil {
		return common.IndependentRun{}, err
	}

	orchestrated, err := OrchestrateEvidence(ctx, EvidenceOrchestrationRequest{
		Stores: stores, StableIDs: stableIDs, Queries: queries, Collector: collector, Report: report,
	})
	if err != nil {
		return common.IndependentRun{}, err
	}
	if err := WriteEvidenceOutput(request.OutputDir, orchestrated.Baseline, orchestrated.Report); err != nil {
		return common.IndependentRun{}, err
	}

	resources := orchestrated.Resources
	return common.IndependentRun{
		RunID:                request.RunID,
		Seed:                 request.Seed,
		BinarySHA256:         request.Identity.BinarySHA256,
		Report:               orchestrated.Report,
		HeapAllocBytes:       resources.HeapAllocBytes,
		TotalAllocBytes:      resources.TotalAllocBytes,
		AllocationsAvailable: resources.Availability.HeapAlloc && resources.Availability.TotalAlloc,
		Outliers:             []common.OutlierDisclosure{},
	}, nil
}

func evidenceQueries(corpus common.Corpus) ([]Query, error) {
	records := make(map[string]common.CorpusRecord, len(corpus.Records))
	for _, record := range corpus.Records {
		records[record.ID] = record
	}

	queries := make([]Query, 0, len(corpus.Queries))
	for _, input := range corpus.Queries {
		options := domain.SearchOptions{Project: input.Labels.Isolation.PrincipalProject, Limit: 10}
		if len(input.Labels.Isolation.EligibleIDs) > 0 {
			record := records[input.Labels.Isolation.EligibleIDs[0]]
			switch input.QueryClass {
			case "type-filter":
				options.Type = record.Type
			case "scope-filter":
				options.Scope = record.Scope
			}
		}
		if input.QueryClass == "temporal-as-of" {
			asOf, err := time.Parse(time.RFC3339, input.Labels.Temporal.ValidAt)
			if err != nil {
				return nil, fmt.Errorf("query %q temporal label: %w", input.ID, err)
			}
			options.AsOf = &asOf
		}

		unsupported := make([]string, 0)
		for field, status := range input.Authority {
			if status == common.CapabilityNotExecuted {
				unsupported = append(unsupported, field)
			}
		}
		sort.Strings(unsupported)
		queries = append(queries, Query{
			ID: input.ID, Text: input.Text, Options: options, UnsupportedCapabilities: unsupported,
		})
	}
	return queries, nil
}

func evidenceReportTemplate(request EvidenceRunRequest, databasePath string) (common.EvidenceReport, error) {
	database, err := os.Stat(databasePath)
	if err != nil {
		return common.EvidenceReport{}, fmt.Errorf("stat evidence database: %w", err)
	}
	available := true
	profiles := make([]common.ProfileReport, 0)
	queryReports := make([]common.QueryReport, 0, len(request.Corpus.Queries))
	seenProfiles := make(map[string]struct{})
	for _, input := range request.Corpus.Queries {
		profileKey := input.ProfileClass + "\x00" + input.QueryClass
		if _, exists := seenProfiles[profileKey]; !exists {
			seenProfiles[profileKey] = struct{}{}
			profiles = append(profiles, common.ProfileReport{
				ProfileID: "current-production", ProfileVersion: input.ProfileClass, QueryClass: input.QueryClass,
				Metrics:    map[string]float64{"retrieved_count": 0},
				Latency:    common.LatencyReport{Unit: "nanoseconds"},
				Throughput: common.ThroughputReport{Unit: "queries_per_second"},
			})
		}
		queryReports = append(queryReports, common.QueryReport{
			QueryID: input.ID, ProfileID: "current-production", ProfileVersion: input.ProfileClass, QueryClass: input.QueryClass,
			Metrics:       map[string]float64{"retrieved_count": 0},
			CurrentOutput: []common.RankedOutput{}, CandidateOutput: []common.RankedOutput{},
		})
	}

	limitations := []string{
		"Candidate evaluation is outside this current-production baseline.",
		"privacy=" + common.CapabilityNotExecuted,
		"lifecycle=" + common.CapabilityNotExecuted,
		"provenance=" + common.CapabilityNotExecuted,
	}
	return common.EvidenceReport{
		SchemaVersion: "cortex.retrieval-evidence/v1", RunID: request.RunID, ReportID: request.RunID + "-report",
		CorpusVersion: request.Corpus.Version, ProtocolVersion: request.ProtocolVersion,
		Build: request.Corpus.Build, Hardware: request.Corpus.Hardware,
		MetricDefinitions: []common.MetricDefinition{{
			Name: "retrieved_count", Unit: "count", Direction: "higher_is_better", Description: "Observed current-production results.",
		}},
		Profiles: profiles, Queries: queryReports,
		Resources: common.ResourceReport{
			CPUUnit: "seconds", CPUAvailable: &available,
			PeakRSSUnit: "bytes", PeakRSSAvailable: &available,
			StorageBytes: database.Size(), StorageUnit: "bytes", StorageAvailable: &available,
			IndexBytes: database.Size(), IndexUnit: "bytes", IndexAvailable: &available,
		},
		Uncertainty: common.UncertaintyReport{
			Method: "single-independent-run", ConfidenceLevel: 0.95, SampleSize: 1,
			Notes: "Raw observations are retained; reproducibility requires multiple independent runs.",
		},
		Limitations: limitations,
	}, nil
}
