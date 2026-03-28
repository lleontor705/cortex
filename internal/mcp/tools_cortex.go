package mcp

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/lleontor705/cortex/internal/domain"
	graphdomain "github.com/lleontor705/cortex/internal/domain/graph"
	scoringdomain "github.com/lleontor705/cortex/internal/domain/scoring"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// registerCortexTools registers the 5 Cortex-exclusive MCP tools.
func registerCortexTools(srv *server.MCPServer, stores *Stores, allowlist map[string]bool) {
	// ─── mem_relate ─────────────────────────────────────────────────────
	if shouldRegister("mem_relate", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_relate",
				mcp.WithTitleAnnotation("Relate Observations"),
				mcp.WithReadOnlyHintAnnotation(false),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithIdempotentHintAnnotation(true),
				mcp.WithOpenWorldHintAnnotation(false),
				mcp.WithDescription("Create a typed relationship between two observations in the knowledge graph."),
				mcp.WithNumber("from_id",
					mcp.Required(),
					mcp.Description("Source observation ID"),
				),
				mcp.WithNumber("to_id",
					mcp.Required(),
					mcp.Description("Target observation ID"),
				),
				mcp.WithString("relation_type",
					mcp.Required(),
					mcp.Description("Relationship type: references, relates_to, follows, supersedes, contradicts"),
				),
				mcp.WithNumber("weight",
					mcp.Description("Relationship strength 0.0-10.0 (default: 1.0)"),
				),
			),
			handleRelate(stores),
		)
	}

	// ─── mem_graph ──────────────────────────────────────────────────────
	if shouldRegister("mem_graph", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_graph",
				mcp.WithTitleAnnotation("Traverse Knowledge Graph"),
				mcp.WithReadOnlyHintAnnotation(true),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithIdempotentHintAnnotation(true),
				mcp.WithOpenWorldHintAnnotation(false),
				mcp.WithDescription("Traverse the knowledge graph starting from an observation. Returns related observations up to the specified depth."),
				mcp.WithNumber("observation_id",
					mcp.Required(),
					mcp.Description("Starting observation ID"),
				),
				mcp.WithNumber("depth",
					mcp.Description("Traversal depth 1-10 (default: 1)"),
				),
			),
			handleGraph(stores),
		)
	}

	// ─── mem_score ──────────────────────────────────────────────────────
	if shouldRegister("mem_score", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_score",
				mcp.WithTitleAnnotation("Get Importance Score"),
				mcp.WithReadOnlyHintAnnotation(true),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithIdempotentHintAnnotation(true),
				mcp.WithOpenWorldHintAnnotation(false),
				mcp.WithDescription("Get or recalculate the importance score for an observation. Score formula: base + accessBonus + recencyBonus + edgeBonus + typeBonus - agePenalty, clamped to [0.0, 5.0]."),
				mcp.WithNumber("observation_id",
					mcp.Required(),
					mcp.Description("Observation ID to score"),
				),
				mcp.WithBoolean("recalculate",
					mcp.Description("Force recalculation (default: false)"),
				),
			),
			handleScore(stores),
		)
	}

	// ─── mem_archive ────────────────────────────────────────────────────
	if shouldRegister("mem_archive", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_archive",
				mcp.WithTitleAnnotation("Archive Observation"),
				mcp.WithReadOnlyHintAnnotation(false),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithIdempotentHintAnnotation(true),
				mcp.WithOpenWorldHintAnnotation(false),
				mcp.WithDescription("Archive an observation by soft-deleting it. Archived observations can still be found with include_archived searches."),
				mcp.WithNumber("observation_id",
					mcp.Required(),
					mcp.Description("Observation ID to archive"),
				),
			),
			handleArchive(stores),
		)
	}

	// ─── mem_search_hybrid ──────────────────────────────────────────────
	if shouldRegister("mem_search_hybrid", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_search_hybrid",
				mcp.WithTitleAnnotation("Hybrid Search"),
				mcp.WithReadOnlyHintAnnotation(true),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithIdempotentHintAnnotation(true),
				mcp.WithOpenWorldHintAnnotation(false),
				mcp.WithDescription("Search using FTS5 full-text search. When vector search is enabled, combines FTS5 and vector results using Reciprocal Rank Fusion. Falls back to FTS5-only when vectors are disabled."),
				mcp.WithString("query",
					mcp.Required(),
					mcp.Description("Search query — natural language or keywords"),
				),
				mcp.WithString("project",
					mcp.Description("Filter by project name"),
				),
				mcp.WithString("scope",
					mcp.Description("Filter by scope: project or personal"),
				),
				mcp.WithNumber("limit",
					mcp.Description("Max results (default: 10, max: 50)"),
				),
			),
			handleSearchHybrid(stores),
		)
	}
}

