package server

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lleontor705/cortex/v2/internal/config"
	"github.com/lleontor705/cortex/v2/internal/domain"
	agentdomain "github.com/lleontor705/cortex/v2/internal/domain/agent"
	"github.com/lleontor705/cortex/v2/internal/domain/code"
)

type recordingAgentOperations struct {
	searchProjectID          string
	searchProject            string
	codeProject              string
	codeResultProject        string
	vectorLookupProjectID    string
	vectorLookupProjectLabel string
}

const recordingAgentProjectID = "10000000-a000-0000-0000-000000000003"

func (o *recordingAgentOperations) SearchObservations(_ context.Context, _ string, opts domain.SearchOptions) ([]*domain.SearchResult, error) {
	return nil, errors.New("unsafe generic search path used")
}

func (o *recordingAgentOperations) SearchAgentObservations(_ context.Context, projectID, projectLabel, _ string, opts domain.SearchOptions) ([]*domain.SearchResult, error) {
	o.searchProjectID = projectID
	o.searchProject = opts.Project
	if projectLabel != opts.Project {
		return nil, errors.New("project label was not preserved")
	}
	return []*domain.SearchResult{{Observation: domain.Observation{Title: "Decision", Content: "Use PostgreSQL", Project: opts.Project}, Rank: .9}}, nil
}

func (o *recordingAgentOperations) GetObservationByID(context.Context, int64) (*domain.Observation, error) {
	return nil, domain.ErrNotFound
}

func (o *recordingAgentOperations) GetAgentObservationByID(_ context.Context, projectID, projectLabel string, id int64) (*domain.Observation, error) {
	o.vectorLookupProjectID, o.vectorLookupProjectLabel = projectID, projectLabel
	return &domain.Observation{ID: id, Project: projectLabel, Title: "Vector", Content: "Scoped"}, nil
}

func (o *recordingAgentOperations) ListCodeSymbols(_ context.Context, filter code.SymbolFilter) ([]code.Symbol, error) {
	o.codeProject = filter.Project
	resultProject := o.codeResultProject
	if resultProject == "" {
		resultProject = recordingAgentProjectID
	}
	return []code.Symbol{{ID: "func:Open", Project: resultProject, Name: "Open", Kind: "func", Signature: "func Open(ctx context.Context, cfg config.Config)", FilePath: "internal/platform/server/server.go", LineNumber: 54, DocSummary: "Builds the server runtime."}}, nil
}

func (o *recordingAgentOperations) GetCodeGraph(context.Context, string) (*code.CodeGraph, error) {
	return &code.CodeGraph{Project: recordingAgentProjectID}, nil
}

type firstHandleCompletion struct{}

func (firstHandleCompletion) Complete(_ context.Context, req agentdomain.CompletionRequest) (agentdomain.CompletionResult, error) {
	start := strings.Index(req.UserPrompt, "src_")
	if start < 0 {
		return agentdomain.CompletionResult{}, nil
	}
	end := start
	for end < len(req.UserPrompt) && req.UserPrompt[end] != '"' {
		end++
	}
	return agentdomain.CompletionResult{Claims: []agentdomain.CompletionClaim{{Text: "Respuesta basada en el proyecto.", CitationHandles: []string{req.UserPrompt[start:end]}}}}, nil
}

func TestServerAgentPreservesProjectLabelAndPublicID(t *testing.T) {
	ops := &recordingAgentOperations{}
	svc := newServerAgentService(ops, nil, nil, firstHandleCompletion{})
	answer, err := svc.Answer(context.Background(), serverAgentRequest{
		tenantID: "tenant-a", workspaceID: "workspace-a",
		project:  agentProject{ID: recordingAgentProjectID, Label: "cortex"},
		question: "¿Cómo inicia el servidor?",
	})
	if err != nil {
		t.Fatalf("Answer() = %v", err)
	}
	if ops.searchProject != "cortex" {
		t.Fatalf("memory project = %q, want label", ops.searchProject)
	}
	if ops.searchProjectID != recordingAgentProjectID {
		t.Fatalf("memory authorization project = %q, want public id", ops.searchProjectID)
	}
	if ops.codeProject != recordingAgentProjectID {
		t.Fatalf("code project = %q, want public id", ops.codeProject)
	}
	if len(answer.Sources) != 1 {
		t.Fatalf("sources = %#v, want one server-issued source", answer.Sources)
	}
}

