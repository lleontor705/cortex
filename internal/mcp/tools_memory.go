package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/lleontor705/cortex/internal/domain"
	"github.com/lleontor705/cortex/internal/domain/entity"
	projectpkg "github.com/lleontor705/cortex/internal/project"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// registerMemoryTools registers all Engram-compatible memory tools.
func registerMemoryTools(srv *server.MCPServer, stores *Stores, allowlist map[string]bool) {
	registerEagerMemoryTools(srv, stores, allowlist)
	registerDeferredMemoryTools(srv, stores, allowlist)
}

// registerEagerMemoryTools registers the 6 tools always loaded in agent context.
func registerEagerMemoryTools(srv *server.MCPServer, stores *Stores, allowlist map[string]bool) {
	// --- mem_save (eager) ---
	if shouldRegister("mem_save", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_save",
				mcp.WithTitleAnnotation("Save Memory"),
				mcp.WithReadOnlyHintAnnotation(false),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithIdempotentHintAnnotation(false),
				mcp.WithOpenWorldHintAnnotation(false),
				mcp.WithDescription(`Save an important observation to persistent memory. Call this PROACTIVELY after completing significant work  -- don't wait to be asked.

WHEN to save (call this after each of these):
- Architectural decisions or tradeoffs
- Bug fixes (what was wrong, why, how you fixed it)
- New patterns or conventions established
- Configuration changes or environment setup
- Important discoveries or gotchas
- File structure changes

FORMAT for content  -- use this structured format:
  **What**: [concise description of what was done]
  **Why**: [the reasoning, user request, or problem that drove it]
  **Where**: [files/paths affected, e.g. src/auth/middleware.ts, internal/store/store.go]
  **Learned**: [any gotchas, edge cases, or decisions made  -- omit if none]

TITLE should be short and searchable, like: "JWT auth middleware", "FTS5 query sanitization", "Fixed N+1 in user list"`),
				mcp.WithString("title",
					mcp.Required(),
					mcp.Description("Short, searchable title (e.g. 'JWT auth middleware', 'Fixed N+1 query')"),
				),
				mcp.WithString("content",
					mcp.Required(),
					mcp.Description("Structured content using **What**, **Why**, **Where**, **Learned** format"),
				),
				mcp.WithString("type",
					mcp.Description("Category: decision, architecture, bugfix, pattern, config, discovery, learning (default: manual)"),
				),
				mcp.WithString("session_id",
					mcp.Description("Session ID to associate with (default: manual-save-{project})"),
				),
				mcp.WithString("project",
					mcp.Description("Project name"),
				),
				mcp.WithString("scope",
					mcp.Description("Scope for this observation: project (default) or personal"),
				),
				mcp.WithString("topic_key",
					mcp.Description("Optional topic identifier for upserts (e.g. architecture/auth-model). Reuses and updates the latest observation in same project+scope."),
				),
				mcp.WithNumber("confidence",
					mcp.Description("Confidence score 0.0-1.0 (default: 1.0). Lower values indicate less certainty."),
				),
				mcp.WithString("source",
					mcp.Description("Origin of this observation: manual (default), ai, auto, import"),
				),
				mcp.WithString("tags",
					mcp.Description("Comma-separated tags (e.g. 'auth,jwt,security')"),
				),
			),
			handleSave(stores),
		)
	}

	// --- mem_search (eager) ---
	if shouldRegister("mem_search", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_search",
				mcp.WithDescription("Search your persistent memory across all sessions. Use this to find past decisions, bugs fixed, patterns used, files changed, or any context from previous coding sessions."),
				mcp.WithTitleAnnotation("Search Memory"),
				mcp.WithReadOnlyHintAnnotation(true),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithIdempotentHintAnnotation(true),
				mcp.WithOpenWorldHintAnnotation(false),
				mcp.WithString("query",
					mcp.Required(),
					mcp.Description("Search query  -- natural language or keywords"),
				),
				mcp.WithString("type",
					mcp.Description("Filter by type: tool_use, file_change, command, file_read, search, manual, decision, architecture, bugfix, pattern"),
				),
				mcp.WithString("project",
					mcp.Description("Filter by project name"),
				),
				mcp.WithString("scope",
					mcp.Description("Filter by scope: project (default) or personal"),
				),
				mcp.WithNumber("limit",
					mcp.Description("Max results (default: 10, max: 20)"),
				),
				mcp.WithBoolean("graph_expand",
					mcp.Description("Include graph-connected observations in results (default: false)"),
				),
			),
			handleSearch(stores),
		)
	}

	// --- mem_context (eager) ---
	if shouldRegister("mem_context", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_context",
				mcp.WithDescription("Get recent memory context from previous sessions. Shows recent sessions and observations to understand what was done before."),
				mcp.WithTitleAnnotation("Get Memory Context"),
				mcp.WithReadOnlyHintAnnotation(true),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithIdempotentHintAnnotation(true),
				mcp.WithOpenWorldHintAnnotation(false),
				mcp.WithString("project",
					mcp.Description("Filter by project (omit for all projects)"),
				),
				mcp.WithString("scope",
					mcp.Description("Filter observations by scope: project (default) or personal"),
				),
				mcp.WithNumber("limit",
					mcp.Description("Number of observations to retrieve (default: 20)"),
				),
			),
			handleContext(stores),
		)
	}

	// --- mem_session_summary (eager) ---
	if shouldRegister("mem_session_summary", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_session_summary",
				mcp.WithTitleAnnotation("Save Session Summary"),
				mcp.WithReadOnlyHintAnnotation(false),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithIdempotentHintAnnotation(false),
				mcp.WithOpenWorldHintAnnotation(false),
				mcp.WithDescription(`Save a comprehensive end-of-session summary. Call this when a session is ending or when significant work is complete. This creates a structured summary that future sessions will use to understand what happened.

FORMAT  -- use this exact structure in the content field:

## Goal
[One sentence: what were we building/working on in this session]

## Instructions
[User preferences, constraints, or context discovered during this session. Things a future agent needs to know about HOW the user wants things done. Skip if nothing notable.]

## Discoveries
- [Technical finding, gotcha, or learning 1]
- [Technical finding 2]
- [Important API behavior, config quirk, etc.]

## Accomplished
- Completed task 1  -- with key implementation details
- Completed task 2  -- mention files changed
- Identified but not yet done  -- for next session

## Relevant Files
- path/to/file.ts  -- [what it does or what changed]
- path/to/other.go  -- [role in the architecture]

GUIDELINES:
- Be CONCISE but don't lose important details (file paths, error messages, decisions)
- Focus on WHAT and WHY, not HOW (the code itself is in the repo)
- Include things that would save a future agent time
- The Discoveries section is the most valuable  -- capture gotchas and non-obvious learnings
- Relevant Files should only include files that were significantly changed or are important for context`),
				mcp.WithString("content",
					mcp.Required(),
					mcp.Description("Full session summary using the Goal/Instructions/Discoveries/Accomplished/Files format"),
				),
				mcp.WithString("session_id",
					mcp.Description("Session ID (default: manual-save-{project})"),
				),
				mcp.WithString("project",
					mcp.Required(),
					mcp.Description("Project name"),
				),
			),
			handleSessionSummary(stores),
		)
	}

	// --- mem_get_observation (eager) ---
	if shouldRegister("mem_get_observation", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_get_observation",
				mcp.WithDescription("Get the full content of a specific observation by ID. Use when you need the complete, untruncated content of an observation found via mem_search or mem_timeline."),
				mcp.WithTitleAnnotation("Get Observation"),
				mcp.WithReadOnlyHintAnnotation(true),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithIdempotentHintAnnotation(true),
				mcp.WithOpenWorldHintAnnotation(false),
				mcp.WithNumber("id",
					mcp.Required(),
					mcp.Description("The observation ID to retrieve"),
				),
			),
			handleGetObservation(stores),
		)
	}

	// --- mem_save_prompt (eager) ---
	if shouldRegister("mem_save_prompt", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_save_prompt",
				mcp.WithDescription("Save a user prompt to persistent memory. Use this to record what the user asked  -- their intent, questions, and requests  -- so future sessions have context about the user's goals."),
				mcp.WithTitleAnnotation("Save User Prompt"),
				mcp.WithReadOnlyHintAnnotation(false),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithIdempotentHintAnnotation(false),
				mcp.WithOpenWorldHintAnnotation(false),
				mcp.WithString("content",
					mcp.Required(),
					mcp.Description("The user's prompt text"),
				),
				mcp.WithString("session_id",
					mcp.Description("Session ID to associate with (default: manual-save-{project})"),
				),
				mcp.WithString("project",
					mcp.Description("Project name"),
				),
			),
			handleSavePrompt(stores),
		)
	}
}

