// Package retrieval implements hybrid, multi-signal, and adaptive retrieval pipelines for Cortex.
package retrieval

import (
	"context"
	"regexp"
	"strconv"
	"strings"

	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/domain/graph"
)

// QueryTier defines the complexity routing level in Adaptive-RAG.
type QueryTier string

const (
	// TierDirectFactual represents direct lookups (symbols, IDs, exact keywords) ~1-5ms.
	TierDirectFactual QueryTier = "direct_factual"
	// TierSemanticHybrid represents conceptual queries combining lexical + dense vectors.
	TierSemanticHybrid QueryTier = "semantic_hybrid"
	// TierMultiHopGraph represents complex relational questions resolved via HippoRAG PPR.
	TierMultiHopGraph QueryTier = "multi_hop_graph"
	// TierArchitecturalGlobal represents macro architectural questions resolved via LightRAG Community Summaries.
	TierArchitecturalGlobal QueryTier = "architectural_global"
)

var (
	// Patterns indicating direct code or identifier lookups
	directLookupRegex = regexp.MustCompile(`^(func|struct|type|class|interface|const|var)\s+|^[a-zA-Z0-9_-]+\.[a-zA-Z0-9]+$|^#[0-9]+$|^[a-z0-9_]{3,32}$`)

	// Keywords indicating macro architectural / community overviews (LightRAG)
	architecturalKeywords = []string{
		"architecture", "arquitectura", "overview", "resumen general", "structure", "estructura",
		"módulos", "modules", "high level", "alto nivel", "communities", "comunidades", "explain system",
	}

	// Keywords indicating multi-hop relational or dependency reasoning (HippoRAG)
	multiHopKeywords = []string{
		"why", "por que", "por qué", "impact", "impacto", "depend", "dependencia",
		"connect", "conecta", "relat", "relacion", "relación", "caused by", "causa",
		"flow", "flujo", "workflow", "cycle", "ciclo", "trace", "trazabilidad",
		"blast radius", "blast_radius", "break", "rompe", "afecta", "afectado",
	}
)

// ClassifyQueryComplexity routes a query to the optimal retrieval tier in < 0.1ms.
func ClassifyQueryComplexity(query string) QueryTier {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return TierDirectFactual
	}

	lower := strings.ToLower(trimmed)

	// 1. Check for Multi-Hop Graph reasoning indicators (HippoRAG) - highest analytical precedence
	for _, kw := range multiHopKeywords {
		if strings.Contains(lower, kw) {
			return TierMultiHopGraph
		}
	}

	// 2. Check for Macro Architectural indicators (LightRAG)
	for _, kw := range architecturalKeywords {
		if strings.Contains(lower, kw) {
			return TierArchitecturalGlobal
		}
	}

	// 3. Check for direct symbol/identifier lookup
	if directLookupRegex.MatchString(trimmed) || (!strings.Contains(trimmed, " ") && len(trimmed) < 40) {
		return TierDirectFactual
	}

	// 4. Default to standard semantic hybrid
	return TierSemanticHybrid
}

// AdaptiveSearchOptions controls the adaptive retrieval engine.
type AdaptiveSearchOptions struct {
	Mode         string // "auto", "direct", "semantic", "multi_hop"
	Project      string
	Scope        string
	Types        []string
	Limit        int
	GraphNodes   []graph.GraphAnalyticsNode
	GraphEdges   []graph.GraphAnalyticsEdge
	CRAGConfig   *CRAGConfig
}

// AdaptiveSearchResult represents the enriched search output with RAG metadata.
type AdaptiveSearchResult struct {
	Tier            QueryTier                `json:"tier"`
	Confidence      ConfidenceGrade          `json:"confidence"`
	ConfidenceScore float64                  `json:"confidence_score"`
	NeedsRefinement bool                     `json:"needs_refinement"`
	Results         []*domain.SearchResult  `json:"results"`
}