func TestAgentCodeRetrieverSeparatesUUIDIdentityFromLabel(t *testing.T) {
	ops := &recordingAgentOperations{}
	ctx := context.WithValue(context.Background(), agentProjectIDKey{}, recordingAgentProjectID)
	evidence, err := (agentCodeRetriever{ops: ops}).Retrieve(ctx, agentdomain.Scope{Project: "cortex"}, "Open", 5)
	if err != nil {
		t.Fatalf("Retrieve() = %v", err)
	}
	if ops.codeProject != recordingAgentProjectID || len(evidence) != 1 || evidence[0].Path == "" {
		t.Fatalf("code retrieval project=%q evidence=%#v", ops.codeProject, evidence)
	}
}

func TestAgentCodeRetrieverRejectsSymbolFromDifferentUUID(t *testing.T) {
	ops := &recordingAgentOperations{codeResultProject: "20000000-a000-0000-0000-000000000003"}
	ctx := context.WithValue(context.Background(), agentProjectIDKey{}, recordingAgentProjectID)
	evidence, err := (agentCodeRetriever{ops: ops}).Retrieve(ctx, agentdomain.Scope{Project: "cortex"}, "Open", 5)
	if err == nil || len(evidence) != 0 {
		t.Fatalf("Retrieve() = %#v, %v; want UUID mismatch denied", evidence, err)
	}
}

type agentRecordingVectorIndex struct{ query domain.VectorQuery }

func (*agentRecordingVectorIndex) ID() string                                         { return "recording" }
func (*agentRecordingVectorIndex) Upsert(context.Context, []domain.VectorPoint) error { return nil }
func (v *agentRecordingVectorIndex) Search(_ context.Context, q domain.VectorQuery) ([]domain.VectorCandidate, error) {
	v.query = q
	return []domain.VectorCandidate{{ID: 7, Score: .9}}, nil
}
func (*agentRecordingVectorIndex) Delete(context.Context, []int64) error { return nil }
func (*agentRecordingVectorIndex) Health(context.Context) domain.Health {
	return domain.Health{Status: domain.StatusHealthy}
}
func (*agentRecordingVectorIndex) Capabilities(context.Context) (domain.Capabilities, error) {
	return domain.Capabilities{Filters: "PreFilter"}, nil
}
func (*agentRecordingVectorIndex) Close() error { return nil }

type fixedAgentEmbedding struct{}

func (fixedAgentEmbedding) Embed(context.Context, string) ([]float32, error) {
	return []float32{1, 0}, nil
}
func (fixedAgentEmbedding) Dimensions() int { return 2 }
func (fixedAgentEmbedding) Model() string   { return "test" }

func TestAgentVectorRetrievalUsesUUIDAndRevalidatesSameProject(t *testing.T) {
	ops := &recordingAgentOperations{}
	vectors := &agentRecordingVectorIndex{}
	ctx := context.WithValue(context.Background(), agentProjectIDKey{}, recordingAgentProjectID)
	_, err := (agentMemoryRetriever{ops: ops, vectors: vectors, embeddings: fixedAgentEmbedding{}}).Retrieve(ctx, agentdomain.Scope{TenantID: "tenant", WorkspaceID: "workspace", Project: "duplicate-label"}, "query", 5)
	if err != nil {
		t.Fatal(err)
	}
	if vectors.query.Filters["project_id"] != recordingAgentProjectID {
		t.Fatalf("vector filters=%v", vectors.query.Filters)
	}
	if ops.vectorLookupProjectID != recordingAgentProjectID || ops.vectorLookupProjectLabel != "duplicate-label" {
		t.Fatalf("hydration identity=%q label=%q", ops.vectorLookupProjectID, ops.vectorLookupProjectLabel)
	}
}