// ─── Cortex Tool Handlers ───────────────────────────────────────────────────

func handleRelate(stores *Stores) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		fromID := int64(intArg(req, "from_id", 0))
		toID := int64(intArg(req, "to_id", 0))
		relationType := stringArg(req, "relation_type")
		weight := floatArg(req, "weight", 1.0)

		if fromID == 0 || toID == 0 {
			return errorResult("from_id and to_id are required")
		}
		if relationType == "" {
			return errorResult("relation_type is required — use: references, relates_to, follows, supersedes, contradicts")
		}

		svc := graphdomain.NewService(stores.Graph)
		edge := &domain.Edge{
			FromObsID:    fromID,
			ToObsID:      toID,
			RelationType: relationType,
			Weight:       weight,
		}

		if err := svc.CreateEdge(ctx, edge); err != nil {
			return errorResult("Failed to create relationship: %s", err)
		}

		return textResult("Relationship created: observation %d -[%s]-> observation %d (weight: %.1f, edge ID: %d)",
			fromID, relationType, toID, weight, edge.ID)
	}
}

func handleGraph(stores *Stores) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		obsID := int64(intArg(req, "observation_id", 0))
		depth := intArg(req, "depth", 1)

		if obsID == 0 {
			return errorResult("observation_id is required")
		}

		svc := graphdomain.NewService(stores.Graph)
		related, err := svc.GetRelated(ctx, obsID, depth)
		if err != nil {
			return errorResult("Failed to traverse graph: %s", err)
		}

		if len(related) == 0 {
			return textResult("No related observations found for ID %d (depth: %d)", obsID, depth)
		}

		var sb strings.Builder
		fmt.Fprintf(&sb, "Related observations for ID %d (depth: %d, found: %d):\n\n", obsID, depth, len(related))
		for _, obs := range related {
			fmt.Fprintf(&sb, "- [%d] %s (%s) — %s\n", obs.ID, obs.Title, obs.Type, truncate(obs.Content, 80))
		}

		return textResult("%s", sb.String())
	}
}

func handleScore(stores *Stores) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		obsID := int64(intArg(req, "observation_id", 0))
		recalculate := boolArg(req, "recalculate", false)

		if obsID == 0 {
			return errorResult("observation_id is required")
		}

		svc := scoringdomain.NewService(stores.Scoring)

		if recalculate {
			score, err := svc.CalculateScore(ctx, obsID)
			if err != nil {
				return errorResult("Failed to recalculate score: %s", err)
			}
			return textResult("Score recalculated for observation %d: %.2f", obsID, score)
		}

		score, err := svc.GetScore(ctx, obsID)
		if err != nil {
			return errorResult("Failed to get score: %s", err)
		}

		return textResult("Observation %d — score: %.2f, access_count: %d, last_accessed: %s",
			obsID, score.Score, score.AccessCount, score.LastAccessed.Format("2006-01-02 15:04:05"))
	}
}

func handleArchive(stores *Stores) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		obsID := int64(intArg(req, "observation_id", 0))

		if obsID == 0 {
			return errorResult("observation_id is required")
		}

		if err := stores.Observations.Delete(ctx, obsID); err != nil {
			return errorResult("Failed to archive observation: %s", err)
		}

		return textResult("Observation %d archived (soft-deleted)", obsID)
	}
}