// registerDeferredMemoryTools registers tools loaded on demand (update, delete, stats, etc.).
func registerDeferredMemoryTools(srv *server.MCPServer, stores *Stores, allowlist map[string]bool) {
	// --- mem_update (deferred) ---
	if shouldRegister("mem_update", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_update",
				mcp.WithDescription("Update an existing observation by ID. Only provided fields are changed."),
				mcp.WithDeferLoading(true),
				mcp.WithTitleAnnotation("Update Memory"),
				mcp.WithReadOnlyHintAnnotation(false),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithIdempotentHintAnnotation(false),
				mcp.WithOpenWorldHintAnnotation(false),
				mcp.WithNumber("id",
					mcp.Required(),
					mcp.Description("Observation ID to update"),
				),
				mcp.WithString("title",
					mcp.Description("New title"),
				),
				mcp.WithString("content",
					mcp.Description("New content"),
				),
				mcp.WithString("type",
					mcp.Description("New type/category"),
				),
				mcp.WithString("project",
					mcp.Description("New project value"),
				),
				mcp.WithString("scope",
					mcp.Description("New scope: project or personal"),
				),
				mcp.WithString("topic_key",
					mcp.Description("New topic key (normalized internally)"),
				),
			),
			handleUpdate(stores),
		)
	}

	// --- mem_suggest_topic_key (deferred) ---
	if shouldRegister("mem_suggest_topic_key", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_suggest_topic_key",
				mcp.WithDescription("Suggest a stable topic_key for memory upserts. Use this before mem_save when you want evolving topics (like architecture decisions) to update a single observation over time."),
				mcp.WithDeferLoading(true),
				mcp.WithTitleAnnotation("Suggest Topic Key"),
				mcp.WithReadOnlyHintAnnotation(true),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithIdempotentHintAnnotation(true),
				mcp.WithOpenWorldHintAnnotation(false),
				mcp.WithString("type",
					mcp.Description("Observation type/category, e.g. architecture, decision, bugfix"),
				),
				mcp.WithString("title",
					mcp.Description("Observation title (preferred input for stable keys)"),
				),
				mcp.WithString("content",
					mcp.Description("Observation content used as fallback if title is empty"),
				),
			),
			handleSuggestTopicKey(),
		)
	}

	// --- mem_session_start (deferred) ---
	if shouldRegister("mem_session_start", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_session_start",
				mcp.WithDescription("Register the start of a new coding session. Call this at the beginning of a session to track activity."),
				mcp.WithDeferLoading(true),
				mcp.WithTitleAnnotation("Start Session"),
				mcp.WithReadOnlyHintAnnotation(false),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithIdempotentHintAnnotation(true),
				mcp.WithOpenWorldHintAnnotation(false),
				mcp.WithString("id",
					mcp.Required(),
					mcp.Description("Unique session identifier"),
				),
				mcp.WithString("project",
					mcp.Required(),
					mcp.Description("Project name"),
				),
				mcp.WithString("directory",
					mcp.Description("Working directory"),
				),
			),
			handleSessionStart(stores),
		)
	}

	// --- mem_session_end (deferred) ---
	if shouldRegister("mem_session_end", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_session_end",
				mcp.WithDescription("Mark a coding session as completed with an optional summary."),
				mcp.WithDeferLoading(true),
				mcp.WithTitleAnnotation("End Session"),
				mcp.WithReadOnlyHintAnnotation(false),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithIdempotentHintAnnotation(true),
				mcp.WithOpenWorldHintAnnotation(false),
				mcp.WithString("id",
					mcp.Required(),
					mcp.Description("Session identifier to close"),
				),
				mcp.WithString("summary",
					mcp.Description("Summary of what was accomplished"),
				),
			),
			handleSessionEnd(stores),
		)
	}

	// --- mem_stats (deferred) ---
	if shouldRegister("mem_stats", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_stats",
				mcp.WithDescription("Show memory system statistics  -- total sessions, observations, and projects tracked."),
				mcp.WithDeferLoading(true),
				mcp.WithTitleAnnotation("Memory Stats"),
				mcp.WithReadOnlyHintAnnotation(true),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithIdempotentHintAnnotation(true),
				mcp.WithOpenWorldHintAnnotation(false),
			),
			handleStats(stores),
		)
	}

	// --- mem_delete (deferred) ---
	if shouldRegister("mem_delete", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_delete",
				mcp.WithDescription("Delete an observation by ID. Soft-delete by default; set hard_delete=true for permanent deletion."),
				mcp.WithDeferLoading(true),
				mcp.WithTitleAnnotation("Delete Memory"),
				mcp.WithReadOnlyHintAnnotation(false),
				mcp.WithDestructiveHintAnnotation(true),
				mcp.WithIdempotentHintAnnotation(false),
				mcp.WithOpenWorldHintAnnotation(false),
				mcp.WithNumber("id",
					mcp.Required(),
					mcp.Description("Observation ID to delete"),
				),
				mcp.WithBoolean("hard_delete",
					mcp.Description("If true, permanently deletes the observation"),
				),
			),
			handleDelete(stores),
		)
	}

	// mem_timeline (deferred)
	if shouldRegister("mem_timeline", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_timeline",
				mcp.WithDescription("Show chronological context around a specific observation. Use after mem_search to drill into the timeline of events surrounding a search result. This is the progressive disclosure pattern: search first, then timeline to understand context."),
				mcp.WithDeferLoading(true),
				mcp.WithTitleAnnotation("Memory Timeline"),
				mcp.WithReadOnlyHintAnnotation(true),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithIdempotentHintAnnotation(true),
				mcp.WithOpenWorldHintAnnotation(false),
				mcp.WithNumber("observation_id",
					mcp.Required(),
					mcp.Description("The observation ID to center the timeline on (from mem_search results)"),
				),
				mcp.WithNumber("before",
					mcp.Description("Number of observations to show before the focus (default: 5)"),
				),
				mcp.WithNumber("after",
					mcp.Description("Number of observations to show after the focus (default: 5)"),
				),
			),
			handleTimeline(stores),
		)
	}

	if shouldRegister("mem_revision_history", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_revision_history",
				mcp.WithDescription("Show structured revision snapshots for a specific observation. Use this when you want machine-readable history for topic_key upserts and updates."),
				mcp.WithDeferLoading(true),
				mcp.WithTitleAnnotation("Revision History"),
				mcp.WithReadOnlyHintAnnotation(true),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithIdempotentHintAnnotation(true),
				mcp.WithOpenWorldHintAnnotation(false),
				mcp.WithNumber("observation_id",
					mcp.Required(),
					mcp.Description("The observation ID to inspect"),
				),
				mcp.WithNumber("limit",
					mcp.Description("Maximum number of revision snapshots to return (default: 20)"),
				),
			),
			handleRevisionHistory(stores),
		)
	}

	// mem_capture_passive (deferred)
	if shouldRegister("mem_capture_passive", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_capture_passive",
				mcp.WithDeferLoading(true),
				mcp.WithTitleAnnotation("Capture Learnings"),
				mcp.WithReadOnlyHintAnnotation(false),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithIdempotentHintAnnotation(true),
				mcp.WithOpenWorldHintAnnotation(false),
				mcp.WithDescription(`Extract and save structured learnings from text output. Use this at the end of a task to capture knowledge automatically.

The tool looks for sections like "## Key Learnings:" or "## Aprendizajes Clave:" and extracts numbered or bulleted items. Each item is saved as a separate observation.

Duplicates are automatically detected and skipped  -- safe to call multiple times with the same content.`),
				mcp.WithString("content",
					mcp.Required(),
					mcp.Description("The text output containing a '## Key Learnings:' section with numbered or bulleted items"),
				),
				mcp.WithString("session_id",
					mcp.Description("Session ID (default: manual-save-{project})"),
				),
				mcp.WithString("project",
					mcp.Description("Project name"),
				),
				mcp.WithString("source",
					mcp.Description("Source identifier (e.g. 'subagent-stop', 'session-end')"),
				),
			),
			handleCapturePassive(stores),
		)
	}
}

