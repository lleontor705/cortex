package common

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

// EvidenceReport is the versioned release-evidence contract for retrieval runs.
// It is intentionally separate from BenchmarkResult, which remains the legacy
// answer-evaluation format used by existing benchmark runners.
type EvidenceReport struct {
	SchemaVersion     string             `json:"schema_version"`
	RunID             string             `json:"run_id,omitempty"`
	ReportID          string             `json:"report_id"`
	CorpusVersion     string             `json:"corpus_version"`
	ProtocolVersion   string             `json:"protocol_version"`
	Build             BuildMetadata      `json:"build"`
	Hardware          HardwareMetadata   `json:"hardware"`
	MetricDefinitions []MetricDefinition `json:"metric_definitions"`
	Profiles          []ProfileReport    `json:"profiles"`
	Queries           []QueryReport      `json:"queries"`
	Resources         ResourceReport     `json:"resources"`
	Uncertainty       UncertaintyReport  `json:"uncertainty"`
	Limitations       []string           `json:"limitations"`
}

// MetricDefinition records how a reported metric is interpreted before
// candidate results are evaluated.
type MetricDefinition struct {
	Name        string `json:"name"`
	Unit        string `json:"unit"`
	Direction   string `json:"direction"`
	Description string `json:"description"`
}

// ProfileReport contains aggregate metrics and performance distributions for
// one immutable profile version and query class.
type ProfileReport struct {
	ProfileID      string             `json:"profile_id"`
	ProfileVersion string             `json:"profile_version"`
	QueryClass     string             `json:"query_class"`
	Metrics        map[string]float64 `json:"metrics"`
	Latency        LatencyReport      `json:"latency"`
	Throughput     ThroughputReport   `json:"throughput"`
}

// LatencyReport records required latency quantiles with an explicit unit.
type LatencyReport struct {
	Unit string  `json:"unit"`
	P50  float64 `json:"p50"`
	P95  float64 `json:"p95"`
	P99  float64 `json:"p99"`
}

// ThroughputReport records completed queries per second with an explicit unit.
type ThroughputReport struct {
	Unit             string  `json:"unit"`
	QueriesPerSecond float64 `json:"queries_per_second"`
}

// QueryReport preserves traceable current and candidate ranked outputs for one
// immutable query/profile execution.
type QueryReport struct {
	QueryID         string             `json:"query_id"`
	ProfileID       string             `json:"profile_id"`
	ProfileVersion  string             `json:"profile_version"`
	QueryClass      string             `json:"query_class"`
	Metrics         map[string]float64 `json:"metrics"`
	CurrentOutput   []RankedOutput     `json:"current_output"`
	CandidateOutput []RankedOutput     `json:"candidate_output"`
}

// RankedOutput identifies one stable result in an observed ranking.
type RankedOutput struct {
	StableID string  `json:"stable_id"`
	Rank     int     `json:"rank"`
	Score    float64 `json:"score"`
}

// ResourceReport records CPU, peak RSS, corpus storage, and retrieval-index
// costs. Units are explicit so reports cannot silently reinterpret values.
type ResourceReport struct {
	CPUSeconds       float64 `json:"cpu_seconds"`
	CPUUnit          string  `json:"cpu_unit"`
	CPUAvailable     *bool   `json:"cpu_available,omitempty"`
	PeakRSSBytes     int64   `json:"peak_rss_bytes"`
	PeakRSSUnit      string  `json:"peak_rss_unit"`
	PeakRSSAvailable *bool   `json:"peak_rss_available,omitempty"`
	StorageBytes     int64   `json:"storage_bytes"`
	StorageUnit      string  `json:"storage_unit"`
	StorageAvailable *bool   `json:"storage_available,omitempty"`
	IndexBytes       int64   `json:"index_bytes"`
	IndexUnit        string  `json:"index_unit"`
	IndexAvailable   *bool   `json:"index_available,omitempty"`
}

// UncertaintyReport discloses the uncertainty method and sampling basis.
type UncertaintyReport struct {
	Method          string  `json:"method"`
	ConfidenceLevel float64 `json:"confidence_level"`
	SampleSize      int     `json:"sample_size"`
	Notes           string  `json:"notes"`
}

