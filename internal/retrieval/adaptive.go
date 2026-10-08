// Package retrieval implements hybrid, multi-signal, and adaptive retrieval pipelines for Cortex.
package retrieval

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/domain/graph"
)

// FusionRouteFeatures holds training-free routing signals derived from an RRF
// fusion score distribution (SkewRoute style, REQ-ROUTE-001).
type FusionRouteFeatures struct {
	Skewness   float64 `json:"skewness"`
	Top1Margin float64 `json:"top1_margin"`
	Available  bool    `json:"available"`
}

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
	// Patterns indicating direct code, symbol, or identifier lookups
	directLookupRegex = regexp.MustCompile(`^(?i)(func|struct|type|class|interface|const|var)\s+|^[a-zA-Z0-9_.-]+\.[a-zA-Z0-9]+$|^#[0-9]+$|^[a-zA-Z0-9_]{2,40}(\(\))?$`)

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

// tierCostOrder ranks retrieval tiers from cheapest to most expensive.
var tierCostOrder = [...]QueryTier{TierDirectFactual, TierSemanticHybrid, TierMultiHopGraph, TierArchitecturalGlobal}

func costRank(t QueryTier) int {
	for i, tier := range tierCostOrder {
		if tier == t {
			return i
		}
	}
	return -1
}

// Routing thresholds for the SkewRoute-style decision (REQ-ROUTE-001):
// a strongly peaked fusion distribution with a dominant top-1 result skips
// expensive tiers; a flat, near-tied distribution escalates one tier.
const (
	peakedMinSkewness   = 1.0
	peakedMinTop1Margin = 0.5
	flatMaxSkewness     = 0.5
	flatMaxTop1Margin   = 0.2
)

// ComputeFusionFeatures derives training-free SkewRoute routing signals from an
// RRF fusion score distribution: Pearson skewness of the score distribution and
// the relative top-1 margin (s1 - s2) / s1. Scores are copied before sorting so
// the caller's slice is never reordered. Non-finite scores or fewer than two
// candidates yield unavailable features, which callers treat as "fall back to
// the heuristic".
func ComputeFusionFeatures(scores []float64) FusionRouteFeatures {
	if len(scores) < 2 {
		return FusionRouteFeatures{}
	}
	for _, s := range scores {
		if math.IsNaN(s) || math.IsInf(s, 0) {
			return FusionRouteFeatures{}
		}
	}

	ordered := make([]float64, len(scores))
	copy(ordered, scores)
	sort.Float64s(ordered)

	top1 := ordered[len(ordered)-1]
	top2 := ordered[len(ordered)-2]
	margin := 0.0
	if top1 != 0 {
		margin = (top1 - top2) / top1
	}

	var sum, sumSq, sumCube float64
	for _, s := range ordered {
		sum += s
	}
	mean := sum / float64(len(ordered))
	for _, s := range ordered {
		d := s - mean
		sumSq += d * d
		sumCube += d * d * d
	}
	variance := sumSq / float64(len(ordered))
	skewness := 0.0
	if variance > 0 {
		skewness = (sumCube / float64(len(ordered))) / math.Pow(variance, 1.5)
	}

	return FusionRouteFeatures{
		Skewness:   skewness,
		Top1Margin: margin,
		Available:  true,
	}
}

// ClassifyQueryWithFusion routes a query using fusion features when available,
// otherwise it falls back to the text heuristic unchanged.
func ClassifyQueryWithFusion(query string, features FusionRouteFeatures) QueryTier {
	base := ClassifyQueryComplexity(query)
	if !features.Available {
		return base
	}
	baseRank := costRank(base)
	if baseRank < 0 {
		return base
	}

	peaked := features.Skewness >= peakedMinSkewness && features.Top1Margin >= peakedMinTop1Margin
	if peaked {
		// Confident fusion: cap at the cheapest tier that still runs vector
		// search, skipping graph PPR and community-summary tiers.
		if baseRank > costRank(TierSemanticHybrid) {
			return TierSemanticHybrid
		}
		return base
	}

	flat := features.Skewness <= flatMaxSkewness && features.Top1Margin <= flatMaxTop1Margin
	if flat && baseRank < len(tierCostOrder)-1 {
		return tierCostOrder[baseRank+1]
	}

	return base
}

