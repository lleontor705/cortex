package common

import (
	"encoding/hex"
	"fmt"
	"math"
	"strings"
	"time"
)

const (
	// DirectionHigherIsBetter and DirectionLowerIsBetter are the only supported
	// numeric gate directions.
	DirectionHigherIsBetter = "higher_is_better"
	DirectionLowerIsBetter  = "lower_is_better"

	// Blocking policies distinguish non-negotiable correctness from
	// profile-specific release criteria and advisory observations.
	BlockingUniversal = "universal"
	BlockingProfile   = "profile"
	BlockingAdvisory  = "advisory"

	// Fixed correctness rules are predicates, not baseline-derived thresholds.
	RuleZeroViolations   = "zero_violations"
	RuleExactFilterMatch = "exact_filter_match"

	GateIDZeroIsolationLeakage   = "zero-isolation-leakage"
	GateIDExactFilterCorrectness = "exact-filter-correctness"
)

// GateBaseline binds a protocol to representative, independently preserved
// baseline evidence and its approved corpus and hardware envelope.
type GateBaseline struct {
	CorpusVersion        string           `json:"corpus_version"`
	Hardware             HardwareMetadata `json:"hardware"`
	BaselineReportIDs    []string         `json:"baseline_report_ids"`
	BaselineReportSHA256 []string         `json:"baseline_report_sha256"`
	ReproSHA256          string           `json:"repro_sha256"`
	EvidenceCompletedAt  string           `json:"evidence_completed_at"`
	Representative       bool             `json:"representative"`
	VarianceAnalyzed     bool             `json:"variance_analyzed"`
	CorpusApprovedBy     string           `json:"corpus_approved_by"`
	HardwareApprovedBy   string           `json:"hardware_approved_by"`
}

// GateApproval records reviewer sign-off and its written rationale.
type GateApproval struct {
	Reviewers  []string `json:"reviewers"`
	Rationale  string   `json:"rationale"`
	ApprovedAt string   `json:"approved_at"`
}

// GateDefinition declares one preregistered metric or correctness predicate.
// Threshold remains nil until representative baseline evidence is approved.
type GateDefinition struct {
	ID            string           `json:"id"`
	Metric        MetricDefinition `json:"metric"`
	QueryClass    string           `json:"query_class"`
	SampleSize    int              `json:"sample_size"`
	Blocking      string           `json:"blocking"`
	CriticalClass bool             `json:"critical_class"`
	Threshold     *float64         `json:"threshold,omitempty"`
	Rule          string           `json:"rule,omitempty"`
}

// GateProtocol is the input used to create an immutable gate registry version.
type GateProtocol struct {
	Version                          string           `json:"version"`
	RegisteredBeforeCandidateResults bool             `json:"registered_before_candidate_results"`
	Baseline                         GateBaseline     `json:"baseline"`
	Approval                         GateApproval     `json:"approval"`
	Gates                            []GateDefinition `json:"gates"`
}

// GateRevision is mandatory evidence for changing a protocol after candidate
// results have been observed.
type GateRevision struct {
	Rationale              string `json:"rationale"`
	ApprovedBy             string `json:"approved_by"`
	FreshHeldOutEvaluation bool   `json:"fresh_held_out_evaluation"`
	ReusesDecisionEvidence bool   `json:"reuses_decision_evidence"`
}

// GateObservation contains only the evidence needed to evaluate registered
// gates. Correctness predicates remain independent of aggregate quality gains.
type GateObservation struct {
	IsolationViolations int                `json:"isolation_violations"`
	ExactFilterMatch    bool               `json:"exact_filter_match"`
	Metrics             map[string]float64 `json:"metrics"`
}

// GateDecision records every blocking failure; aggregate improvements cannot
// hide a universal or critical-class regression.
type GateDecision struct {
	Passed           bool     `json:"passed"`
	BlockingFailures []string `json:"blocking_failures"`
}

// GateRegistry is immutable from outside this package. Construction and
// accessors defensively copy all slices and threshold pointers.
type GateRegistry struct {
	version                  string
	gates                    []GateDefinition
	candidateResultsObserved bool
}