// Validate rejects reports that cannot support reproducible retrieval claims.
func (r EvidenceReport) Validate() error {
	if r.SchemaVersion == "" || r.ReportID == "" {
		return fmt.Errorf("schema_version and report_id are required")
	}
	if r.CorpusVersion == "" {
		return fmt.Errorf("corpus_version is required")
	}
	if r.RunID != "" && r.RunID == r.ReportID {
		return fmt.Errorf("run_id and report_id must identify distinct artifacts")
	}
	if r.ProtocolVersion == "" {
		return fmt.Errorf("protocol_version is required")
	}
	if r.Build.Commit == "" {
		return fmt.Errorf("build.commit is required")
	}
	if r.Hardware.ProfileID == "" || r.Hardware.OS == "" || r.Hardware.Arch == "" || r.Hardware.CPU == "" || r.Hardware.MemoryMB <= 0 {
		return fmt.Errorf("hardware profile_id, os, arch, cpu, and positive memory_mb are required")
	}
	if len(r.MetricDefinitions) == 0 {
		return fmt.Errorf("metric_definitions are required")
	}
	definitions := make(map[string]struct{}, len(r.MetricDefinitions))
	for i, definition := range r.MetricDefinitions {
		if definition.Name == "" || definition.Unit == "" || definition.Direction == "" || definition.Description == "" {
			return fmt.Errorf("metric_definitions[%d] name, unit, direction, and description are required", i)
		}
		if definition.Direction != "higher_is_better" && definition.Direction != "lower_is_better" {
			return fmt.Errorf("metric_definitions[%d].direction is invalid", i)
		}
		if _, duplicate := definitions[definition.Name]; duplicate {
			return fmt.Errorf("metric_definitions[%d].name %q is duplicated", i, definition.Name)
		}
		definitions[definition.Name] = struct{}{}
	}
	if len(r.Profiles) == 0 {
		return fmt.Errorf("profiles are required")
	}
	profileIdentities := make(map[string]struct{}, len(r.Profiles))
	for i, profile := range r.Profiles {
		if err := profile.validate(definitions); err != nil {
			return fmt.Errorf("profiles[%d]: %w", i, err)
		}
		identity := profileIdentity(profile.ProfileID, profile.ProfileVersion, profile.QueryClass)
		if _, duplicate := profileIdentities[identity]; duplicate {
			return fmt.Errorf("profiles[%d] profile identity %q is duplicated", i, identity)
		}
		profileIdentities[identity] = struct{}{}
	}
	if len(r.Queries) == 0 {
		return fmt.Errorf("queries are required")
	}
	queryIdentities := make(map[string]struct{}, len(r.Queries))
	for i, query := range r.Queries {
		if err := query.validate(definitions); err != nil {
			return fmt.Errorf("queries[%d]: %w", i, err)
		}
		if _, duplicate := queryIdentities[query.QueryID]; duplicate {
			return fmt.Errorf("queries[%d] query identity %q is duplicated", i, query.QueryID)
		}
		queryIdentities[query.QueryID] = struct{}{}
		identity := profileIdentity(query.ProfileID, query.ProfileVersion, query.QueryClass)
		if _, exists := profileIdentities[identity]; !exists {
			return fmt.Errorf("queries[%d] references unknown profile identity %q", i, identity)
		}
	}
	if err := r.Resources.validate(); err != nil {
		return fmt.Errorf("resources: %w", err)
	}
	if r.Uncertainty.Method == "" || r.Uncertainty.SampleSize <= 0 || r.Uncertainty.ConfidenceLevel <= 0 || r.Uncertainty.ConfidenceLevel >= 1 || r.Uncertainty.Notes == "" {
		return fmt.Errorf("uncertainty method, confidence_level between zero and one, positive sample_size, and notes are required")
	}
	if len(r.Limitations) == 0 {
		return fmt.Errorf("limitations are required")
	}
	for i, limitation := range r.Limitations {
		if limitation == "" {
			return fmt.Errorf("limitations[%d] is empty", i)
		}
	}
	return nil
}

