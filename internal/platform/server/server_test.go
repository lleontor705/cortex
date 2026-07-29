package server

import (
	"context"
	"errors"
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