// RegisterGateRegistry validates and freezes a versioned preregistered gate
// protocol. Universal correctness gates are installed by Cortex and cannot be
// supplied, removed, or relaxed by callers.
func RegisterGateRegistry(protocol GateProtocol) (GateRegistry, error) {
	if err := validateGateProtocol(protocol); err != nil {
		return GateRegistry{}, err
	}

	gates := cloneGateDefinitions(protocol.Gates)
	gates = append(gates, universalCorrectnessGates()...)
	return GateRegistry{version: protocol.Version, gates: gates}, nil
}

// Version returns the immutable protocol version.
func (r GateRegistry) Version() string { return r.version }

// Gates returns a defensive copy of all registered gates.
func (r GateRegistry) Gates() []GateDefinition { return cloneGateDefinitions(r.gates) }

// WithCandidateResultsObserved returns a sealed copy. It never mutates the
// original registry, so a caller cannot edit a protocol in place after results.
func (r GateRegistry) WithCandidateResultsObserved() GateRegistry {
	sealed := GateRegistry{version: r.version, gates: cloneGateDefinitions(r.gates), candidateResultsObserved: true}
	return sealed
}

// ReviseGateRegistry creates a separately versioned registry. Once candidate
// results exist, written approval and fresh held-out evidence are mandatory.
func ReviseGateRegistry(previous GateRegistry, next GateProtocol, revision GateRevision) (GateRegistry, error) {
	if strings.TrimSpace(next.Version) == "" || next.Version == previous.version {
		return GateRegistry{}, fmt.Errorf("gate revision requires a new protocol version")
	}
	if previous.candidateResultsObserved {
		if strings.TrimSpace(revision.Rationale) == "" {
			return GateRegistry{}, fmt.Errorf("post-result gate revision rationale is required")
		}
		if strings.TrimSpace(revision.ApprovedBy) == "" {
			return GateRegistry{}, fmt.Errorf("post-result gate revision approved_by is required")
		}
		if !revision.FreshHeldOutEvaluation {
			return GateRegistry{}, fmt.Errorf("post-result gate revision requires a fresh held-out evaluation")
		}
		if revision.ReusesDecisionEvidence {
			return GateRegistry{}, fmt.Errorf("post-result gate revision must not reuse held-out decision evidence")
		}
	}
	return RegisterGateRegistry(next)
}

// Evaluate applies universal predicates before all numeric gates. Every
// numeric release gate must have an explicitly registered threshold.
func (r GateRegistry) Evaluate(observation GateObservation) (GateDecision, error) {
	if observation.IsolationViolations < 0 {
		return GateDecision{}, fmt.Errorf("isolation_violations must be non-negative")
	}
	decision := GateDecision{Passed: true, BlockingFailures: []string{}}
	for _, gate := range r.gates {
		failed := false
		switch gate.Rule {
		case RuleZeroViolations:
			failed = observation.IsolationViolations != 0
		case RuleExactFilterMatch:
			failed = !observation.ExactFilterMatch
		default:
			if gate.Threshold == nil {
				return GateDecision{}, fmt.Errorf("gate %q threshold is unset", gate.ID)
			}
			value, exists := observation.Metrics[gate.ID]
			if !exists || !finite(value) {
				return GateDecision{}, fmt.Errorf("gate %q requires a finite observed metric", gate.ID)
			}
			if gate.Metric.Direction == DirectionHigherIsBetter {
				failed = value < *gate.Threshold
			} else {
				failed = value > *gate.Threshold
			}
		}
		if failed && gate.Blocking != BlockingAdvisory {
			decision.Passed = false
			decision.BlockingFailures = append(decision.BlockingFailures, gate.ID)
		}
	}
	return decision, nil
}

