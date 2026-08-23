// Package retrieval implements hybrid, multi-signal, and adaptive retrieval pipelines for Cortex.
package retrieval

import (
	"sort"
	"strings"
	"unicode"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

// TokenizeLateInteraction cleans and splits text into normalized word and subword tokens.
func TokenizeLateInteraction(text string) []string {
	var tokens []string
	var sb strings.Builder

	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			sb.WriteRune(unicode.ToLower(r))
		} else if sb.Len() > 0 {
			tok := sb.String()
			if len(tok) > 1 {
				tokens = append(tokens, tok)
			}
			sb.Reset()
		}
	}
	if sb.Len() > 0 {
		tok := sb.String()
		if len(tok) > 1 {
			tokens = append(tokens, tok)
		}
	}
	return tokens
}

// TokenSimilarity calculates token-level string overlap similarity (Jaro-Winkler / trigram-like).
func TokenSimilarity(a, b string) float64 {
	if a == b {
		return 1.0
	}
	if len(a) == 0 || len(b) == 0 {
		return 0.0
	}
	if strings.Contains(a, b) || strings.Contains(b, a) {
		shortLen := len(a)
		if len(b) < shortLen {
			shortLen = len(b)
		}
		longLen := len(a)
		if len(b) > longLen {
			longLen = len(b)
		}
		return float64(shortLen) / float64(longLen)
	}

	// 2-gram overlap
	if len(a) >= 2 && len(b) >= 2 {
		gramsA := make(map[string]bool)
		for i := 0; i < len(a)-1; i++ {
			gramsA[a[i:i+2]] = true
		}
		var common int
		for i := 0; i < len(b)-1; i++ {
			if gramsA[b[i:i+2]] {
				common++
			}
		}
		totalGrams := len(gramsA) + len(b) - 1
		if totalGrams > 0 {
			return (2.0 * float64(common)) / float64(totalGrams)
		}
	}

	return 0.0
}

// ComputeMaxSimScore computes the ColBERT-style Late-Interaction MaxSim score between query and document.
//
// MaxSim(Q, D) = sum_{q in Q} max_{d in D} sim(q, d)
func ComputeMaxSimScore(queryTokens, docTokens []string) float64 {
	if len(queryTokens) == 0 || len(docTokens) == 0 {
		return 0.0
	}

	var totalMaxSim float64

	for _, q := range queryTokens {
		var maxSim float64
		for _, d := range docTokens {
			sim := TokenSimilarity(q, d)
			if sim > maxSim {
				maxSim = sim
			}
			if maxSim >= 1.0 {
				break
			}
		}
		totalMaxSim += maxSim
	}

	// Normalize by query token count to yield a [0.0, 1.0] range
	return totalMaxSim / float64(len(queryTokens))
}

// ReRankWithLateInteraction re-ranks search results using ColBERT-inspired Late-Interaction MaxSim.
func ReRankWithLateInteraction(query string, results []*domain.SearchResult) []*domain.SearchResult {
	if len(results) <= 1 {
		return results
	}

	queryTokens := TokenizeLateInteraction(query)
	if len(queryTokens) == 0 {
		return results
	}

	type scoredResult struct {
		result   *domain.SearchResult
		maxSim   float64
		newScore float64
	}

	scored := make([]scoredResult, len(results))

	for i, r := range results {
		docText := r.Title + " " + r.TopicKey + " " + r.Content
		docTokens := TokenizeLateInteraction(docText)
		maxSim := ComputeMaxSimScore(queryTokens, docTokens)

		// Combined rank: original FTS/RRF rank (60%) + Late Interaction MaxSim score (40%)
		combinedScore := (r.Rank * 0.6) + (maxSim * 2.0 * 0.4)
		scored[i] = scoredResult{
			result:   r,
			maxSim:   maxSim,
			newScore: combinedScore,
		}
	}

	sort.Slice(scored, func(i, j int) bool {
		return scored[i].newScore > scored[j].newScore
	})

	output := make([]*domain.SearchResult, len(results))
	for i, s := range scored {
		s.result.Rank = s.newScore
		output[i] = s.result
	}

	return output
}