func handleSearchHybrid(stores *Stores) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		query := stringArg(req, "query")
		project := stringArg(req, "project")
		scope := stringArg(req, "scope")
		limit := intArg(req, "limit", 10)

		if query == "" {
			return errorResult("query is required")
		}
		if limit <= 0 {
			limit = 10
		}
		if limit > 50 {
			limit = 50
		}

		// FTS5 search (always available)
		opts := domain.SearchOptions{
			Query:   query,
			Project: project,
			Scope:   scope,
			Limit:   limit,
		}

		ftsResults, err := stores.Search.Search(ctx, query, opts)
		if err != nil {
			return errorResult("FTS5 search failed: %s", err)
		}

		searchMode := "FTS5"

		// Vector search (when available and embeddings exist)
		if stores.Vectors != nil && stores.Vectors.IsAvailable() {
			searchMode = "hybrid (FTS5 + vector)"

			// Try to find a matching observation's embedding to use as query vector.
			// In a full implementation, an embedding service would generate the query
			// vector. For now, vector results boost FTS5 results when embeddings exist.
			if len(ftsResults) > 0 {
				embedding, _, embErr := stores.Vectors.GetEmbedding(ctx, ftsResults[0].ID)
				if embErr == nil && len(embedding) > 0 {
					vecOpts := domain.VectorSearchOptions{
						Embedding: embedding,
						Limit:     limit,
						Threshold: 0.3,
						Project:   project,
						Scope:     scope,
					}
					vecResults, vecErr := stores.Vectors.SearchByVector(ctx, vecOpts)
					if vecErr == nil && len(vecResults) > 0 {
						ftsResults = fuseResults(ftsResults, vecResults, limit)
					}
				}
			}
		}

		if len(ftsResults) == 0 {
			return textResult("No results found for %q", query)
		}

		var sb strings.Builder
		fmt.Fprintf(&sb, "Search results for %q [%s] (%d found):\n\n", query, searchMode, len(ftsResults))
		for i, r := range ftsResults {
			fmt.Fprintf(&sb, "%d. [%d] %s (%s, rank: %.2f)\n   %s\n\n",
				i+1, r.ID, r.Title, r.Type, r.Rank, truncate(r.Content, 120))
		}

		return textResult("%s", sb.String())
	}
}

// fuseResults combines FTS5 and vector search results using Reciprocal Rank Fusion (k=60).
func fuseResults(ftsResults []*domain.SearchResult, vecResults []*domain.VectorSearchResult, limit int) []*domain.SearchResult {
	const k = 60.0

	type scored struct {
		result *domain.SearchResult
		score  float64
	}

	scoreMap := make(map[int64]*scored)

	// Score FTS5 results
	for rank, r := range ftsResults {
		scoreMap[r.ID] = &scored{
			result: r,
			score:  1.0 / (k + float64(rank+1)),
		}
	}

	// Add vector result scores
	for rank, vr := range vecResults {
		rrf := 1.0 / (k + float64(rank+1))
		if existing, ok := scoreMap[vr.ID]; ok {
			existing.score += rrf
		} else {
			scoreMap[vr.ID] = &scored{
				result: &domain.SearchResult{
					Observation: vr.Observation,
					Rank:        vr.Similarity,
				},
				score: rrf,
			}
		}
	}

	// Sort by RRF score
	sorted := make([]*scored, 0, len(scoreMap))
	for _, s := range scoreMap {
		sorted = append(sorted, s)
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].score > sorted[j].score
	})

	results := make([]*domain.SearchResult, 0, limit)
	for i, s := range sorted {
		if i >= limit {
			break
		}
		results = append(results, s.result)
	}

	return results
}

// floatArg extracts a float64 argument with a default value.
func floatArg(req mcp.CallToolRequest, key string, defaultVal float64) float64 {
	v, ok := req.GetArguments()[key].(float64)
	if !ok {
		return defaultVal
	}
	return v
}
