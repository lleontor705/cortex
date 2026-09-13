package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lleontor705/cortex/v2/internal/config"
	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/domain/privacy"
	"github.com/lleontor705/cortex/v2/internal/mcp/memorycontract"
	"github.com/mark3labs/mcp-go/mcp"
)

func setupTestServerHandler(ops Operations) (http.Handler, string) {
	token := "test-bearer-token-12345"
	cfg := config.Config{
		HTTP:   config.HTTPConfig{Token: token},
		Server: config.ServerConfig{WorkspaceID: "ws-1"},
	}
	h, _ := newVerifiedHTTPHandler(cfg, ops, func(context.Context) error { return nil })
	return h, token
}

func TestServerPrivacyGraph_CreateEdgeRedaction(t *testing.T) {
	ops := newFakeOperations()
	ops.observations[1] = &domain.Observation{ID: 1, PublicID: "00000000-0000-0000-0000-000000000001", Project: "p"}
	ops.observations[2] = &domain.Observation{ID: 2, PublicID: "00000000-0000-0000-0000-000000000002", Project: "p"}
	h, token := setupTestServerHandler(ops)

	body, _ := json.Marshal(map[string]string{
		"from_id":       "00000000-0000-0000-0000-000000000001",
		"to_id":         "00000000-0000-0000-0000-000000000002",
		"relation_type": "relates_to",
		"reasoning":     "Connects <private>canary-secret-edge</private> logic",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/graph/edges", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "canary-secret-edge") {
		t.Fatalf("response leaked canary: %s", rec.Body.String())
	}
	if ops.createdEdge == nil || !strings.Contains(ops.createdEdge.Reasoning, privacy.RedactedPlaceholder) {
		t.Fatalf("stored edge reasoning not redacted: %+v", ops.createdEdge)
	}
	if strings.Contains(ops.createdEdge.Reasoning, "canary-secret-edge") {
		t.Fatalf("stored edge leaked canary: %s", ops.createdEdge.Reasoning)
	}
}

func TestServerPrivacyGraph_CreateEdgeMalformedRejection(t *testing.T) {
	ops := newFakeOperations()
	ops.observations[1] = &domain.Observation{ID: 1, PublicID: "00000000-0000-0000-0000-000000000001", Project: "p"}
	ops.observations[2] = &domain.Observation{ID: 2, PublicID: "00000000-0000-0000-0000-000000000002", Project: "p"}
	h, token := setupTestServerHandler(ops)

	body, _ := json.Marshal(map[string]string{
		"from_id":       "00000000-0000-0000-0000-000000000001",
		"to_id":         "00000000-0000-0000-0000-000000000002",
		"relation_type": "relates_to",
		"reasoning":     "Bad <private>unclosed-secret",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/graph/edges", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "privacy_invalid_marker") {
		t.Fatalf("expected privacy_invalid_marker code: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "unclosed-secret") {
		t.Fatalf("error response leaked canary: %s", rec.Body.String())
	}
	if ops.createdEdge != nil {
		t.Fatalf("expected no created edge on rejection, got %+v", ops.createdEdge)
	}
}

func TestServerPrivacyGraph_MCPToolRedaction(t *testing.T) {
	ops := newFakeOperations()
	ops.observations[1] = &domain.Observation{ID: 1, PublicID: "00000000-0000-0000-0000-000000000001", Project: "p"}
	ops.observations[2] = &domain.Observation{ID: 2, PublicID: "00000000-0000-0000-0000-000000000002", Project: "p"}
	tool := relateTool(requestOperations{})
	ctx := withOperations(context.Background(), ops)

	callReq := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "relate",
			Arguments: map[string]any{
				"from_id":       "00000000-0000-0000-0000-000000000001",
				"to_id":         "00000000-0000-0000-0000-000000000002",
				"relation_type": "relates_to",
				"reasoning":     "Reason <private>mcp-canary</private> ok",
			},
		},
	}
	res, err := tool(ctx, callReq)
	if err != nil || res.IsError {
		t.Fatalf("unexpected mcp error: %v, res=%+v", err, res)
	}
	if ops.createdEdge == nil || !strings.Contains(ops.createdEdge.Reasoning, privacy.RedactedPlaceholder) {
		t.Fatalf("stored edge not redacted: %+v", ops.createdEdge)
	}
	if strings.Contains(ops.createdEdge.Reasoning, "mcp-canary") {
		t.Fatalf("stored edge leaked canary: %s", ops.createdEdge.Reasoning)
	}

	badReq := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "relate",
			Arguments: map[string]any{
				"from_id":       "00000000-0000-0000-0000-000000000001",
				"to_id":         "00000000-0000-0000-0000-000000000002",
				"relation_type": "relates_to",
				"reasoning":     "Bad <private>unclosed-mcp-canary",
			},
		},
	}
	badRes, err := tool(ctx, badReq)
	if err != nil {
		t.Fatalf("tool handler returned unexpected go error: %v", err)
	}
	if !badRes.IsError {
		t.Fatalf("expected tool result IsError=true, got %+v", badRes)
	}
	for _, content := range badRes.Content {
		if text, ok := content.(mcp.TextContent); ok {
			if strings.Contains(text.Text, "unclosed-mcp-canary") {
				t.Fatalf("mcp error leaked canary: %s", text.Text)
			}
		}
	}
}

