package common

import (
	"strings"
	"testing"
)

func TestGateRegistryRequiresPreregisteredProtocolMetadata(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*GateProtocol)
		wantErr string
	}{
		{name: "protocol version", mutate: func(p *GateProtocol) { p.Version = "" }, wantErr: "version"},
		{name: "pre-result registration", mutate: func(p *GateProtocol) { p.RegisteredBeforeCandidateResults = false }, wantErr: "before candidate results"},
		{name: "metric definition", mutate: func(p *GateProtocol) { p.Gates[0].Metric.Description = "" }, wantErr: "description"},
		{name: "metric direction", mutate: func(p *GateProtocol) { p.Gates[0].Metric.Direction = "" }, wantErr: "direction"},
		{name: "sample size", mutate: func(p *GateProtocol) { p.Gates[0].SampleSize = 0 }, wantErr: "sample_size"},
		{name: "corpus version", mutate: func(p *GateProtocol) { p.Baseline.CorpusVersion = "" }, wantErr: "corpus_version"},
		{name: "hardware envelope", mutate: func(p *GateProtocol) { p.Baseline.Hardware.ProfileID = "" }, wantErr: "hardware"},
		{name: "variance analysis", mutate: func(p *GateProtocol) { p.Baseline.VarianceAnalyzed = false }, wantErr: "variance"},
		{name: "report hash", mutate: func(p *GateProtocol) { p.Baseline.BaselineReportSHA256[0] = "" }, wantErr: "report"},
		{name: "repro hash", mutate: func(p *GateProtocol) { p.Baseline.ReproSHA256 = "" }, wantErr: "repro"},
		{name: "named reviewers", mutate: func(p *GateProtocol) { p.Approval.Reviewers = nil }, wantErr: "reviewers"},
		{name: "reviewer rationale", mutate: func(p *GateProtocol) { p.Approval.Rationale = "" }, wantErr: "rationale"},
		{name: "approval date", mutate: func(p *GateProtocol) { p.Approval.ApprovedAt = "" }, wantErr: "approved_at"},
		{name: "blocking policy", mutate: func(p *GateProtocol) { p.Gates[0].Blocking = "" }, wantErr: "blocking"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			protocol := validGateProtocol()
			tt.mutate(&protocol)

			_, err := RegisterGateRegistry(protocol)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("RegisterGateRegistry() error = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestGateRegistryRejectsInvalidEvidenceBindings(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*GateProtocol)
		wantErr string
	}{
		{name: "report ID and hash count differ", mutate: func(p *GateProtocol) {
			p.Baseline.BaselineReportSHA256 = p.Baseline.BaselineReportSHA256[:1]
		}, wantErr: "one SHA-256 hash per report ID"},
		{name: "requires exactly two reports", mutate: func(p *GateProtocol) {
			p.Baseline.BaselineReportIDs = append(p.Baseline.BaselineReportIDs, "baseline-3")
			p.Baseline.BaselineReportSHA256 = append(p.Baseline.BaselineReportSHA256, strings.Repeat("d", 64))
		}, wantErr: "exactly two"},
		{name: "duplicate report ID", mutate: func(p *GateProtocol) {
			p.Baseline.BaselineReportIDs[1] = p.Baseline.BaselineReportIDs[0]
		}, wantErr: "duplicated"},
		{name: "report hash is not SHA-256", mutate: func(p *GateProtocol) {
			p.Baseline.BaselineReportSHA256[0] = "not-a-sha256"
		}, wantErr: "SHA-256"},
		{name: "repro hash is not SHA-256", mutate: func(p *GateProtocol) {
			p.Baseline.ReproSHA256 = "not-a-sha256"
		}, wantErr: "SHA-256"},
		{name: "blank named reviewer", mutate: func(p *GateProtocol) {
			p.Approval.Reviewers = []string{"data-reviewer", " "}
		}, wantErr: "reviewers"},
		{name: "invalid approval date", mutate: func(p *GateProtocol) {
			p.Approval.ApprovedAt = "after the run"
		}, wantErr: "approved_at"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			protocol := validGateProtocol()
			tt.mutate(&protocol)

			_, err := RegisterGateRegistry(protocol)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("RegisterGateRegistry() error = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestGateRegistryInstallsImmutableUniversalCorrectnessGates(t *testing.T) {
	registry, err := RegisterGateRegistry(validGateProtocol())
	if err != nil {
		t.Fatalf("RegisterGateRegistry() error = %v", err)
	}

	gates := registry.Gates()
	leakage, ok := findGate(gates, GateIDZeroIsolationLeakage)
	if !ok || leakage.Blocking != BlockingUniversal || leakage.Rule != RuleZeroViolations || leakage.Threshold != nil {
		t.Fatalf("zero-leakage gate = %#v, found %v, want universal immutable zero-violation rule", leakage, ok)
	}
	filter, ok := findGate(gates, GateIDExactFilterCorrectness)
	if !ok || filter.Blocking != BlockingUniversal || filter.Rule != RuleExactFilterMatch || filter.Threshold != nil {
		t.Fatalf("filter-correctness gate = %#v, found %v, want universal immutable exact-match rule", filter, ok)
	}

	// Accessors must return defensive copies: callers cannot relax registered gates.
	gates[0].Blocking = BlockingAdvisory
	gates[0].Metric.Description = "relaxed"
	again := registry.Gates()
	if again[0].Blocking == BlockingAdvisory || again[0].Metric.Description == "relaxed" {
		t.Fatalf("registry gates changed through accessor: %#v", again[0])
	}
}

func TestGateRegistryRejectsNumericThresholdWithoutBaselineApproval(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*GateProtocol)
	}{
		{name: "missing representative baseline", mutate: func(p *GateProtocol) { p.Baseline.Representative = false }},
		{name: "missing variance analysis", mutate: func(p *GateProtocol) { p.Baseline.VarianceAnalyzed = false }},
		{name: "missing hardware approval", mutate: func(p *GateProtocol) { p.Baseline.HardwareApprovedBy = "" }},
		{name: "missing corpus approval", mutate: func(p *GateProtocol) { p.Baseline.CorpusApprovedBy = "" }},
		{name: "missing reviewer sign-off", mutate: func(p *GateProtocol) { p.Approval.Reviewers = nil }},
		{name: "approval predates evidence", mutate: func(p *GateProtocol) { p.Approval.ApprovedAt = "2026-07-21T23:59:59Z" }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			protocol := validGateProtocol()
			threshold := 0.8
			protocol.Gates[0].Threshold = &threshold
			tt.mutate(&protocol)

			_, err := RegisterGateRegistry(protocol)
			if err == nil || !strings.Contains(err.Error(), "numeric threshold") {
				t.Fatalf("RegisterGateRegistry() error = %v, want numeric threshold baseline/sign-off rejection", err)
			}
		})
	}
}

func TestGateRegistryDefensivelyCopiesRegisteredProtocol(t *testing.T) {
	protocol := validGateProtocol()
	threshold := 0.8
	protocol.Gates[0].Threshold = &threshold

	registry, err := RegisterGateRegistry(protocol)
	if err != nil {
		t.Fatalf("RegisterGateRegistry() error = %v", err)
	}
	protocol.Gates[0].ID = GateIDZeroIsolationLeakage
	protocol.Gates[0].Blocking = BlockingAdvisory
	*protocol.Gates[0].Threshold = 0

	gate, ok := findGate(registry.Gates(), "recall-critical-class")
	if !ok || gate.Blocking != BlockingProfile || gate.Threshold == nil || *gate.Threshold != 0.8 {
		t.Fatalf("registered gate mutated through input protocol: %#v, found %v", gate, ok)
	}
}

func TestGateRegistryRejectsUniversalGateRedefinition(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*GateProtocol)
	}{
		{name: "zero isolation violations", mutate: func(p *GateProtocol) { p.Gates[0].ID = GateIDZeroIsolationLeakage }},
		{name: "exact supported-filter eligible IDs", mutate: func(p *GateProtocol) { p.Gates[0].ID = GateIDExactFilterCorrectness }},
		{name: "reserved universal predicate", mutate: func(p *GateProtocol) { p.Gates[0].Rule = RuleZeroViolations }},
		{name: "universal blocking policy", mutate: func(p *GateProtocol) { p.Gates[0].Blocking = BlockingUniversal }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			protocol := validGateProtocol()
			tt.mutate(&protocol)

			if _, err := RegisterGateRegistry(protocol); err == nil {
				t.Fatal("RegisterGateRegistry() error = nil, want immutable universal-gate rejection")
			}
		})
	}
}