// HyDEAllowedForTier reports whether the retrieval tier may engage HyDE
// hypothetical-document generation (REQ-RET-108). Only high-uncertainty
// conceptual tiers qualify: TierSemanticHybrid and TierMultiHopGraph.
// TierDirectFactual (cheap lookups) and TierArchitecturalGlobal (community
// summaries) NEVER trigger a generation call, regardless of the feature flag.
func HyDEAllowedForTier(tier QueryTier) bool {
	switch tier {
	case TierSemanticHybrid, TierMultiHopGraph:
		return true
	default:
		return false
	}
}

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
	Mode string // "auto", "direct", "semantic", "multi_hop"
	// FusionScores are the RRF fusion scores of a prior retrieval stage. When
	// present and well-formed they drive SkewRoute-style tier routing in auto
	// mode; otherwise the heuristic classifier runs unchanged.
	FusionScores []float64
	// QueryVector is the query embedding for the multi-hop tier's dense leg.
	// When empty the tier seeds propagation from lexical evidence only and
	// omits the dense fusion input instead of failing.
	QueryVector []float32
	Project     string
	Scope       string
	Types       []string
	Limit       int
	GraphNodes  []graph.GraphAnalyticsNode
	GraphEdges  []graph.GraphAnalyticsEdge
	CRAGConfig  *CRAGConfig
}

