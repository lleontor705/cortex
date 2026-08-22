package mcp

import (
	"context"
	"encoding/json"
	"errors"
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
				withIntegerID("from_id", "Source observation ID"),
				withIntegerID("to_id", "Target observation ID"),
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
				mcp.WithDescription("Traverse the knowledge graph starting from an observation. Returns related observations up to the specified depth, ordered by minimum hop then observation ID. Bounded by max_visited (default 1000, cap 10000, counts the root plus unique visited nodes) and max_results (default 100, cap 1000, counts emitted non-root rows); when eligible data is omitted the output reports truncated=true with the reason(s)."),
				withIntegerID("observation_id", "Starting observation ID"),
				mcp.WithNumber("depth",
					mcp.Description("Traversal depth 1-10 (default: 1)"),
				),
				mcp.WithNumber("max_visited",
					mcp.Description("Global visited budget 1-10000 (default: 1000): the root plus unique visited nodes"),
				),
				mcp.WithNumber("max_results",
					mcp.Description("Maximum emitted related observations 1-1000 (default: 100)"),
				),
			),
			handleGraph(stores),
		)
	}

	if shouldRegister("cortex_graph_relationships", allowlist) {
		srv.AddTool(
			mcp.NewTool("cortex_graph_relationships",
				mcp.WithTitleAnnotation("List Graph Relationships"),
				mcp.WithReadOnlyHintAnnotation(true),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithIdempotentHintAnnotation(true),
				mcp.WithOpenWorldHintAnnotation(false),
				mcp.WithDescription("List incoming and outgoing relationships for an observation, including type, weight, confidence, provenance, and temporal metadata."),
				withIntegerID("observation_id", "Observation ID"),
			),
			handleGraphRelationships(stores),
		)
	}

	if shouldRegister("cortex_graph_path", allowlist) {
		srv.AddTool(
			mcp.NewTool("cortex_graph_path",
				mcp.WithTitleAnnotation("Find Graph Path"),
				mcp.WithReadOnlyHintAnnotation(true),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithIdempotentHintAnnotation(true),
				mcp.WithOpenWorldHintAnnotation(false),
				mcp.WithDescription("Find the lexicographically smallest shortest observation path between two nodes using bounded breadth-first traversal. On success the payload is the []int64 path (empty array when no path exists within the depth). If the max_visited budget (default 1000, cap 10000, counts the root plus unique visited nodes including the destination) is exhausted before the path can be proved or disproved, a stable traversal-truncated error is returned instead of a false no-path result."),
				withIntegerID("from_id", "Starting observation ID"),
				withIntegerID("to_id", "Destination observation ID"),
				mcp.WithNumber("max_depth", mcp.Description("Maximum traversal depth 1-10 (default: 5)")),
				mcp.WithNumber("max_visited", mcp.Description("Global visited budget 1-10000 (default: 1000): the root plus unique visited nodes")),
			),
			handleGraphPath(stores),
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
				withIntegerID("observation_id", "Observation ID to score"),
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
				withIntegerID("observation_id", "Observation ID to archive"),
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

	// --- cortex_resolve_query -------------------------------------------
	if shouldRegister("cortex_resolve_query", allowlist) {
		srv.AddTool(
			mcp.NewTool("cortex_resolve_query",
				mcp.WithTitleAnnotation("Resolve Query with Active Mode"),
				mcp.WithReadOnlyHintAnnotation(true),
				mcp.WithDescription("Intelligently resolve a query using Cortex memory and knowledge graph in the active runtime mode (Local SQLite FTS5/Graph or Server PostgreSQL). Returns structured relevant observations, recent context, and operational metadata."),
				mcp.WithString("query",
					mcp.Required(),
					mcp.Description("The question, topic or search query to resolve"),
				),
				mcp.WithString("project",
					mcp.Description("Optional project filter (defaults to all or current workspace)"),
				),
				mcp.WithNumber("limit",
					mcp.Description("Maximum number of observations to retrieve (default: 10)"),
				),
			),
			handleResolveQuery(stores),
		)
	}

	// --- cortex_get_status ----------------------------------------------
	if shouldRegister("cortex_get_status", allowlist) {
		srv.AddTool(
			mcp.NewTool("cortex_get_status",
				mcp.WithTitleAnnotation("Get Cortex Status and Runtime Mode"),
				mcp.WithReadOnlyHintAnnotation(true),
				mcp.WithDescription("Get the active operational mode (local SQLite vs server PostgreSQL), version, storage status, and enabled capabilities."),
			),
			handleGetStatus(stores),
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
		fromID, fromOK := positiveIDArg(req, "from_id")
		toID, toOK := positiveIDArg(req, "to_id")
		relationType := stringArg(req, "relation_type")
		weight := floatArg(req, "weight", 1.0)
		confidence := floatArg(req, "confidence", 1.0)
		source := stringArg(req, "source")
		reasoning := stringArg(req, "reasoning")

		if !fromOK || !toOK {
			return errorResult("from_id and to_id must be positive integers")
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
			return errorResult("Failed to create relationship: %s", localErrorText(err))
		}

		return textResult("Relationship created: observation %d -[%s]-> observation %d (weight: %.1f, edge ID: %d)",
			fromID, relationType, toID, weight, edge.ID)
	}
}

func handleGraph(stores *Stores) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		obsID, ok := positiveIDArg(req, "observation_id")
		if !ok {
			return errorResult("observation_id must be a positive integer")
		}
		depth := intArg(req, "depth", 1)

		svc := graphdomain.NewService(stores.Graph)
		related, err := svc.GetRelatedBounded(ctx, obsID, domain.GraphTraversalOptions{
			Depth:      depth,
			MaxVisited: intArg(req, "max_visited", 0),
			MaxResults: intArg(req, "max_results", 0),
		})
		if err != nil {
			return errorResult("Failed to traverse graph: %s", localErrorText(err))
		}

		if len(related.Observations) == 0 {
			if related.Truncated {
				return textResult("No related observations emitted for ID %d (depth: %d): traversal truncated (%s)",
					obsID, depth, strings.Join(related.TruncationReasons, ", "))
			}
			return textResult("No related observations found for ID %d (depth: %d)", obsID, depth)
		}

		var sb strings.Builder
		fmt.Fprintf(&sb, "Related observations for ID %d (depth: %d, found: %d):\n\n", obsID, depth, len(related.Observations))
		for _, obs := range related.Observations {
			fmt.Fprintf(&sb, "- [%d] %s (%s) -- %s\n", obs.ID, obs.Title, obs.Type, truncate(obs.Content, 80))
		}
		if related.Truncated {
			fmt.Fprintf(&sb, "\nTraversal truncated (reason: %s): raise max_visited/max_results to see omitted observations.\n",
				strings.Join(related.TruncationReasons, ", "))
		}

		return textResult("%s", sb.String())
	}
}