// -- Tool Handlers --

func handleSave(stores *Stores) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		title := stringArg(req, "title")
		content := stringArg(req, "content")
		typ := stringArg(req, "type")
		sessionID := stringArg(req, "session_id")
		project := stringArg(req, "project")
		scope := stringArg(req, "scope")
		topicKey := stringArg(req, "topic_key")
		source := stringArg(req, "source")
		tagsStr := stringArg(req, "tags")
		confidence := floatArg(req, "confidence", 1.0)

		if typ == "" {
			typ = "manual"
		}
		if source == "" {
			source = domain.SourceManual
		}

		var tags []string
		if tagsStr != "" {
			for _, t := range strings.Split(tagsStr, ",") {
				t = strings.TrimSpace(t)
				if t != "" {
					tags = append(tags, t)
				}
			}
		}
		if sessionID == "" {
			sessionID = defaultSessionID(project)
		}

		suggested := suggestTopicKey(typ, title, content)

		// Ensure the session exists (ignore error if already created)
		_ = stores.Sessions.Create(ctx, &domain.Session{
			ID:        sessionID,
			Project:   project,
			Directory: ".",
		})

		obs := &domain.Observation{
			Title:      title,
			Content:    content,
			Type:       typ,
			SessionID:  sessionID,
			Project:    project,
			Scope:      scope,
			TopicKey:   topicKey,
			Confidence: confidence,
			Source:     source,
			Tags:       tags,
		}

		if err := stores.Observations.Save(ctx, obs); err != nil {
			return errorResult("Failed to save: %s", err)
		}

		// Extract and save entity links
		if stores.Entities != nil {
			entitySvc := entity.NewService(stores.Entities)
			_ = entitySvc.ExtractAndSave(ctx, obs) // best-effort
		}

		msg := fmt.Sprintf("Memory saved: %q (%s)", title, typ)
		if topicKey == "" && suggested != "" {
			msg += fmt.Sprintf("\nSuggested topic_key: %s", suggested)
		}

		// Project normalization warning
		if project != "" {
			if normalized, warn := projectpkg.NormalizeProject(project); warn != "" {
				msg += fmt.Sprintf("\n%s", warn)
				_ = normalized // normalization applied by store layer
			}
		}

		// Similarity warning for new projects
		if project != "" {
			stats, statsErr := stores.Observations.Stats(ctx)
			if statsErr == nil && len(stats.Projects) > 0 {
				matches := projectpkg.FindSimilar(project, stats.Projects, 3)
				if len(matches) > 0 {
					msg += fmt.Sprintf("\nSimilar project found: %q. Consider using that name instead.", matches[0].Name)
				}
			}
		}

		return textResult("%s", msg)
	}
}

