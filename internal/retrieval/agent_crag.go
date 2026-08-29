package retrieval

import (
	"errors"
	"math"
	"strings"
	"unicode/utf8"
)

const AgentCRAGMaxQueryRunes = 320

var ErrInvalidAgentCRAGScore = errors.New("agent CRAG score must be finite and normalized")

func EvaluateAgentCRAG(scores []float64, cfg CRAGConfig) (ConfidenceGrade, error) {
	if cfg.HighThreshold <= 0 {
		cfg.HighThreshold = .65
	}
	if cfg.LowThreshold <= 0 {
		cfg.LowThreshold = .30
	}
	if cfg.MinScoreFloor < 0 {
		cfg.MinScoreFloor = .005
	}
	top := -1.0
	for _, score := range scores {
		if math.IsNaN(score) || math.IsInf(score, 0) || score < 0 || score > 1 {
			return ConfidenceGradeLow, ErrInvalidAgentCRAGScore
		}
		if score >= cfg.MinScoreFloor && score > top {
			top = score
		}
	}
	if top < 0 || top < cfg.LowThreshold {
		return ConfidenceGradeLow, nil
	}
	if top >= cfg.HighThreshold {
		return ConfidenceGradeHigh, nil
	}
	return ConfidenceGradeMedium, nil
}

func RefineAgentCRAGQuery(query string) string {
	mandatory := []string{"project-context", "implementation", "symbols", "decisions", "files", "context"}
	refined := append([]string(nil), mandatory...)
	seen := make(map[string]bool, len(mandatory)+40)
	for _, word := range mandatory {
		seen[word] = true
	}
	usedRunes := utf8.RuneCountInString(strings.Join(refined, " "))
	originals := 0
	for _, word := range strings.Fields(query) {
		if originals >= 40 {
			break
		}
		key := strings.ToLower(word)
		if seen[key] {
			continue
		}
		wordRunes := utf8.RuneCountInString(word)
		if usedRunes+1+wordRunes > AgentCRAGMaxQueryRunes {
			continue
		}
		seen[key] = true
		refined = append(refined, word)
		usedRunes += 1 + wordRunes
		originals++
	}
	return strings.Join(refined, " ")
}
