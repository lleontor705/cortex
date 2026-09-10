package code

import (
	"fmt"
	"sort"
	"strings"
)

// GenerateRepoMap creates a compact, token-budgeted structural map of the repository
// using high-centrality AST symbols, tailored for LLM system prompts (inspired by Aider).
func GenerateRepoMap(graph *CodeGraph, maxTokens int) string {
	if graph == nil || len(graph.Symbols) == 0 {
		return "No indexed code symbols found in project."
	}

	if maxTokens <= 0 {
		maxTokens = 2048
	}

	// Approximate 1 token ≈ 4 characters
	charBudget := maxTokens * 4

	// 1. Calculate symbol centrality (degree) from relations
	degree := make(map[string]int, len(graph.Symbols))
	for _, r := range graph.Relations {
		degree[r.SourceID]++
		degree[r.TargetID] += 2 // In-degree is twice as valuable for discovering public APIs
	}

	// 2. Group symbols by file
	type fileGroup struct {
		filePath string
		symbols  []Symbol
	}

	groups := make(map[string][]Symbol)
	for _, s := range graph.Symbols {
		if s.FilePath == "" {
			continue
		}
		groups[s.FilePath] = append(groups[s.FilePath], s)
	}

	// Sort files by path for deterministic layout
	fileList := make([]fileGroup, 0, len(groups))
	for fPath, syms := range groups {
		// Sort symbols inside file: types/interfaces first, then by centrality score
		sort.Slice(syms, func(i, j int) bool {
			scoreI := degree[syms[i].ID]
			scoreJ := degree[syms[j].ID]

			// Prioritize types and interfaces
			typeI := syms[i].Kind == KindStruct || syms[i].Kind == KindInterface || syms[i].Kind == KindClass
			typeJ := syms[j].Kind == KindStruct || syms[j].Kind == KindInterface || syms[j].Kind == KindClass

			if typeI != typeJ {
				return typeI
			}
			if scoreI != scoreJ {
				return scoreI > scoreJ
			}
			return syms[i].Name < syms[j].Name
		})

		fileList = append(fileList, fileGroup{
			filePath: fPath,
			symbols:  syms,
		})
	}

	sort.Slice(fileList, func(i, j int) bool {
		return fileList[i].filePath < fileList[j].filePath
	})

	var sb strings.Builder
	fmt.Fprintf(&sb, "# Repository Map (%s, %d files, %d symbols)\n\n", graph.Project, len(fileList), len(graph.Symbols))

	totalChars := sb.Len()
	budgetReached := false

	for _, fg := range fileList {
		if budgetReached {
			break
		}

		var fileSb strings.Builder
		fileSb.WriteString(fg.filePath + ":\n")

		renderedCount := 0
		for _, s := range fg.symbols {
			line := formatSymbolLine(s)
			if line == "" {
				continue
			}

			// Check budget
			if totalChars+fileSb.Len()+len(line)+1 > charBudget {
				remaining := len(fg.symbols) - renderedCount
				if remaining > 0 {
					fmt.Fprintf(&fileSb, "  ... (+%d symbols)\n", remaining)
				}
				budgetReached = true
				break
			}

			fileSb.WriteString(line + "\n")
			renderedCount++
		}

		fileSb.WriteString("\n")
		sb.WriteString(fileSb.String())
		totalChars = sb.Len()
	}

	return strings.TrimSpace(sb.String())
}

// formatSymbolLine outputs a condensed signature line for a symbol.
func formatSymbolLine(s Symbol) string {
	switch s.Kind {
	case KindStruct, "type":
		return fmt.Sprintf("  type %s struct", s.Name)
	case KindInterface:
		return fmt.Sprintf("  type %s interface", s.Name)
	case KindClass:
		return fmt.Sprintf("  class %s", s.Name)
	case KindFunc:
		sig := s.Signature
		if sig != "" {
			return fmt.Sprintf("  func %s%s", s.Name, sig)
		}
		return fmt.Sprintf("  func %s()", s.Name)
	case KindMethod:
		sig := s.Signature
		parent := s.ParentID
		if parent != "" {
			parts := strings.Split(parent, ".")
			receiver := parts[len(parts)-1]
			if sig != "" {
				return fmt.Sprintf("  func (%s) %s%s", receiver, s.Name, sig)
			}
			return fmt.Sprintf("  func (%s) %s()", receiver, s.Name)
		}
		if sig != "" {
			return fmt.Sprintf("  func %s%s", s.Name, sig)
		}
		return fmt.Sprintf("  func %s()", s.Name)
	default:
		if s.Name != "" {
			return fmt.Sprintf("  [%s] %s", s.Kind, s.Name)
		}
		return ""
	}
}