func handleSearch(stores *Stores) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		query := stringArg(req, "query")
		typ := stringArg(req, "type")
		project := stringArg(req, "project")
		scope := stringArg(req, "scope")
		limit := intArg(req, "limit", 10)
		graphExpand := boolArg(req, "graph_expand", false)

		results, err := stores.Search.Search(ctx, query, domain.SearchOptions{
			Type:        typ,
			Project:     project,
			Scope:       scope,
			Limit:       limit,
			GraphExpand: graphExpand,
		})
		if err != nil {
			return errorResult("Search error: %s. Try simpler keywords.", err)
		}

		// Track last search query for implicit feedback logging
		stores.LastSearchQuery = query

		if len(results) == 0 {
			return textResult("No memories found for: %q", query)
		}

		var b strings.Builder
		fmt.Fprintf(&b, "Found %d memories:\n\n", len(results))
		anyTruncated := false
		for i, r := range results {
			projectInfo := ""
			if r.Project != "" {
				projectInfo = fmt.Sprintf(" | project: %s", r.Project)
			}
			preview := truncate(r.Content, 300)
			if len(r.Content) > 300 {
				anyTruncated = true
				preview += " [preview]"
			}
			fmt.Fprintf(&b, "[%d] #%d (%s)  -- %s\n    %s\n    %s%s | scope: %s\n",
				i+1, r.ID, r.Type, r.Title,
				preview,
				r.CreatedAt.Format(time.RFC3339), projectInfo, r.Scope)
			if explanation := formatSearchBreakdown(r.ScoreBreakdown); explanation != "" {
				fmt.Fprintf(&b, "    explain: %s\n", explanation)
			}
			b.WriteString("\n")
		}
		if anyTruncated {
			fmt.Fprintf(&b, "---\nResults above are previews (300 chars). To read the full content of a specific memory, call mem_get_observation(id: <ID>).\n")
		}

		return textResult("%s", b.String())
	}
}

