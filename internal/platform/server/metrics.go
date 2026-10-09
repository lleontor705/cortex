// Hand-rolled Prometheus text exposition (v0.0.4) for the server runtime:
// a tiny registry of sync/atomic counters plus render-time gauges. No new
// dependency: the format is a strictly line-oriented protocol that a minimal
// writer can satisfy, and the exposed values are the counters the server
// already maintains (query-embedding cache hit-rate, rerank gate outcomes,
// provider request/error counts, background embedding worker activity, and
// process uptime).
package server

import (
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

// metricsCounter is one named monotonic counter. The zero value is ready.
type metricsCounter struct {
	name  string
	help  string
	value atomic.Int64
}

// Inc advances the counter by one.
func (c *metricsCounter) Inc() { c.value.Add(1) }

// Add advances the counter by n (n must be >= 0 for counters).
func (c *metricsCounter) Add(n int64) { c.value.Add(n) }

// Value reports the current count.
func (c *metricsCounter) Value() int64 { return c.value.Load() }

// metricsRegistry owns the process-wide counter set. Registration is
// idempotent per metric name so concurrent callers always share one counter.
type metricsRegistry struct {
	mu       sync.Mutex
	counters []*metricsCounter
}

func (r *metricsRegistry) counter(name, help string) *metricsCounter {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.counters {
		if c.name == name {
			return c
		}
	}
	c := &metricsCounter{name: name, help: help}
	r.counters = append(r.counters, c)
	return c
}

// render writes the v0.0.4 exposition: for every counter a HELP line, a TYPE
// line, and one sample line. Values are read with atomic loads, so a scrape
// never observes a torn count.
func (r *metricsRegistry) render(w io.Writer) {
	r.mu.Lock()
	counters := append([]*metricsCounter(nil), r.counters...)
	r.mu.Unlock()
	for _, c := range counters {
		fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s counter\n%s %d\n", c.name, c.help, c.name, c.name, c.Value())
	}
}

// serverMetrics is the exposed runtime surface for the server process.
type serverMetrics struct {
	registry *metricsRegistry

	queryEmbeddingCacheHits   *metricsCounter
	queryEmbeddingCacheMisses *metricsCounter

	rerankApplied   *metricsCounter
	rerankGateFails *metricsCounter

	embeddingProviderRequests *metricsCounter
	embeddingProviderErrors   *metricsCounter

	workerEmbeddingBatches *metricsCounter
	workerEmbeddings       *metricsCounter
	workerErrors           *metricsCounter
}

// prometheusMetrics is the process-wide registry. Counters are incremented at
// their natural seams (cache embed classification, rerank gate, background
// worker drain) and rendered by GET /metrics.
var prometheusMetrics = newServerMetrics()

func newServerMetrics() *serverMetrics {
	r := &metricsRegistry{}
	return &serverMetrics{
		registry: r,

		queryEmbeddingCacheHits:   r.counter("cortex_query_embedding_cache_hits_total", "Hybrid-search query embeddings served from the process-wide cache."),
		queryEmbeddingCacheMisses: r.counter("cortex_query_embedding_cache_misses_total", "Hybrid-search query embeddings that required a provider round-trip."),

		rerankApplied:   r.counter("cortex_rerank_applied_total", "Hybrid searches whose fused candidate list was reordered by the composed reranker."),
		rerankGateFails: r.counter("cortex_rerank_gate_fail_total", "Hybrid searches where the rerank gate failed open and the unreranked fusion order was returned."),

		embeddingProviderRequests: r.counter("cortex_embedding_provider_requests_total", "Embedding provider round-trips issued on query-embedding cache misses."),
		embeddingProviderErrors:   r.counter("cortex_embedding_provider_errors_total", "Embedding provider round-trips that failed on the query-embedding path."),

		workerEmbeddingBatches: r.counter("cortex_worker_embedding_batches_total", "Background embedding worker drain batches that reached the embedding stage."),
		workerEmbeddings:       r.counter("cortex_worker_embeddings_total", "Vectors produced by the background embedding worker."),
		workerErrors:           r.counter("cortex_worker_errors_total", "Background embedding worker errors (list, embed, or upsert failures)."),
	}
}

// render writes the full exposition: registered counters plus the runtime
// gauge (process uptime).
func (m *serverMetrics) render(w io.Writer) {
	m.registry.render(w)
	fmt.Fprintf(w, "# HELP cortex_process_uptime_seconds Server process uptime in seconds.\n")
	fmt.Fprintf(w, "# TYPE cortex_process_uptime_seconds gauge\n")
	fmt.Fprintf(w, "cortex_process_uptime_seconds %.3f\n", time.Since(serverStartTime).Seconds())
}
