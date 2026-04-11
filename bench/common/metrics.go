// Package common provides shared utilities for benchmark evaluation.
package common

import (
	"math"
	"strings"
)

// BenchmarkResult holds aggregated benchmark results.
type BenchmarkResult struct {
	Benchmark string             `json:"benchmark"`
	Overall   float64            `json:"overall_accuracy"`
	ByType    map[string]float64 `json:"by_type"`
	Total     int                `json:"total_questions"`
	Correct   int                `json:"correct"`
	Details   []QuestionResult   `json:"details,omitempty"`
}

// QuestionResult holds the result of a single question evaluation.
type QuestionResult struct {
	ID       string  `json:"id"`
	Type     string  `json:"type"`
	Query    string  `json:"query"`
	Expected string  `json:"expected"`
	Got      string  `json:"got"`
	Score    float64 `json:"score"`
	Correct  bool    `json:"correct"`
}

// F1Score computes token-level F1 between prediction and reference.
func F1Score(prediction, reference string) float64 {
	predTokens := tokenize(prediction)
	refTokens := tokenize(reference)

	if len(predTokens) == 0 && len(refTokens) == 0 {
		return 1.0
	}
	if len(predTokens) == 0 || len(refTokens) == 0 {
		return 0.0
	}

	refSet := make(map[string]int)
	for _, t := range refTokens {
		refSet[t]++
	}

	common := 0
	for _, t := range predTokens {
		if refSet[t] > 0 {
			common++
			refSet[t]--
		}
	}

	if common == 0 {
		return 0.0
	}

	precision := float64(common) / float64(len(predTokens))
	recall := float64(common) / float64(len(refTokens))
	return 2 * precision * recall / (precision + recall)
}

// RougeL computes ROUGE-L F1 score using longest common subsequence.
func RougeL(prediction, reference string) float64 {
	predTokens := tokenize(prediction)
	refTokens := tokenize(reference)

	if len(predTokens) == 0 && len(refTokens) == 0 {
		return 1.0
	}
	if len(predTokens) == 0 || len(refTokens) == 0 {
		return 0.0
	}

	lcsLen := lcs(predTokens, refTokens)
	if lcsLen == 0 {
		return 0.0
	}

	precision := float64(lcsLen) / float64(len(predTokens))
	recall := float64(lcsLen) / float64(len(refTokens))
	return 2 * precision * recall / (precision + recall)
}

// Aggregate computes overall and per-type accuracy from question results.
func Aggregate(results []QuestionResult) BenchmarkResult {
	byType := make(map[string][]float64)
	total := len(results)
	correct := 0

	for _, r := range results {
		byType[r.Type] = append(byType[r.Type], r.Score)
		if r.Correct {
			correct++
		}
	}

	typeAvg := make(map[string]float64)
	for t, scores := range byType {
		sum := 0.0
		for _, s := range scores {
			sum += s
		}
		typeAvg[t] = sum / float64(len(scores))
	}

	overall := 0.0
	if total > 0 {
		overall = float64(correct) / float64(total)
	}

	return BenchmarkResult{
		Overall: math.Round(overall*1000) / 1000,
		ByType:  typeAvg,
		Total:   total,
		Correct: correct,
		Details: results,
	}
}

func tokenize(s string) []string {
	s = strings.ToLower(s)
	return strings.Fields(s)
}

func lcs(a, b []string) int {
	m, n := len(a), len(b)
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}
	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if a[i-1] == b[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else {
				dp[i][j] = max(dp[i-1][j], dp[i][j-1])
			}
		}
	}
	return dp[m][n]
}
