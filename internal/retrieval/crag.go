// Package retrieval implements hybrid, multi-signal, and adaptive retrieval pipelines for Cortex.
package retrieval

import (
	"sort"

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
	DynamicMode   bool    // enables dynamic score gap & relative elbow calibration (for RRF/dense distributions)
}

// DefaultCRAGConfig returns standard CRAG evaluation parameters.
func DefaultCRAGConfig() CRAGConfig {
	return CRAGConfig{
		HighThreshold: 0.65,
		LowThreshold:  0.30,
		MinScoreFloor: 0.005, // Minimum RRF / combined score to consider non-noise
		DynamicMode:   false,
	}
}

// DynamicCRAGConfig returns CRAG configuration optimized for dynamic/RRF score distributions.
func DynamicCRAGConfig() CRAGConfig {
	return CRAGConfig{
		HighThreshold: 0.65,
		LowThreshold:  0.30,
		MinScoreFloor: 0.005,
		DynamicMode:   true,
	}
}

// CRAGEvaluation encapsulates the confidence evaluation of retrieved results.
type CRAGEvaluation struct {
	Grade           ConfidenceGrade        `json:"grade"`
	Confidence      float64                `json:"confidence"`
	NeedsRefinement bool                   `json:"needs_refinement"`
	FilteredResults []*domain.SearchResult `json:"filtered_results"`
}

// RecognitionConfig configures the post-PPR recognition-memory filter that
// prunes weak candidates before final fusion while still filling the
// requested top-k.
type RecognitionConfig struct {
	TopK          int     // requested final candidate count; <= 0 disables prune-only
	RelativeFloor float64 // keep candidates scoring at least RelativeFloor * top score
	MinScoreFloor float64 // absolute noise floor on Rank
}

// DefaultRecognitionConfig returns standard recognition-memory parameters.
func DefaultRecognitionConfig() RecognitionConfig {
	return RecognitionConfig{
		TopK:          0,
		RelativeFloor: 0.05,
		MinScoreFloor: DefaultCRAGConfig().MinScoreFloor,
	}
}

// ApplyRecognitionFilter prunes post-PPR candidates below the recognition
// floors, then refills from the strongest pruned candidates so the requested
// TopK is always filled (and never exceeded), preserving descending-score
// order with ties broken by descending ID to mirror the RRF contract.
func ApplyRecognitionFilter(results []*domain.SearchResult, cfg RecognitionConfig) []*domain.SearchResult {
	if len(results) == 0 {
		return nil
	}

	relativeFloor := cfg.RelativeFloor
	if relativeFloor <= 0 {
		relativeFloor = 0.05
	}
	minFloor := cfg.MinScoreFloor
	if minFloor < 0 {
		minFloor = 0
	}

	sorted := make([]*domain.SearchResult, len(results))
	copy(sorted, results)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Rank != sorted[j].Rank {
			return sorted[i].Rank > sorted[j].Rank
		}
		return sorted[i].ID > sorted[j].ID
	})

	topScore := sorted[0].Rank
	recognized := make([]*domain.SearchResult, 0, len(sorted))
	pruned := make([]*domain.SearchResult, 0, len(sorted))
	for _, r := range sorted {
		if r.Rank >= minFloor && r.Rank >= relativeFloor*topScore {
			recognized = append(recognized, r)
		} else {
			pruned = append(pruned, r)
		}
	}

	if cfg.TopK <= 0 {
		return recognized
	}
	if len(recognized) < cfg.TopK {
		// Recognition pruned too aggressively: refill with the strongest
		// pruned candidates so the requested top-k never starves.
		room := cfg.TopK - len(recognized)
		if room > len(pruned) {
			room = len(pruned)
		}
		return append(recognized, pruned[:room]...)
	}
	if len(recognized) > cfg.TopK {
		return recognized[:cfg.TopK]
	}
	return recognized
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

	if cfg.DynamicMode && topScore > 0 && topScore < cfg.LowThreshold {
		// Dynamic Calibration Mode: When scores are in RRF space (< LowThreshold, e.g. 0.005 - 0.05),
		// evaluate confidence based on the relative gap between top score and tail or noise floor.
		var avgTail float64
		if len(filtered) > 1 {
			var tailSum float64
			for _, r := range filtered[1:] {
				tailSum += r.Rank
			}
			avgTail = tailSum / float64(len(filtered)-1)
		}

		if len(filtered) == 1 && topScore >= cfg.MinScoreFloor*2 {
			grade = ConfidenceGradeHigh
			needsRefinement = false
		} else if avgTail > 0 && (topScore/avgTail) >= 1.4 {
			// Clear winner with significant margin over subsequent results
			grade = ConfidenceGradeHigh
			needsRefinement = false
		} else if len(filtered) > 0 && topScore >= cfg.MinScoreFloor {
			grade = ConfidenceGradeMedium
			needsRefinement = false
		} else {
			grade = ConfidenceGradeLow
			needsRefinement = true
		}
	} else {
		// Standard absolute thresholding
		if topScore >= cfg.HighThreshold {
			grade = ConfidenceGradeHigh
		} else if topScore < cfg.LowThreshold || len(filtered) == 0 {
			grade = ConfidenceGradeLow
			needsRefinement = true
		}
	}

	return CRAGEvaluation{
		Grade:           grade,
		Confidence:      topScore,
		NeedsRefinement: needsRefinement,
		FilteredResults: filtered,
	}
}