func handleContext(stores *Stores) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		project := stringArg(req, "project")
		scope := stringArg(req, "scope")
		limit := intArg(req, "limit", 20)

		// Gather sessions
		sessions, err := stores.Sessions.List(ctx, project)
		if err != nil {
			return errorResult("Failed to get sessions: %s", err)
		}

		// Gather recent observations
		observations, err := stores.Observations.List(ctx, domain.ObservationFilter{
			Project: project,
			Scope:   scope,
			Limit:   limit,
		})
		if err != nil {
			return errorResult("Failed to get observations: %s", err)
		}

		// Gather recent prompts
		prompts, _ := stores.Prompts.List(ctx, project, 5)

		if len(sessions) == 0 && len(observations) == 0 {
			return textResult("No previous session memories found.")
		}

		var b strings.Builder

		// Recent sessions
		if len(sessions) > 0 {
			b.WriteString("## Recent Sessions\n\n")
			shown := len(sessions)
			if shown > 5 {
				shown = 5
			}
			for _, s := range sessions[:shown] {
				status := "active"
				if s.EndedAt != nil {
					status = "ended"
				}
				summary := ""
				if s.Summary != "" {
					summary = fmt.Sprintf("  -- %s", truncate(s.Summary, 100))
				}
				fmt.Fprintf(&b, "- **%s** (%s, %s)%s\n", s.ID, s.Project, status, summary)
			}
			b.WriteString("\n")
		}

		// Recent prompts
		if len(prompts) > 0 {
			b.WriteString("## Recent Prompts\n\n")
			for _, p := range prompts {
				fmt.Fprintf(&b, "- %s\n", truncate(p.Content, 120))
			}
			b.WriteString("\n")
		}

		// Recent observations
		if len(observations) > 0 {
			b.WriteString("## Recent Observations\n\n")
			for _, o := range observations {
				projectInfo := ""
				if o.Project != "" {
					projectInfo = fmt.Sprintf(" | %s", o.Project)
				}
				fmt.Fprintf(&b, "- #%d [%s] %s%s\n", o.ID, o.Type, o.Title, projectInfo)
			}
			b.WriteString("\n")
		}

		// Stats summary
		obsStats, _ := stores.Observations.Stats(ctx)
		sessStats, _ := stores.Sessions.GetStats(ctx)
		var totalSessions, totalObs int
		var projects []string
		if sessStats != nil {
			totalSessions = sessStats.TotalSessions
			projects = sessStats.Projects
		}
		if obsStats != nil {
			totalObs = obsStats.TotalObservations
			if len(projects) == 0 {
				projects = obsStats.Projects
			}
		}
		projectStr := "none"
		if len(projects) > 0 {
			projectStr = strings.Join(projects, ", ")
		}
		fmt.Fprintf(&b, "---\nMemory stats: %d sessions, %d observations across projects: %s",
			totalSessions, totalObs, projectStr)

		return textResult("%s", b.String())
	}
}

func handleSessionSummary(stores *Stores) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		content := stringArg(req, "content")
		sessionID := stringArg(req, "session_id")
		project := stringArg(req, "project")

		if sessionID == "" {
			sessionID = defaultSessionID(project)
		}

		// Ensure the session exists
		_ = stores.Sessions.Create(ctx, &domain.Session{
			ID:        sessionID,
			Project:   project,
			Directory: ".",
		})

		obs := &domain.Observation{
			SessionID: sessionID,
			Type:      "session_summary",
			Title:     fmt.Sprintf("Session summary: %s", project),
			Content:   content,
			Project:   project,
		}

		if err := stores.Observations.Save(ctx, obs); err != nil {
			return errorResult("Failed to save session summary: %s", err)
		}

		return textResult("Session summary saved for project %q", project)
	}
}

func handleGetObservation(stores *Stores) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id := int64(intArg(req, "id", 0))
		if id == 0 {
			return errorResult("id is required")
		}

		obs, err := stores.Observations.GetByID(ctx, id)
		if err != nil {
			return errorResult("Observation #%d not found", id)
		}

		// Record implicit feedback: accessing an observation signals relevance.
		// This builds data for recency-boosted search ranking.
		if stores.Scoring != nil {
			_ = stores.Scoring.RecordAccess(ctx, id) // best-effort
		}

		// Log search-to-observation feedback for Learning-to-Rank training.
		if stores.LastSearchQuery != "" {
			_ = stores.Observations.RecordSearchFeedback(ctx, stores.LastSearchQuery, id, 0)
			stores.LastSearchQuery = "" // consume once
		}

		projectInfo := ""
		if obs.Project != "" {
			projectInfo = fmt.Sprintf("\nProject: %s", obs.Project)
		}
		scopeInfo := fmt.Sprintf("\nScope: %s", obs.Scope)
		topicInfo := ""
		if obs.TopicKey != "" {
			topicInfo = fmt.Sprintf("\nTopic: %s", obs.TopicKey)
		}

		result := fmt.Sprintf("#%d [%s] %s\n%s\nSession: %s%s%s\nCreated: %s",
			obs.ID, obs.Type, obs.Title,
			obs.Content,
			obs.SessionID, projectInfo+scopeInfo+topicInfo,
			"",
			obs.CreatedAt.Format(time.RFC3339),
		)

		return textResult("%s", result)
	}
}

func handleSavePrompt(stores *Stores) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		content := stringArg(req, "content")
		sessionID := stringArg(req, "session_id")
		project := stringArg(req, "project")

		if sessionID == "" {
			sessionID = defaultSessionID(project)
		}

		// Ensure the session exists
		_ = stores.Sessions.Create(ctx, &domain.Session{
			ID:        sessionID,
			Project:   project,
			Directory: ".",
		})

		p := &domain.Prompt{
			Content:   content,
			Project:   project,
			SessionID: sessionID,
		}

		if err := stores.Prompts.Save(ctx, p); err != nil {
			return errorResult("Failed to save prompt: %s", err)
		}

		return textResult("Prompt saved: %q", truncate(content, 80))
	}
}

func handleUpdate(stores *Stores) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id := int64(intArg(req, "id", 0))
		if id == 0 {
			return errorResult("id is required")
		}

		// Fetch existing observation
		obs, err := stores.Observations.GetByID(ctx, id)
		if err != nil {
			return errorResult("Failed to find memory #%d: %s", id, err)
		}

		// Apply only the provided fields
		changed := false
		if v, ok := req.GetArguments()["title"].(string); ok {
			obs.Title = v
			changed = true
		}
		if v, ok := req.GetArguments()["content"].(string); ok {
			obs.Content = v
			changed = true
		}
		if v, ok := req.GetArguments()["type"].(string); ok {
			obs.Type = v
			changed = true
		}
		if v, ok := req.GetArguments()["project"].(string); ok {
			obs.Project = v
			changed = true
		}
		if v, ok := req.GetArguments()["scope"].(string); ok {
			obs.Scope = v
			changed = true
		}
		if v, ok := req.GetArguments()["topic_key"].(string); ok {
			obs.TopicKey = v
			changed = true
		}

		if !changed {
			return errorResult("provide at least one field to update")
		}

		if err := stores.Observations.Update(ctx, obs); err != nil {
			return errorResult("Failed to update memory: %s", err)
		}

		return textResult("Memory updated: #%d %q (%s, scope=%s)", obs.ID, obs.Title, obs.Type, obs.Scope)
	}
}