// SerializeEvidenceReport validates and emits stable, human-readable JSON.
// Order-insensitive report collections are sorted on a copy; ranked outputs
// retain their observed order.
func SerializeEvidenceReport(report EvidenceReport) ([]byte, error) {
	if err := report.Validate(); err != nil {
		return nil, err
	}
	canonical := report
	canonical.MetricDefinitions = append([]MetricDefinition(nil), report.MetricDefinitions...)
	canonical.Profiles = append([]ProfileReport(nil), report.Profiles...)
	canonical.Queries = append([]QueryReport(nil), report.Queries...)
	canonical.Limitations = append([]string(nil), report.Limitations...)
	canonical.Resources = report.Resources.withExplicitAvailability()
	sort.Slice(canonical.MetricDefinitions, func(i, j int) bool {
		return canonical.MetricDefinitions[i].Name < canonical.MetricDefinitions[j].Name
	})
	sort.Slice(canonical.Profiles, func(i, j int) bool {
		left, right := canonical.Profiles[i], canonical.Profiles[j]
		if left.ProfileID != right.ProfileID {
			return left.ProfileID < right.ProfileID
		}
		if left.ProfileVersion != right.ProfileVersion {
			return left.ProfileVersion < right.ProfileVersion
		}
		return left.QueryClass < right.QueryClass
	})
	sort.Slice(canonical.Queries, func(i, j int) bool {
		left, right := canonical.Queries[i], canonical.Queries[j]
		if left.QueryID != right.QueryID {
			return left.QueryID < right.QueryID
		}
		if left.ProfileID != right.ProfileID {
			return left.ProfileID < right.ProfileID
		}
		return left.ProfileVersion < right.ProfileVersion
	})
	sort.Strings(canonical.Limitations)

	encoded, err := json.MarshalIndent(canonical, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal evidence report: %w", err)
	}
	return append(encoded, '\n'), nil
}

func (r ProfileReport) validate(definitions map[string]struct{}) error {
	if r.ProfileID == "" || r.ProfileVersion == "" || r.QueryClass == "" {
		return fmt.Errorf("profile_id, profile_version, and query_class are required")
	}
	if err := validateMetricValues(r.Metrics, definitions); err != nil {
		return err
	}
	if r.Latency.Unit == "" {
		return fmt.Errorf("latency.unit is required")
	}
	if !finiteNonNegative(r.Latency.P50) || !finiteNonNegative(r.Latency.P95) || !finiteNonNegative(r.Latency.P99) || r.Latency.P50 > r.Latency.P95 || r.Latency.P95 > r.Latency.P99 {
		return fmt.Errorf("latency p50, p95, and p99 must be finite, non-negative, and ordered")
	}
	if r.Throughput.Unit == "" {
		return fmt.Errorf("throughput.unit is required")
	}
	if !finiteNonNegative(r.Throughput.QueriesPerSecond) {
		return fmt.Errorf("throughput.queries_per_second must be finite and non-negative")
	}
	return nil
}

func (r QueryReport) validate(definitions map[string]struct{}) error {
	if r.QueryID == "" || r.ProfileID == "" || r.ProfileVersion == "" || r.QueryClass == "" {
		return fmt.Errorf("query_id, profile_id, profile_version, and query_class are required")
	}
	if err := validateMetricValues(r.Metrics, definitions); err != nil {
		return err
	}
	if r.CurrentOutput == nil {
		return fmt.Errorf("current_output is required")
	}
	if r.CandidateOutput == nil {
		return fmt.Errorf("candidate_output is required")
	}
	for name, outputs := range map[string][]RankedOutput{"current_output": r.CurrentOutput, "candidate_output": r.CandidateOutput} {
		stableIDs := make(map[string]struct{}, len(outputs))
		for i, output := range outputs {
			if output.StableID == "" || output.Rank <= 0 || !finite(output.Score) {
				return fmt.Errorf("%s[%d] stable_id, positive rank, and finite score are required", name, i)
			}
			if output.Rank != i+1 {
				return fmt.Errorf("%s ranks must be contiguous in observed order starting at 1", name)
			}
			if _, duplicate := stableIDs[output.StableID]; duplicate {
				return fmt.Errorf("%s[%d].stable_id %q is duplicated", name, i, output.StableID)
			}
			stableIDs[output.StableID] = struct{}{}
		}
	}
	return nil
}