func validateGateProtocol(protocol GateProtocol) error {
	if strings.TrimSpace(protocol.Version) == "" {
		return fmt.Errorf("gate protocol version is required")
	}
	if !protocol.RegisteredBeforeCandidateResults {
		return fmt.Errorf("gate protocol must be registered before candidate results are observed")
	}
	if len(protocol.Gates) == 0 {
		return fmt.Errorf("at least one profile quality or resource gate is required")
	}

	seen := make(map[string]struct{}, len(protocol.Gates)+2)
	for i, gate := range protocol.Gates {
		if err := validateCallerGate(gate); err != nil {
			return fmt.Errorf("gates[%d]: %w", i, err)
		}
		if gate.ID == GateIDZeroIsolationLeakage || gate.ID == GateIDExactFilterCorrectness {
			return fmt.Errorf("gates[%d] attempts to redefine immutable universal gate %q", i, gate.ID)
		}
		if _, duplicate := seen[gate.ID]; duplicate {
			return fmt.Errorf("gates[%d].id %q is duplicated", i, gate.ID)
		}
		seen[gate.ID] = struct{}{}
		if gate.Threshold != nil {
			if err := validateNumericThresholdEvidence(protocol); err != nil {
				return fmt.Errorf("gates[%d]: numeric threshold: %w", i, err)
			}
		}
	}

	if err := validateGateBaseline(protocol.Baseline); err != nil {
		return err
	}
	if err := validateGateApproval(protocol.Approval); err != nil {
		return err
	}
	return nil
}

func validateCallerGate(gate GateDefinition) error {
	if strings.TrimSpace(gate.ID) == "" {
		return fmt.Errorf("id is required")
	}
	if gate.Rule != "" {
		return fmt.Errorf("rule is reserved for immutable universal gates")
	}
	if gate.Metric.Name == "" || gate.Metric.Unit == "" || gate.Metric.Description == "" {
		return fmt.Errorf("metric name, unit, and description are required")
	}
	if gate.Metric.Direction != DirectionHigherIsBetter && gate.Metric.Direction != DirectionLowerIsBetter {
		return fmt.Errorf("metric direction must be %q or %q", DirectionHigherIsBetter, DirectionLowerIsBetter)
	}
	if strings.TrimSpace(gate.QueryClass) == "" {
		return fmt.Errorf("query_class is required")
	}
	if gate.SampleSize <= 0 {
		return fmt.Errorf("positive sample_size is required")
	}
	if gate.Blocking != BlockingProfile && gate.Blocking != BlockingAdvisory {
		return fmt.Errorf("blocking policy must be %q or %q", BlockingProfile, BlockingAdvisory)
	}
	if gate.CriticalClass && gate.Blocking != BlockingProfile {
		return fmt.Errorf("critical class gate must use profile blocking policy")
	}
	if gate.Threshold != nil && (math.IsNaN(*gate.Threshold) || math.IsInf(*gate.Threshold, 0)) {
		return fmt.Errorf("threshold must be finite")
	}
	return nil
}

func validateGateBaseline(baseline GateBaseline) error {
	if strings.TrimSpace(baseline.CorpusVersion) == "" {
		return fmt.Errorf("baseline corpus_version is required")
	}
	if !validHardwareEnvelope(baseline.Hardware) {
		return fmt.Errorf("baseline hardware envelope is incomplete")
	}
	if len(baseline.BaselineReportIDs) != 2 {
		return fmt.Errorf("exactly two independent baseline_report_ids are required")
	}
	if len(baseline.BaselineReportSHA256) != len(baseline.BaselineReportIDs) {
		return fmt.Errorf("baseline requires one SHA-256 hash per report ID")
	}
	seenReportIDs := make(map[string]struct{}, len(baseline.BaselineReportIDs))
	for i, id := range baseline.BaselineReportIDs {
		if strings.TrimSpace(id) == "" {
			return fmt.Errorf("baseline_report_ids[%d] is empty", i)
		}
		if _, exists := seenReportIDs[id]; exists {
			return fmt.Errorf("baseline_report_ids[%d] %q is duplicated", i, id)
		}
		seenReportIDs[id] = struct{}{}
		if !validSHA256(baseline.BaselineReportSHA256[i]) {
			return fmt.Errorf("baseline_report_sha256[%d] must be a SHA-256 hash", i)
		}
	}
	if !validSHA256(baseline.ReproSHA256) {
		return fmt.Errorf("baseline repro_sha256 must be a SHA-256 hash")
	}
	if _, err := time.Parse(time.RFC3339, baseline.EvidenceCompletedAt); err != nil {
		return fmt.Errorf("baseline evidence_completed_at must be RFC3339: %w", err)
	}
	if !baseline.Representative {
		return fmt.Errorf("representative baseline evidence is required")
	}
	if !baseline.VarianceAnalyzed {
		return fmt.Errorf("baseline variance analysis is required")
	}
	if strings.TrimSpace(baseline.CorpusApprovedBy) == "" || strings.TrimSpace(baseline.HardwareApprovedBy) == "" {
		return fmt.Errorf("baseline corpus and hardware approvals are required")
	}
	return nil
}