// ExecuteAdaptiveSearch runs the adaptive RAG pipeline, dynamically selecting
// between direct lexical search, semantic hybrid vectors, and HippoRAG graph propagation.
func ExecuteAdaptiveSearch(
	ctx context.Context,
	query string,
	opts AdaptiveSearchOptions,
	lexicalSearch func(ctx context.Context, q domain.SearchOptions) ([]*domain.SearchResult, error),
	vectorSearch func(ctx context.Context, q domain.VectorQuery) ([]*domain.VectorSearchResult, error),
) (*AdaptiveSearchResult, error) {
	var tier QueryTier

	if opts.Mode != "" && opts.Mode != "auto" {
		switch opts.Mode {
		case "direct":
			tier = TierDirectFactual
		case "semantic":
			tier = TierSemanticHybrid
		case "multi_hop":
			tier = TierMultiHopGraph
		default:
			tier = ClassifyQueryComplexity(query)
		}
	} else {
		tier = ClassifyQueryComplexity(query)
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = 10
	}

	var results []*domain.SearchResult

	switch tier {
	case TierDirectFactual:
		// Fast direct lexical path
		if lexicalSearch != nil {
			sq := domain.SearchOptions{
				Query:   query,
				Project: opts.Project,
				Scope:   opts.Scope,
				Limit:   limit,
			}
			lexResults, err := lexicalSearch(ctx, sq)
			if err == nil {
				results = lexResults
			}
		}

	case TierArchitecturalGlobal:
		// LightRAG Path: Retrieve and prioritize community summaries and architectural overviews
		if lexicalSearch != nil {
			sq := domain.SearchOptions{
				Query:   query,
				Project: opts.Project,
				Scope:   opts.Scope,
				Limit:   limit * 2,
			}
			lexResults, err := lexicalSearch(ctx, sq)
			if err == nil {
				for _, r := range lexResults {
					if r.Type == "community_summary" || r.Type == "pattern" || r.Type == "decision" {
						r.Rank += 2.5 // Boost macro architectural nodes
					}
					results = append(results, r)
				}
			}
		}

	case TierMultiHopGraph:
		// HippoRAG Path: Seed with lexical/vector hits, then propagate along knowledge graph
		var candidateMap = make(map[int64]*domain.SearchResult)
		var seeds = make(map[string]float64)

		if lexicalSearch != nil {
			sq := domain.SearchOptions{
				Query:   query,
				Project: opts.Project,
				Scope:   opts.Scope,
				Limit:   limit * 2,
			}
			lex, _ := lexicalSearch(ctx, sq)
			for _, r := range lex {
				candidateMap[r.ID] = r
				nodeKey := strconv.FormatInt(r.ID, 10)
				seeds[nodeKey] += r.Rank
			}
		}

		if len(opts.GraphNodes) > 0 && len(opts.GraphEdges) > 0 && len(seeds) > 0 {
			// Propagate via HippoRAG Personalized PageRank in memory
			pprScores := graph.ComputePersonalizedPageRank(opts.GraphNodes, opts.GraphEdges, seeds, graph.DefaultPPROptions())
			for _, r := range candidateMap {
				nodeKey := strconv.FormatInt(r.ID, 10)
				if pprBoost, ok := pprScores[nodeKey]; ok {
					r.Rank += pprBoost * 2.0 // Boost topologically relevant multi-hop nodes
				}
			}
		}

		for _, r := range candidateMap {
			results = append(results, r)
		}

	default: // TierSemanticHybrid
		if lexicalSearch != nil {
			sq := domain.SearchOptions{
				Query:   query,
				Project: opts.Project,
				Scope:   opts.Scope,
				Limit:   limit,
			}
			lexResults, _ := lexicalSearch(ctx, sq)
			results = lexResults
		}
	}

	// Apply CRAG confidence gating
	cragCfg := DefaultCRAGConfig()
	if opts.CRAGConfig != nil {
		cragCfg = *opts.CRAGConfig
	}

	cragEval := EvaluateCRAG(results, cragCfg)

	return &AdaptiveSearchResult{
		Tier:            tier,
		Confidence:      cragEval.Grade,
		ConfidenceScore: cragEval.Confidence,
		NeedsRefinement: cragEval.NeedsRefinement,
		Results:         cragEval.FilteredResults,
	}, nil
}