func handleSuggestTopicKey() server.ToolHandlerFunc {
	return func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		typ := stringArg(req, "type")
		title := stringArg(req, "title")
		content := stringArg(req, "content")

		if strings.TrimSpace(title) == "" && strings.TrimSpace(content) == "" {
			return errorResult("provide title or content to suggest a topic_key")
		}

		topicKey := suggestTopicKey(typ, title, content)
		if topicKey == "" {
			return errorResult("could not suggest topic_key from input")
		}

		return textResult("Suggested topic_key: %s", topicKey)
	}
}

func handleSessionStart(stores *Stores) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id := stringArg(req, "id")
		project := stringArg(req, "project")
		directory := stringArg(req, "directory")

		if directory == "" {
			directory = "."
		}

		if err := stores.Sessions.Create(ctx, &domain.Session{
			ID:        id,
			Project:   project,
			Directory: directory,
		}); err != nil {
			return errorResult("Failed to start session: %s", err)
		}

		return textResult("Session %q started for project %q", id, project)
	}
}

func handleSessionEnd(stores *Stores) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id := stringArg(req, "id")
		summary := stringArg(req, "summary")

		if err := stores.Sessions.End(ctx, id, summary); err != nil {
			return errorResult("Failed to end session: %s", err)
		}

		return textResult("Session %q completed", id)
	}
}

func handleStats(stores *Stores) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		obsStats, err := stores.Observations.Stats(ctx)
		if err != nil {
			return errorResult("Failed to get stats: %s", err)
		}

		sessStats, err := stores.Sessions.GetStats(ctx)
		if err != nil {
			return errorResult("Failed to get session stats: %s", err)
		}

		projects := "none yet"
		if len(sessStats.Projects) > 0 {
			projects = strings.Join(sessStats.Projects, ", ")
		} else if len(obsStats.Projects) > 0 {
			projects = strings.Join(obsStats.Projects, ", ")
		}

		return textResult("Memory System Stats:\n- Sessions: %d\n- Observations: %d\n- Projects: %s",
			sessStats.TotalSessions, obsStats.TotalObservations, projects)
	}
}

func handleDelete(stores *Stores) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id := int64(intArg(req, "id", 0))
		if id == 0 {
			return errorResult("id is required")
		}

		hardDelete := boolArg(req, "hard_delete", false)

		var err error
		if hardDelete {
			err = stores.Observations.HardDelete(ctx, id)
		} else {
			err = stores.Observations.Delete(ctx, id)
		}
		if err != nil {
			return errorResult("Failed to delete memory: %s", err)
		}

		mode := "soft-deleted"
		if hardDelete {
			mode = "permanently deleted"
		}
		return textResult("Memory #%d %s", id, mode)
	}
}
func handleTimeline(stores *Stores) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		observationID := int64(intArg(req, "observation_id", 0))
		if observationID == 0 {
			return errorResult("observation_id is required")
		}
		before := intArg(req, "before", 5)
		after := intArg(req, "after", 5)

		focus, err := stores.Observations.GetByID(ctx, observationID)
		if err != nil {
			return errorResult("Observation #%d not found", observationID)
		}

		filter := domain.ObservationFilter{
			Limit: before + after + 20,
		}
		if focus.SessionID != "" {
			allObs, err := stores.Observations.List(ctx, filter)
			if err != nil {
				return errorResult("Timeline error: %s", err)
			}

			var beforeObs, afterObs []*domain.Observation
			foundFocus := false
			for _, o := range allObs {
				if o.ID == observationID {
					foundFocus = true
					continue
				}
				if !foundFocus {
					if len(afterObs) < after {
						afterObs = append(afterObs, o)
					}
				} else {
					if len(beforeObs) < before {
						beforeObs = append(beforeObs, o)
					}
				}
			}

			var b strings.Builder
			sess, serr := stores.Sessions.GetByID(ctx, focus.SessionID)
			if serr == nil {
				summary := ""
				if sess.Summary != "" {
					summary = fmt.Sprintf(" - %s", truncate(sess.Summary, 100))
				}
				fmt.Fprintf(&b, "Session: %s (%s)%s\n\n", sess.Project, sess.StartedAt.Format(time.RFC3339), summary)
			}

			if len(beforeObs) > 0 {
				b.WriteString("--- Before ---\n")
				for i := len(beforeObs) - 1; i >= 0; i-- {
					e := beforeObs[i]
					fmt.Fprintf(&b, "  #%d [%s] %s - %s\n", e.ID, e.Type, e.Title, truncate(e.Content, 150))
				}
				b.WriteString("\n")
			}

			fmt.Fprintf(&b, ">>> #%d [%s] %s <<<\n", focus.ID, focus.Type, focus.Title)
			fmt.Fprintf(&b, "    %s\n", truncate(focus.Content, 500))
			fmt.Fprintf(&b, "    %s\n\n", focus.CreatedAt.Format(time.RFC3339))
			appendRevisionHistory(ctx, &b, stores, focus.ID)

			if len(afterObs) > 0 {
				b.WriteString("--- After ---\n")
				for i := len(afterObs) - 1; i >= 0; i-- {
					e := afterObs[i]
					fmt.Fprintf(&b, "  #%d [%s] %s - %s\n", e.ID, e.Type, e.Title, truncate(e.Content, 150))
				}
			}

			return textResult("%s", b.String())
		}

		var b strings.Builder
		fmt.Fprintf(&b, ">>> #%d [%s] %s <<<\n", focus.ID, focus.Type, focus.Title)
		fmt.Fprintf(&b, "    %s\n", truncate(focus.Content, 500))
		fmt.Fprintf(&b, "    %s\n", focus.CreatedAt.Format(time.RFC3339))
		appendRevisionHistory(ctx, &b, stores, focus.ID)
		return textResult("%s", b.String())
	}
}

