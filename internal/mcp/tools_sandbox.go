package mcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/lleontor705/cortex/v2/internal/domain/payload"
	"github.com/lleontor705/cortex/v2/internal/domain/sandbox"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// registerSandboxTools registers execution sandbox and payload inspection tools.
func registerSandboxTools(srv *server.MCPServer, stores *Stores, allowlist map[string]bool) {
	// --- cortex_execute -------------------------------------------------
	if shouldRegister("cortex_execute", allowlist) {
		srv.AddTool(
			mcp.NewTool("cortex_execute",
				mcp.WithTitleAnnotation("Execute Code in Sandbox"),
				mcp.WithReadOnlyHintAnnotation(false),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithIdempotentHintAnnotation(false),
				mcp.WithOpenWorldHintAnnotation(false),
				mcp.WithDescription("Execute scripts in an isolated subprocess sandbox (supports python, node/bun, go, powershell, bash). Injects FILE_CONTENT and FILE_PATH for processing large files locally without dumping them into context. Outputs >= 100 KB are automatically externalized to transient FTS5 storage, returning an ID and summary."),
				mcp.WithString("code",
					mcp.Required(),
					mcp.Description("Script code to execute in the sandbox"),
				),
				mcp.WithString("language",
					mcp.Required(),
					mcp.Description("Language to execute: python, node, bun, go, powershell, bash"),
				),
				mcp.WithString("file_path",
					mcp.Description("Optional local file path to process; accessible via FILE_PATH or FILE_CONTENT env vars"),
				),
				mcp.WithNumber("timeout_seconds",
					mcp.Description("Execution timeout in seconds (default: 15, max: 60)"),
				),
				mcp.WithString("project",
					mcp.Description("Optional project name for categorization"),
				),
				mcp.WithString("session_id",
					mcp.Description("Optional session ID for scoping externalized payloads"),
				),
			),
			handleExecute(stores),
		)
	}

	// --- cortex_search_payload ------------------------------------------
	if shouldRegister("cortex_search_payload", allowlist) {
		srv.AddTool(
			mcp.NewTool("cortex_search_payload",
				mcp.WithTitleAnnotation("Search Externalized Payload"),
				mcp.WithReadOnlyHintAnnotation(true),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithIdempotentHintAnnotation(true),
				mcp.WithOpenWorldHintAnnotation(false),
				mcp.WithDescription("Search within externalized execution outputs or transient payloads using full-text search (FTS5)."),
				mcp.WithString("query",
					mcp.Required(),
					mcp.Description("Search terms or keywords"),
				),
				mcp.WithString("payload_id",
					mcp.Description("Optional specific payload ID to restrict search"),
				),
				mcp.WithNumber("limit",
					mcp.Description("Maximum number of snippet results (default: 5)"),
				),
			),
			handleSearchPayload(stores),
		)
	}
}

func handleExecute(stores *Stores) server.ToolHandlerFunc {
	runner := sandbox.NewDefaultRunner()

	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		code := stringArg(req, "code")
		if strings.TrimSpace(code) == "" {
			return errorResult("code argument cannot be empty")
		}

		langStr := stringArg(req, "language")
		lang, err := sandbox.NormalizeLanguage(langStr)
		if err != nil {
			return errorResult("%s", err.Error())
		}

		filePath := stringArg(req, "file_path")
		timeoutSec := intArg(req, "timeout_seconds", 15)
		project := stringArg(req, "project")
		sessionID := stringArg(req, "session_id")
		if sessionID == "" {
			sessionID = "default"
		}

		timeout := time.Duration(timeoutSec) * time.Second

		execReq := sandbox.ExecutionRequest{
			Language: lang,
			Code:     code,
			FilePath: filePath,
			Timeout:  timeout,
		}

		res, err := runner.Execute(ctx, execReq)
		if err != nil {
			return errorResult("Sandbox execution failed: %s", err.Error())
		}

		if res.ExitCode != 0 {
			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("Execution failed with exit code %d (in %v):\n", res.ExitCode, res.Duration))
			if res.Stderr != "" {
				sb.WriteString("STDERR:\n" + res.Stderr + "\n")
			}
			if res.Stdout != "" {
				sb.WriteString("STDOUT:\n" + res.Stdout)
			}
			return errorResult("%s", sb.String())
		}

		// Check if output meets auto-externalization threshold (>100 KB)
		if stores != nil && stores.TransientPayloads != nil && payload.ShouldExternalize(len(res.Stdout)) {
			tp := payload.NewTransientPayload(sessionID, project, "cortex_execute", res.Stdout)
			if saveErr := stores.TransientPayloads.Save(ctx, tp); saveErr == nil {
				return textResult("[OUTPUT EXTERNALIZED TO TRANSIENT STORE]\n"+
					"Payload ID: %s\n"+
					"Raw Output Size: %d bytes (~%d estimated tokens)\n"+
					"Tokens Saved: ~%d tokens\n"+
					"Execution Time: %v\n\n"+
					"Snippet Preview:\n%s\n\n"+
					"Hint: Use cortex_search_payload(payload_id: %q, query: \"...\") to inspect details.",
					tp.ID, tp.ByteCount, payload.EstimateTokens(tp.Content), tp.TokensSaved,
					res.Duration, tp.Snippet, tp.ID,
				)
			}
		}

		// Standard direct response
		var sb strings.Builder
		sb.WriteString(res.Stdout)
		if res.Stderr != "" {
			sb.WriteString("\n[STDERR]\n" + res.Stderr)
		}
		if res.Truncated {
			sb.WriteString("\n[Note: Output reached buffer size limit and was truncated]")
		}
		return textResult("%s", sb.String())
	}
}

func handleSearchPayload(stores *Stores) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if stores == nil || stores.TransientPayloads == nil {
			return errorResult("Transient payload store is not available")
		}

		query := stringArg(req, "query")
		if strings.TrimSpace(query) == "" {
			return errorResult("query argument cannot be empty")
		}

		payloadID := stringArg(req, "payload_id")
		limit := intArg(req, "limit", 5)

		results, err := stores.TransientPayloads.Search(ctx, payloadID, query, limit)
		if err != nil {
			return errorResult("Search failed: %s", err.Error())
		}

		if len(results) == 0 {
			return textResult("No matches found for query %q in payload %q.", query, payloadID)
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Found %d match(es) for query %q:\n\n", len(results), query))
		for i, match := range results {
			sb.WriteString(fmt.Sprintf("%d. %s\n\n", i+1, match.Snippet))
		}

		return textResult("%s", strings.TrimSpace(sb.String()))
	}
}
