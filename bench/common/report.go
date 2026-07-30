package common

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
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
