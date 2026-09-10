package retrieval

import (
	"testing"
	"time"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

func TestFuseResultsWithOptions_TemporalDecay(t *testing.T) {
	refTime := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

	// Obs 1: Fresh observation (1 day old)
	// Obs 2: Stale observation (90 days old)
	// FTS and Vector rank both equally in position 1 and 2 respectively.
	// Under standard RRF (no decay), Obs 1 and Obs 2 have identical scores if ranks are swapped.
	fts := []*domain.SearchResult{
		{
			Observation: domain.Observation{
				ID:        1,
				Title:     "Fresh Observation",
				UpdatedAt: refTime.Add(-24 * time.Hour), // 1 day old
			},
			Rank: 0.9,
		},
		{
			Observation: domain.Observation{
				ID:        2,
				Title:     "Old Observation",
				UpdatedAt: refTime.Add(-90 * 24 * time.Hour), // 90 days old
			},
			Rank: 0.85,
		},
	}

	// Without decay (DecayHalfLifeDays: 0), ranks are purely position-based
	resNoDecay := FuseResultsWithOptions(fts, nil, FuseOptions{
		Limit:             10,
		DecayHalfLifeDays: 0,
		ReferenceTime:     refTime,
	})
	if len(resNoDecay) != 2 {
		t.Fatalf("expected 2 results without decay, got %d", len(resNoDecay))
	}
	if resNoDecay[0].ID != 1 || resNoDecay[1].ID != 2 {
		t.Errorf("expected [1, 2] order without decay, got [%d, %d]", resNoDecay[0].ID, resNoDecay[1].ID)
	}

	// Now construct a case where the old observation is rank 1 in FTS, but fresh observation is rank 2
	ftsInverted := []*domain.SearchResult{
		{
			Observation: domain.Observation{
				ID:        2,
				Title:     "Old Observation",
				UpdatedAt: refTime.Add(-90 * 24 * time.Hour), // 90 days old (3 half-lives if half-life = 30d)
			},
			Rank: 0.9, // rank 1 in FTS -> RRF = 1/61 ≈ 0.01639, decayed by 2^(-3) = 0.125 -> 0.00205
		},
		{
			Observation: domain.Observation{
				ID:        1,
				Title:     "Fresh Observation",
				UpdatedAt: refTime.Add(-1 * time.Hour), // 1 hour old -> decay factor ≈ 1.0
			},
			Rank: 0.85, // rank 2 in FTS -> RRF = 1/62 ≈ 0.01613, decayed ≈ 0.01613
		},
	}

	// With 30-day half-life decay, Fresh Observation (ID 1) MUST overtake Old Observation (ID 2)
	resWithDecay := FuseResultsWithOptions(ftsInverted, nil, FuseOptions{
		Limit:             10,
		DecayHalfLifeDays: 30.0,
		ReferenceTime:     refTime,
	})

	if len(resWithDecay) != 2 {
		t.Fatalf("expected 2 results with decay, got %d", len(resWithDecay))
	}

	if resWithDecay[0].ID != 1 {
		t.Errorf("expected fresh observation (ID 1) to overtake old observation (ID 2), got ID %d at rank 1", resWithDecay[0].ID)
	}
	if resWithDecay[1].ID != 2 {
		t.Errorf("expected old observation (ID 2) at rank 2, got ID %d", resWithDecay[1].ID)
	}
}

func TestEvaluateCRAG_DynamicCalibration(t *testing.T) {
	// Simulate RRF score distribution with a prominent top candidate
	// Rank 1 in FTS+Vector: RRF score ~ 0.0328 (way below standard 0.65 threshold)
	// Tail candidate: RRF score ~ 0.0161
	results := []*domain.SearchResult{
		{
			Rank:        0.0328,
			Observation: domain.Observation{ID: 10, Title: "Top RRF Match"},
		},
		{
			Rank:        0.0161,
			Observation: domain.Observation{ID: 20, Title: "Secondary Match"},
		},
	}

	// Under standard static config, 0.0328 < 0.30 -> Low Confidence
	staticEval := EvaluateCRAG(results, DefaultCRAGConfig())
	if staticEval.Grade != ConfidenceGradeLow {
		t.Errorf("expected static evaluation to yield Low confidence for 0.0328, got %v", staticEval.Grade)
	}

	// Under dynamic calibration mode, gap ratio is 0.0328 / 0.0161 ≈ 2.03 (> 1.4)
	// Promotes to High Confidence without false negative suppression!
	dynamicEval := EvaluateCRAG(results, DynamicCRAGConfig())
	if dynamicEval.Grade != ConfidenceGradeHigh {
		t.Errorf("expected dynamic evaluation to recognize clear RRF winner as High confidence, got %v", dynamicEval.Grade)
	}
	if dynamicEval.NeedsRefinement {
		t.Error("expected needs_refinement = false for prominent RRF match under dynamic mode")
	}
	if len(dynamicEval.FilteredResults) != 2 {
		t.Errorf("expected both non-noise results retained, got %d", len(dynamicEval.FilteredResults))
	}
}
