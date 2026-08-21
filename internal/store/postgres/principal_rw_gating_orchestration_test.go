//go:build postgres_integration

package postgres

import (
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	rwR1R24BlocksPerRepetition = 2
	rwR1R24PopulationsPerBlock = 2
)

// rwR1R24Block describes one retained SD->DS or DS->SD measurement block.
// Population names are semantic labels; they must not be swapped when the
// starting order alternates.
type rwR1R24Block struct {
	Path       string
	Repetition int
	Block      int
	Order      []string
}

// rwR1R24Identity is the stable key for retained evidence.  Block is part of
// the identity: two blocks in one repetition are not interchangeable.
type rwR1R24Identity struct {
	Path, Repetition, Block string
}

type rwR1R24Counts struct {
	Population     int
	PathRepetition int
	Global         int
}

type rwC32PathDescriptor struct {
	name string
}

func rwC32PoolForPath(h *rwHarness, path string) *pgxpool.Pool {
	switch path {
	case "direct":
		if h == nil {
			return nil
		}
		return h.direct
	case "pooler":
		if h == nil {
			return nil
		}
		return h.pooler
	default:
		return nil
	}
}

func TestPrincipalRWPathPoolSelection(t *testing.T) {
	direct := &pgxpool.Pool{}
	pooler := &pgxpool.Pool{}
	h := &rwHarness{direct: direct, pooler: pooler}
	if got := rwC32PoolForPath(h, "direct"); got != direct {
		t.Fatalf("direct path selected %p, want %p", got, direct)
	}
	if got := rwC32PoolForPath(h, "pooler"); got != pooler {
		t.Fatalf("pooler path selected %p, want %p", got, pooler)
	}
}

func rwR1R24EvidenceCounts(blocks []rwC32MachineBlock, paths, repetitions int) (rwR1R24Counts, error) {
	counts := rwR1R24Counts{}
	perPathRepetition := make(map[string]int)
	identities := make(map[rwR1R24Identity]struct{}, len(blocks))
	validPaths := make(map[string]struct{}, paths)
	for _, path := range []string{"direct", "pooler"} {
		validPaths[path] = struct{}{}
	}
	for _, block := range blocks {
		if _, ok := validPaths[block.Path]; !ok {
			return counts, fmt.Errorf("unknown path %q", block.Path)
		}
		if block.Repetition < 1 || block.Repetition > repetitions || block.Block < 1 || block.Block > rwR1R24BlocksPerRepetition {
			return counts, fmt.Errorf("invalid block identity path=%s repetition=%d block=%d", block.Path, block.Repetition, block.Block)
		}
		identity := rwR1R24Identity{Path: block.Path, Repetition: fmt.Sprintf("%d", block.Repetition), Block: fmt.Sprintf("%d", block.Block)}
		if _, exists := identities[identity]; exists {
			return counts, fmt.Errorf("duplicate block identity %s/%s/%s", identity.Path, identity.Repetition, identity.Block)
		}
		identities[identity] = struct{}{}
		if len(block.SameEvidence) != rwC32Workers*rwC32Iters || len(block.DistinctEvidence) != rwC32Workers*rwC32Iters {
			return counts, fmt.Errorf("%s repetition %d retained same=%d distinct=%d, want %d each", block.Path, block.Repetition, len(block.SameEvidence), len(block.DistinctEvidence), rwC32Workers*rwC32Iters)
		}
		counts.Population = len(block.SameEvidence)
		perPathRepetition[fmt.Sprintf("%s/%d", block.Path, block.Repetition)] += len(block.SameEvidence) + len(block.DistinctEvidence)
		counts.Global += len(block.SameEvidence) + len(block.DistinctEvidence)
	}
	wantBlocks := paths * repetitions * rwR1R24BlocksPerRepetition
	if len(blocks) != wantBlocks {
		return counts, fmt.Errorf("retained blocks=%d, want %d", len(blocks), wantBlocks)
	}
	wantPathRepetition := rwC32Workers * rwC32Iters * rwR1R24PopulationsPerBlock * rwR1R24BlocksPerRepetition
	for path := range validPaths {
		for repetition := 1; repetition <= repetitions; repetition++ {
			key := fmt.Sprintf("%s/%d", path, repetition)
			if got := perPathRepetition[key]; got != wantPathRepetition {
				return counts, fmt.Errorf("%s path-repetition samples=%d, want %d", key, got, wantPathRepetition)
			}
		}
	}
	counts.PathRepetition = wantPathRepetition
	if counts.Global != paths*repetitions*rwR1R24BlocksPerRepetition*rwR1R24PopulationsPerBlock*rwC32Workers*rwC32Iters {
		return counts, fmt.Errorf("global samples=%d, want %d", counts.Global, paths*repetitions*rwR1R24BlocksPerRepetition*rwR1R24PopulationsPerBlock*rwC32Workers*rwC32Iters)
	}
	return counts, nil
}

