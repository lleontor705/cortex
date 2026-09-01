package server

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/embedding"
)

type backgroundEmbeddingWorker struct {
	pool       *pgxpool.Pool
	embeddings embedding.Service
	vectors    domain.VectorIndex
	interval   time.Duration
}

func startBackgroundEmbeddingWorker(ctx context.Context, pool *pgxpool.Pool, emb embedding.Service, vec domain.VectorIndex) {
	if pool == nil || emb == nil || vec == nil {
		return
	}
	worker := &backgroundEmbeddingWorker{
		pool:       pool,
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

	query := `
		SELECT o.id, o.title, o.content, COALESCE(o.project_key, ''), COALESCE(p.public_id::text, ''),
		       COALESCE(o.scope, ''), COALESCE(o.tenant_id::text, ''), COALESCE(o.workspace_id::text, ''),
		       COALESCE(o.source, ''), COALESCE(o.type, '')
		  FROM observations o
		  LEFT JOIN projects p ON p.tenant_id = o.tenant_id AND p.workspace_id = o.workspace_id AND p.name = o.project_key
		 WHERE o.deleted_at IS NULL
		   AND NOT EXISTS (
		       SELECT 1 FROM cortex_vector.embeddings e WHERE e.id = o.id
		   )
		 LIMIT 10`

	rows, err := w.pool.Query(ctx, query)
	if err != nil {
		return
	}
	defer rows.Close()

	type unindexedObservation struct {
		id                                                   int64
		title, content, projectKey, projectPublicID          string
		scope, tenantID, workspaceID, source, obsType        string
	}

	batch := make([]unindexedObservation, 0, 10)
	for rows.Next() {
		var item unindexedObservation
		if err := rows.Scan(&item.id, &item.title, &item.content, &item.projectKey, &item.projectPublicID,
			&item.scope, &item.tenantID, &item.workspaceID, &item.source, &item.obsType); err == nil {
			batch = append(batch, item)
		}
	}
	rows.Close()

	if len(batch) == 0 {
		return
	}

	points := make([]domain.VectorPoint, 0, len(batch))
	for _, obs := range batch {
		text := strings.TrimSpace(obs.title + "\n" + obs.content)
		if text == "" {
			continue
		}
		vec, err := w.embeddings.Embed(ctx, text)
		if err != nil || len(vec) == 0 {
			continue
		}
		points = append(points, domain.VectorPoint{
			ID:     obs.id,
			Vector: vec,
			ModelInfo: domain.ModelInfo{
				Name:      w.embeddings.Model(),
				Dimension: w.embeddings.Dimensions(),
			},
			Metadata: map[string]any{
				"project":      obs.projectKey,
				"project_id":   obs.projectPublicID,
				"scope":        obs.scope,
				"tenant_id":    obs.tenantID,
				"workspace_id": obs.workspaceID,
				"source":       obs.source,
				"type":         obs.obsType,
			},
		})
	}

	if len(points) > 0 {
		if err := w.vectors.Upsert(ctx, points); err != nil {
			log.Printf("server: background embedding worker upsert error: %v", err)
		}
	}
}