type timelineRevisionSnapshot struct {
	Reason   string `json:"reason"`
	Previous struct {
		Title         string `json:"title"`
		Content       string `json:"content"`
		RevisionCount int    `json:"revision_count"`
	} `json:"previous"`
}

type revisionHistoryEntry struct {
	Timestamp      time.Time `json:"timestamp"`
	Reason         string    `json:"reason"`
	RevisionCount  int       `json:"revision_count"`
	Title          string    `json:"title"`
	ContentPreview string    `json:"content_preview,omitempty"`
}

func handleRevisionHistory(stores *Stores) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		observationID := int64(intArg(req, "observation_id", 0))
		if observationID == 0 {
			return errorResult("observation_id is required")
		}
		limit := intArg(req, "limit", 20)
		if limit <= 0 {
			limit = 20
		}

		history, err := loadRevisionHistory(ctx, stores, observationID, limit)
		if err != nil {
			return errorResult("Revision history error: %s", err)
		}
		if len(history) == 0 {
			return textResult("[]")
		}

		payload, err := json.Marshal(history)
		if err != nil {
			return errorResult("Failed to serialize revision history: %s", err)
		}
		return textResult("%s", payload)
	}
}

func appendRevisionHistory(ctx context.Context, b *strings.Builder, stores *Stores, observationID int64) {
	history, err := loadRevisionHistory(ctx, stores, observationID, 20)
	if b == nil || err != nil || len(history) == 0 {
		return
	}

	b.WriteString("--- Revision History ---\n")
	for _, entry := range history {
		label := entry.Reason
		if label == "" {
			label = "revision"
		}
		fmt.Fprintf(b, "  - %s [%s] rev=%d %s\n",
			entry.Timestamp.Format(time.RFC3339),
			label,
			entry.RevisionCount,
			entry.Title,
		)
		if entry.ContentPreview != "" {
			fmt.Fprintf(b, "    %s\n", entry.ContentPreview)
		}
	}
	b.WriteString("\n")
}

func loadRevisionHistory(ctx context.Context, stores *Stores, observationID int64, limit int) ([]revisionHistoryEntry, error) {
	if stores == nil || stores.TemporalSnapshots == nil {
		return nil, nil
	}

	snapshots, err := stores.TemporalSnapshots.GetByRootObservation(ctx, observationID)
	if err != nil || len(snapshots) == 0 {
		return nil, err
	}

	history := make([]revisionHistoryEntry, 0, len(snapshots))
	for _, snapshot := range snapshots {
		entry := timelineRevisionSnapshot{}
		if err := json.Unmarshal([]byte(snapshot.Description), &entry); err != nil {
			continue
		}
		history = append(history, revisionHistoryEntry{
			Timestamp:      snapshot.Timestamp,
			Reason:         entry.Reason,
			RevisionCount:  entry.Previous.RevisionCount,
			Title:          entry.Previous.Title,
			ContentPreview: truncate(entry.Previous.Content, 150),
		})
		if len(history) >= limit {
			break
		}
	}

	return history, nil
}

func handleCapturePassive(stores *Stores) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		content := stringArg(req, "content")
		sessionID := stringArg(req, "session_id")
		project := stringArg(req, "project")
		source := stringArg(req, "source")

		if content == "" {
			return errorResult("content is required  -- include text with a '## Key Learnings:' section")
		}

		if sessionID == "" {
			sessionID = defaultSessionID(project)
			_ = stores.Sessions.Create(ctx, &domain.Session{
				ID:        sessionID,
				Project:   project,
				Directory: ".",
			})
		}

		if source == "" {
			source = "mcp-passive"
		}

		learnings := extractLearnings(content)
		extracted := len(learnings)
		saved := 0
		duplicates := 0

		for _, learning := range learnings {
			title := truncate(learning, 60)

			obs := &domain.Observation{
				SessionID: sessionID,
				Type:      "passive",
				Title:     title,
				Content:   learning,
				Project:   project,
				Source:    source,
				Scope:     "project",
				TopicKey:  fmt.Sprintf("learning/%s", normalizeTopicSegment(title)),
			}

			err := stores.Observations.Save(ctx, obs)
			if err != nil {
				// Treat save errors as duplicates (dedup by topic_key)
				duplicates++
				continue
			}
			saved++
		}
		duplicates += extracted - saved - duplicates
		if duplicates < 0 {
			duplicates = 0
		}

		return textResult("Passive capture complete: extracted=%d saved=%d duplicates=%d",
			extracted, saved, duplicates)
	}
}

// -- Helpers --

// defaultSessionID returns a project-scoped default session ID.
func defaultSessionID(project string) string {
	if project == "" {
		return "manual-save"
	}
	return "manual-save-" + project
}

// truncate returns the first max runes of s, appending "..." if truncated.
func truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "..."
}

func formatSearchBreakdown(b domain.SearchScoreBreakdown) string {
	parts := make([]string, 0, 4)
	if b.Strategy != "" {
		parts = append(parts, "strategy="+b.Strategy)
	}
	if b.TopicKeyExact {
		parts = append(parts, "topic_key_exact=true")
	}
	if b.KeywordBM25 != 0 {
		parts = append(parts, fmt.Sprintf("bm25=%.4f", b.KeywordBM25))
	}
	if b.FusionScore != 0 {
		parts = append(parts, fmt.Sprintf("fusion=%.4f", b.FusionScore))
	}
	return strings.Join(parts, " | ")
}

