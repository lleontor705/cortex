// Package common provides shared utilities for benchmark evaluation.
package common

import (
	"math"
	"sort"
	"strconv"
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

// RecallAtK returns the fraction of unique relevant IDs present in the first k
// results. Duplicate results occupy ranks but receive credit only once.
func RecallAtK(retrieved, relevant []string, k int) float64 {
	relevantSet := stringSet(relevant)
	if k <= 0 || len(relevantSet) == 0 {
		return 0
	}
	if k > len(retrieved) {
		k = len(retrieved)
	}

	seen := make(map[string]struct{}, k)
	hits := 0
	for _, id := range retrieved[:k] {
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		if _, ok := relevantSet[id]; ok {
			hits++
		}
	}
	return float64(hits) / float64(len(relevantSet))
}

// MRR returns the reciprocal rank of the first unique relevant result.
// Duplicate results occupy ranks but cannot receive relevance credit twice.
func MRR(retrieved, relevant []string) float64 {
	relevantSet := stringSet(relevant)
	if len(relevantSet) == 0 {
		return 0
	}

	seen := make(map[string]struct{}, len(retrieved))
	for i, id := range retrieved {
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		if _, ok := relevantSet[id]; ok {
			return 1 / float64(i+1)
		}
	}
	return 0
}

// NDCGAtK computes normalized discounted cumulative gain using exponential
// gain for positive graded relevance. Duplicate results occupy ranks and gain
// credit only at their first occurrence.
func NDCGAtK(retrieved []string, relevance map[string]float64, k int) float64 {
	if k <= 0 || len(relevance) == 0 {
		return 0
	}

	grades := make([]float64, 0, len(relevance))
	for _, grade := range relevance {
		if validGrade(grade) {
			grades = append(grades, grade)
		}
	}
	if len(grades) == 0 {
		return 0
	}
	sort.Slice(grades, func(i, j int) bool { return grades[i] > grades[j] })
	idealLimit := k
	if idealLimit > len(grades) {
		idealLimit = len(grades)
	}
	ideal := discountedGain(grades[:idealLimit])
	if ideal == 0 {
		return 0
	}

	cutoff := k
	if cutoff > len(retrieved) {
		cutoff = len(retrieved)
	}
	rankedGrades := make([]float64, 0, cutoff)
	seen := make(map[string]struct{}, cutoff)
	for i := 0; i < cutoff; i++ {
		id := retrieved[i]
		grade := relevance[id]
		if _, duplicate := seen[id]; duplicate || !validGrade(grade) {
			grade = 0
		} else {
			seen[id] = struct{}{}
		}
		rankedGrades = append(rankedGrades, grade)
	}
	return discountedGain(rankedGrades) / ideal
}

// EvidenceRecall returns exact labelled-evidence recall. Stable episode and
// fact IDs match by ID; spans match by episode ID and exact half-open offsets.
func EvidenceRecall(retrieved, relevant []EvidenceRef) float64 {
	relevantSet := evidenceSet(relevant)
	if len(relevantSet) == 0 {
		return 0
	}

	seen := make(map[string]struct{}, len(retrieved))
	hits := 0
	for _, evidence := range retrieved {
		key := evidenceKey(evidence)
		if key == "" {
			continue
		}
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		if _, ok := relevantSet[key]; ok {
			hits++
		}
	}
	return float64(hits) / float64(len(relevantSet))
}

// NoAnswerCorrect reports whether no-answer/abstention behavior matches the
// label. A no-answer query requires an explicit abstention and zero results.
func NoAnswerCorrect(noAnswer bool, retrieved []string, abstained bool) bool {
	if noAnswer {
		return abstained && len(retrieved) == 0
	}
	return !abstained && len(retrieved) > 0
}

// IsolationViolationCount counts every returned occurrence of an excluded ID.
// It is an absolute blocking correctness count, not an averaged quality score.
func IsolationViolationCount(retrieved, excluded []string) int {
	excludedSet := stringSet(excluded)
	violations := 0
	for _, id := range retrieved {
		if _, blocked := excludedSet[id]; blocked {
			violations++
		}
	}
	return violations
}

// FilterEligibilityExact reports exact equality between returned and eligible
// stable-ID sets. Ordering and duplicate occurrences do not change set parity.
func FilterEligibilityExact(returned, eligible []string) bool {
	returnedSet := stringSet(returned)
	eligibleSet := stringSet(eligible)
	if len(returnedSet) != len(eligibleSet) {
		return false
	}
	for id := range returnedSet {
		if _, ok := eligibleSet[id]; !ok {
			return false
		}
	}
	return true
}

func stringSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}

func validGrade(grade float64) bool {
	return grade > 0 && !math.IsNaN(grade) && !math.IsInf(grade, 0)
}

func discountedGain(grades []float64) float64 {
	total := 0.0
	for i, grade := range grades {
		if validGrade(grade) {
			total += (math.Exp2(grade) - 1) / math.Log2(float64(i)+2)
		}
	}
	return total
}

func evidenceSet(values []EvidenceRef) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		if key := evidenceKey(value); key != "" {
			set[key] = struct{}{}
		}
	}
	return set
}

func evidenceKey(evidence EvidenceRef) string {
	switch {
	case evidence.Span != nil:
		span := evidence.Span
		if span.EpisodeID == "" || span.StartByte < 0 || span.EndByte <= span.StartByte {
			return ""
		}
		return "span\x00" + span.EpisodeID + "\x00" + strconv.Itoa(span.StartByte) + "\x00" + strconv.Itoa(span.EndByte)
	case evidence.EpisodeID != "":
		return "episode\x00" + evidence.EpisodeID
	case evidence.FactID != "":
		return "fact\x00" + evidence.FactID
	default:
		return ""
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
