package embedding

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// mockMRLServer captures the raw request body so tests can assert the
// `dimensions` parameter, and answers with a vector of the requested size.
func mockMRLServer(t *testing.T, wantKey string, capture func(body map[string]any)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embeddings" {
			t.Errorf("path = %q, want /embeddings", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+wantKey {
			t.Errorf("authorization = %q, want bearer %q", got, wantKey)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		if capture != nil {
			capture(body)
		}
		dims := 4096
		if d, ok := body["dimensions"].(float64); ok && d > 0 {
			dims = int(d)
		}
		count := 1
		if inputs, ok := body["input"].([]any); ok {
			count = len(inputs)
		}
		data := make([]map[string]any, count)
		for i := range data {
			emb := make([]float64, dims)
			for j := range emb {
				emb[j] = float64(j) / float64(dims)
			}
			data[i] = map[string]any{"index": i, "embedding": emb}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
}

// TestDimensionsParameterOmittedByDefault pins the default behavior: without
// a configured dimension the request body carries no `dimensions` key and the
// provider default (4096 for qwen3-embedding) flows through unchanged.
func TestDimensionsParameterOmittedByDefault(t *testing.T) {
	var gotDimensions any = "absent"
	srv := mockMRLServer(t, "test-key", func(body map[string]any) {
		gotDimensions, _ = body["dimensions"]
	})
	defer srv.Close()

	svc := newWithClient(Config{Provider: "openai-compatible", APIKey: "test-key", BaseURL: srv.URL, Model: "qwen3-embedding"}, &http.Client{}, 0, 0)
	vec, err := svc.Embed(context.Background(), "probe")
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	if len(vec) != 4096 {
		t.Fatalf("len(vec) = %d, want provider default 4096", len(vec))
	}
	if gotDimensions != nil {
		t.Fatalf("request body contained dimensions = %v, want absent", gotDimensions)
	}
}

// TestDimensionsParameterSentWhenConfigured: the configured native dimension
// appears in both single and batch request bodies, the response vector has
// exactly that size, and Dimensions() reports the configured value at
// construction time (no live-response caching wait — this is what unblocks
// the pgvector HNSW chain at boot).
func TestDimensionsParameterSentWhenConfigured(t *testing.T) {
	singleBody := make(chan map[string]any, 2)
	batchBody := make(chan map[string]any, 2)
	srv := mockMRLServer(t, "test-key", func(body map[string]any) {
		if _, batch := body["input"].([]any); batch {
			select {
			case batchBody <- body:
			default:
			}
		} else {
			select {
			case singleBody <- body:
			default:
			}
		}
	})
	defer srv.Close()

	svc := newWithClient(Config{Provider: "openai-compatible", APIKey: "test-key", BaseURL: srv.URL, Model: "qwen3-embedding", Dimensions: 1024}, &http.Client{}, 0, 0)
	if svc == nil {
		t.Fatal("openai-compatible preset did not construct a client")
	}

	// Dimensions() reports the configured value BEFORE any embed call.
	if dims := svc.Dimensions(); dims != 1024 {
		t.Fatalf("construction-time dimensions = %d, want configured 1024", dims)
	}

	vec, err := svc.Embed(context.Background(), "probe")
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	if len(vec) != 1024 {
		t.Fatalf("len(vec) = %d, want 1024", len(vec))
	}
	if b := <-singleBody; int(b["dimensions"].(float64)) != 1024 {
		t.Fatalf("single request dimensions = %v, want 1024", b["dimensions"])
	}

	batcher, ok := svc.(interface {
		EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
	})
	if !ok {
		t.Fatal("service does not expose EmbedBatch")
	}
	batch, err := batcher.EmbedBatch(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatalf("embed batch: %v", err)
	}
	if len(batch) != 2 || len(batch[0]) != 1024 {
		t.Fatalf("batch = %d vecs of %d dims, want 2 vecs of 1024", len(batch), len(batch[0]))
	}
	if b := <-batchBody; int(b["dimensions"].(float64)) != 1024 {
		t.Fatalf("batch request dimensions = %v, want 1024", b["dimensions"])
	}
}

// TestDimensionsZeroMeansProviderDefault: Dimensions: 0 behaves exactly like
// the unset case.
func TestDimensionsZeroMeansProviderDefault(t *testing.T) {
	var sawDimensions any
	srv := mockMRLServer(t, "test-key", func(body map[string]any) {
		sawDimensions, _ = body["dimensions"]
	})
	defer srv.Close()

	svc := newWithClient(Config{Provider: "openai-compatible", APIKey: "test-key", BaseURL: srv.URL, Model: "qwen3-embedding", Dimensions: 0}, &http.Client{}, 0, 0)
	if dims := svc.Dimensions(); dims != 0 {
		t.Fatalf("construction-time dimensions = %d, want 0 (unknown until first live embed)", dims)
	}
	if _, err := svc.Embed(context.Background(), "probe"); err != nil {
		t.Fatalf("embed: %v", err)
	}
	if sawDimensions != nil {
		t.Fatalf("dimensions sent = %v, want absent for zero config", sawDimensions)
	}
}
