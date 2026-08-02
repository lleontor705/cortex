package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/lleontor705/cortex/internal/config"
	"github.com/lleontor705/cortex/internal/domain"
)

type closeCountingVector struct {
	mu    sync.Mutex
	calls int
}

func (v *closeCountingVector) ID() string                                         { return "test" }
func (v *closeCountingVector) Upsert(context.Context, []domain.VectorPoint) error { return nil }
func (v *closeCountingVector) Search(context.Context, domain.VectorQuery) ([]domain.VectorCandidate, error) {
	return nil, nil
}
func (v *closeCountingVector) Delete(context.Context, []int64) error { return nil }
func (v *closeCountingVector) Health(context.Context) domain.Health  { return domain.Health{} }
func (v *closeCountingVector) Capabilities(context.Context) (domain.Capabilities, error) {
	return domain.Capabilities{}, nil
}

func (v *closeCountingVector) Close() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.calls++
	return errors.New("vector close")
}

type closeCountingEmbedding struct {
	mu    sync.Mutex
	calls int
}

func (e *closeCountingEmbedding) Embed(context.Context, string) ([]float32, error) { return nil, nil }
func (e *closeCountingEmbedding) Dimensions() int                                  { return 0 }
func (e *closeCountingEmbedding) Model() string                                    { return "test" }

func (e *closeCountingEmbedding) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls++
	return errors.New("embedding close")
}

func TestOpenFailsFastWithoutPostgresDSN(t *testing.T) {
	cfg := config.Config{Server: config.ServerConfig{Storage: config.ServerStorageConfig{Driver: "postgres"}}}
	_, err := Open(context.Background(), cfg)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "dsn") {
		t.Fatalf("Open() error = %v, want fail-fast DSN error", err)
	}
}

func TestServerConfigStringRedactsSecrets(t *testing.T) {
	cfg := config.Config{Server: config.ServerConfig{
		Storage: config.ServerStorageConfig{DSN: "postgres://user:secret@db/cortex"},
		Secrets: config.ServerSecretsConfig{SigningKey: "secret-key"},
	}}
	text := cfg.String()
	if strings.Contains(text, "secret") {
		t.Fatalf("Config.String leaked server secret: %q", text)
	}
}

func TestRuntimeCloseIsIdempotentAndConcurrencySafe(t *testing.T) {
	vector := &closeCountingVector{}
	embedding := &closeCountingEmbedding{}
	rt := &Runtime{Vectors: vector, Embeddings: embedding}

	const callers = 32
	var wg sync.WaitGroup
	results := make(chan error, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- rt.Close()
		}()
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err == nil || !strings.Contains(err.Error(), "vector close") {
			t.Fatalf("Close() error = %v, want stored first cleanup error", err)
		}
	}
	if vector.calls != 1 {
		t.Fatalf("vector Close calls = %d, want 1", vector.calls)
	}
	if embedding.calls != 1 {
		t.Fatalf("embedding Close calls = %d, want 1", embedding.calls)
	}
}

func TestValidateConfigRejectsInvalidServerInputs(t *testing.T) {
	base := config.Config{Server: config.ServerConfig{Storage: config.ServerStorageConfig{Driver: "postgres", DSN: "dsn"}, TenantID: "bad", WorkspaceID: "bad", PrincipalSubject: "p"}}
	cases := []struct {
		name   string
		mutate func(*config.Config)
		want   string
	}{
		{"driver", func(c *config.Config) { c.Server.Storage.Driver = "sqlite" }, "driver"},
		{"dsn", func(c *config.Config) { c.Server.Storage.DSN = "" }, "DSN"},
		{"tenant", func(c *config.Config) { c.Server.TenantID = "" }, "tenant_id"},
		{"tenant uuid", func(c *config.Config) { c.Server.TenantID = "bad" }, "tenant_id"},
		{"workspace uuid", func(c *config.Config) {
			c.Server.TenantID = "00000000-0000-0000-0000-000000000001"
			c.Server.WorkspaceID = "bad"
		}, "workspace_id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := base
			tc.mutate(&c)
			if err := validateConfig(c); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error=%v want %q", err, tc.want)
			}
		})
	}
}

func TestEmbeddingDimensions(t *testing.T) {
	for provider, want := range map[string]int{"openai": 1536, "ollama": 768, "none": 0} {
		if got := embeddingDimensions(provider); got != want {
			t.Errorf("%s=%d want %d", provider, got, want)
		}
	}
}

func TestServeStopsOnContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	rt := &Runtime{httpServer: &http.Server{Addr: "127.0.0.1:0", Handler: http.NewServeMux()}}
	done := make(chan error, 1)
	go func() { done <- rt.Serve(ctx) }()
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	if err := rt.Close(); err != nil {
		t.Fatalf("Close() after Serve = %v", err)
	}
}