// -- Topic Key Suggestion --

// suggestTopicKey generates a family/slug topic key from type, title, and content.
func suggestTopicKey(typ, title, content string) string {
	family := inferTopicFamily(typ, title, content)
	segment := normalizeTopicSegment(title)

	if segment == "" {
		words := strings.Fields(strings.ToLower(content))
		if len(words) > 8 {
			words = words[:8]
		}
		segment = normalizeTopicSegment(strings.Join(words, " "))
	}

	if segment == "" {
		segment = "general"
	}

	if strings.HasPrefix(segment, family+"-") {
		segment = strings.TrimPrefix(segment, family+"-")
	}

	return family + "/" + segment
}

func inferTopicFamily(typ, title, content string) string {
	t := strings.TrimSpace(strings.ToLower(typ))
	switch t {
	case "architecture", "design", "adr", "refactor":
		return "architecture"
	case "bug", "bugfix", "fix", "incident", "hotfix":
		return "bug"
	case "decision":
		return "decision"
	case "pattern", "convention", "guideline":
		return "pattern"
	case "config", "setup", "infra", "infrastructure", "ci":
		return "config"
	case "discovery", "investigation", "root_cause", "root-cause":
		return "discovery"
	case "learning", "learn":
		return "learning"
	case "session_summary":
		return "session"
	}

	text := strings.ToLower(title + " " + content)
	if hasAny(text, "bug", "fix", "panic", "error", "crash", "regression", "incident", "hotfix") {
		return "bug"
	}
	if hasAny(text, "architecture", "design", "adr", "boundary", "hexagonal", "refactor") {
		return "architecture"
	}
	if hasAny(text, "decision", "tradeoff", "chose", "choose", "decide") {
		return "decision"
	}
	if hasAny(text, "pattern", "convention", "naming", "guideline") {
		return "pattern"
	}
	if hasAny(text, "config", "setup", "environment", "env", "docker", "pipeline") {
		return "config"
	}
	if hasAny(text, "discovery", "investigate", "investigation", "found", "root cause") {
		return "discovery"
	}
	if hasAny(text, "learned", "learning") {
		return "learning"
	}

	if t != "" && t != "manual" {
		return normalizeTopicSegment(t)
	}

	return "topic"
}

func hasAny(text string, words ...string) bool {
	for _, w := range words {
		if strings.Contains(text, w) {
			return true
		}
	}
	return false
}

// normalizeTopicSegment converts a string into a URL-safe slug.
var nonAlphanumericRe = regexp.MustCompile(`[^a-z0-9]+`)

func normalizeTopicSegment(s string) string {
	v := strings.ToLower(strings.TrimSpace(s))
	if v == "" {
		return ""
	}
	v = nonAlphanumericRe.ReplaceAllString(v, " ")
	v = strings.Join(strings.Fields(v), "-")
	if len(v) > 100 {
		v = v[:100]
	}
	return v
}

// -- Passive Capture: Learning Extraction --

var (
	learningHeaderPattern = regexp.MustCompile(`(?im)^#{2,3}\s+(?:Aprendizajes(?:\s+Clave)?|Key\s+Learnings?|Learnings?):?\s*$`)
	nextHeaderRe          = regexp.MustCompile(`\n#{1,3} `)
	numberedItemRe        = regexp.MustCompile(`(?m)^\s*\d+[.)]\s+(.+)`)
	bulletItemRe          = regexp.MustCompile(`(?m)^\s*[-*]\s+(.+)`)
)

const (
	minLearningLength = 20
	minLearningWords  = 4
)

// extractLearnings parses structured learning items from text.
func extractLearnings(text string) []string {
	matches := learningHeaderPattern.FindAllStringIndex(text, -1)
	if len(matches) == 0 {
		return nil
	}

	// Process sections in reverse  -- use first valid one
	for i := len(matches) - 1; i >= 0; i-- {
		sectionStart := matches[i][1]
		sectionText := text[sectionStart:]

		// Cut off at next major section header
		if nextHeader := nextHeaderRe.FindStringIndex(sectionText); nextHeader != nil {
			sectionText = sectionText[:nextHeader[0]]
		}

		var learnings []string

		// Try numbered items: "1. text" or "1) text"
		numbered := numberedItemRe.FindAllStringSubmatch(sectionText, -1)
		if len(numbered) > 0 {
			for _, m := range numbered {
				cleaned := cleanMarkdown(m[1])
				if len(cleaned) >= minLearningLength && len(strings.Fields(cleaned)) >= minLearningWords {
					learnings = append(learnings, cleaned)
				}
			}
		}

		// Fall back to bullet items: "- text" or "* text"
		if len(learnings) == 0 {
			bullets := bulletItemRe.FindAllStringSubmatch(sectionText, -1)
			for _, m := range bullets {
				cleaned := cleanMarkdown(m[1])
				if len(cleaned) >= minLearningLength && len(strings.Fields(cleaned)) >= minLearningWords {
					learnings = append(learnings, cleaned)
				}
			}
		}

		if len(learnings) > 0 {
			return learnings
		}
	}

	return nil
}

var (
	boldRe   = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	codeRe   = regexp.MustCompile("`([^`]+)`")
	italicRe = regexp.MustCompile(`\*([^*]+)\*`)
)

// cleanMarkdown strips basic markdown formatting and collapses whitespace.
func cleanMarkdown(text string) string {
	text = boldRe.ReplaceAllString(text, "$1")   // bold
	text = codeRe.ReplaceAllString(text, "$1")   // inline code
	text = italicRe.ReplaceAllString(text, "$1") // italic
	return strings.TrimSpace(strings.Join(strings.Fields(text), " "))
}