func (r ResourceReport) validate() error {
	if r.CPUUnit == "" || r.PeakRSSUnit == "" || r.StorageUnit == "" || r.IndexUnit == "" {
		return fmt.Errorf("cpu, peak RSS, storage, and index units are required")
	}
	if !finiteNonNegative(r.CPUSeconds) || r.PeakRSSBytes < 0 || r.StorageBytes < 0 || r.IndexBytes < 0 {
		return fmt.Errorf("cpu, peak RSS, storage, and index values must be non-negative")
	}
	if explicitlyUnavailable(r.CPUAvailable) && r.CPUSeconds != 0 {
		return fmt.Errorf("cpu availability is false but cpu_seconds is non-zero")
	}
	if explicitlyUnavailable(r.PeakRSSAvailable) && r.PeakRSSBytes != 0 {
		return fmt.Errorf("peak RSS availability is false but peak_rss_bytes is non-zero")
	}
	if explicitlyUnavailable(r.StorageAvailable) && r.StorageBytes != 0 {
		return fmt.Errorf("storage availability is false but storage_bytes is non-zero")
	}
	if explicitlyUnavailable(r.IndexAvailable) && r.IndexBytes != 0 {
		return fmt.Errorf("index availability is false but index_bytes is non-zero")
	}
	return nil
}

func (r ResourceReport) withExplicitAvailability() ResourceReport {
	r.CPUAvailable = availabilityOrDefault(r.CPUAvailable)
	r.PeakRSSAvailable = availabilityOrDefault(r.PeakRSSAvailable)
	r.StorageAvailable = availabilityOrDefault(r.StorageAvailable)
	r.IndexAvailable = availabilityOrDefault(r.IndexAvailable)
	return r
}

func availabilityOrDefault(value *bool) *bool {
	if value != nil {
		copy := *value
		return &copy
	}
	available := true
	return &available
}

func explicitlyUnavailable(value *bool) bool {
	return value != nil && !*value
}

func profileIdentity(profileID, profileVersion, queryClass string) string {
	return strings.Join([]string{profileID, profileVersion, queryClass}, "@")
}

