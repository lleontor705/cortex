package common

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"
)

// IndependentRun preserves one complete, independently executed evidence report
// and the seed used by its registered protocol.
type IndependentRun struct {
	RunID    string              `json:"run_id"`
	Seed     string              `json:"seed"`
	Report   EvidenceReport      `json:"report"`
	Outliers []OutlierDisclosure `json:"outliers,omitempty"`
}

// OutlierDisclosure records an observed anomaly without silently excluding the
// associated run from variance calculations.
type OutlierDisclosure struct {
	RunID     string `json:"run_id,omitempty"`
	MetricKey string `json:"metric_key"`
	Reason    string `json:"reason"`
}

// ReproProtocol identifies the reviewer-approved dispersion method and any
// numeric tolerances registered before candidate evaluation.
type ReproProtocol struct {
	Version          string            `json:"version"`
	DispersionMethod string            `json:"dispersion_method"`
	ApprovedBy       string            `json:"approved_by"`
	Tolerances       []MetricTolerance `json:"tolerances,omitempty"`
}

// MetricTolerance is an explicitly approved maximum observed range. Cortex
// never supplies a default because thresholds require baseline evidence.
type MetricTolerance struct {
	MetricKey  string  `json:"metric_key"`
	MaxRange   float64 `json:"max_range"`
	ApprovedBy string  `json:"approved_by"`
}

// ReproIdentity contains every field that must match before runs are compared.
type ReproIdentity struct {
	Seed            string           `json:"seed"`
	Build           BuildMetadata    `json:"build"`
	CorpusVersion   string           `json:"corpus_version"`
	Hardware        HardwareMetadata `json:"hardware"`
	ProtocolVersion string           `json:"protocol_version"`
}

// MetricVariance reports descriptive dispersion without assigning an
// unregistered release threshold.
type MetricVariance struct {
	MetricKey               string  `json:"metric_key"`
	SampleSize              int     `json:"sample_size"`
	Minimum                 float64 `json:"minimum"`
	Maximum                 float64 `json:"maximum"`
	Mean                    float64 `json:"mean"`
	Range                   float64 `json:"range"`
	SampleStandardDeviation float64 `json:"sample_standard_deviation"`
	DispersionMethod        string  `json:"dispersion_method"`
	DispersionApprovedBy    string  `json:"dispersion_approved_by"`
}

// ToleranceEvaluation compares observed dispersion only with a caller-supplied,
// reviewer-approved preregistered tolerance.
type ToleranceEvaluation struct {
	MetricKey         string  `json:"metric_key"`
	ObservedRange     float64 `json:"observed_range"`
	RegisteredMaximum float64 `json:"registered_maximum"`
	ApprovedBy        string  `json:"approved_by"`
	ProtocolVersion   string  `json:"protocol_version"`
	Passed            bool    `json:"passed"`
}

// ReproAnalysis preserves raw independent runs alongside exact deterministic
// comparison, descriptive variance, outlier disclosure, and tolerance results.
type ReproAnalysis struct {
	Identity                 ReproIdentity         `json:"identity"`
	Runs                     []IndependentRun      `json:"runs"`
	DeterministicMatch       bool                  `json:"deterministic_match"`
	DeterministicDifferences []string              `json:"deterministic_differences"`
	Variance                 []MetricVariance      `json:"variance"`
	Outliers                 []OutlierDisclosure   `json:"outliers"`
	ToleranceEvaluations     []ToleranceEvaluation `json:"tolerance_evaluations"`
}

