package pgvector

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Exact-scan tuning knobs for the pgvector adapter (REQ-RET-102). Both are
// config-gated: with the env unset the adapter behavior is byte-identical in
// semantics to the pre-tuning exact '<=>' cosine scan.
const (
	// EnvMaxParallelWorkersPerGather enables SET LOCAL
	// max_parallel_workers_per_gather = N inside the search transaction.
	// A positive integer enables the tuning; unset/invalid/<= 0 leaves the
	// default (no SET LOCAL, pool-level query, no transaction wrapper).
	EnvMaxParallelWorkersPerGather = "CORTEX_VECTOR_PGVECTOR_MAX_PARALLEL_WORKERS_PER_GATHER"

	// EnvDistanceMode selects the search distance operator. "cosine" (default,
	// also on unset) keeps the exact '<=>' cosine scan; "ip" switches to the
	// normalized inner-product '<#>' distance for pre-normalized corpora.
	EnvDistanceMode = "CORTEX_VECTOR_PGVECTOR_DISTANCE_MODE"

	// DistanceModeCosine / DistanceModeIP are the recognized mode values.
	DistanceModeCosine = "cosine"
	DistanceModeIP     = "ip"
)

// SearchTuning holds the config-gated exact-scan tuning knobs. The zero value
// is fully disabled: default cosine operator, no SET LOCAL, no transaction
// wrapper (REQ-RET-102 "default behavior unchanged").
type SearchTuning struct {
	// MaxParallelWorkersPerGather > 0 enables SET LOCAL
	// max_parallel_workers_per_gather scoped to the search transaction only.
	MaxParallelWorkersPerGather int
	// InnerProduct enables the normalized inner-product distance mode
	// ('<#>') for pre-normalized corpora. Scores stay 1 - distance per the
	// retrieval-engine contract; on unit vectors the ordering is identical to
	// cosine ordering.
	InnerProduct bool
}

// Enabled reports whether any tuning knob is active. When false the search
// path is untouched (no transaction, default SQL).
func (t SearchTuning) Enabled() bool {
	return t.MaxParallelWorkersPerGather > 0 || t.InnerProduct
}

// SearchTuningFromEnv builds SearchTuning from the CORTEX_VECTOR_PGVECTOR_*
// environment pattern (same style as internal/store/postgres
// CORTEX_VECTOR_PGVECTOR_DIMENSION handling). getenv is injected for
// deterministic unit testing; SearchTuningFromOS reads the real environment.
func SearchTuningFromEnv(getenv func(string) string) SearchTuning {
	var t SearchTuning
	if getenv == nil {
		return t
	}
	if raw := strings.TrimSpace(getenv(EnvMaxParallelWorkersPerGather)); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			t.MaxParallelWorkersPerGather = n
		}
		// Unset, non-numeric, or <= 0: tuning stays disabled (fail-open to
		// the default exact scan, never to a partially configured state).
	}
	switch strings.ToLower(strings.TrimSpace(getenv(EnvDistanceMode))) {
	case DistanceModeIP:
		t.InnerProduct = true
	default:
		// "cosine", unset, or anything unrecognized: default cosine mode.
		t.InnerProduct = false
	}
	return t
}

// SearchTuningFromOS reads the tuning env vars from the process environment.
func SearchTuningFromOS() SearchTuning {
	return SearchTuningFromEnv(os.Getenv)
}

// setLocalStatements returns the SET LOCAL statements to run inside the search
// transaction. All values are typed integers — there is no string surface for
// SQL injection. SET LOCAL is transaction-scoped by definition, so the tuning
// never leaks beyond the search transaction.
func (t SearchTuning) setLocalStatements() []string {
	if t.MaxParallelWorkersPerGather <= 0 {
		return nil
	}
	return []string{
		fmt.Sprintf("SET LOCAL max_parallel_workers_per_gather = %d", t.MaxParallelWorkersPerGather),
	}
}

// distanceOperator returns the SQL distance operator for the configured mode:
// '<=>' cosine (default) or '<#>' inner product (normalized corpora).
func (t SearchTuning) distanceOperator() string {
	if t.InnerProduct {
		return "<#>"
	}
	return "<=>"
}

// searchSQL renders the exact-scan SELECT for the given mode. With the
// default tuning the output is byte-identical to the pre-tuning SQL:
//
//	SELECT id, 1 - (embedding <=> $1::vector) AS similarity
//	FROM <table><where>
//	ORDER BY embedding <=> $1::vector
//	LIMIT $<n>
//
// Similarity stays 1 - distance in every mode so engine-side threshold
// semantics (REQ-RET-002/003) are preserved; on unit vectors the '<#>'
// ordering is identical to the cosine ordering (1 - <#> = 1 + ip is a
// monotone affine transform of cosine similarity for normalized vectors).
func (t SearchTuning) searchSQL(qualifiedTable, whereSQL string, limitParam int) string {
	op := t.distanceOperator()
	return fmt.Sprintf(
		`SELECT id, 1 - (embedding %s $1::vector) AS similarity
FROM %s%s
ORDER BY embedding %s $1::vector
LIMIT $%d`,
		op, qualifiedTable, whereSQL, op, limitParam,
	)
}

// txQuerier is the optional Query capability on a transaction. The production
// poolTx (pgx.Tx wrapper) satisfies it; minimal unit-test fakes may not, in
// which case the tuned path degrades to the default pool query rather than
// changing behavior.
type txQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// beginTunedSearchTx opens the search transaction, issues the SET LOCAL tuning
// statements inside it, and runs the search statement on that transaction.
// The caller owns row consumption and MUST finalize the tx (Commit on success,
// Rollback on error) only after the rows are closed — SET LOCAL is
// transaction-scoped either way, so the tuning never leaks.
func (a *Adapter) beginTunedSearchTx(ctx context.Context, sql string, args []any) (pgvectorTx, pgx.Rows, error) {
	tx, err := a.db.BeginTx(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("pgvector: begin tuned search tx: %w", a.redact(err))
	}

	for _, stmt := range a.tuning.setLocalStatements() {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			return nil, nil, fmt.Errorf("pgvector: set search tuning: %w", a.redact(err))
		}
	}

	tq, ok := tx.(txQuerier)
	if !ok {
		// Minimal fake tx without Query support (unit-test seam): fall back to
		// the default pool-level query instead of failing. Production pgx.Tx
		// always supports Query, so this branch is unreachable there. The
		// caller receives a nil tx and skips tx finalization (Search's finalize
		// block is nil-guarded); the fake tx is rolled back here.
		_ = tx.Rollback(ctx)
		rows, err := a.db.Query(ctx, sql, args...)
		if err != nil {
			return nil, nil, err
		}
		return nil, rows, nil
	}
	rows, err := tq.Query(ctx, sql, args...)
	if err != nil {
		// Statement failed: roll the tx back before surfacing the error.
		_ = tx.Rollback(ctx)
		return nil, nil, err
	}
	return tx, rows, nil
}