func validateMetricValues(values map[string]float64, definitions map[string]struct{}) error {
	if values == nil {
		return fmt.Errorf("metrics are required")
	}
	for name, value := range values {
		if _, defined := definitions[name]; !defined {
			return fmt.Errorf("metric %q has no definition", name)
		}
		if !finite(value) {
			return fmt.Errorf("metric %q must be finite", name)
		}
	}
	return nil
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func finiteNonNegative(value float64) bool {
	return finite(value) && value >= 0
}

// EvalReportSchemaVersion identifies the published evaluation JSON contract.
const EvalReportSchemaVersion = "eval-comparison/v1"

// FixedJudgeProtocolVersion pins the shared judging protocol: Ollama-only
// endpoint, format:"json", temperature=0, seed=42 (bench/common/llm_judge.go).
const FixedJudgeProtocolVersion = "fixed-judge/ollama/v1"

// Benchmarks published by the offline evaluation suite.
const (
	EvalBenchmarkLongMemEval = "LongMemEval"
	EvalBenchmarkLOCOMO      = "LOCOMO"
)

// Landed tasks bound to published before/after slices (board cortex-rag-perf).
const (
	EvalTaskLongMemEvalPack = "rag-t04-longmemeval-pack"
	EvalTaskSkewRoute       = "rag-t05-skewroute-routing"
	EvalTaskVectorScan      = "rag-t07-vector-scan-modernization"
	EvalTaskHippoRAG2Wiring = "rag-t11-hipporag2-wiring"
)

// EvalSliceOverall is the whole-benchmark slice key.
const EvalSliceOverall = "overall"

// Published row statuses for one task comparison.
const (
	EvalRowCompared            = "compared"
	EvalRowBaselineNotRecorded = "baseline_not_recorded"
	EvalRowNotMeasured         = "not_measured"
)

// Published gate statuses for one evaluation report.
const (
	EvalGatePassed       = "passed"
	EvalGateFailed       = "failed"
	EvalGateNotEvaluated = "not_evaluated"
)

// BlockedError reports an evaluation that cannot produce trustworthy scores.
// Callers must surface it (see EvalExitCode) instead of publishing partial or
// fabricated results; scores are never silently skipped.
type BlockedError struct {
	Reason      string
	Remediation string
	Cause       error
}

func (e *BlockedError) Error() string {
	message := "BLOCKED: " + e.Reason
	if e.Remediation != "" {
		message += "; remediation: " + e.Remediation
	}
	if e.Cause != nil {
		message += "; cause: " + e.Cause.Error()
	}
	return message
}

func (e *BlockedError) Unwrap() error { return e.Cause }

// IsBlocked reports whether err is (or wraps) a BlockedError.
func IsBlocked(err error) bool {
	var blocked *BlockedError
	return errors.As(err, &blocked)
}

// NewJudgeBlockedError builds the fail-closed condition for a judge endpoint
// that is configured but did not produce a score.
func NewJudgeBlockedError(endpoint, model string, cause error) *BlockedError {
	return &BlockedError{
		Reason:      fmt.Sprintf("fixed-judge endpoint %q (model %q) did not produce a score", endpoint, model),
		Remediation: fmt.Sprintf("start the judge with 'ollama serve', pull the model with 'ollama pull %s', and set OLLAMA_ENDPOINT to the Ollama server root", model),
		Cause:       cause,
	}
}

// RegressionError names every task whose published delta fell below the gate.
type RegressionError struct {
	Failures []string
}

func (e *RegressionError) Error() string {
	return fmt.Sprintf("eval gate failed: %d task(s) below the baseline gate: %s", len(e.Failures), strings.Join(e.Failures, "; "))
}

// EvalExitCode maps harness outcomes to process exit codes: 0 pass, 2 BLOCKED
// (judge or environment unavailable), 1 regression or any other failure.
func EvalExitCode(err error) int {
	if err == nil {
		return 0
	}
	if IsBlocked(err) {
		return 2
	}
	return 1
}

// judgeProbeTimeout caps the reachability probe so a black-holed endpoint
// cannot stall a published run for the full per-request judge timeout.
const judgeProbeTimeout = 5 * time.Second

// RequireJudge probes the fixed-judge endpoint before scoring and fails closed
// with a BlockedError carrying operator remediation when it is unreachable.
func RequireJudge(ctx context.Context, cfg *JudgeConfig) error {
	if cfg == nil || strings.TrimSpace(cfg.Endpoint) == "" {
		return &BlockedError{
			Reason:      "fixed-judge endpoint is not configured",
			Remediation: "set OLLAMA_ENDPOINT to a running Ollama server root (default http://localhost:11434) and pull the judge model with 'ollama pull <OLLAMA_JUDGE_MODEL>'",
		}
	}
	timeout := judgeProbeTimeout
	if cfg.Timeout > 0 && cfg.Timeout < timeout {
		timeout = cfg.Timeout
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, cfg.Endpoint+"/api/tags", nil)
	if err != nil {
		return &BlockedError{
			Reason:      fmt.Sprintf("fixed-judge endpoint %q is not a usable URL", cfg.Endpoint),
			Remediation: "set OLLAMA_ENDPOINT to the Ollama server root, for example http://localhost:11434",
			Cause:       err,
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return &BlockedError{
			Reason:      fmt.Sprintf("fixed-judge endpoint %s is unreachable", cfg.Endpoint),
			Remediation: fmt.Sprintf("start the judge with 'ollama serve', pull the model with 'ollama pull %s', and set OLLAMA_ENDPOINT to the server root", cfg.Model),
			Cause:       err,
		}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &BlockedError{
			Reason:      fmt.Sprintf("fixed-judge endpoint %s answered HTTP %d on /api/tags", cfg.Endpoint, resp.StatusCode),
			Remediation: "point OLLAMA_ENDPOINT at an Ollama server root; published scores require the fixed Ollama-only judge",
		}
	}
	return nil
}

// JudgeProtocolSummary records the judging parameters behind a published score
// so readers can verify the fixed protocol was used.
type JudgeProtocolSummary struct {
	Protocol    string  `json:"protocol"`
	Enabled     bool    `json:"enabled"`
	Endpoint    string  `json:"endpoint,omitempty"`
	Model       string  `json:"model,omitempty"`
	Temperature float64 `json:"temperature"`
	Seed        int64   `json:"seed"`
	Format      string  `json:"format"`
}

// DescribeJudgeProtocol summarizes cfg under the fixed protocol constants; a
// nil cfg yields Enabled=false, disclosing an F1-only run.
func DescribeJudgeProtocol(cfg *JudgeConfig) JudgeProtocolSummary {
	summary := JudgeProtocolSummary{
		Protocol:    FixedJudgeProtocolVersion,
		Temperature: 0,
		Seed:        42,
		Format:      "json",
	}
	if cfg != nil {
		summary.Enabled = true
		summary.Endpoint = cfg.Endpoint
		summary.Model = cfg.Model
	}
	return summary
}

// EvalBaseline holds recorded before-scores keyed by BaselineKey.
type EvalBaseline map[string]float64

// BaselineKey builds the "<benchmark>/<slice>" key used by EvalBaseline.
func BaselineKey(benchmark, slice string) string {
	return benchmark + "/" + slice
}

// EvalBaselineFromReport extracts before-scores from a previously published
// report: every measured row contributes its after-score, which becomes the
// baseline of the next run. It refuses foreign schemas so a stale or unrelated
// artifact can never masquerade as a baseline.
func EvalBaselineFromReport(data []byte) (EvalBaseline, error) {
	var report EvalReport
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, fmt.Errorf("parse published eval report: %w", err)
	}
	if report.SchemaVersion != EvalReportSchemaVersion {
		return nil, fmt.Errorf("published eval report schema_version %q does not match %q", report.SchemaVersion, EvalReportSchemaVersion)
	}
	baseline := make(EvalBaseline, len(report.Tasks))
	for _, row := range report.Tasks {
		if row.After == nil {
			continue
		}
		baseline[BaselineKey(row.Benchmark, row.Slice)] = *row.After
	}
	if len(baseline) == 0 {
		return nil, fmt.Errorf("published eval report contains no measured slices")
	}
	return baseline, nil
}

// TaskComparison is one published before/after row binding a landed task to
// the benchmark slice it targets. Before and Delta stay nil until a baseline
// records that slice; After is nil when this run did not measure the slice.
type TaskComparison struct {
	TaskID    string   `json:"task_id"`
	Benchmark string   `json:"benchmark"`
	Slice     string   `json:"slice"`
	Status    string   `json:"status"`
	Before    *float64 `json:"before,omitempty"`
	After     *float64 `json:"after,omitempty"`
	Delta     *float64 `json:"delta,omitempty"`
	MinDelta  float64  `json:"min_delta"`
	Passed    *bool    `json:"passed,omitempty"`
}

// EvalScore publishes the current fixed-judge scores for one benchmark run.
type EvalScore struct {
	Metric  string             `json:"metric"`
	After   float64            `json:"after"`
	BySlice map[string]float64 `json:"by_slice"`
	Total   int                `json:"total_questions"`
	Correct int                `json:"correct"`
}

// EvalGate records the outcome of the per-task regression gate.
type EvalGate struct {
	Status   string   `json:"status"`
	Failures []string `json:"failures,omitempty"`
}

// EvalReport is the published before/after evaluation artifact for one
// benchmark run under the fixed-judge protocol.
type EvalReport struct {
	SchemaVersion   string               `json:"schema_version"`
	ProtocolVersion string               `json:"protocol_version"`
	Benchmark       string               `json:"benchmark"`
	GeneratedAt     string               `json:"generated_at"`
	Judge           JudgeProtocolSummary `json:"judge"`
	Score           EvalScore            `json:"score"`
	BaselineStatus  string               `json:"baseline_status"`
	Tasks           []TaskComparison     `json:"tasks"`
	Gate            EvalGate             `json:"gate"`
	Limitations     []string             `json:"limitations"`
}

// BuildEvalReport publishes the per-task before/after comparison for one run.
// vectorEnabled registers the vector-scan row; minDelta is the per-task
// regression gate (0 means a task may never fall below its recorded baseline).
// Missing baseline entries are disclosed, never filled with invented numbers.
func BuildEvalReport(benchmark string, result BenchmarkResult, vectorEnabled bool, judge JudgeProtocolSummary, baseline EvalBaseline, minDelta float64) (EvalReport, error) {
	if benchmark != EvalBenchmarkLongMemEval && benchmark != EvalBenchmarkLOCOMO {
		return EvalReport{}, fmt.Errorf("eval report: unsupported benchmark %q", benchmark)
	}
	if result.Benchmark != "" && result.Benchmark != benchmark {
		return EvalReport{}, fmt.Errorf("eval report: result benchmarks as %q, not %q", result.Benchmark, benchmark)
	}
	if !judge.Enabled || judge.Protocol != FixedJudgeProtocolVersion {
		return EvalReport{}, fmt.Errorf("eval report: published scores require the fixed-judge protocol (protocol %q enabled %t)", judge.Protocol, judge.Enabled)
	}
	if !finite(minDelta) {
		return EvalReport{}, fmt.Errorf("eval report: min_delta must be finite")
	}
	if !finite(result.Overall) || result.Total <= 0 || result.Correct < 0 || result.Correct > result.Total {
		return EvalReport{}, fmt.Errorf("eval report: score aggregate is inconsistent")
	}

	bySlice := make(map[string]float64, len(result.Details)+1)
	bySlice[EvalSliceOverall] = result.Overall
	if len(result.Details) != result.Total {
		return EvalReport{}, fmt.Errorf("eval report: %d score details for %d questions", len(result.Details), result.Total)
	}
	typeCorrect := make(map[string]int)
	typeTotal := make(map[string]int)
	for _, detail := range result.Details {
		if detail.Type == "" {
			continue
		}
		typeTotal[detail.Type]++
		if detail.Correct {
			typeCorrect[detail.Type]++
		}
	}
	for slice, total := range typeTotal {
		accuracy := math.Round(float64(typeCorrect[slice])/float64(total)*1000) / 1000
		bySlice[slice] = accuracy
	}

	baselineStatus := "not_recorded"
	if len(baseline) > 0 {
		baselineStatus = "recorded"
	}

	rows := registeredEvalTasks(benchmark, vectorEnabled)
	gate := EvalGate{Status: EvalGateNotEvaluated}
	allCompared := len(rows) > 0
	for i := range rows {
		row := &rows[i]
		measured, ok := measuredSlice(bySlice, row.Slice)
		if !ok {
			row.Status = EvalRowNotMeasured
			allCompared = false
			continue
		}
		after := measured
		row.After = &after
		before, ok := baseline[BaselineKey(benchmark, row.Slice)]
		if !ok {
			row.Status = EvalRowBaselineNotRecorded
			allCompared = false
			continue
		}
		if !finite(before) {
			return EvalReport{}, fmt.Errorf("eval report: baseline %q is not finite", BaselineKey(benchmark, row.Slice))
		}
		delta := measured - before
		passed := delta >= minDelta
		row.Status = EvalRowCompared
		row.Before = &before
		row.Delta = &delta
		row.Passed = &passed
		if !passed {
			gate.Failures = append(gate.Failures, fmt.Sprintf(
				"task %s slice %s: before %.4f after %.4f delta %+.4f below gate %.4f",
				row.TaskID, BaselineKey(benchmark, row.Slice), before, measured, delta, minDelta))
		}
	}
	switch {
	case len(gate.Failures) > 0:
		gate.Status = EvalGateFailed
	case allCompared:
		gate.Status = EvalGatePassed
	}

	limitations := []string{
		"Task rows bind each landed task to the benchmark slice it targets; deltas measure slice movement against the recorded baseline, not per-task causality.",
		"Deltas attribute to the runner's default query path (retrieval.ExecuteAdaptiveSearch with the prior stage's fusion scores; Config.LegacyPath=true reproduces the pre-adaptive FTS+RRF row), so rag-t05 routing and rag-t11 adaptive wiring changes now move these numbers; changes outside the query path still show zero delta.",
		"Judge scores measure answer acceptability under the fixed protocol; they are not labelled retrieval relevance evidence.",
	}
	if baselineStatus == "not_recorded" {
		limitations = append(limitations, "No baseline was supplied: before/after values stay unpublished and the gate is not evaluated; feed this report to EvalBaselineFromReport to record it for the next run.")
	}
	if vectorEnabled {
		limitations = append(limitations, "rag-t07 vector rows require a -tags cortex_vectors build and a healthy vector index; a degraded index skips vector fusion for the whole run.")
	}

	return EvalReport{
		SchemaVersion:   EvalReportSchemaVersion,
		ProtocolVersion: FixedJudgeProtocolVersion,
		Benchmark:       benchmark,
		GeneratedAt:     time.Now().UTC().Format(time.RFC3339),
		Judge:           judge,
		Score: EvalScore{
			Metric:  "fixed_judge_accuracy",
			After:   result.Overall,
			BySlice: bySlice,
			Total:   result.Total,
			Correct: result.Correct,
		},
		BaselineStatus: baselineStatus,
		Tasks:          rows,
		Gate:           gate,
		Limitations:    limitations,
	}, nil
}

// RegressionError returns a *RegressionError when the gate failed, else nil.
func (r EvalReport) RegressionError() error {
	if r.Gate.Status != EvalGateFailed {
		return nil
	}
	return &RegressionError{Failures: append([]string(nil), r.Gate.Failures...)}
}

// SerializeEvalReport validates and emits stable, human-readable JSON.
func SerializeEvalReport(report EvalReport) ([]byte, error) {
	if report.SchemaVersion != EvalReportSchemaVersion {
		return nil, fmt.Errorf("eval report: schema_version %q is not %q", report.SchemaVersion, EvalReportSchemaVersion)
	}
	if report.Benchmark == "" {
		return nil, fmt.Errorf("eval report: benchmark is required")
	}
	if !report.Judge.Enabled || report.Judge.Protocol != FixedJudgeProtocolVersion {
		return nil, fmt.Errorf("eval report: published scores require the fixed-judge protocol")
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal eval report: %w", err)
	}
	return append(encoded, '\n'), nil
}

// PublishEvalReport writes the serialized evaluation report to path.
func PublishEvalReport(path string, report EvalReport) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("eval report: publish path is required")
	}
	encoded, err := SerializeEvalReport(report)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		return fmt.Errorf("publish eval report: %w", err)
	}
	return nil
}