// AnalyzeReproducibility compares independent baseline runs only when their
// seed, build, corpus, hardware, and evaluated protocol identities match.
func AnalyzeReproducibility(runs []IndependentRun, protocol ReproProtocol) (ReproAnalysis, error) {
	if err := validateReproProtocol(protocol); err != nil {
		return ReproAnalysis{}, err
	}
	if len(runs) < 2 {
		return ReproAnalysis{}, fmt.Errorf("at least two independent runs are required")
	}

	seenRunIDs := make(map[string]struct{}, len(runs))
	for i := range runs {
		if strings.TrimSpace(runs[i].RunID) == "" || strings.TrimSpace(runs[i].Seed) == "" {
			return ReproAnalysis{}, fmt.Errorf("runs[%d] run_id and seed are required", i)
		}
		if _, exists := seenRunIDs[runs[i].RunID]; exists {
			return ReproAnalysis{}, fmt.Errorf("run_id %q is duplicated", runs[i].RunID)
		}
		seenRunIDs[runs[i].RunID] = struct{}{}
		if runs[i].Report.Build.Dirty {
			return ReproAnalysis{}, fmt.Errorf("runs[%d] must use a clean committed build", i)
		}
		if err := runs[i].Report.Validate(); err != nil {
			return ReproAnalysis{}, fmt.Errorf("runs[%d] report: %w", i, err)
		}
	}

	identity := reproIdentity(runs[0])
	for i := 1; i < len(runs); i++ {
		if err := compareReproIdentity(identity, reproIdentity(runs[i])); err != nil {
			return ReproAnalysis{}, fmt.Errorf("runs[%d] identity mismatch: %w", i, err)
		}
	}

	analysis := ReproAnalysis{
		Identity:             identity,
		Runs:                 append([]IndependentRun(nil), runs...),
		DeterministicMatch:   true,
		Variance:             []MetricVariance{},
		Outliers:             []OutlierDisclosure{},
		ToleranceEvaluations: []ToleranceEvaluation{},
	}
	baseline := canonicalQueries(runs[0].Report.Queries)
	for i := 1; i < len(runs); i++ {
		if !reflect.DeepEqual(baseline, canonicalQueries(runs[i].Report.Queries)) {
			analysis.DeterministicMatch = false
			analysis.DeterministicDifferences = append(analysis.DeterministicDifferences,
				fmt.Sprintf("run %q query results differ from run %q", runs[i].RunID, runs[0].RunID))
		}
	}

	samples, err := collectMetricSamples(runs)
	if err != nil {
		return ReproAnalysis{}, err
	}
	keys := make([]string, 0, len(samples))
	for key := range samples {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		analysis.Variance = append(analysis.Variance, calculateVariance(key, samples[key], protocol))
	}

	knownMetrics := make(map[string]struct{}, len(samples))
	for key := range samples {
		knownMetrics[key] = struct{}{}
	}
	for _, run := range runs {
		for _, disclosure := range run.Outliers {
			if disclosure.MetricKey == "" || strings.TrimSpace(disclosure.Reason) == "" {
				return ReproAnalysis{}, fmt.Errorf("run %q outlier metric_key and reason are required", run.RunID)
			}
			if _, exists := knownMetrics[disclosure.MetricKey]; !exists {
				return ReproAnalysis{}, fmt.Errorf("run %q outlier references unknown metric %q", run.RunID, disclosure.MetricKey)
			}
			disclosure.RunID = run.RunID
			analysis.Outliers = append(analysis.Outliers, disclosure)
		}
	}

	varianceByKey := make(map[string]MetricVariance, len(analysis.Variance))
	for _, variance := range analysis.Variance {
		varianceByKey[variance.MetricKey] = variance
	}
	for _, tolerance := range protocol.Tolerances {
		variance, exists := varianceByKey[tolerance.MetricKey]
		if !exists {
			return ReproAnalysis{}, fmt.Errorf("tolerance references unknown metric %q", tolerance.MetricKey)
		}
		analysis.ToleranceEvaluations = append(analysis.ToleranceEvaluations, ToleranceEvaluation{
			MetricKey:         tolerance.MetricKey,
			ObservedRange:     variance.Range,
			RegisteredMaximum: tolerance.MaxRange,
			ApprovedBy:        tolerance.ApprovedBy,
			ProtocolVersion:   protocol.Version,
			Passed:            variance.Range <= tolerance.MaxRange,
		})
	}

	return analysis, nil
}

func validateReproProtocol(protocol ReproProtocol) error {
	if strings.TrimSpace(protocol.Version) == "" {
		return fmt.Errorf("repro protocol version is required")
	}
	if protocol.DispersionMethod != "sample_standard_deviation" {
		return fmt.Errorf("approved dispersion method must be sample_standard_deviation")
	}
	if strings.TrimSpace(protocol.ApprovedBy) == "" {
		return fmt.Errorf("repro protocol approved_by is required")
	}
	seen := make(map[string]struct{}, len(protocol.Tolerances))
	for i, tolerance := range protocol.Tolerances {
		if tolerance.MetricKey == "" || !finiteNonNegative(tolerance.MaxRange) || strings.TrimSpace(tolerance.ApprovedBy) == "" {
			return fmt.Errorf("tolerance[%d] metric_key, finite non-negative max_range, and approved_by are required", i)
		}
		if _, duplicate := seen[tolerance.MetricKey]; duplicate {
			return fmt.Errorf("tolerance[%d] metric %q is duplicated", i, tolerance.MetricKey)
		}
		seen[tolerance.MetricKey] = struct{}{}
	}
	return nil
}

