package code

import (
	"regexp"
	"sort"
	"strings"
)

// SymbolSearchQuery specifies parameters for searching code symbols.
type SymbolSearchQuery struct {
	Query    string `json:"query"`
	IsRegex  bool   `json:"is_regex"`
	Kind     string `json:"kind,omitempty"`
	FilePath string `json:"file_path,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}

// SymbolSearchResult represents a matched symbol with relevance score.
type SymbolSearchResult struct {
	Symbol     Symbol  `json:"symbol"`
	Score      float64 `json:"score"`
	MatchField string  `json:"match_field"` // "name_exact", "name_prefix", "name_contains", "signature", "doc"
}

// SearchSymbols queries symbols in the given CodeGraph by substring or regular expression.
func SearchSymbols(graph *CodeGraph, query SymbolSearchQuery) []SymbolSearchResult {
	if graph == nil || len(graph.Symbols) == 0 {
		return []SymbolSearchResult{}
	}

	qStr := strings.TrimSpace(query.Query)
	if qStr == "" {
		return []SymbolSearchResult{}
	}

	limit := query.Limit
	if limit <= 0 {
		limit = 50
	}

	var re *regexp.Regexp
	var regexErr error
	if query.IsRegex {
		re, regexErr = regexp.Compile("(?i)" + qStr)
	}

	lowerQ := strings.ToLower(qStr)
	var matches []SymbolSearchResult

	for _, s := range graph.Symbols {
		// Filter by kind
		if query.Kind != "" && !strings.EqualFold(s.Kind, query.Kind) {
			continue
		}
		// Filter by file path substring
		if query.FilePath != "" && !strings.Contains(strings.ToLower(s.FilePath), strings.ToLower(query.FilePath)) {
			continue
		}

		score := 0.0
		matchField := ""

		if query.IsRegex {
			if regexErr == nil && re != nil {
				if re.MatchString(s.Name) {
					score = 1.0
					matchField = "name_regex"
				} else if re.MatchString(s.Signature) {
					score = 0.5
					matchField = "signature_regex"
				} else if re.MatchString(s.DocSummary) {
					score = 0.25
					matchField = "doc_regex"
				}
			}
		} else {
			lowerName := strings.ToLower(s.Name)
			lowerSig := strings.ToLower(s.Signature)
			lowerDoc := strings.ToLower(s.DocSummary)

			if lowerName == lowerQ {
				score = 1.0
				matchField = "name_exact"
			} else if strings.HasPrefix(lowerName, lowerQ) {
				score = 0.8
				matchField = "name_prefix"
			} else if strings.Contains(lowerName, lowerQ) {
				score = 0.6
				matchField = "name_contains"
			} else if strings.Contains(lowerSig, lowerQ) {
				score = 0.4
				matchField = "signature"
			} else if strings.Contains(lowerDoc, lowerQ) {
				score = 0.2
				matchField = "doc"
			}
		}

		if score > 0 {
			matches = append(matches, SymbolSearchResult{
				Symbol:     s,
				Score:      score,
				MatchField: matchField,
			})
		}
	}

	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Score != matches[j].Score {
			return matches[i].Score > matches[j].Score
		}
		if matches[i].Symbol.Name != matches[j].Symbol.Name {
			return matches[i].Symbol.Name < matches[j].Symbol.Name
		}
		return matches[i].Symbol.FilePath < matches[j].Symbol.FilePath
	})

	if len(matches) > limit {
		matches = matches[:limit]
	}

	return matches
}
