package mcp

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain/payload"
	"github.com/lleontor705/cortex/v2/internal/domain/sandbox"
	"github.com/lleontor705/cortex/v2/internal/store/bundle"
	sqlitestore "github.com/lleontor705/cortex/v2/internal/store/sqlite"
	"github.com/mark3labs/mcp-go/mcp"
	_ "modernc.org/sqlite"
)

func newTestSandboxStores(t *testing.T) (*bundle.Stores, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}

	transientStore, err := sqlitestore.NewTransientPayloadStore(db)
	if err != nil {
		t.Fatalf("NewTransientPayloadStore failed: %v", err)
	}

	stores := &bundle.Stores{
		TransientPayloads: transientStore,
	}
	return stores, db
}

func TestSandboxTools_ExecuteAndExternalize(t *testing.T) {
	runner := sandbox.NewDefaultRunner()
	runtimes := runner.AvailableRuntimes()
	if len(runtimes) == 0 {
		t.Skip("No runtimes available on host for sandbox test")
	}

	stores, db := newTestSandboxStores(t)
	defer func() { _ = db.Close() }()

	srv := NewServer(stores)

	// Pick available language
	var lang string
	var smallScript, largeScript string
	if _, ok := runtimes[sandbox.LanguagePowerShell]; ok {
		lang = "powershell"
		smallScript = `Write-Output "cortex-small-output-test"`
		// Generates ~120 KB of text
		largeScript = `$line = "A" * 100; for ($i=0; $i -lt 1200; $i++) { Write-Output ("Line " + $i + " " + $line + " special_unique_token_xyz") }`
	} else if _, ok := runtimes[sandbox.LanguageGo]; ok {
		lang = "go"
		smallScript = `package main; import "fmt"; func main() { fmt.Println("cortex-small-output-test") }`
		largeScript = `package main; import "fmt"; import "strings"; func main() { line := strings.Repeat("A", 100); for i:=0; i<1200; i++ { fmt.Printf("Line %d: %s special_unique_token_xyz\n", i, line) } }`
	} else {
		t.Skip("Neither powershell nor go available")
	}

	ctx := context.Background()

	// 1. Test Small Output Execution
	t.Run("SmallOutput", func(t *testing.T) {
		req := mcp.CallToolRequest{}
		req.Params.Name = "cortex_execute"
		req.Params.Arguments = map[string]any{
			"language": lang,
			"code":     smallScript,
		}

		handler := handleExecute(stores)
		res, err := handler(ctx, req)
		if err != nil {
			t.Fatalf("handleExecute failed: %v", err)
		}
		if res.IsError {
			t.Fatalf("Execution returned error result: %+v", res)
		}

		text := res.Content[0].(mcp.TextContent).Text
		if !strings.Contains(text, "cortex-small-output-test") {
			t.Errorf("Expected 'cortex-small-output-test' in output, got: %q", text)
		}
		if strings.Contains(text, "OUTPUT EXTERNALIZED") {
			t.Errorf("Small output should not be externalized: %q", text)
		}
	})

	// 2. Test Large Output Auto-Externalization (>100 KB)
	var payloadID string
	t.Run("LargeOutputExternalization", func(t *testing.T) {
		req := mcp.CallToolRequest{}
		req.Params.Name = "cortex_execute"
		req.Params.Arguments = map[string]any{
			"language": lang,
			"code":     largeScript,
			"project":  "test-proj",
		}

		handler := handleExecute(stores)
		res, err := handler(ctx, req)
		if err != nil {
			t.Fatalf("handleExecute large script failed: %v", err)
		}
		if res.IsError {
			t.Fatalf("Large execution returned error result: %+v", res)
		}

		text := res.Content[0].(mcp.TextContent).Text
		if !strings.Contains(text, "OUTPUT EXTERNALIZED TO TRANSIENT STORE") {
			t.Fatalf("Expected output to be externalized, got: %q", text)
		}

		// Extract payload ID
		for _, line := range strings.Split(text, "\n") {
			if strings.HasPrefix(line, "Payload ID: payload-") {
				payloadID = strings.TrimSpace(strings.TrimPrefix(line, "Payload ID:"))
				break
			}
		}

		if payloadID == "" {
			t.Fatalf("Failed to parse Payload ID from result: %s", text)
		}
		t.Logf("Extracted Payload ID: %s", payloadID)
	})

	// 3. Test Search in Externalized Payload
	t.Run("SearchExternalizedPayload", func(t *testing.T) {
		if payloadID == "" {
			t.Skip("Skipping search: no payloadID from previous step")
		}

		req := mcp.CallToolRequest{}
		req.Params.Name = "cortex_search_payload"
		req.Params.Arguments = map[string]any{
			"payload_id": payloadID,
			"query":      "special_unique_token_xyz",
			"limit":      3,
		}

		searchHandler := handleSearchPayload(stores)
		res, err := searchHandler(ctx, req)
		if err != nil {
			t.Fatalf("handleSearchPayload failed: %v", err)
		}
		if res.IsError {
			t.Fatalf("Search returned error result: %+v", res)
		}

		text := res.Content[0].(mcp.TextContent).Text
		if !strings.Contains(text, "special_unique_token_xyz") {
			t.Errorf("Expected match in search output, got: %q", text)
		}
	})

	_ = srv
}

func TestStats_WithTransientPayloads(t *testing.T) {
	stores := setupTestStores(t)
	ctx := context.Background()

	// 1. Initial stats without payloads
	statsHandler := handleStats(stores)
	req := mcp.CallToolRequest{}
	req.Params.Name = "cortex_stats"

	res, err := statsHandler(ctx, req)
	if err != nil {
		t.Fatalf("handleStats failed: %v", err)
	}
	text := res.Content[0].(mcp.TextContent).Text
	if strings.Contains(text, "Context Optimization:") {
		t.Errorf("Stats without payloads should not contain Context Optimization: %s", text)
	}

	// 2. Save a transient payload
	tp := payload.NewTransientPayload("sess-1", "proj-1", "cortex_execute", strings.Repeat("test log data\n", 100))
	if err := stores.TransientPayloads.Save(ctx, tp); err != nil {
		t.Fatalf("Save payload failed: %v", err)
	}

	// 3. Stats with payload
	resWithPayload, err := statsHandler(ctx, req)
	if err != nil {
		t.Fatalf("handleStats with payload failed: %v", err)
	}
	textWithPayload := resWithPayload.Content[0].(mcp.TextContent).Text
	if !strings.Contains(textWithPayload, "Context Optimization:") {
		t.Errorf("Expected Context Optimization in stats output, got: %s", textWithPayload)
	}
	if !strings.Contains(textWithPayload, "Externalized Payloads: 1") {
		t.Errorf("Expected Externalized Payloads: 1 in stats output, got: %s", textWithPayload)
	}
	if !strings.Contains(textWithPayload, "Estimated Tokens Saved:") {
		t.Errorf("Expected Estimated Tokens Saved in stats output, got: %s", textWithPayload)
	}
}
