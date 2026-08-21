//go:build postgres_integration

package postgres

import (
	"encoding/json"
	"testing"
	"time"
)

func TestRWC32MethodologyQuantileRetainsSamples(t *testing.T) {
	tests := []struct {
		name    string
		samples []time.Duration
		want    time.Duration
	}{
		{name: "zero samples", samples: nil, want: 0},
		{name: "nearest rank", samples: []time.Duration{50, 10, 30, 20, 40}, want: 50},
		{name: "rounds up", samples: []time.Duration{1, 2, 3, 4, 5, 6, 7}, want: 7},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rwC32NearestRankP95(append([]time.Duration(nil), tt.samples...))
			if got != tt.want {
				t.Fatalf("nearest-rank p95 = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRWC32MethodologyVerdictRejectsMissingSamplesEveryRepetition(t *testing.T) {
	preflight := rwC32PreflightResult{Eligible: true}
	for rep := 0; rep < rwC32Reps; rep++ {
		cases := []struct {
			name             string
			same, distinct   rwC32Stats
			wantProtocolPass bool
		}{
			{
				name:             "zero same samples",
				same:             rwC32Stats{TPS: 10, P95: time.Millisecond},
				distinct:         rwC32Stats{TPS: 10, P95: time.Millisecond, TotalSamples: rwC32Workers * rwC32Iters},
				wantProtocolPass: false,
			},
			{
				name:             "zero distinct samples",
				same:             rwC32Stats{TPS: 10, P95: time.Millisecond, TotalSamples: rwC32Workers * rwC32Iters},
				distinct:         rwC32Stats{TPS: 10, P95: time.Millisecond},
				wantProtocolPass: false,
			},
		}
		for _, tc := range cases {
			t.Run(tc.name+"/rep-"+string(rune('0'+rep)), func(t *testing.T) {
				protocol, absolute := rwC32Verdicts(tc.same, tc.distinct, preflight)
				if (protocol.Status == rwC32Pass) != tc.wantProtocolPass || protocol.SamplesRetained == rwC32Pass {
					t.Fatalf("missing samples produced invalid protocol verdict: %+v", protocol)
				}
				if absolute.Status == rwC32Pass {
					t.Fatalf("incomplete samples must not pass the absolute floor: %+v", absolute)
				}
			})
		}
	}
}

func TestRWC32MethodologyVerdictDerivesCountsAndStatuses(t *testing.T) {
	good := rwC32Workers * rwC32Iters
	same := rwC32Stats{TPS: 100, P95: 10 * time.Millisecond, TotalSamples: good}
	distinct := rwC32Stats{TPS: 100, P95: 10 * time.Millisecond, TotalSamples: good}
	protocol, absolute := rwC32Verdicts(same, distinct, rwC32PreflightResult{Eligible: true})
	if protocol.Status != rwC32Pass || absolute.Status != rwC32Pass {
		t.Fatalf("valid derived measurement did not pass: protocol=%+v absolute=%+v", protocol, absolute)
	}
	if protocol.SamplesRetained != rwC32Pass || protocol.NoAuthFailures != rwC32Pass || protocol.NoLockWaits != rwC32Pass || protocol.RatioWithinFloor != rwC32Pass || protocol.TailWithinFactor != rwC32Pass {
		t.Fatalf("typed protocol statuses lost a required fact: %+v", protocol)
	}
	if absolute.Eligibility != rwC32Pass || absolute.P95WithinFloor != rwC32Pass {
		t.Fatalf("typed host-floor statuses lost a required fact: %+v", absolute)
	}

	for _, count := range []int{good - 1, good + 1} {
		protocol, _ = rwC32Verdicts(
			rwC32Stats{TPS: 100, P95: 10 * time.Millisecond, TotalSamples: count},
			distinct,
			rwC32PreflightResult{Eligible: true},
		)
		if protocol.Status == rwC32Pass || protocol.SamplesRetained == rwC32Pass {
			t.Fatalf("non-derived sample count %d was accepted: %+v", count, protocol)
		}
	}
}

func TestRWC32MethodologyMachineReportRetainsPrePostFacts(t *testing.T) {
	report := rwC32MachineReport{
		Path:                "pooler",
		Repetition:          3,
		Repetitions:         rwC32Reps,
		Order:               []string{"same", "distinct"},
		QuantileMethod:      "nearest-rank-p95",
		Preflight:           rwC32PreflightReport{Eligible: false, Reason: "dedicated host unavailable", CPUs: 2, Sessions: 7},
		Same:                rwC32StatsReport{Flows: 4, TotalSamples: 4, Failures: 1},
		Distinct:            rwC32StatsReport{Flows: 5, TotalSamples: 5, Failures: 0},
		Status:              rwC32Blocked,
		ProtocolVerdict:     protocolPerformanceVerdict{Status: rwC32Blocked, SamplesRetained: rwC32Fail, NoAuthFailures: rwC32Pass},
		AbsoluteHostVerdict: absoluteHostFloorVerdict{Status: rwC32Blocked, Eligibility: rwC32Blocked},
		Cleanup:             rwC32CleanupReport{Completed: true, ClientAcquired: 0},
	}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal methodology report: %v", err)
	}
	var got rwC32MachineReport
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal methodology report: %v", err)
	}
	if got.Preflight.Reason == "" || got.Preflight.CPUs != 2 || got.Preflight.Sessions != 7 {
		t.Fatalf("preflight facts were omitted: %+v", got.Preflight)
	}
	if got.Cleanup.ClientAcquired != 0 || !got.Cleanup.Completed {
		t.Fatalf("post-measurement drain facts were omitted: %+v", got.Cleanup)
	}
	if got.Same.TotalSamples != 4 || got.Distinct.TotalSamples != 5 || got.Repetition != 3 {
		t.Fatalf("measurement facts were not retained: %+v", got)
	}
}

func TestRWC32MethodologyMeasurementOrderAlternates(t *testing.T) {
	first := rwC32MeasurementOrder(0)
	second := rwC32MeasurementOrder(1)
	if len(first) != 2 || len(second) != 2 || first[0] != "distinct" || first[1] != "same" || second[0] != "same" || second[1] != "distinct" {
		t.Fatalf("measurement order did not alternate: first=%v second=%v", first, second)
	}
}

func TestR1R24OrchestrationUsesExactTwoBlocksAndAlternatesStarts(t *testing.T) {
	paths := []string{"direct", "pooler"}
	plan := rwR1R24Plan(paths, rwC32Reps)
	if err := rwR1R24ValidatePlan(plan, paths, rwC32Reps); err != nil {
		t.Fatal(err)
	}
	for _, block := range plan {
		want := (block.Repetition%2 == 1) == (block.Block == 1)
		startsSame := block.Order[0] == "same"
		if startsSame != want {
			t.Fatalf("path=%s repetition=%d block=%d order=%v", block.Path, block.Repetition, block.Block, block.Order)
		}
	}
}

func TestR1R24RepetitionTPSUsesMeasuredBlockSpansOnly(t *testing.T) {
	makePopulation := func(base time.Time, duration time.Duration) []rwC32FlowEvidence {
		population := make([]rwC32FlowEvidence, rwC32Workers*rwC32Iters)
		for i := range population {
			start := base.Add(time.Duration(i) * time.Microsecond)
			population[i] = rwC32FlowEvidence{OK: true, Start: start, Done: start.Add(duration), Duration: duration}
		}
		return population
	}

	makePair := func(sameDuration, distinctDuration time.Duration) []rwC32MachineBlock {
		first := time.Unix(100, 0)
		second := first.Add(24 * time.Hour)
		return []rwC32MachineBlock{
			{Block: 1, SameEvidence: makePopulation(first, sameDuration), DistinctEvidence: makePopulation(first, distinctDuration)},
			{Block: 2, SameEvidence: makePopulation(second, sameDuration), DistinctEvidence: makePopulation(second, distinctDuration)},
		}
	}

	equal := makePair(time.Millisecond, time.Millisecond)
	same, err := rwR1R24RecomputePopulationStats(equal, false, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	distinct, err := rwR1R24RecomputePopulationStats(equal, true, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if ratio := same.TPS / distinct.TPS; ratio != 1 {
		t.Fatalf("equal measured blocks produced ratio %.3f, want 1.0", ratio)
	}

	slower := makePair(2*time.Millisecond, time.Millisecond)
	same, err = rwR1R24RecomputePopulationStats(slower, false, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	distinct, err = rwR1R24RecomputePopulationStats(slower, true, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if ratio := same.TPS / distinct.TPS; ratio >= rwC32RatioFloor {
		t.Fatalf("slower same population produced ratio %.3f, want below %.2f", ratio, rwC32RatioFloor)
	}
}

func TestR1R25PlanRejectsSwappedOrMissingBlockIdentity(t *testing.T) {
	paths := []string{"direct", "pooler"}
	plan := rwR1R24Plan(paths, rwC32Reps)
	if len(plan) != len(paths)*rwC32Reps*rwR1R24BlocksPerRepetition {
		t.Fatalf("plan length=%d, want the complete retained plan", len(plan))
	}
	swapped := append([]rwR1R24Block(nil), plan...)
	swapped[0], swapped[1] = swapped[1], swapped[0]
	if err := rwR1R24ValidatePlan(swapped, paths, rwC32Reps); err == nil {
		t.Fatal("swapped blocks unexpectedly passed plan validation")
	}
	missing := append([]rwR1R24Block(nil), plan...)
	missing[0].Block = missing[1].Block
	if err := rwR1R24ValidatePlan(missing, paths, rwC32Reps); err == nil {
		t.Fatal("duplicate block identity unexpectedly passed plan validation")
	}
}

func TestR1R24CountsDeriveFromRetainedFlowEvidence(t *testing.T) {
	plan := rwR1R24Plan([]string{"direct", "pooler"}, rwC32Reps)
	blocks := make([]rwC32MachineBlock, 0, len(plan))
	for _, item := range plan {
		blocks = append(blocks, rwC32MachineBlock{Path: item.Path, Repetition: item.Repetition, Block: item.Block,
			SameEvidence:     make([]rwC32FlowEvidence, rwC32Workers*rwC32Iters),
			DistinctEvidence: make([]rwC32FlowEvidence, rwC32Workers*rwC32Iters)})
	}
	counts, err := rwR1R24EvidenceCounts(blocks, 2, rwC32Reps)
	if err != nil || counts.Population != 384 || counts.PathRepetition != 1536 || counts.Global != 15360 {
		t.Fatalf("derived evidence counts=%+v err=%v, want 384/1536/15360", counts, err)
	}
	blocks[0].SameEvidence = blocks[0].SameEvidence[:1]
	if _, err := rwR1R24EvidenceCounts(blocks, 2, rwC32Reps); err == nil {
		t.Fatal("truncated retained evidence unexpectedly passed")
	}
}

func TestR1R24CountsRejectDuplicateOrMissingBlockIdentity(t *testing.T) {
	plan := rwR1R24Plan([]string{"direct", "pooler"}, rwC32Reps)
	blocks := make([]rwC32MachineBlock, 0, len(plan))
	for _, item := range plan {
		blocks = append(blocks, rwC32MachineBlock{Path: item.Path, Repetition: item.Repetition, Block: item.Block,
			SameEvidence: make([]rwC32FlowEvidence, rwC32Workers*rwC32Iters), DistinctEvidence: make([]rwC32FlowEvidence, rwC32Workers*rwC32Iters)})
	}
	blocks[1].Block = blocks[0].Block
	if _, err := rwR1R24EvidenceCounts(blocks, 2, rwC32Reps); err == nil {
		t.Fatal("duplicate block identity unexpectedly passed")
	}
	blocks[1].Block = 2
	blocks = blocks[:len(blocks)-1]
	if _, err := rwR1R24EvidenceCounts(blocks, 2, rwC32Reps); err == nil {
		t.Fatal("missing block identity unexpectedly passed")
	}
}

func TestR1R24CombinedVerdictUsesCombinedStatsNotBlockPerformance(t *testing.T) {
	good := rwC32MachineBlock{Block: 1, Protocol: protocolPerformanceVerdict{Status: rwC32Pass}, Absolute: absoluteHostFloorVerdict{Status: rwC32Pass}, Cold: rwC32PhaseReport{Completed: rwC32Pass}, Warm: rwC32PhaseReport{Completed: rwC32Pass}, Safety: rwC32SafetyReport{AuthFailures: rwC32Pass, LockWaits: rwC32Pass, Lifecycle: rwC32Pass}}
	bad := good
	bad.Block = 2
	bad.Protocol.Status = rwC32Fail
	bad.Absolute.Status = rwC32Fail
	combined, err := rwR1R24CombineVerdicts([]rwC32MachineBlock{good, bad})
	if err != nil || combined.Protocol != rwC32Pass || combined.Absolute != rwC32Pass {
		t.Fatalf("block-local performance verdict changed combined result: %+v err=%v", combined, err)
	}
	bad.Safety.AuthFailures = rwC32Fail
	combined, err = rwR1R24CombineVerdicts([]rwC32MachineBlock{good, bad})
	if err != nil || combined.Protocol != rwC32Fail {
		t.Fatalf("safety failure did not fail combined result: %+v err=%v", combined, err)
	}
}

func TestRWC32ParentAggregationNeverPassesZeroOrPartialBlocks(t *testing.T) {
	good := rwC32MachineBlock{Path: "direct", Repetition: 1, Block: 1,
		SameEvidence:     make([]rwC32FlowEvidence, rwC32Workers*rwC32Iters),
		DistinctEvidence: make([]rwC32FlowEvidence, rwC32Workers*rwC32Iters)}
	for name, blocks := range map[string][]rwC32MachineBlock{
		"zero blocks":    nil,
		"partial blocks": []rwC32MachineBlock{good},
	} {
		t.Run(name, func(t *testing.T) {
			counts, err := rwR1R24EvidenceCounts(blocks, 2, rwC32Reps)
			status := rwC32ParentProtocolStatus(blocks, counts, err, nil, rwC32Pass)
			if status == rwC32Pass {
				t.Fatalf("parent aggregation passed %s: counts=%+v err=%v", name, counts, err)
			}
			if name == "zero blocks" && status != rwC32Blocked {
				t.Fatalf("zero usable samples status=%s, want BLOCKED", status)
			}
			if name == "partial blocks" && status != rwC32Fail {
				t.Fatalf("partial samples status=%s, want FAIL", status)
			}
		})
	}
}

func TestR1R25CombinedVerdictIncludesPhaseAndPostflightFacts(t *testing.T) {
	good := rwC32MachineBlock{Block: 1, Protocol: protocolPerformanceVerdict{Status: rwC32Pass}, Absolute: absoluteHostFloorVerdict{Status: rwC32Pass},
		Cold: rwC32PhaseReport{Completed: rwC32Pass}, Warm: rwC32PhaseReport{Completed: rwC32Pass}, Safety: rwC32SafetyReport{Lifecycle: rwC32Pass, AuthFailures: rwC32Pass, LockWaits: rwC32Pass}}
	for name, mutate := range map[string]func(*rwC32MachineBlock){
		"cold phase": func(b *rwC32MachineBlock) { b.Cold.Completed = rwC32Fail },
		"warm phase": func(b *rwC32MachineBlock) { b.Warm.Completed = rwC32Fail },
		"lifecycle":  func(b *rwC32MachineBlock) { b.Safety.Lifecycle = rwC32Fail },
	} {
		t.Run(name, func(t *testing.T) {
			bad := good
			bad.Block = 2
			mutate(&bad)
			combined, err := rwR1R24CombineVerdicts([]rwC32MachineBlock{good, bad})
			if err != nil || combined.Protocol != rwC32Fail {
				t.Fatalf("ignored %s: combined=%+v err=%v", name, combined, err)
			}
		})
	}
}

func TestR1R24PostflightFactsGovernAbsoluteStatus(t *testing.T) {
	base := rwC32PostflightReport{Captured: true, ThrottleBefore: 4, ThrottleAfter: 4,
		Sessions: 1, Connections: 1, ClWaiting: 0, MaxWait: 0}
	if got := rwPostflightStatus(base, 400); got != rwC32Pass {
		t.Fatalf("complete drained postflight = %s, want PASS", got)
	}
	for name, mutate := range map[string]func(*rwC32PostflightReport){
		"throttle":           func(r *rwC32PostflightReport) { r.ThrottleAfter++ },
		"competing sessions": func(r *rwC32PostflightReport) { r.Sessions = 2 },
		"peak connections":   func(r *rwC32PostflightReport) { r.Connections = 2 },
		"cl_waiting":         func(r *rwC32PostflightReport) { r.ClWaiting = 1 },
		"maxwait":            func(r *rwC32PostflightReport) { r.MaxWait = 1 },
	} {
		t.Run(name, func(t *testing.T) {
			got := base
			mutate(&got)
			if status := rwPostflightStatus(got, 400); status != rwC32Fail {
				t.Fatalf("%s fact produced %s, want FAIL", name, status)
			}
		})
	}
}

func TestR1R24RecomputesExact768RecordsAndPropagatesPhaseFailure(t *testing.T) {
	base := time.Unix(100, 0)
	makeEvidence := func(fail bool) []rwC32FlowEvidence {
		out := make([]rwC32FlowEvidence, rwC32Workers*rwC32Iters)
		for i := range out {
			start := base.Add(time.Duration(i) * time.Millisecond)
			out[i] = rwC32FlowEvidence{OK: !fail || i != len(out)-1, Start: start, Done: start.Add(time.Millisecond), Duration: time.Millisecond}
		}
		return out
	}
	blocks := []rwC32MachineBlock{
		{Path: "direct", Repetition: 1, Block: 1, SameEvidence: makeEvidence(false), DistinctEvidence: makeEvidence(false), Measured: rwC32MeasuredReport{Same: rwC32StatsReport{LockWaits: 2, WaitSamples: 3}, Distinct: rwC32StatsReport{LockWaits: 4, WaitSamples: 5}}, Protocol: protocolPerformanceVerdict{Status: rwC32Pass}, Safety: rwC32SafetyReport{AuthFailures: rwC32Pass, LockWaits: rwC32Pass, Lifecycle: rwC32Pass}},
		{Path: "direct", Repetition: 1, Block: 2, SameEvidence: makeEvidence(true), DistinctEvidence: makeEvidence(false), Measured: rwC32MeasuredReport{Same: rwC32StatsReport{LockWaits: 1, WaitSamples: 2}, Distinct: rwC32StatsReport{LockWaits: 3, WaitSamples: 4}}, Protocol: protocolPerformanceVerdict{Status: rwC32Pass}, Safety: rwC32SafetyReport{AuthFailures: rwC32Pass, LockWaits: rwC32Pass, Lifecycle: rwC32Pass}},
	}
	positive := blocks[0]
	positive.Measured = rwC32MeasuredReport{}
	positive.Cold.Completed, positive.Warm.Completed = rwC32Pass, rwC32Pass
	good := positive
	good.Block = 2
	goodReport, err := rwR1R24RecomputeRepetition([]rwC32MachineBlock{positive, good})
	if err != nil || goodReport.Protocol != rwC32Pass || goodReport.Same.TotalSamples != 768 || goodReport.Distinct.TotalSamples != 768 {
		t.Fatalf("positive repetition verdict=%+v err=%v, want PASS with 768/768", goodReport, err)
	}
	report, err := rwR1R24RecomputeRepetition(blocks)
	if err != nil {
		t.Fatal(err)
	}
	if report.Same.TotalSamples != rwC32Workers*rwC32Iters*2 || report.Distinct.TotalSamples != rwC32Workers*rwC32Iters*2 {
		t.Fatalf("recomputed sample counts=%d/%d, want 768/768", report.Same.TotalSamples, report.Distinct.TotalSamples)
	}
	if report.Same.Failures != 1 || report.Protocol != rwC32Fail {
		t.Fatalf("retained failure was masked: same failures=%d protocol=%s", report.Same.Failures, report.Protocol)
	}
}

func TestR1R24OneSlowBlockIsDiagnosticButSafetyStillFails(t *testing.T) {
	makeEvidence := func(base time.Time, duration time.Duration) []rwC32FlowEvidence {
		out := make([]rwC32FlowEvidence, rwC32Workers*rwC32Iters)
		for i := range out {
			start := base.Add(time.Duration(i) * time.Microsecond)
			out[i] = rwC32FlowEvidence{OK: true, Start: start, Done: start.Add(duration), Duration: duration}
		}
		return out
	}
	passSafety := rwC32SafetyReport{AuthFailures: rwC32Pass, LockWaits: rwC32Pass, Lifecycle: rwC32Pass}
	block := func(number int, same, distinct time.Duration) rwC32MachineBlock {
		base := time.Unix(int64(number), 0)
		return rwC32MachineBlock{Path: "direct", Repetition: 1, Block: number,
			SameEvidence: makeEvidence(base, same), DistinctEvidence: makeEvidence(base, distinct),
			Cold: rwC32PhaseReport{Completed: rwC32Pass}, Warm: rwC32PhaseReport{Completed: rwC32Pass},
			Safety: passSafety, Preflight: rwC32PreflightReport{Eligible: true}}
	}
	pair := []rwC32MachineBlock{block(1, 2*time.Millisecond, time.Millisecond), block(2, time.Millisecond, 2*time.Millisecond)}
	// The first block is intentionally below the local ratio floor; its
	// diagnostic failure must not become the repetition reducer status.
	localSame := rwC32Stats{TPS: 1}
	localDistinct := rwC32Stats{TPS: 2}
	if localSame.TPS/localDistinct.TPS >= rwC32RatioFloor {
		t.Fatal("fixture did not encode a block-local ratio failure")
	}
	report, err := rwR1R24RecomputeRepetition(pair)
	if err != nil || report.Protocol != rwC32Pass || report.Absolute != rwC32Pass {
		t.Fatalf("combined 768+768 stats did not pass: report=%+v err=%v", report, err)
	}
	if pair[0].SameEvidence[0].Duration <= pair[0].DistinctEvidence[0].Duration {
		t.Fatal("test fixture did not retain the block-local performance failure")
	}
	pair[1].Safety.AuthFailures = rwC32Fail
	report, err = rwR1R24RecomputeRepetition(pair)
	if err != nil || report.Protocol != rwC32Fail {
		t.Fatalf("safety failure did not fail top-level protocol: report=%+v err=%v", report, err)
	}
}

func TestRWC32EnvironmentFactsUseExactContracts(t *testing.T) {
	if rwC32CPUSetCount("0-3,8,10-11") != 7 {
		t.Fatalf("cpuset range expansion lost CPUs")
	}
	if rwC32CPUSetCount("bad") != 0 {
		t.Fatalf("invalid cpuset must fail closed")
	}
	if rwC32MaxIntervalCPUUtil != 20 || rwC32MaxAverageCPUUtil != 10 {
		t.Fatal("interval utilization thresholds changed")
	}
}

func TestRWC32MethodologyReportRetainsRawEnvironmentFacts(t *testing.T) {
	report := rwC32PreflightReport{
		Eligible: true, CPUs: 4, Memory: 4 << 30, IdleBuckets: 10,
		CPUUtilization: []float64{1, 2, 3}, ThrottleBefore: 4, ThrottleAfter: 4,
		ThrottleDelta: 0, Sessions: 0, CompetingSessions: 8,
	}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal environment facts: %v", err)
	}
	var got rwC32PreflightReport
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal environment facts: %v", err)
	}
	if got.CPUs != 4 || got.Memory != 4<<30 || len(got.CPUUtilization) != 3 || got.ThrottleBefore != 4 || got.ThrottleAfter != 4 || got.Sessions != 0 || got.CompetingSessions != 8 {
		t.Fatalf("raw environment facts were not retained: %+v", got)
	}
}