func handleGraphRelationships(stores *Stores) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		obsID, ok := positiveIDArg(req, "observation_id")
		if !ok {
			return errorResult("observation_id must be a positive integer")
		}

		edges, err := graphdomain.NewService(stores.Graph).GetRelationships(ctx, obsID)
		if err != nil {
			return errorResult("Failed to get graph relationships: %s", localErrorText(err))
		}
		if edges == nil {
			edges = []*domain.Edge{}
		}
		return jsonTextResult(edges)
	}
}

func handleGraphPath(stores *Stores) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		fromID, fromOK := positiveIDArg(req, "from_id")
		toID, toOK := positiveIDArg(req, "to_id")
		if !fromOK || !toOK {
			return errorResult("from_id and to_id must be positive integers")
		}

		path, err := graphdomain.NewService(stores.Graph).FindPathBounded(ctx, fromID, toID,
			intArg(req, "max_depth", graphdomain.DefaultMaxDepth),
			intArg(req, "max_visited", 0))
		if err != nil {
			// The truncation sentinel is a stable, domain-authored constant:
			// surface it verbatim so clients can distinguish a bounded sweep
			// from a true no-path result; all other errors stay classified.
			if errors.Is(err, graphdomain.ErrTraversalTruncated) {
				return errorResult("Failed to find graph path: %s", graphdomain.ErrTraversalTruncated.Error())
			}
			return errorResult("Failed to find graph path: %s", localErrorText(err))
		}
		if path == nil {
			path = []int64{}
		}
		return jsonTextResult(path)
	}
}

