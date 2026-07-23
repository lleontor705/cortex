package cortex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/bench/common"
)

// TestEvidence is the RED contract for #651 and design #720. It closes the
// missing native evidence path reported by #676 after incidents #775/#776 and
// A2A task-22072c0b; downstream task 2.5B is responsible for the first GREEN.
func TestEvidence(t *testing.T) {
	t.Run("validates every identity before database creation", func(t *testing.T) {
		tests := []struct {
			name    string
			mutate  func(*EvidenceRunRequest)
			wantErr string
		}{
			{name: "corpus hash", mutate: func(r *EvidenceRunRequest) { r.Identity.CorpusSHA256 = strings.Repeat("0", 64) }, wantErr: "corpus"},
			{name: "protocol hash", mutate: func(r *EvidenceRunRequest) { r.Identity.ProtocolSHA256 = strings.Repeat("0", 64) }, wantErr: "protocol"},
			{name: "binary hash", mutate: func(r *EvidenceRunRequest) { r.Identity.BinarySHA256 = strings.Repeat("0", 64) }, wantErr: "binary"},
			{name: "hardware", mutate: func(r *EvidenceRunRequest) { r.Identity.Hardware.CPU = "different-cpu" }, wantErr: "hardware"},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				workspace := t.TempDir()
				request := newEvidenceRequest(t, filepath.Join(workspace, "output"), "identity-red")
				request.WorkDir = filepath.Join(workspace, "work")
				if err := os.Mkdir(request.WorkDir, 0o755); err != nil {
					t.Fatalf("Mkdir(work) error = %v", err)
				}
				t.Setenv("TMPDIR", request.WorkDir)
				t.Setenv("TEMP", request.WorkDir)
				t.Setenv("TMP", request.WorkDir)
				tt.mutate(&request)

				_, err := RunEvidence(context.Background(), request)
				if err == nil || !strings.Contains(strings.ToLower(err.Error()), tt.wantErr) {
					t.Fatalf("RunEvidence() error = %v, want identity error containing %q", err, tt.wantErr)
				}
				assertEmptyDirectory(t, request.WorkDir, "identity validation created a database before rejecting the run")
				if _, statErr := os.Stat(request.OutputDir); !os.IsNotExist(statErr) {
					t.Fatalf("output stat error = %v, want no output before identity validation", statErr)
				}
			})
		}
	})

	t.Run("creates a fresh SQLite database and app for every invocation", func(t *testing.T) {
		first := runEvidenceContract(t, "fresh-run-001")
		second := runEvidenceContract(t, "fresh-run-002")

		if first.RunID == second.RunID || first.Report.ReportID == second.Report.ReportID {
			t.Fatalf("run identities were reused: first=%q/%q second=%q/%q", first.RunID, first.Report.ReportID, second.RunID, second.Report.ReportID)
		}
		if first.Report.RunID != first.RunID || second.Report.RunID != second.RunID {
			t.Fatalf("report run IDs = %q, %q; want independent invocation IDs %q, %q", first.Report.RunID, second.Report.RunID, first.RunID, second.RunID)
		}
	})

	t.Run("invokes the unchanged current production runner", func(t *testing.T) {
		output := filepath.Join(t.TempDir(), "production-path")
		runEvidenceContractAt(t, output, "production-path")

		var raw BaselineRun
		readJSONFile(t, filepath.Join(output, "raw.json"), &raw)
		if len(raw.Queries) == 0 {
			t.Fatal("raw queries are empty; current production runner was not invoked")
		}
		for _, trace := range raw.Queries {
			for _, ranked := range trace.Ranked {
				if ranked.Strategy == "" {
					t.Fatalf("query %q result %q has no production search strategy", trace.QueryID, ranked.StableID)
				}
			}
		}
	})

	t.Run("stages output atomically without overwrite", func(t *testing.T) {
		parent := t.TempDir()
		output := filepath.Join(parent, "atomic-output")
		run := runEvidenceContractAt(t, output, "atomic-output")

		entries, err := os.ReadDir(output)
		if err != nil {
			t.Fatalf("ReadDir(output) error = %v", err)
		}
		gotNames := make([]string, 0, len(entries))
		for _, entry := range entries {
			gotNames = append(gotNames, entry.Name())
			if strings.Contains(entry.Name(), ".tmp") || strings.Contains(entry.Name(), ".partial") {
				t.Fatalf("partial staging artifact remains after success: %q", entry.Name())
			}
		}
		wantNames := []string{"independent-run.json", "raw.json", "report.json"}
		if !reflect.DeepEqual(gotNames, wantNames) {
			t.Fatalf("output files = %v, want %v", gotNames, wantNames)
		}

		var persisted common.EvidenceReport
		readJSONFile(t, filepath.Join(output, "report.json"), &persisted)
		if persisted.ReportID != run.Report.ReportID {
			t.Fatalf("persisted report ID = %q, want %q", persisted.ReportID, run.Report.ReportID)
		}
	})

	t.Run("refuses every external provider mode offline", func(t *testing.T) {
		tests := []struct {
			name string
			env  string
		}{
			{name: "Ollama endpoint", env: "OLLAMA_ENDPOINT"},
			{name: "OpenAI API", env: "OPENAI_API_KEY"},
			{name: "Anthropic API", env: "ANTHROPIC_API_KEY"},
			{name: "embedding provider", env: "CORTEX_EMBEDDING_PROVIDER"},
			{name: "judge provider", env: "CORTEX_JUDGE_PROVIDER"},
			{name: "network mode", env: "CORTEX_BENCH_NETWORK"},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				clearExternalProviderEnvironment(t)
				output := filepath.Join(t.TempDir(), "must-not-exist")
				request := newEvidenceRequest(t, output, "offline-refusal")
				t.Setenv(tt.env, "configured")

				_, err := RunEvidence(context.Background(), request)
				if err == nil || !strings.Contains(strings.ToLower(err.Error()), "external") {
					t.Fatalf("RunEvidence() error = %v, want typed external-provider refusal for %s", err, tt.env)
				}
				if _, statErr := os.Stat(output); !os.IsNotExist(statErr) {
					t.Fatalf("output stat error = %v, want no output for %s", statErr, tt.env)
				}
			})
		}
	})

	t.Run("refuses an existing output without mutation", func(t *testing.T) {
		output := filepath.Join(t.TempDir(), "existing")
		if err := os.Mkdir(output, 0o755); err != nil {
			t.Fatalf("Mkdir(output) error = %v", err)
		}
		sentinel := filepath.Join(output, "sentinel.txt")
		if err := os.WriteFile(sentinel, []byte("do not overwrite"), 0o600); err != nil {
			t.Fatalf("WriteFile(sentinel) error = %v", err)
		}

		_, err := RunEvidence(context.Background(), newEvidenceRequest(t, output, "existing-output"))
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), "exist") {
			t.Fatalf("RunEvidence() error = %v, want existing-output refusal", err)
		}
		contents, readErr := os.ReadFile(sentinel)
		if readErr != nil || string(contents) != "do not overwrite" {
			t.Fatalf("sentinel after refusal = %q, %v; existing output was mutated", contents, readErr)
		}
	})

	t.Run("constructs a valid EvidenceReport with resource capture", func(t *testing.T) {
		run := runEvidenceContract(t, "report-resources")
		if err := run.Report.Validate(); err != nil {
			t.Fatalf("EvidenceReport.Validate() error = %v", err)
		}
		if !run.AllocationsAvailable || run.TotalAllocBytes == 0 {
			t.Fatalf("allocation evidence = available:%v heap:%d total:%d, want measured allocations", run.AllocationsAvailable, run.HeapAllocBytes, run.TotalAllocBytes)
		}
		resources := run.Report.Resources
		if resources.CPUUnit == "" || resources.PeakRSSUnit == "" || resources.StorageUnit == "" || resources.IndexUnit == "" {
			t.Fatalf("resource units are incomplete: %+v", resources)
		}
		if resources.CPUAvailable == nil || resources.PeakRSSAvailable == nil || resources.StorageAvailable == nil || resources.IndexAvailable == nil {
			t.Fatalf("resource availability is ambiguous: %+v", resources)
		}
	})

	t.Run("report build provenance uses validated execution identity", func(t *testing.T) {
		output := filepath.Join(t.TempDir(), "provenance")
		request := newEvidenceRequest(t, output, "provenance-identity")
		run, err := RunEvidence(context.Background(), request)
		if err != nil {
			t.Fatalf("RunEvidence() error = %v", err)
		}
		if run.Report.Build.Commit != request.Identity.Commit {
			t.Fatalf("Report.Build.Commit = %q, want request identity commit %q", run.Report.Build.Commit, request.Identity.Commit)
		}
		if run.Report.Build.Dirty {
			t.Fatalf("Report.Build.Dirty = true, want false for clean validated execution")
		}
		corpus := readCorpus(t, filepath.Join("..", "evidence", "cortex-native", "v1"))
		if run.Report.Build.Commit == corpus.Build.Commit {
			t.Fatalf("Report.Build.Commit = %q equals stale corpus build commit; provenance must reflect evaluated HEAD", run.Report.Build.Commit)
		}
	})

	t.Run("preserves unsupported authority as not_executed_capability", func(t *testing.T) {
		run := runEvidenceContract(t, "unsupported-authority")
		serialized, err := json.Marshal(run.Report)
		if err != nil {
			t.Fatalf("Marshal(report) error = %v", err)
		}
		for _, field := range []string{"privacy", "lifecycle", "provenance"} {
			if !strings.Contains(string(serialized), field) || !strings.Contains(string(serialized), common.CapabilityNotExecuted) {
				t.Fatalf("report does not preserve %q as %q: %s", field, common.CapabilityNotExecuted, serialized)
			}
		}
	})
}