func validateNumericThresholdEvidence(protocol GateProtocol) error {
	if err := validateGateBaseline(protocol.Baseline); err != nil {
		return fmt.Errorf("approved representative baseline evidence is required: %w", err)
	}
	if err := validateGateApproval(protocol.Approval); err != nil {
		return fmt.Errorf("reviewer sign-off is required: %w", err)
	}
	evidenceCompletedAt, evidenceErr := time.Parse(time.RFC3339, protocol.Baseline.EvidenceCompletedAt)
	approvedAt, approvalErr := time.Parse(time.RFC3339, protocol.Approval.ApprovedAt)
	if evidenceErr != nil || approvalErr != nil || !approvedAt.After(evidenceCompletedAt) {
		return fmt.Errorf("numeric thresholds require reviewer approval after baseline evidence completion")
	}
	return nil
}

func validateGateApproval(approval GateApproval) error {
	if len(approval.Reviewers) == 0 {
		return fmt.Errorf("gate approval reviewers are required")
	}
	for i, reviewer := range approval.Reviewers {
		if strings.TrimSpace(reviewer) == "" {
			return fmt.Errorf("gate approval reviewers[%d] is empty", i)
		}
	}
	if strings.TrimSpace(approval.Rationale) == "" {
		return fmt.Errorf("gate approval rationale is required")
	}
	if _, err := time.Parse(time.RFC3339, approval.ApprovedAt); err != nil {
		return fmt.Errorf("gate approval approved_at must be RFC3339: %w", err)
	}
	return nil
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}

func validHardwareEnvelope(hardware HardwareMetadata) bool {
	return strings.TrimSpace(hardware.ProfileID) != "" && strings.TrimSpace(hardware.OS) != "" &&
		strings.TrimSpace(hardware.Arch) != "" && strings.TrimSpace(hardware.CPU) != "" && hardware.MemoryMB > 0
}

func universalCorrectnessGates() []GateDefinition {
	return []GateDefinition{
		{
			ID:         GateIDZeroIsolationLeakage,
			Metric:     MetricDefinition{Name: "isolation_violations", Unit: "count", Direction: DirectionLowerIsBetter, Description: "Unauthorized or ineligible returned IDs."},
			QueryClass: "all", SampleSize: 1, Blocking: BlockingUniversal, CriticalClass: true, Rule: RuleZeroViolations,
		},
		{
			ID:         GateIDExactFilterCorrectness,
			Metric:     MetricDefinition{Name: "filter_eligibility_exact", Unit: "boolean", Direction: DirectionHigherIsBetter, Description: "Exact equality with the authoritative eligible stable-ID set."},
			QueryClass: "all", SampleSize: 1, Blocking: BlockingUniversal, CriticalClass: true, Rule: RuleExactFilterMatch,
		},
	}
}

func cloneGateDefinitions(source []GateDefinition) []GateDefinition {
	clone := make([]GateDefinition, len(source))
	for i, gate := range source {
		clone[i] = gate
		if gate.Threshold != nil {
			threshold := *gate.Threshold
			clone[i].Threshold = &threshold
		}
	}
	return clone
}