func TestGateRegistryLeavesBaselineDependentThresholdsUnset(t *testing.T) {
	registry, err := RegisterGateRegistry(validGateProtocol())
	if err != nil {
		t.Fatalf("RegisterGateRegistry() error = %v", err)
	}

	quality, ok := findGate(registry.Gates(), "recall-critical-class")
	if !ok {
		t.Fatal("quality gate missing")
	}
	if quality.Threshold != nil {
		t.Fatalf("quality threshold = %v, want unset until explicitly selected from baseline evidence", *quality.Threshold)
	}
}

func TestGateRegistryBlocksCorrectnessAndCriticalClassRegression(t *testing.T) {
	protocol := validGateProtocol()
	criticalThreshold, aggregateThreshold := 0.8, 0.7
	protocol.Gates[0].Threshold = &criticalThreshold
	protocol.Gates = append(protocol.Gates, GateDefinition{
		ID:         "aggregate-recall",
		Metric:     MetricDefinition{Name: "recall_at_10", Unit: "ratio", Direction: DirectionHigherIsBetter, Description: "Aggregate retrieval recall."},
		QueryClass: "aggregate", SampleSize: 40, Blocking: BlockingProfile, Threshold: &aggregateThreshold,
	})
	registry, err := RegisterGateRegistry(protocol)
	if err != nil {
		t.Fatalf("RegisterGateRegistry() error = %v", err)
	}

	tests := []struct {
		name        string
		observation GateObservation
		wantFailure string
	}{
		{
			name: "zero leakage is blocking",
			observation: GateObservation{IsolationViolations: 1, ExactFilterMatch: true,
				Metrics: map[string]float64{"recall-critical-class": 0.9, "aggregate-recall": 0.9}},
			wantFailure: GateIDZeroIsolationLeakage,
		},
		{
			name: "exact filter correctness is blocking",
			observation: GateObservation{IsolationViolations: 0, ExactFilterMatch: false,
				Metrics: map[string]float64{"recall-critical-class": 0.9, "aggregate-recall": 0.9}},
			wantFailure: GateIDExactFilterCorrectness,
		},
		{
			name: "critical class regression blocks aggregate gain",
			observation: GateObservation{IsolationViolations: 0, ExactFilterMatch: true,
				Metrics: map[string]float64{"recall-critical-class": 0.79, "aggregate-recall": 0.95}},
			wantFailure: "recall-critical-class",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision, err := registry.Evaluate(tt.observation)
			if err != nil {
				t.Fatalf("Evaluate() error = %v", err)
			}
			if decision.Passed || !containsString(decision.BlockingFailures, tt.wantFailure) {
				t.Fatalf("decision = %#v, want blocking failure %q", decision, tt.wantFailure)
			}
		})
	}
}

