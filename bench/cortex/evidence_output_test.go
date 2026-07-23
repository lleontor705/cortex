package cortex

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/lleontor705/cortex/bench/common"
)

func TestEvidenceOutput(t *testing.T) {
	t.Run("integrates identity ingestion orchestration and atomic output", func(t *testing.T) {
		output := filepath.Join(t.TempDir(), "integrated")
		run, err := RunEvidence(context.Background(), newEvidenceRequest(t, output, "output-integration"))
		if err != nil {
			t.Fatalf("RunEvidence() error = %v", err)
		}
		if run.RunID != "output-integration" || run.Report.RunID != run.RunID {
			t.Fatalf("run identity = %q/%q, want output-integration", run.RunID, run.Report.RunID)
		}
		if err := run.Report.Validate(); err != nil {
			t.Fatalf("EvidenceReport.Validate() error = %v", err)
		}
		for _, name := range []string{"raw.json", "report.json"} {
			if _, err := os.Stat(filepath.Join(output, name)); err != nil {
				t.Fatalf("Stat(%s) error = %v", name, err)
			}
		}
	})

	t.Run("cleans run work and output after orchestration failure", func(t *testing.T) {
		workspace := t.TempDir()
		work := filepath.Join(workspace, "work")
		if err := os.Mkdir(work, 0o755); err != nil {
			t.Fatalf("Mkdir(work) error = %v", err)
		}
		output := filepath.Join(workspace, "output")
		request := newEvidenceRequest(t, output, "output-cleanup")
		request.WorkDir = work
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		if _, err := RunEvidence(ctx, request); err == nil {
			t.Fatal("RunEvidence() error = nil, want canceled orchestration")
		}
		entries, err := os.ReadDir(work)
		if err != nil {
			t.Fatalf("ReadDir(work) error = %v", err)
		}
		if len(entries) != 0 {
			t.Fatalf("work artifacts remain after failure: %v", entries)
		}
		if _, err := os.Stat(output); !os.IsNotExist(err) {
			t.Fatalf("output stat error = %v, want no partial output", err)
		}
	})

	t.Run("writes raw and report JSON atomically", func(t *testing.T) {
		output := filepath.Join(t.TempDir(), "evidence")
		raw := BaselineRun{Queries: []QueryTrace{}}
		report := validOutputReport()

		if err := WriteEvidenceOutput(output, raw, report); err != nil {
			t.Fatalf("WriteEvidenceOutput() error = %v", err)
		}

		entries, err := os.ReadDir(output)
		if err != nil {
			t.Fatalf("ReadDir(output) error = %v", err)
		}
		got := make([]string, 0, len(entries))
		for _, entry := range entries {
			got = append(got, entry.Name())
		}
		if want := []string{"raw.json", "report.json"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("output entries = %v, want %v", got, want)
		}
	})

	t.Run("refuses existing evidence without mutation", func(t *testing.T) {
		output := t.TempDir()
		existing := filepath.Join(output, "raw.json")
		if err := os.WriteFile(existing, []byte("sentinel"), 0o600); err != nil {
			t.Fatalf("WriteFile(existing) error = %v", err)
		}

		err := WriteEvidenceOutput(output, BaselineRun{}, validOutputReport())
		if !errors.Is(err, ErrEvidenceOutputExists) {
			t.Fatalf("WriteEvidenceOutput() error = %v, want ErrEvidenceOutputExists", err)
		}
		contents, readErr := os.ReadFile(existing)
		if readErr != nil || string(contents) != "sentinel" {
			t.Fatalf("existing output = %q, %v; want preserved sentinel", contents, readErr)
		}
		if _, statErr := os.Stat(filepath.Join(output, "report.json")); !os.IsNotExist(statErr) {
			t.Fatalf("report.json stat error = %v, want no partial output", statErr)
		}
	})

	t.Run("cleans staging directory when serialization fails", func(t *testing.T) {
		parent := t.TempDir()
		output := filepath.Join(parent, "evidence")

		err := WriteEvidenceOutput(output, BaselineRun{}, common.EvidenceReport{})
		if err == nil {
			t.Fatal("WriteEvidenceOutput() error = nil, want invalid report error")
		}
		if _, statErr := os.Stat(output); !os.IsNotExist(statErr) {
			t.Fatalf("output stat error = %v, want cleanup after failure", statErr)
		}
		entries, readErr := os.ReadDir(parent)
		if readErr != nil {
			t.Fatalf("ReadDir(parent) error = %v", readErr)
		}
		if len(entries) != 0 {
			t.Fatalf("staging artifacts remain after failure: %v", entries)
		}
	})

	t.Run("refuses external provider configuration", func(t *testing.T) {
		for _, name := range externalProviderEnvironment {
			t.Run(name, func(t *testing.T) {
				clearExternalProviderEnvironment(t)
				t.Setenv(name, "configured")
				if err := RefuseExternalProviders(); !errors.Is(err, ErrExternalProviderConfigured) {
					t.Fatalf("RefuseExternalProviders() error = %v, want ErrExternalProviderConfigured", err)
				}
			})
		}
	})
}

func validOutputReport() common.EvidenceReport {
	available := true
	return common.EvidenceReport{
		SchemaVersion:   "cortex.retrieval-evidence/v1",
		RunID:           "output-run",
		ReportID:        "output-report",
		CorpusVersion:   "cortex-native/v1",
		ProtocolVersion: "cortex-native-v1",
		Build:           common.BuildMetadata{Commit: "test-commit"},
		Hardware: common.HardwareMetadata{
			ProfileID: "test-hardware",
			OS:        "test-os",
			Arch:      "test-arch",
			CPU:       "test-cpu",
			MemoryMB:  1024,
		},
		MetricDefinitions: []common.MetricDefinition{{
			Name: "retrieved_count", Unit: "count", Direction: "higher_is_better", Description: "Observed production results.",
		}},
		Profiles: []common.ProfileReport{{
			ProfileID: "current-production", ProfileVersion: "v1", QueryClass: "lexical",
			Metrics:    map[string]float64{"retrieved_count": 0},
			Latency:    common.LatencyReport{Unit: "nanoseconds"},
			Throughput: common.ThroughputReport{Unit: "queries_per_second"},
		}},
		Queries: []common.QueryReport{{
			QueryID: "query-output", ProfileID: "current-production", ProfileVersion: "v1", QueryClass: "lexical",
			Metrics: map[string]float64{"retrieved_count": 0}, CurrentOutput: []common.RankedOutput{}, CandidateOutput: []common.RankedOutput{},
		}},
		Resources: common.ResourceReport{
			CPUUnit: "seconds", CPUAvailable: &available,
			PeakRSSUnit: "bytes", PeakRSSAvailable: &available,
			StorageUnit: "bytes", StorageAvailable: &available,
			IndexUnit: "bytes", IndexAvailable: &available,
		},
		Uncertainty: common.UncertaintyReport{Method: "single-run", ConfidenceLevel: 0.95, SampleSize: 1, Notes: "Focused output test."},
		Limitations: []string{"Output serialization only."},
	}
}