// registeredEvalTasks lists the landed-task comparison rows for a benchmark.
// The vector-scan row is registered only when the run enables vector search.
func registeredEvalTasks(benchmark string, vectorEnabled bool) []TaskComparison {
	newRow := func(taskID, slice string) TaskComparison {
		return TaskComparison{TaskID: taskID, Benchmark: benchmark, Slice: slice}
	}
	switch benchmark {
	case EvalBenchmarkLongMemEval:
		return []TaskComparison{
			newRow(EvalTaskLongMemEvalPack, EvalSliceOverall),
			newRow(EvalTaskSkewRoute, EvalSliceOverall),
			newRow(EvalTaskHippoRAG2Wiring, "MR"),
		}
	case EvalBenchmarkLOCOMO:
		rows := []TaskComparison{
			newRow(EvalTaskLongMemEvalPack, EvalSliceOverall),
			newRow(EvalTaskSkewRoute, EvalSliceOverall),
			newRow(EvalTaskHippoRAG2Wiring, "multi-hop"),
		}
		if vectorEnabled {
			rows = append(rows, newRow(EvalTaskVectorScan, EvalSliceOverall))
		}
		return rows
	default:
		return nil
	}
}

// measuredSlice looks up a slice score, tolerating dataset case drift (the
// upstream LongMemEval ability codes are upper-cased at ingestion).
func measuredSlice(bySlice map[string]float64, slice string) (float64, bool) {
	if value, ok := bySlice[slice]; ok {
		return value, true
	}
	for key, value := range bySlice {
		if strings.EqualFold(key, slice) {
			return value, true
		}
	}
	return 0, false
}