func reproIdentity(run IndependentRun) ReproIdentity {
	return ReproIdentity{
		Seed:            run.Seed,
		Build:           run.Report.Build,
		CorpusVersion:   run.Report.CorpusVersion,
		Hardware:        run.Report.Hardware,
		ProtocolVersion: run.Report.ProtocolVersion,
	}
}

func compareReproIdentity(want, got ReproIdentity) error {
	if got.Seed != want.Seed {
		return fmt.Errorf("seed differs")
	}
	if got.Build != want.Build {
		return fmt.Errorf("build differs")
	}
	if got.CorpusVersion != want.CorpusVersion {
		return fmt.Errorf("corpus version differs")
	}
	if got.Hardware != want.Hardware {
		return fmt.Errorf("hardware differs")
	}
	if got.ProtocolVersion != want.ProtocolVersion {
		return fmt.Errorf("protocol version differs")
	}
	return nil
}

func canonicalQueries(queries []QueryReport) []QueryReport {
	canonical := append([]QueryReport(nil), queries...)
	sort.Slice(canonical, func(i, j int) bool {
		left, right := canonical[i], canonical[j]
		if left.QueryID != right.QueryID {
			return left.QueryID < right.QueryID
		}
		if left.ProfileID != right.ProfileID {
			return left.ProfileID < right.ProfileID
		}
		return left.ProfileVersion < right.ProfileVersion
	})
	return canonical
}

func collectMetricSamples(runs []IndependentRun) (map[string][]float64, error) {
	all := make(map[string][]float64)
	for runIndex, run := range runs {
		current := make(map[string]float64)
		for _, profile := range run.Report.Profiles {
			for name, value := range profile.Metrics {
				key := metricKey(profile.ProfileID, profile.ProfileVersion, profile.QueryClass, name)
				if _, duplicate := current[key]; duplicate {
					return nil, fmt.Errorf("run %q contains duplicate metric %q", run.RunID, key)
				}
				current[key] = value
			}
		}
		if runIndex > 0 && len(current) != len(all) {
			return nil, fmt.Errorf("run %q metric set differs from run %q", run.RunID, runs[0].RunID)
		}
		for key, value := range current {
			if runIndex > 0 {
				if _, exists := all[key]; !exists {
					return nil, fmt.Errorf("run %q metric set differs at %q", run.RunID, key)
				}
			}
			all[key] = append(all[key], value)
		}
	}
	return all, nil
}

func metricKey(profileID, profileVersion, queryClass, metricName string) string {
	return profileID + "@" + profileVersion + "/" + queryClass + "/" + metricName
}

func calculateVariance(key string, samples []float64, protocol ReproProtocol) MetricVariance {
	minimum, maximum, total := samples[0], samples[0], 0.0
	for _, sample := range samples {
		minimum = math.Min(minimum, sample)
		maximum = math.Max(maximum, sample)
		total += sample
	}
	mean := total / float64(len(samples))
	squaredDifference := 0.0
	for _, sample := range samples {
		difference := sample - mean
		squaredDifference += difference * difference
	}
	standardDeviation := 0.0
	if len(samples) > 1 {
		standardDeviation = math.Sqrt(squaredDifference / float64(len(samples)-1))
	}
	return MetricVariance{
		MetricKey:               key,
		SampleSize:              len(samples),
		Minimum:                 minimum,
		Maximum:                 maximum,
		Mean:                    mean,
		Range:                   maximum - minimum,
		SampleStandardDeviation: standardDeviation,
		DispersionMethod:        protocol.DispersionMethod,
		DispersionApprovedBy:    protocol.ApprovedBy,
	}
}

// MarshalReproAnalysis emits deterministic JSON while preserving run order.
func MarshalReproAnalysis(analysis ReproAnalysis) ([]byte, error) {
	encoded, err := json.MarshalIndent(analysis, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal reproducibility analysis: %w", err)
	}
	return append(encoded, '\n'), nil
}