func newEvidenceRequest(t *testing.T, outputDir, runID string) EvidenceRunRequest {
	t.Helper()
	clearExternalProviderEnvironment(t)

	root := filepath.Join("..", "evidence", "cortex-native", "v1")
	binary, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable() error = %v", err)
	}
	identity := EvidenceIdentity{
		Commit:         currentHEAD(t),
		BinarySHA256:   fileSHA256(t, binary),
		CorpusSHA256:   fileSHA256(t, filepath.Join(root, "corpus.json")),
		ProtocolSHA256: fileSHA256(t, filepath.Join(root, "protocol.json")),
		Hardware:       readCorpus(t, root).Hardware,
	}
	request, err := NewEvidenceRunRequest(root, outputDir, runID, "seed-42", "cortex-native-v1", identity)
	if err != nil {
		t.Fatalf("NewEvidenceRunRequest() error = %v", err)
	}
	return request
}

func runEvidenceContract(t *testing.T, runID string) common.IndependentRun {
	t.Helper()
	return runEvidenceContractAt(t, filepath.Join(t.TempDir(), runID), runID)
}

func runEvidenceContractAt(t *testing.T, outputDir, runID string) common.IndependentRun {
	t.Helper()
	run, err := RunEvidence(context.Background(), newEvidenceRequest(t, outputDir, runID))
	if err != nil {
		t.Fatalf("RunEvidence() error = %v", err)
	}
	return run
}

func readCorpus(t *testing.T, root string) common.Corpus {
	t.Helper()
	var corpus common.Corpus
	readJSONFile(t, filepath.Join(root, "corpus.json"), &corpus)
	return corpus
}

func readJSONFile(t *testing.T, path string, target any) {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	if err := json.Unmarshal(contents, target); err != nil {
		t.Fatalf("Unmarshal(%q) error = %v", path, err)
	}
}

func fileSHA256(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	sum := sha256.Sum256(contents)
	return hex.EncodeToString(sum[:])
}

func clearExternalProviderEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"OLLAMA_ENDPOINT",
		"OPENAI_API_KEY",
		"ANTHROPIC_API_KEY",
		"CORTEX_EMBEDDING_PROVIDER",
		"CORTEX_JUDGE_PROVIDER",
		"CORTEX_BENCH_NETWORK",
	} {
		t.Setenv(name, "")
	}
}

func assertEmptyDirectory(t *testing.T, path, message string) {
	t.Helper()
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatalf("ReadDir(%q) error = %v", path, err)
	}
	if len(entries) != 0 {
		t.Fatalf("%s: found %v", message, entries)
	}
}