func TestConfiguredChatProviderUsesOnlyAdminConfigurationAndHardenedTransport(t *testing.T) {
	const apiKey = "server-owned-key"
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer "+apiKey {
			t.Fatalf("unexpected provider request path=%q authorization=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		var body struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Messages) != 2 || body.Messages[0].Role != "system" {
			t.Fatalf("invalid chat payload: %#v err=%v", body, err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"claims\":[{\"text\":\"ok\",\"citation_handles\":[\"src_test_001\"]}]}"}}],"usage":{"prompt_tokens":7,"completion_tokens":3}}`))
	}))
	defer provider.Close()
	pool := x509.NewCertPool()
	pool.AddCert(provider.Certificate())
	completion, err := newConfiguredChatProvider(config.ServerLLMConfig{
		Provider: "generic", BaseURL: provider.URL + "/v1", APIKey: apiKey, Model: "admin-model",
		AllowLoopback: true, MaxConcurrent: 1, MaxRedirects: 1, MaxResponseBodyBytes: 4096,
		MaxErrorBodyBytes: 1024, CACertPool: pool,
	})
	if err != nil {
		t.Fatalf("newConfiguredChatProvider() = %v", err)
	}
	result, err := completion.Complete(context.Background(), agentdomain.CompletionRequest{SystemPrompt: "policy", UserPrompt: "evidence"})
	if err != nil || len(result.Claims) != 1 || result.Claims[0].Text != "ok" || len(result.Claims[0].CitationHandles) != 1 || result.InputTokens != 7 || result.OutputTokens != 3 {
		t.Fatalf("Complete() = %#v, %v", result, err)
	}
}

func TestConfiguredChatProviderStreamsOpenAICompatibleClaimsProgressively(t *testing.T) {
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Stream bool `json:"stream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !body.Stream {
			t.Fatalf("stream payload=%#v err=%v", body, err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"{\\\"text\\\":\\\"uno\\\",\\\"citation_handles\\\":[\\\"src_test_001\\\"]}\\n\"}}]}\n\n"))
		flusher.Flush()
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"{\\\"text\\\":\\\"dos\\\",\\\"citation_handles\\\":[\\\"src_test_001\\\"]}\\n\"}}],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":3}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer provider.Close()
	pool := x509.NewCertPool()
	pool.AddCert(provider.Certificate())
	completion, err := newConfiguredChatProvider(config.ServerLLMConfig{
		Provider: "generic", BaseURL: provider.URL, Model: "admin-model", AllowLoopback: true,
		MaxConcurrent: 1, MaxRedirects: 1, MaxResponseBodyBytes: 4096, MaxErrorBodyBytes: 1024, CACertPool: pool,
	})
	if err != nil {
		t.Fatalf("newConfiguredChatProvider: %v", err)
	}
	streamer, ok := completion.(agentdomain.StreamingCompletionProvider)
	if !ok {
		t.Fatal("configured provider does not implement streaming")
	}
	var claims []string
	usage, err := streamer.Stream(context.Background(), agentdomain.CompletionRequest{SystemPrompt: "policy", UserPrompt: "evidence"}, func(claim agentdomain.CompletionClaim) error {
		claims = append(claims, claim.Text)
		return nil
	})
	if err != nil || strings.Join(claims, ",") != "uno,dos" || usage.InputTokens != 7 || usage.OutputTokens != 3 {
		t.Fatalf("Stream claims=%#v usage=%#v err=%v", claims, usage, err)
	}
}

func TestConfiguredChatProviderStreamPropagatesCancellationUpstream(t *testing.T) {
	upstreamStarted := make(chan struct{})
	upstreamCancelled := make(chan struct{})
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		close(upstreamStarted)
		<-r.Context().Done()
		close(upstreamCancelled)
	}))
	defer provider.Close()
	pool := x509.NewCertPool()
	pool.AddCert(provider.Certificate())
	completion, err := newConfiguredChatProvider(config.ServerLLMConfig{
		Provider: "generic", BaseURL: provider.URL, Model: "admin-model", AllowLoopback: true,
		MaxConcurrent: 1, MaxRedirects: 1, MaxResponseBodyBytes: 4096, MaxErrorBodyBytes: 1024, CACertPool: pool,
	})
	if err != nil {
		t.Fatalf("newConfiguredChatProvider: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, streamErr := completion.(agentdomain.StreamingCompletionProvider).Stream(ctx, agentdomain.CompletionRequest{SystemPrompt: "policy", UserPrompt: "evidence"}, func(agentdomain.CompletionClaim) error { return nil })
		done <- streamErr
	}()
	select {
	case <-upstreamStarted:
	case <-time.After(time.Second):
		t.Fatal("provider request did not start")
	}
	cancel()
	select {
	case <-upstreamCancelled:
	case <-time.After(time.Second):
		t.Fatal("provider request context was not cancelled")
	}
	select {
	case streamErr := <-done:
		if streamErr == nil {
			t.Fatal("Stream returned nil after cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("Stream did not return after cancellation")
	}
}
