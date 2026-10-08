package server

import (
	"github.com/lleontor705/cortex/v2/internal/vector/pgvector"
)

// pgvectorTuningStatus surfaces the active pgvector exact-scan tuning state
// (REQ-RET-102, ret-105/ret-106) for the GET /api/settings payload so
// operators can verify the configured CORTEX_VECTOR_PGVECTOR_* knobs at
// runtime. The values mirror the adapter's composition-time environment
// read: the process environment is static, so a request-time re-read equals
// the construction-time snapshot. No credentials or DSN material is exposed.
func pgvectorTuningStatus() map[string]any {
	tuning := pgvector.SearchTuningFromOS()
	mode := pgvector.DistanceModeCosine
	if tuning.InnerProduct {
		mode = pgvector.DistanceModeIP
	}
	return map[string]any{
		"enabled":                         tuning.Enabled(),
		"distance_mode":                   mode,
		"max_parallel_workers_per_gather": tuning.MaxParallelWorkersPerGather,
	}
}
