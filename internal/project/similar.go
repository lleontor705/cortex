package project

import (
	"sort"
	"strings"
)

type ProjectMatch struct {
	Name      string
	MatchType string // "case-insensitive", "substring", "levenshtein"
	Distance  int
}

func FindSimilar(name string, existing []string, maxDistance int) []ProjectMatch {
	if maxDistance < 0 {
		maxDistance = 0
	}
	nameLower := strings.ToLower(strings.TrimSpace(name))
	effectiveMax := maxDistance
	if len(nameLower) > 0 {
		halfLen := len(nameLower) / 2
		if halfLen < 1 {
			halfLen = 1
		}
		if effectiveMax > halfLen {
			effectiveMax = halfLen
		}
	}

	var caseMatches, subMatches, levMatches []ProjectMatch
	seen := make(map[string]bool)

	for _, candidate := range existing {
		if candidate == name {
			continue
		}
		candidateLower := strings.ToLower(strings.TrimSpace(candidate))
		if candidateLower == nameLower {
			if candidate != name && !seen[candidate] {
				seen[candidate] = true
				caseMatches = append(caseMatches, ProjectMatch{Name: candidate, MatchType: "case-insensitive", Distance: 0})
			}
			continue
		}
		if len(nameLower) >= 3 {
			if strings.Contains(candidateLower, nameLower) || strings.Contains(nameLower, candidateLower) {
				if !seen[candidate] {
					seen[candidate] = true
					subMatches = append(subMatches, ProjectMatch{Name: candidate, MatchType: "substring", Distance: 0})
				}
				continue
			}
		}
		dist := levenshtein(nameLower, candidateLower)
		if dist <= effectiveMax && !seen[candidate] {
			seen[candidate] = true
			levMatches = append(levMatches, ProjectMatch{Name: candidate, MatchType: "levenshtein", Distance: dist})
		}
	}

	sort.Slice(levMatches, func(i, j int) bool {
		return levMatches[i].Distance < levMatches[j].Distance
	})
	result := make([]ProjectMatch, 0, len(caseMatches)+len(subMatches)+len(levMatches))
	result = append(result, caseMatches...)
	result = append(result, subMatches...)
	result = append(result, levMatches...)
	return result
}

func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	la, lb := len(ra), len(rb)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	if la > lb {
		ra, rb = rb, ra
		la, lb = lb, la
	}
	prev := make([]int, la+1)
	curr := make([]int, la+1)
	for i := 0; i <= la; i++ {
		prev[i] = i
	}
	for j := 1; j <= lb; j++ {
		curr[0] = j
		for i := 1; i <= la; i++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			del := prev[i] + 1
			ins := curr[i-1] + 1
			sub := prev[i-1] + cost
			m := del
			if ins < m {
				m = ins
			}
			if sub < m {
				m = sub
			}
			curr[i] = m
		}
		prev, curr = curr, prev
	}
	return prev[la]
}