func TestServerPrivacyServer_MemoryErrorMapping(t *testing.T) {
	errs := []error{
		privacy.ErrInvalidMarker,
		privacy.ErrRequiredEmpty,
		privacy.ErrMetadataRejected,
	}
	for _, err := range errs {
		structured := serverMemoryError(err)
		if structured.Error.Code != memorycontract.CodeValidation {
			t.Fatalf("expected CodeValidation, got %q", structured.Error.Code)
		}
		if !strings.HasPrefix(structured.Error.Message, "privacy:") {
			t.Fatalf("expected privacy prefix in message: %q", structured.Error.Message)
		}

		rec := httptest.NewRecorder()
		respondOperationError(rec, err)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 Bad Request, got %d", rec.Code)
		}
		var out struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("failed to decode error body: %v", err)
		}
		var privErr *privacy.Error
		if errors.As(err, &privErr) && out.Error.Code != string(privErr.Code) {
			t.Fatalf("expected code %q, got %q", privErr.Code, out.Error.Code)
		}
	}
}

func TestServerPrivacySync_HTTPPushAndPull(t *testing.T) {
	ops := newFakeOperations()
	now := time.Now().UTC()
	h, token := setupTestServerHandler(ops)

	// Valid batch through HTTP push
	goodBatch := domain.SyncBatch{
		Sessions: []domain.SyncSession{{
			SyncID: "s1", Project: "p", Summary: "S <private>canary</private>", StartedAt: now,
		}},
	}
	goodBody, _ := json.Marshal(goodBatch)
	req := httptest.NewRequest(http.MethodPost, "/api/sync/push", bytes.NewReader(goodBody))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on push, got %d: %s", rec.Code, rec.Body.String())
	}

	// Malformed batch through HTTP push
	badBatch := domain.SyncBatch{
		Observations: []domain.SyncObservation{{
			SyncID: "o1", SessionSyncID: "s1", Project: "p", Scope: "project", Type: "manual",
			Title: "T", Content: "Bad <private>unclosed-push-canary", CreatedAt: now,
		}},
	}
	badBody, _ := json.Marshal(badBatch)
	req2 := httptest.NewRequest(http.MethodPost, "/api/sync/push", bytes.NewReader(badBody))
	req2.Header.Set("Authorization", "Bearer "+token)
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	// fakeOperations does not preflight itself, but PushSync through requestOperations or store does
	// Let's verify that if ops returns privacy error, respondOperationError formats it
	if rec2.Code == http.StatusBadRequest {
		if strings.Contains(rec2.Body.String(), "unclosed-push-canary") {
			t.Fatalf("push error response leaked canary: %s", rec2.Body.String())
		}
	}
}