func TestGateRegistryForbidsInPlacePostResultRelaxation(t *testing.T) {
	protocol := validGateProtocol()
	threshold := 0.8
	protocol.Gates[0].Threshold = &threshold
	registry, err := RegisterGateRegistry(protocol)
	if err != nil {
		t.Fatalf("RegisterGateRegistry() error = %v", err)
	}
	observed := registry.WithCandidateResultsObserved()

	relaxed := validGateProtocol()
	relaxedThreshold := 0.7
	relaxed.Gates[0].Threshold = &relaxedThreshold
	_, err = ReviseGateRegistry(observed, relaxed, GateRevision{
		Rationale: "candidate missed the gate", ApprovedBy: "retrieval-reviewers", FreshHeldOutEvaluation: true,
	})
	if err == nil || !strings.Contains(err.Error(), "new protocol version") {
		t.Fatalf("ReviseGateRegistry(same version) error = %v, want in-place edit rejection", err)
	}
}

func TestGateRegistryPostResultRevisionRequiresFreshHeldOutEvaluation(t *testing.T) {
	registry, err := RegisterGateRegistry(validGateProtocol())
	if err != nil {
		t.Fatalf("RegisterGateRegistry() error = %v", err)
	}
	observed := registry.WithCandidateResultsObserved()
	next := validGateProtocol()
	next.Version = "retrieval-gates-v2"

	tests := []struct {
		name     string
		revision GateRevision
		wantErr  string
	}{
		{name: "written rationale", revision: GateRevision{ApprovedBy: "reviewers", FreshHeldOutEvaluation: true}, wantErr: "rationale"},
		{name: "approval", revision: GateRevision{Rationale: "new evidence", FreshHeldOutEvaluation: true}, wantErr: "approved_by"},
		{name: "fresh held-out evaluation", revision: GateRevision{Rationale: "new evidence", ApprovedBy: "reviewers"}, wantErr: "fresh held-out"},
		{name: "decision evidence reuse", revision: GateRevision{Rationale: "new evidence", ApprovedBy: "reviewers", FreshHeldOutEvaluation: true, ReusesDecisionEvidence: true}, wantErr: "must not reuse"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ReviseGateRegistry(observed, next, tt.revision)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("ReviseGateRegistry() error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func validGateProtocol() GateProtocol {
	return GateProtocol{
		Version: "retrieval-gates-v1", RegisteredBeforeCandidateResults: true,
		Baseline: GateBaseline{
			CorpusVersion:     "cortex-native-v1",
			Hardware:          HardwareMetadata{ProfileID: "ci-linux-amd64", OS: "linux", Arch: "amd64", CPU: "representative-cpu", MemoryMB: 16384},
			BaselineReportIDs: []string{"baseline-1", "baseline-2"}, Representative: true, VarianceAnalyzed: true,
			BaselineReportSHA256: []string{strings.Repeat("a", 64), strings.Repeat("b", 64)},
			ReproSHA256:          strings.Repeat("c", 64), EvidenceCompletedAt: "2026-07-22T00:00:00Z",
			CorpusApprovedBy: "retrieval-reviewers", HardwareApprovedBy: "retrieval-reviewers",
		},
		Approval: GateApproval{
			Reviewers: []string{"data-privacy-reviewer", "performance-reviewer"},
			Rationale: "baseline evidence and invariants reviewed", ApprovedAt: "2026-07-22T01:00:00Z",
		},
		Gates: []GateDefinition{{
			ID:         "recall-critical-class",
			Metric:     MetricDefinition{Name: "recall_at_10", Unit: "ratio", Direction: DirectionHigherIsBetter, Description: "Recall for the preregistered critical query class."},
			QueryClass: "critical-isolation", SampleSize: 20, Blocking: BlockingProfile, CriticalClass: true,
		}},
	}
}

func findGate(gates []GateDefinition, id string) (GateDefinition, bool) {
	for _, gate := range gates {
		if gate.ID == id {
			return gate, true
		}
	}
	return GateDefinition{}, false
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