// AdaptiveSearchResult represents the enriched search output with RAG metadata.
type AdaptiveSearchResult struct {
	Tier            QueryTier              `json:"tier"`
	Confidence      ConfidenceGrade        `json:"confidence"`
	ConfidenceScore float64                `json:"confidence_score"`
	NeedsRefinement bool                   `json:"needs_refinement"`
	Results         []*domain.SearchResult `json:"results"`
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
		tier = ClassifyQueryWithFusion(query, ComputeFusionFeatures(opts.FusionScores))
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
		results = executeMultiHopTier(ctx, query, opts, limit, lexicalSearch, vectorSearch)

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
		if len(results) == 0 {
			results = semanticDenseFallback(ctx, query, opts, limit, vectorSearch)
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

// executeMultiHopTier runs the HippoRAG 2 multi-hop tier: lexical hits seed a
// PPR pass fused with dense similarity evidence, the recognition filter prunes
// weak post-PPR candidates while refilling the requested top-k, the survivors
// PLUS the dense-derived pool go through the Chain-of-Note reading stage, and
// the note-ranked list fuses as a third position-only RRF input alongside the
// dense leg (REQ-LME-001).
// RunHippoRAG2 never errors; a sparse or empty graph reports Applied=false and
// the tier keeps the legacy dense-plus-lexical fusion path.
func executeMultiHopTier(
	ctx context.Context,
	query string,
	opts AdaptiveSearchOptions,
	limit int,
	lexicalSearch func(context.Context, domain.SearchOptions) ([]*domain.SearchResult, error),
	vectorSearch func(context.Context, domain.VectorQuery) ([]*domain.VectorSearchResult, error),
) []*domain.SearchResult {
	lexicalHits := searchMultiHopLexical(ctx, query, opts, limit*2, lexicalSearch)
	denseHits := searchMultiHopDense(ctx, opts, limit*multiHopDensePoolFactor, vectorSearch)

	candidates := cloneSearchResults(lexicalHits)
	propagateMultiHopGraph(query, opts, candidates, denseHits)

	recognition := DefaultRecognitionConfig()
	recognition.TopK = limit
	recognized := ApplyRecognitionFilter(candidates, recognition)

	readingInput := recognized
	if !speculativeQuery(query) {
		readingInput = readingPool(recognized, denseHits)
	}
	_, noteRanked := ChainOfNoteStage(query, readingInput)
	return FuseResultsWithNotes(recognized, denseHits, noteRanked, FuseOptions{Limit: limit})
}

// multiHopDensePoolFactor sizes the dense pool relative to the requested
// limit. Beyond final RRF fusion (whose deep positions contribute almost
// nothing) the pool feeds the Chain-of-Note reading stage, which must SEE
// vector-only candidates the lexical leg never returned — an FTS-starved query
// would otherwise read no passage at all and emit raw dense order. Widening the
// pool past limit*2 measurably hurt the judged multi-hop slice, so the reading
// reach stops at twice the requested limit.
const multiHopDensePoolFactor = 2

// readingPool unions the recognized lexical candidates with the dense-derived
// pool as the Chain-of-Note input, deduplicated by observation ID so a passage
// present in both legs is read (and note-ranked) exactly once.
func readingPool(recognized []*domain.SearchResult, denseHits []*domain.VectorSearchResult) []*domain.SearchResult {
	pool := make([]*domain.SearchResult, 0, len(recognized)+len(denseHits))
	seen := make(map[int64]struct{}, len(recognized)+len(denseHits))
	for _, r := range recognized {
		if _, ok := seen[r.ID]; ok {
			continue
		}
		seen[r.ID] = struct{}{}
		pool = append(pool, r)
	}
	for _, vr := range vectorResultsForFusion(denseHits) {
		if _, ok := seen[vr.ID]; ok {
			continue
		}
		seen[vr.ID] = struct{}{}
		pool = append(pool, vr)
	}
	return pool
}

// semanticDenseFallback answers a semantic-hybrid query whose lexical leg
// returned nothing. Without it the tier returns an empty context and the judge
// scores an unanswerable question; with a healthy embedder the dense pool is
// the substitute, in similarity-rank order.
//
// Context is served only when the reading stage marks at least one dense
// passage answer-bearing: a pool where nothing reads as relevant to the query
// is noise, and serving it replaces an answerable empty context with
// misleading evidence. No embedder, no vector search call, no results.
func semanticDenseFallback(
	ctx context.Context,
	query string,
	opts AdaptiveSearchOptions,
	limit int,
	search func(context.Context, domain.VectorQuery) ([]*domain.VectorSearchResult, error),
) []*domain.SearchResult {
	if search == nil || len(opts.QueryVector) == 0 || speculativeQuery(query) {
		return nil
	}
	hits, err := search(ctx, domain.VectorQuery{
		Vector: opts.QueryVector,
		Limit:  limit,
		Filters: map[string]any{
			"project": opts.Project,
			"scope":   opts.Scope,
		},
	})
	if err != nil || len(hits) == 0 {
		return nil
	}
	fused := vectorResultsForFusion(hits)
	if len(fused) > limit {
		fused = fused[:limit]
	}
	notes, _ := ChainOfNoteStage(query, fused)
	for _, note := range notes {
		if note.AnswerBearing {
			return fused
		}
	}
	return nil
}

// speculativeQuery reports whether the query asks for a prediction or opinion
// rather than a verifiable fact. No passage literally answers such a query, so
// similarity-retrieved context can only masquerade as support: the reading
// stage marks topical passages answer-bearing and the answer gets anchored on
// evidence that does not answer the question, which measured as correct-to-
// incorrect flips against the empty-context behavior.
var speculativeQueryPattern = regexp.MustCompile(`\b(would|might|likely|probably)\b`)

func speculativeQuery(query string) bool {
	return speculativeQueryPattern.MatchString(strings.ToLower(query))
}

// searchMultiHopLexical gathers the lexical candidate pool; a retrieval error
// degrades to an empty pool so the tier never fails the caller.
func searchMultiHopLexical(
	ctx context.Context,
	query string,
	opts AdaptiveSearchOptions,
	limit int,
	search func(context.Context, domain.SearchOptions) ([]*domain.SearchResult, error),
) []*domain.SearchResult {
	if search == nil {
		return nil
	}
	hits, err := search(ctx, domain.SearchOptions{
		Query:   query,
		Project: opts.Project,
		Scope:   opts.Scope,
		Limit:   limit,
	})
	if err != nil {
		return nil
	}
	return hits
}

// searchMultiHopDense gathers dense vector evidence for passage seeding and
// final fusion. Without a query embedding there is nothing to search, so the
// tier degrades to lexical-only seeds instead of erroring.
func searchMultiHopDense(
	ctx context.Context,
	opts AdaptiveSearchOptions,
	limit int,
	search func(context.Context, domain.VectorQuery) ([]*domain.VectorSearchResult, error),
) []*domain.VectorSearchResult {
	if search == nil || len(opts.QueryVector) == 0 {
		return nil
	}
	hits, err := search(ctx, domain.VectorQuery{
		Vector: opts.QueryVector,
		Limit:  limit,
		Filters: map[string]any{
			"project": opts.Project,
			"scope":   opts.Scope,
		},
	})
	if err != nil {
		return nil
	}
	return hits
}

// cloneSearchResults copies candidate payloads so PPR rank boosts never mutate
// the caller-owned search results, keeping repeated adaptive runs idempotent.
func cloneSearchResults(hits []*domain.SearchResult) []*domain.SearchResult {
	if len(hits) == 0 {
		return nil
	}
	out := make([]*domain.SearchResult, len(hits))
	for i, hit := range hits {
		clone := *hit
		out[i] = &clone
	}
	return out
}

// propagateMultiHopGraph builds the joint knowledge graph, seeds it with fused
// dense-plus-lexical passage evidence and query-named concepts, and boosts
// candidate ranks with the propagated passage scores. A missing or sparse graph
// reports Applied=false and leaves every rank untouched, so the caller keeps
// the legacy dense-plus-lexical path without an error.
func propagateMultiHopGraph(
	query string,
	opts AdaptiveSearchOptions,
	candidates []*domain.SearchResult,
	denseHits []*domain.VectorSearchResult,
) {
	if len(opts.GraphNodes) == 0 || len(opts.GraphEdges) == 0 || len(candidates) == 0 {
		return
	}

	// Heterogeneous bipartite graph: knowledge/code symbol nodes plus the
	// retrieved observation passages that mention them.
	jointNodes := make([]graph.GraphAnalyticsNode, len(opts.GraphNodes), len(opts.GraphNodes)+len(candidates))
	copy(jointNodes, opts.GraphNodes)

	jointEdges := make([]graph.GraphAnalyticsEdge, len(opts.GraphEdges), len(opts.GraphEdges)+(len(candidates)*4))
	copy(jointEdges, opts.GraphEdges)

	symbolLookup := make(map[string]string)
	for _, gn := range opts.GraphNodes {
		if gn.Label != "" && len(gn.Label) >= 3 {
			symbolLookup[strings.ToLower(gn.Label)] = gn.ID
		}
		if gn.ID != "" && len(gn.ID) >= 3 {
			symbolLookup[strings.ToLower(gn.ID)] = gn.ID
		}
	}

	for _, candidate := range candidates {
		obsNodeID := fmt.Sprintf("obs:%d", candidate.ID)
		jointNodes = append(jointNodes, graph.GraphAnalyticsNode{
			ID:    obsNodeID,
			Label: candidate.Title,
			Kind:  graph.NodeKindObservation,
		})

		jointEdges = append(jointEdges, graph.GraphAnalyticsEdge{
			Source: obsNodeID,
			Target: strconv.FormatInt(candidate.ID, 10),
			Type:   domain.RelationRelatesTo,
			Weight: 1.0,
		})

		contentLower := strings.ToLower(candidate.Title + " " + candidate.TopicKey + " " + candidate.Content)
		for symText, targetNodeID := range symbolLookup {
			if strings.Contains(contentLower, symText) {
				jointEdges = append(jointEdges, graph.GraphAnalyticsEdge{
					Source: obsNodeID,
					Target: targetNodeID,
					Type:   graph.EdgeTypeMentions,
					Weight: 1.5,
				})
			}
		}
	}

	denseByID := densePassageScores(denseHits)
	passageSeeds := make([]graph.PassageSeed, 0, len(candidates))
	for _, candidate := range candidates {
		passageSeeds = append(passageSeeds, graph.PassageSeed{
			PassageID:    candidate.ID,
			DenseScore:   denseByID[candidate.ID],
			LexicalScore: candidate.Rank,
		})
	}

	pprOpts := graph.DefaultPPROptions()
	pprOpts.Directed = false // Associative reasoning flows bidirectionally between code & docs

	outcome := graph.RunHippoRAG2(graph.HippoRAG2Request{
		Nodes:        jointNodes,
		Edges:        jointEdges,
		PassageSeeds: passageSeeds,
		ConceptSeeds: conceptSeedsFromQuery(query, symbolLookup),
		Options:      pprOpts,
	})
	if !outcome.Applied {
		return
	}

	for _, candidate := range candidates {
		if score := outcome.PassageScores[candidate.ID]; score > 0 {
			candidate.Rank += score * 2.0 // Boost topologically relevant multi-hop nodes
		}
	}
}

// densePassageScores indexes dense vector similarity per observation ID so a
// passage's PPR seed weight fuses its dense and lexical evidence.
func densePassageScores(denseHits []*domain.VectorSearchResult) map[int64]float64 {
	scores := make(map[int64]float64, len(denseHits))
	for _, hit := range denseHits {
		if hit.Similarity > scores[hit.ID] {
			scores[hit.ID] = hit.Similarity
		}
	}
	return scores
}

// conceptSeedsFromQuery seeds the graph symbols the query names explicitly,
// linking query entities into the same PPR pass as passage evidence.
func conceptSeedsFromQuery(query string, symbolLookup map[string]string) map[string]float64 {
	lowered := strings.ToLower(query)
	seeds := make(map[string]float64)
	for term, nodeID := range symbolLookup {
		if strings.Contains(lowered, term) {
			seeds[nodeID] = 1.0
		}
	}
	return seeds
}
