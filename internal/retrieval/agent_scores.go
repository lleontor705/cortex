package retrieval

import (
	"errors"
	"fmt"
	"math"
	"sort"
)

// AgentScoreSignal identifies the native scale used by one retrieval signal.
type AgentScoreSignal string

const (
	AgentScoreLexical AgentScoreSignal = "lexical"
	AgentScoreDense   AgentScoreSignal = "dense"
	AgentScoreMaxSim  AgentScoreSignal = "maxsim"
	AgentScorePPR     AgentScoreSignal = "ppr"
	AgentScoreSummary AgentScoreSignal = "summary"
)

// ErrInvalidAgentScore marks a score that cannot safely participate in
// retrieval ranking or confidence evaluation.
var ErrInvalidAgentScore = errors.New("invalid agent retrieval score")

// AgentScore carries one native retrieval signal and its stable public
// identity. Raw is a pointer so a structurally missing score cannot be confused
// with the valid value zero.
type AgentScore struct {
	Signal       AgentScoreSignal
	Raw          *float64
	Normalized   float64
	SourceKind   string
	PublicHandle string
}

// NormalizeAgentScores validates and normalizes scores, then orders them by
// descending normalized relevance. Bounded dense, MaxSim, PPR and summary
// inputs are clamped to one. Non-negative unbounded lexical inputs use x/(1+x)
// (expressed in an overflow-safe form). Missing, negative and non-finite values
// are rejected instead of receiving a confidence-bearing default.
//
// Equal scores are ordered by source kind and public handle. Stable sorting
// preserves input order only when both public tie breakers are identical.
func NormalizeAgentScores(scores []AgentScore) ([]AgentScore, error) {
	normalized := make([]AgentScore, len(scores))
	for i, score := range scores {
		value, err := normalizeAgentScore(score.Signal, score.Raw)
		if err != nil {
			return nil, fmt.Errorf("agent score %d: %w", i, err)
		}
		score.Normalized = value
		normalized[i] = score
	}

	sort.SliceStable(normalized, func(i, j int) bool {
		left, right := normalized[i], normalized[j]
		if left.Normalized != right.Normalized {
			return left.Normalized > right.Normalized
		}
		if left.SourceKind != right.SourceKind {
			return left.SourceKind < right.SourceKind
		}
		return left.PublicHandle < right.PublicHandle
	})

	return normalized, nil
}

func normalizeAgentScore(signal AgentScoreSignal, raw *float64) (float64, error) {
	if raw == nil {
		return 0, fmt.Errorf("%w: missing %s value", ErrInvalidAgentScore, signal)
	}
	value := *raw
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return 0, fmt.Errorf("%w: %s value %v", ErrInvalidAgentScore, signal, value)
	}

	switch signal {
	case AgentScoreLexical:
		return 1 - 1/(1+value), nil
	case AgentScoreDense, AgentScoreMaxSim, AgentScorePPR, AgentScoreSummary:
		return math.Min(value, 1), nil
	default:
		return 0, fmt.Errorf("%w: unknown signal %q", ErrInvalidAgentScore, signal)
	}
}
