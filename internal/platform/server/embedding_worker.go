package server

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/embedding"
	"github.com/lleontor705/cortex/v2/internal/store/postgres"
)

// UnembeddedSource supplies tenant-bound batches of observations that have no
// row in the vector replica yet. *postgresstore.SystemService implements it.
// The indirection keeps the worker independent of the storage capability while
// guaranteeing every read runs through the authorized (principal-bound,
// tenant-scoped) path — a raw pool handle is RLS-blind and silently returns
// zero rows (issue #115).
type UnembeddedSource interface {
	ListUnembedded(ctx context.Context, limit int) ([]postgres.UnembeddedObservation, error)
}

type backgroundEmbeddingWorker struct {
	source     UnembeddedSource
	embeddings embedding.Service
	vectors    domain.VectorIndex
	interval   time.Duration
}

func startBackgroundEmbeddingWorker(ctx context.Context, source UnembeddedSource, emb embedding.Service, vec domain.VectorIndex) {
	if source == nil || emb == nil || vec == nil {
		return
	}
	worker := &backgroundEmbeddingWorker{
		source:     source,
		embeddings: emb,
		vectors:    vec,
		interval:   5 * time.Second,
	}
	go worker.run(ctx)
}

func (w *backgroundEmbeddingWorker) run(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.drainBatch(ctx)
		}
	}
}

func (w *backgroundEmbeddingWorker) drainBatch(ctx context.Context) {
	if !domain.IsVectorIndexHealthy(ctx, w.vectors) {
		return
	}

	batch, err := w.source.ListUnembedded(ctx, 32)
	if err != nil {
		// Never swallow fetch failures: an unlogged error here was the reason
		// production accumulated zero vectors for the worker's whole lifetime
		// (issue #115).
		log.Printf("server: background embedding worker list unembedded error: %v", err)
		return
	}
	if len(batch) == 0 {
		return
	}

	validObs := make([]postgres.UnembeddedObservation, 0, len(batch))
	texts := make([]string, 0, len(batch))
	for _, obs := range batch {
		text := strings.TrimSpace(obs.Title + "\n" + obs.Content)
		if text == "" {
			continue
		}
		validObs = append(validObs, obs)
		texts = append(texts, text)
	}

	if len(validObs) == 0 {
		return
	}

	var vectors [][]float32
	if batcher, ok := w.embeddings.(embedding.BatchEmbedder); ok {
		var err error
		vectors, err = batcher.EmbedBatch(ctx, texts)
		if err != nil {
			log.Printf("server: background embedding worker batch embed error: %v", err)
			vectors = nil
		}
	}

	points := make([]domain.VectorPoint, 0, len(validObs))
	for i, obs := range validObs {
		var vec []float32
		if len(vectors) == len(validObs) {
			vec = vectors[i]
		} else {
			var err error
			vec, err = w.embeddings.Embed(ctx, texts[i])
			if err != nil || len(vec) == 0 {
				continue
			}
		}
		if len(vec) == 0 {
			continue
		}
		// Defense in depth for the scoped trust model: every server vector
		// point must carry a trusted project_id, and the scoped wrapper
		// rejects the whole batch otherwise (issue #115 follow-up). The
		// repository query already inner-joins projects; this guard keeps a
		// future query drift from silently stalling the worker.
		if strings.TrimSpace(obs.ProjectPublicID) == "" {
			continue
		}
		points = append(points, domain.VectorPoint{
			ID:     obs.ID,
			Vector: vec,
			ModelInfo: domain.ModelInfo{
				Name:      w.embeddings.Model(),
				Dimension: w.embeddings.Dimensions(),
			},
			Metadata: map[string]any{
				"project":      obs.ProjectKey,
				"project_id":   obs.ProjectPublicID,
				"scope":        obs.Scope,
				"tenant_id":    obs.TenantID,
				"workspace_id": obs.WorkspaceID,
				"source":       obs.Source,
				"type":         obs.Type,
			},
		})
	}

	if len(points) > 0 {
		if err := w.vectors.Upsert(ctx, points); err != nil {
			log.Printf("server: background embedding worker upsert error: %v", err)
		}
	}
}
