// Package retrieval implements hybrid, multi-signal, and adaptive retrieval pipelines for Cortex.
package retrieval

import (
	"github.com/lleontor705/cortex/v2/internal/domain"
)

// ConfidenceGrade represents the categorical confidence of retrieved context.
type ConfidenceGrade string

const (
	ConfidenceGradeHigh   ConfidenceGrade = "high"
	ConfidenceGradeMedium ConfidenceGrade = "medium"
	ConfidenceGradeLow    ConfidenceGrade = "low"
)

// CRAGConfig defines thresholds for Corrective RAG evaluation and filtering.
type CRAGConfig struct {
	HighThreshold float64 // typically 0.65
	LowThreshold  float64 // typically 0.30
	MinScoreFloor float64 // noise floor, results below this are stripped
}

// DefaultCRAGConfig returns standard CRAG evaluation parameters.
func DefaultCRAGConfig() CRAGConfig {
	return CRAGConfig{
		HighThreshold: 0.65,
		LowThreshold:  0.30,
		MinScoreFloor: 0.005, // Minimum RRF / combined score to consider non-noise
	}
}

// CRAGEvaluation encapsulates the confidence evaluation of retrieved results.
type CRAGEvaluation struct {
	Grade           ConfidenceGrade        `json:"grade"`
	Confidence      float64                `json:"confidence"`
	NeedsRefinement bool                   `json:"needs_refinement"`
	FilteredResults []*domain.SearchResult `json:"filtered_results"`
}

// EvaluateCRAG evaluates retrieved search results against CRAG confidence thresholds,
// filtering out noisy or irrelevant low-scoring candidates to protect downstream
// agents from hallucinations.
func EvaluateCRAG(results []*domain.SearchResult, cfg CRAGConfig) CRAGEvaluation {
	if len(results) == 0 {
		return CRAGEvaluation{
			Grade:           ConfidenceGradeLow,
			Confidence:      0.0,
			NeedsRefinement: true,
			FilteredResults: nil,
		}
	}

	if cfg.HighThreshold <= 0 {
		cfg.HighThreshold = 0.65
	}
	if cfg.LowThreshold <= 0 {
		cfg.LowThreshold = 0.30
	}

	topScore := results[0].Rank
	var filtered []*domain.SearchResult

	for _, r := range results {
		if r.Rank >= cfg.MinScoreFloor {
			filtered = append(filtered, r)
		}
	}

	grade := ConfidenceGradeMedium
	needsRefinement := false

	if topScore >= cfg.HighThreshold {
		grade = ConfidenceGradeHigh
	} else if topScore < cfg.LowThreshold || len(filtered) == 0 {
		grade = ConfidenceGradeLow
		needsRefinement = true
	}

	return CRAGEvaluation{
		Grade:           grade,
		Confidence:      topScore,
		NeedsRefinement: needsRefinement,
		FilteredResults: filtered,
	}
}