// rwR1R24CombinedVerdict is computed once from both blocks, after both
// populations have been retained.  A block-local PASS must never mask a
// failing companion block.
type rwR1R24CombinedVerdict struct {
	Protocol rwC32Status
	Absolute rwC32Status
}

// rwR1R24RepetitionReport is the protocol oracle for one path/repetition. It
// is derived from both retained blocks, never from their already-normalized
// verdicts, so one failing block cannot be hidden by its companion.
type rwR1R24RepetitionReport struct {
	Path       string
	Repetition int
	Same       rwC32StatsReport
	Distinct   rwC32StatsReport
	Protocol   rwC32Status
	Absolute   rwC32Status
}

// rwR1R24RecomputePopulationStats aggregates a population across the two
// measured blocks without treating the interval between blocks (or the
// companion population's measurement) as work time.  Each block's contiguous
// span is the denominator for that block, while operations and samples are
// still retained across both blocks.
func rwR1R24RecomputePopulationStats(blocks []rwC32MachineBlock, distinct bool, waits, waitSamples int64) (rwC32Stats, error) {
	if len(blocks) != rwR1R24BlocksPerRepetition {
		return rwC32Stats{}, fmt.Errorf("population aggregation requires two blocks")
	}
	var evidence []rwC32FlowEvidence
	var elapsed time.Duration
	firstErr := ""
	failures := 0
	durations := make([]time.Duration, 0, rwC32Workers*rwC32Iters*rwR1R24BlocksPerRepetition)
	for _, block := range blocks {
		population := block.SameEvidence
		if distinct {
			population = block.DistinctEvidence
		}
		if len(population) != rwC32Workers*rwC32Iters {
			return rwC32Stats{}, fmt.Errorf("retained population samples=%d, want %d", len(population), rwC32Workers*rwC32Iters)
		}
		var start, done time.Time
		for _, item := range population {
			if item.Done.Before(item.Start) || item.Start.IsZero() || item.Done.IsZero() {
				return rwC32Stats{}, fmt.Errorf("retained flow has invalid timing")
			}
			if start.IsZero() || item.Start.Before(start) {
				start = item.Start
			}
			if done.IsZero() || item.Done.After(done) {
				done = item.Done
			}
			durations = append(durations, item.Duration)
			if !item.OK {
				failures++
				if firstErr == "" {
					firstErr = item.Err
				}
			}
		}
		blockElapsed := done.Sub(start)
		if blockElapsed <= 0 {
			return rwC32Stats{}, fmt.Errorf("retained block elapsed time is non-positive")
		}
		elapsed += blockElapsed
		evidence = append(evidence, population...)
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	return rwC32Stats{Flows: len(evidence), TotalSamples: len(evidence), Failures: failures,
		FirstErr: firstErr, TPS: float64(len(evidence)) / elapsed.Seconds(),
		P50: durations[len(durations)/2], P95: rwR1R24NearestRankP95(durations),
		LockWaits: waits, Samples: waitSamples, FlowEvidence: evidence}, nil
}

func rwR1R24NearestRankP95(samples []time.Duration) time.Duration {
	if len(samples) == 0 {
		return 0
	}
	rank := (len(samples)*95 + 99) / 100
	return samples[rank-1]
}

func rwR1R24RecomputeRepetition(pair []rwC32MachineBlock) (rwR1R24RepetitionReport, error) {
	if len(pair) != rwR1R24BlocksPerRepetition {
		return rwR1R24RepetitionReport{}, fmt.Errorf("repetition requires two blocks")
	}
	combined, err := rwR1R24CombineVerdicts(pair)
	if err != nil {
		return rwR1R24RepetitionReport{}, err
	}
	var sameWaits, distinctWaits, sameWaitSamples, distinctWaitSamples int64
	for _, block := range pair {
		sameWaits += block.Measured.Same.LockWaits
		distinctWaits += block.Measured.Distinct.LockWaits
		sameWaitSamples += block.Measured.Same.WaitSamples
		distinctWaitSamples += block.Measured.Distinct.WaitSamples
		if block.Cold.Failures != 0 || block.Warm.Failures != 0 || block.Cold.Completed != rwC32Pass || block.Warm.Completed != rwC32Pass || block.Safety.Lifecycle != rwC32Pass || block.Safety.AuthFailures != rwC32Pass || block.Safety.LockWaits != rwC32Pass {
			combined.Protocol = rwC32Fail
		}
	}
	sameStats, err := rwR1R24RecomputePopulationStats(pair, false, sameWaits, sameWaitSamples)
	if err != nil {
		return rwR1R24RepetitionReport{}, fmt.Errorf("same: %w", err)
	}
	distinctStats, err := rwR1R24RecomputePopulationStats(pair, true, distinctWaits, distinctWaitSamples)
	if err != nil {
		return rwR1R24RepetitionReport{}, fmt.Errorf("distinct: %w", err)
	}
	preflight := rwC32PreflightResult{Eligible: pair[0].Preflight.Eligible}
	protocol, absolute := rwC32VerdictsWithSampleCount(sameStats, distinctStats, preflight, rwC32Workers*rwC32Iters*rwR1R24BlocksPerRepetition)
	if combined.Protocol != rwC32Pass || protocol.Status != rwC32Pass {
		protocol.Status = rwC32Fail
	}
	return rwR1R24RepetitionReport{Path: pair[0].Path, Repetition: pair[0].Repetition, Same: makeRWC32StatsReport(sameStats), Distinct: makeRWC32StatsReport(distinctStats), Protocol: protocol.Status, Absolute: absolute.Status}, nil
}

func rwR1R24CombineVerdicts(blocks []rwC32MachineBlock) (rwR1R24CombinedVerdict, error) {
	if len(blocks) != rwR1R24BlocksPerRepetition {
		return rwR1R24CombinedVerdict{}, fmt.Errorf("combined verdict requires %d blocks, got %d", rwR1R24BlocksPerRepetition, len(blocks))
	}
	result := rwR1R24CombinedVerdict{Protocol: rwC32Pass, Absolute: rwC32Pass}
	seen := make(map[int]bool, len(blocks))
	for _, block := range blocks {
		if block.Block < 1 || block.Block > rwR1R24BlocksPerRepetition || seen[block.Block] {
			return rwR1R24CombinedVerdict{}, fmt.Errorf("invalid or duplicate block %d", block.Block)
		}
		seen[block.Block] = true
		if block.Cold.Failures != 0 || block.Warm.Failures != 0 || block.Cold.Completed != rwC32Pass || block.Warm.Completed != rwC32Pass || block.Safety.AuthFailures != rwC32Pass || block.Safety.LockWaits != rwC32Pass || block.Safety.Lifecycle != rwC32Pass {
			result.Protocol = rwC32Fail
		}
	}
	if len(seen) != rwR1R24BlocksPerRepetition {
		return rwR1R24CombinedVerdict{}, fmt.Errorf("missing block in combined verdict")
	}
	return result, nil
}

func rwR1R24BlockOrder(repetition, block int) []string {
	if (repetition+block)%2 == 0 {
		return []string{"same", "distinct"}
	}
	return []string{"distinct", "same"}
}

func rwR1R24Plan(paths []string, repetitions int) []rwR1R24Block {
	plan := make([]rwR1R24Block, 0, len(paths)*repetitions*rwR1R24BlocksPerRepetition)
	for _, path := range paths {
		for repetition := 1; repetition <= repetitions; repetition++ {
			for block := 1; block <= rwR1R24BlocksPerRepetition; block++ {
				plan = append(plan, rwR1R24Block{Path: path, Repetition: repetition, Block: block, Order: rwR1R24BlockOrder(repetition, block)})
			}
		}
	}
	return plan
}

func rwR1R24ValidatePlan(plan []rwR1R24Block, paths []string, repetitions int) error {
	want := len(paths) * repetitions * rwR1R24BlocksPerRepetition
	if len(plan) != want {
		return fmt.Errorf("plan blocks=%d, want %d", len(plan), want)
	}
	for i, block := range plan {
		pathIndex := i / (repetitions * rwR1R24BlocksPerRepetition)
		withinPath := i % (repetitions * rwR1R24BlocksPerRepetition)
		wantRepetition := withinPath/rwR1R24BlocksPerRepetition + 1
		wantBlock := withinPath%rwR1R24BlocksPerRepetition + 1
		if pathIndex >= len(paths) || block.Path != paths[pathIndex] || block.Repetition != wantRepetition || block.Block != wantBlock {
			return fmt.Errorf("block %d has unexpected identity path=%s repetition=%d block=%d", i, block.Path, block.Repetition, block.Block)
		}
		if len(block.Order) != rwR1R24PopulationsPerBlock || block.Order[0] == block.Order[1] {
			return fmt.Errorf("block %d has invalid population order %v", i, block.Order)
		}
		if block.Block < 1 || block.Block > rwR1R24BlocksPerRepetition {
			return fmt.Errorf("block %d has invalid block number %d", i, block.Block)
		}
	}
	return nil
}
