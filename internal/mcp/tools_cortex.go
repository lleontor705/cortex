package mcp

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/lleontor705/cortex/internal/domain"
	"github.com/lleontor705/cortex/internal/domain/dna"
	graphdomain "github.com/lleontor705/cortex/internal/domain/graph"
	scoringdomain "github.com/lleontor705/cortex/internal/domain/scoring"
	"github.com/lleontor705/cortex/internal/retrieval"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// registerCortexTools registers Cortex-native MCP tools (graph, scoring,
// search, consolidation, and admin tools) in the cortex_* namespace.
func registerCortexTools(srv *server.MCPServer, stores *Stores, allowlist map[string]bool) {
	// --- cortex_relate --------------------------------------------------
	if shouldRegister("cortex_relate", allowlist) {
		srv.AddTool(
			mcp.NewTool("cortex_relate",
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
				mcp.WithNumber("confidence",
					mcp.Description("Confidence in this relationship 0.0-1.0 (default: 1.0)"),
				),
				mcp.WithString("source",
					mcp.Description("Who/what created this edge (e.g. 'ai', 'manual')"),
				),
				mcp.WithString("reasoning",
					mcp.Description("Why this relationship exists"),
				),
			),
			handleRelate(stores),
		)
	}

	// --- cortex_graph ---------------------------------------------------
	if shouldRegister("cortex_graph", allowlist) {
		srv.AddTool(
			mcp.NewTool("cortex_graph",
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

	// --- cortex_score ---------------------------------------------------
	if shouldRegister("cortex_score", allowlist) {
		srv.AddTool(
			mcp.NewTool("cortex_score",
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

	// --- cortex_archive -------------------------------------------------
	if shouldRegister("cortex_archive", allowlist) {
		srv.AddTool(
			mcp.NewTool("cortex_archive",
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

	// --- cortex_search_hybrid -------------------------------------------
	if shouldRegister("cortex_search_hybrid", allowlist) {
		srv.AddTool(
			mcp.NewTool("cortex_search_hybrid",
				mcp.WithTitleAnnotation("Hybrid Search"),
				mcp.WithReadOnlyHintAnnotation(true),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithIdempotentHintAnnotation(true),
				mcp.WithOpenWorldHintAnnotation(false),
				mcp.WithDescription("Search using FTS5 full-text search. When vector search is enabled, combines FTS5 and vector results using Reciprocal Rank Fusion. Falls back to FTS5-only when vectors are disabled."),
				mcp.WithString("query",
					mcp.Required(),
					mcp.Description("Search query -- natural language or keywords"),
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

	// --- cortex_search_temporal -----------------------------------------
	if shouldRegister("cortex_search_temporal", allowlist) {
		srv.AddTool(
			mcp.NewTool("cortex_search_temporal",
				mcp.WithTitleAnnotation("Temporal Search"),
				mcp.WithReadOnlyHintAnnotation(true),
				mcp.WithDescription("Search memories as-of a specific date. Graph expansion only follows edges that were valid at that time. Observations created after that date are excluded from graph neighbors."),
				mcp.WithString("query",
					mcp.Required(),
					mcp.Description("Search query"),
				),
				mcp.WithString("as_of",
					mcp.Required(),
					mcp.Description("ISO 8601 timestamp — search as if it were this date (e.g. 2025-06-15T00:00:00Z)"),
				),
				mcp.WithString("project",
					mcp.Description("Filter by project name"),
				),
				mcp.WithNumber("limit",
					mcp.Description("Max results (default: 10)"),
				),
			),
			handleSearchTemporal(stores),
		)
	}

	// --- cortex_consolidate ---------------------------------------------
	if shouldRegister("cortex_consolidate", allowlist) {
		srv.AddTool(
			mcp.NewTool("cortex_consolidate",
				mcp.WithTitleAnnotation("Consolidate Memories"),
				mcp.WithReadOnlyHintAnnotation(true),
				mcp.WithDescription("Find observations that share the same topic_key and could be consolidated. Returns candidates grouped by topic key. Use cortex_save with the same topic_key to create a merged observation, then cortex_relate with 'supersedes' to link old ones."),
				mcp.WithString("project",
					mcp.Required(),
					mcp.Description("Project to search for consolidation candidates"),
				),
				mcp.WithString("topic_key",
					mcp.Description("Specific topic key to consolidate. If empty, returns all groups with 2+ observations."),
				),
			),
			handleConsolidate(stores),
		)
	}

	// --- cortex_project_dna ---------------------------------------------
	if shouldRegister("cortex_project_dna", allowlist) {
		srv.AddTool(
			mcp.NewTool("cortex_project_dna",
				mcp.WithTitleAnnotation("Project DNA"),
				mcp.WithReadOnlyHintAnnotation(true),
				mcp.WithDescription("Generate a structured summary of a project's key decisions, patterns, tech stack, and gotchas from stored observations. Useful for onboarding or context recovery."),
				mcp.WithString("project",
					mcp.Required(),
					mcp.Description("Project name to generate DNA for"),
				),
			),
			handleProjectDNA(stores),
		)
	}

	// --- cortex_merge_projects (admin) ---
	if shouldRegister("cortex_merge_projects", allowlist) {
		srv.AddTool(
			mcp.NewTool("cortex_merge_projects",
				mcp.WithDescription("Merge memories from multiple project name variants into one canonical name. Use when project names have drifted (e.g., 'MyApp', 'myapp', 'my-app' should all be 'myapp')."),
				mcp.WithDestructiveHintAnnotation(true),
				mcp.WithString("from",
					mcp.Required(),
					mcp.Description("Comma-separated list of project names to merge FROM"),
				),
				mcp.WithString("to",
					mcp.Required(),
					mcp.Description("The canonical project name to merge INTO"),
				),
			),
			handleMergeProjects(stores),
		)
	}
}

// --- Cortex Tool Handlers ---------------------------------------------------

func handleRelate(stores *Stores) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		fromID := int64(intArg(req, "from_id", 0))
		toID := int64(intArg(req, "to_id", 0))
		relationType := stringArg(req, "relation_type")
		weight := floatArg(req, "weight", 1.0)
		confidence := floatArg(req, "confidence", 1.0)
		source := stringArg(req, "source")
		reasoning := stringArg(req, "reasoning")

		if fromID == 0 || toID == 0 {
			return errorResult("from_id and to_id are required")
		}
		if relationType == "" {
			return errorResult("relation_type is required -- use: references, relates_to, follows, supersedes, contradicts")
		}
		if weight < 0 || weight > 10 {
			return errorResult("weight must be between 0.0 and 10.0")
		}
		if confidence < 0 || confidence > 1 {
			return errorResult("confidence must be between 0.0 and 1.0")
		}

		svc := graphdomain.NewService(stores.Graph)
		edge := &domain.Edge{
			FromObsID:    fromID,
			ToObsID:      toID,
			RelationType: relationType,
			Weight:       weight,
			Confidence:   confidence,
			Source:       source,
			Reasoning:    reasoning,
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
			fmt.Fprintf(&sb, "- [%d] %s (%s) -- %s\n", obs.ID, obs.Title, obs.Type, truncate(obs.Content, 80))
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

		return textResult("Observation %d -- score: %.2f, access_count: %d, last_accessed: %s",
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

		// Vector search (when available). W8.1: stores.Vectors is a
		// domain.VectorIndex; availability is checked via Health.
		if domain.IsVectorIndexHealthy(ctx, stores.Vectors) {
			var queryVec []float32

			// Prefer generating a real query embedding via the embedding service
			if stores.Embeddings != nil {
				var embedErr error
				queryVec, embedErr = stores.Embeddings.Embed(ctx, query)
				if embedErr != nil {
					log.Printf("warning: hybrid search embed failed, falling back to FTS5: %v", embedErr)
				}
			}

			if len(queryVec) > 0 {
				searchMode = "hybrid (FTS5 + vector)"
				vecQuery := domain.VectorQuery{
					Vector:    queryVec,
					Limit:     limit,
					Threshold: 0.3,
					Filters: map[string]any{
						"project": project,
						"scope":   scope,
					},
				}
				// W8.4: use the capability-driven SearchVectors entry point.
				// The engine reads VectorIndex.Capabilities and selects
				// PreFilter (trust adapter) vs PostFilter (pool expansion +
				// in-engine safety net). This replaces the manual Search +
				// RevalidateCandidates + filter-drop-prone path with the
				// centralized strategy selector (REQ-VEC-001/002).
				vecResults, vecErr := retrieval.SearchVectors(ctx, stores.Vectors, vecQuery, stores.Observations)
				if vecErr == nil && len(vecResults) > 0 {
					ftsResults = retrieval.FuseResults(ftsResults, vecResults, limit)
				}
			}
		}

		if len(ftsResults) == 0 {
			return textResult("No results found for %q", query)
		}

		var sb strings.Builder
		fmt.Fprintf(&sb, "Search results for %q [%s] (%d found):\n\n", query, searchMode, len(ftsResults))
		for i, r := range ftsResults {
			fmt.Fprintf(&sb, "%d. [%d] %s (%s, rank: %.2f)\n   %s\n",
				i+1, r.ID, r.Title, r.Type, r.Rank, truncate(r.Content, 120))
			if explanation := formatSearchBreakdown(r.ScoreBreakdown); explanation != "" {
				fmt.Fprintf(&sb, "   explain: %s\n", explanation)
			}
			sb.WriteString("\n")
		}

		return textResult("%s", sb.String())
	}
}

func handleMergeProjects(stores *Stores) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		from := stringArg(req, "from")
		to := stringArg(req, "to")

		if from == "" || to == "" {
			return errorResult("both 'from' and 'to' are required")
		}

		var sources []string
		for _, s := range strings.Split(from, ",") {
			s = strings.TrimSpace(s)
			if s != "" {
				sources = append(sources, s)
			}
		}
		if len(sources) == 0 {
			return errorResult("'from' must contain at least one project name")
		}

		result, err := stores.Observations.MergeProjects(ctx, sources, to)
		if err != nil {
			return errorResult("merge failed: %s", err)
		}

		return textResult("Merged into %q: %d observations, %d sessions updated. Sources merged: %v",
			result.Canonical, result.ObservationsUpdated, result.SessionsUpdated, result.SourcesMerged)
	}
}

func handleSearchTemporal(stores *Stores) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		query := stringArg(req, "query")
		asOfStr := stringArg(req, "as_of")
		project := stringArg(req, "project")
		limit := intArg(req, "limit", 10)

		if query == "" {
			return errorResult("query is required")
		}
		if asOfStr == "" {
			return errorResult("as_of is required (ISO 8601 timestamp, e.g. 2025-06-15T00:00:00Z)")
		}

		asOf, err := time.Parse(time.RFC3339, asOfStr)
		if err != nil {
			return errorResult("invalid as_of timestamp: %s (use ISO 8601 format)", err)
		}

		results, err := stores.Search.Search(ctx, query, domain.SearchOptions{
			Limit:       limit,
			Project:     project,
			GraphExpand: true, // Always expand graph for temporal search
			AsOf:        &asOf,
		})
		if err != nil {
			return errorResult("temporal search failed: %s", err)
		}

		if len(results) == 0 {
			return textResult("No results found for %q as of %s", query, asOfStr)
		}

		var sb strings.Builder
		fmt.Fprintf(&sb, "Temporal search for %q as of %s (%d found):\n\n", query, asOfStr, len(results))
		for i, r := range results {
			fmt.Fprintf(&sb, "%d. [%d] %s (%s)\n   %s\n\n",
				i+1, r.ID, r.Title, r.Type, truncate(r.Content, 120))
		}
		return textResult("%s", sb.String())
	}
}

func handleConsolidate(stores *Stores) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		project := stringArg(req, "project")
		topicKey := stringArg(req, "topic_key")

		if project == "" {
			return errorResult("project is required")
		}

		if topicKey != "" {
			// Return all observations with this topic key
			obs, err := stores.Observations.ListByTopicKey(ctx, project, topicKey)
			if err != nil {
				return errorResult("failed to list observations: %s", err)
			}
			if len(obs) < 2 {
				return textResult("Topic key %q has %d observation(s) — nothing to consolidate.", topicKey, len(obs))
			}

			var sb strings.Builder
			fmt.Fprintf(&sb, "Consolidation candidates for topic_key=%q (%d observations):\n\n", topicKey, len(obs))
			for i, o := range obs {
				fmt.Fprintf(&sb, "%d. [%d] %s (%s, %s)\n   %s\n\n",
					i+1, o.ID, o.Title, o.Type, o.CreatedAt.Format("2006-01-02"), truncate(o.Content, 200))
			}
			sb.WriteString("To consolidate: save a merged observation with cortex_save using the same topic_key, ")
			sb.WriteString("then use cortex_relate with relation_type='supersedes' from the new observation to each old one.")
			return textResult("%s", sb.String())
		}

		// Find all consolidation candidate groups
		groups, err := stores.Observations.FindConsolidationCandidates(ctx, project, 2)
		if err != nil {
			return errorResult("failed to find candidates: %s", err)
		}

		if len(groups) == 0 {
			return textResult("No consolidation candidates found in project %q.", project)
		}

		var sb strings.Builder
		fmt.Fprintf(&sb, "Consolidation candidates in project %q:\n\n", project)
		for _, g := range groups {
			fmt.Fprintf(&sb, "  %-40s  %d observations  (latest: %s)\n", g.TopicKey, g.Count, g.Latest)
		}
		fmt.Fprintf(&sb, "\nTotal: %d topic keys with 2+ observations.\n", len(groups))
		sb.WriteString("Use cortex_consolidate with topic_key=<key> to see full content.")
		return textResult("%s", sb.String())
	}
}

func handleProjectDNA(stores *Stores) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		project := stringArg(req, "project")
		if project == "" {
			return errorResult("project is required")
		}

		svc := dna.NewService(stores.Observations, stores.Scoring, stores.Graph)
		result, err := svc.Generate(ctx, project)
		if err != nil {
			return errorResult("failed to generate DNA: %s", err)
		}

		return textResult("%s", result)
	}
}

// floatArg extracts a float64 argument with a default value.
func floatArg(req mcp.CallToolRequest, key string, defaultVal float64) float64 {
	v, ok := req.GetArguments()[key].(float64)
	if !ok {
		return defaultVal
	}
	return v
}
