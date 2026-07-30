package cortex

import (
	"context"
	"fmt"

	"github.com/lleontor705/cortex/bench/common"
)

// EvidenceOrchestrationRequest provides the existing production runner,
// resource collector, and report inputs for one evidence run.
type EvidenceOrchestrationRequest struct {
	Stores    *common.BenchStores
	StableIDs map[int64]string
	Queries   []Query
	Collector ResourceCollector
	Report    common.EvidenceReport
}

// EvidenceOrchestrationResult retains the raw production trace, measured
// process resources, and the report derived from that trace.
type EvidenceOrchestrationResult struct {
	Baseline  BaselineRun
	Resources ProcessResources
	Report    common.EvidenceReport
}

// OrchestrateEvidence measures one invocation of the unchanged current
// production baseline runner and maps its observed ranking into EvidenceReport.
func OrchestrateEvidence(ctx context.Context, request EvidenceOrchestrationRequest) (EvidenceOrchestrationResult, error) {
	if request.Collector == nil {
		return EvidenceOrchestrationResult{}, fmt.Errorf("resource collector is required")
	}
	if err := request.Collector.Start(ctx); err != nil {
		return EvidenceOrchestrationResult{}, fmt.Errorf("start resource collector: %w", err)
	}

	baseline, runErr := RunCurrentProductionBaseline(ctx, request.Stores, request.StableIDs, request.Queries)
	resources, resourceErr := request.Collector.Snapshot(ctx)
	result := EvidenceOrchestrationResult{Baseline: baseline, Resources: resources}
	if resourceErr != nil {
		return result, fmt.Errorf("snapshot process resources: %w", resourceErr)
	}
	if err := requireMeasuredResources(resources); err != nil {
		return result, err
	}
	if runErr != nil {
		return result, runErr
	}

	report, err := evidenceReportFromBaseline(request.Report, baseline, resources)
	if err != nil {
		return result, err
	}
	result.Report = report
	return result, nil
}

func requireMeasuredResources(resources ProcessResources) error {
	if err := resources.Validate(); err != nil {
		return fmt.Errorf("validate process resources: %w", err)
	}
	availability := resources.Availability
	if availability.Wall && availability.CPU && availability.PeakRSS && availability.HeapAlloc && availability.TotalAlloc {
		return nil
	}
	return &MeasurementUnavailableError{
		Collector: resources.Collector,
		Resource:  "wall, CPU, peak RSS, heap allocation, or total allocation",
	}
}

func evidenceReportFromBaseline(report common.EvidenceReport, baseline BaselineRun, resources ProcessResources) (common.EvidenceReport, error) {
	templates := make(map[string]common.QueryReport, len(report.Queries))
	for _, query := range report.Queries {
		templates[query.QueryID] = query
	}

	report.Queries = make([]common.QueryReport, 0, len(baseline.Queries))
	for _, trace := range baseline.Queries {
		query, ok := templates[trace.QueryID]
		if !ok {
			return common.EvidenceReport{}, fmt.Errorf("report query template %q is required", trace.QueryID)
		}
		query.CurrentOutput = make([]common.RankedOutput, 0, len(trace.Ranked))
		for _, ranked := range trace.Ranked {
			query.CurrentOutput = append(query.CurrentOutput, common.RankedOutput{
				StableID: ranked.StableID,
				Rank:     ranked.Position,
				Score:    ranked.Score,
			})
		}
		if query.CandidateOutput == nil {
			query.CandidateOutput = []common.RankedOutput{}
		}
		report.Queries = append(report.Queries, query)
	}

	cpuAvailable := resources.Availability.CPU
	peakRSSAvailable := resources.Availability.PeakRSS
	report.Resources.CPUSeconds = resources.CPU.Seconds()
	report.Resources.CPUUnit = "seconds"
	report.Resources.CPUAvailable = &cpuAvailable
	report.Resources.PeakRSSBytes = resources.PeakRSSBytes
	report.Resources.PeakRSSUnit = resources.Units.PeakRSS
	report.Resources.PeakRSSAvailable = &peakRSSAvailable
	if err := report.Validate(); err != nil {
		return common.EvidenceReport{}, fmt.Errorf("validate evidence report: %w", err)
	}
	return report, nil
}