func jsonTextResult(value any) (*mcp.CallToolResult, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return mcp.NewToolResultText(string(encoded)), nil
}

func handleScore(stores *Stores) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		obsID, ok := positiveIDArg(req, "observation_id")
		if !ok {
			return errorResult("observation_id must be a positive integer")
		}
		recalculate := boolArg(req, "recalculate", false)

		svc := scoringdomain.NewService(stores.Scoring)

		if recalculate {
			score, err := svc.CalculateScore(ctx, obsID)
			if err != nil {
				return errorResult("Failed to recalculate score: %s", localErrorText(err))
			}
			return textResult("Score recalculated for observation %d: %.2f", obsID, score)
		}

		score, err := svc.GetScore(ctx, obsID)
		if err != nil {
			return errorResult("Failed to get score: %s", localErrorText(err))
		}

		return textResult("Observation %d -- score: %.2f, access_count: %d, last_accessed: %s",
			obsID, score.Score, score.AccessCount, score.LastAccessed.Format("2006-01-02 15:04:05"))
	}
}

func handleArchive(stores *Stores) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		obsID, ok := positiveIDArg(req, "observation_id")
		if !ok {
			return errorResult("observation_id must be a positive integer")
		}

		if err := stores.Observations.Delete(ctx, obsID); err != nil {
			return errorResult("Failed to archive observation: %s", localErrorText(err))
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
			return errorResult("FTS5 search failed: %s", localErrorText(err))
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
			return errorResult("merge failed: %s", localErrorText(err))
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
			return errorResult("invalid as_of timestamp (use ISO 8601 format, e.g. 2025-06-15T00:00:00Z)")
		}

		results, err := stores.Search.Search(ctx, query, domain.SearchOptions{
			Limit:       limit,
			Project:     project,
			GraphExpand: true, // Always expand graph for temporal search
			AsOf:        &asOf,
		})
		if err != nil {
			return errorResult("temporal search failed: %s", localErrorText(err))
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
				return errorResult("failed to list observations: %s", localErrorText(err))
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
			return errorResult("failed to find candidates: %s", localErrorText(err))
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
			return errorResult("failed to generate DNA: %s", localErrorText(err))
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

func handleResolveQuery(stores *Stores) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		query := stringArg(req, "query")
		if query == "" {
			return errorResult("query is required")
		}
		project := stringArg(req, "project")
		limit := intArg(req, "limit", 10)
		if limit <= 0 || limit > 100 {
			limit = 10
		}

		results, err := stores.Search.Search(ctx, query, domain.SearchOptions{
			Project: project,
			Limit:   limit,
		})
		if err != nil {
			return errorResult("search error: %v", err)
		}

		// Also fetch recent context for surrounding context
		recent, _ := stores.Observations.List(ctx, domain.ObservationFilter{
			Project: project,
			Limit:   5,
		})

		response := map[string]any{
			"mode":               "local",
			"database":           "sqlite",
			"query":              query,
			"project":            project,
			"total_matches":      len(results),
			"observations":       results,
			"recent_surrounding": recent,
		}

		b, err := json.MarshalIndent(response, "", "  ")
		if err != nil {
			return errorResult("serialize response: %v", err)
		}
		return mcp.NewToolResultText(string(b)), nil
	}
}

func handleGetStatus(stores *Stores) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		status := map[string]any{
			"mode":         "local",
			"database":     "sqlite",
			"version":      serverVersion,
			"capabilities": []string{"fts5_search", "knowledge_graph", "scoring", "temporal", "dna", "handoff", "hybrid_search"},
			"profiles":     []string{"agent", "admin", "temporal"},
		}
		b, err := json.MarshalIndent(status, "", "  ")
		if err != nil {
			return errorResult("serialize status: %v", err)
		}
		return mcp.NewToolResultText(string(b)), nil
	}
}

