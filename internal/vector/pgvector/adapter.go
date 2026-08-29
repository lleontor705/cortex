// Package pgvector implements the second external VectorIndex adapter
// (ADR-05, REQ-VEC-001/002). It uses the pgx pure-Go driver (zero-CGO) to
// talk to a PostgreSQL database with the pgvector extension.
//
// SERVER-TRACK BOUNDARY: this package is SERVER-track (arch_test.go bans it
// from local composition). The local composition path (internal/app) wires the
// sqlite_blob zero-CGO default and MUST NOT import this package. Provider
// selection (pgvector.New) happens ONLY in the server/external composition path.
// This preserves REQ-FOUND-001: CGO_ENABLED=0 local build with zero external
// vector dependencies.
//
// ISOLATED REPLICA: SQLite remains authoritative for observation data. The
// pgvector table is a vector REPLICA keyed by observation ID (BIGINT PK from
// SQLite). This is NOT a full Postgres source-of-truth (W11) — it is a
// read-optional dense candidate source for vector similarity search. No
// observation metadata (content, timestamps, lifecycle) is replicated; only
// the vector embedding and the filter columns needed for PreFilter/PostFilter
// search.
//
// Schema/table isolation: the adapter creates its own schema (default
// cortex_vector) and table (default embeddings). These identifiers are
// validated against a safe regex before interpolation into SQL (schema/table
// names cannot be parameterized in PostgreSQL DDL). All data values use
// parameterized queries ($N) — no string interpolation of user data.
//
// Dimension/model mismatch are rejected FAIL-CLOSED before any DB call
// (REQ-VEC-001 dim-mismatch corruption pin; model-namespace mismatch).
//
// No plaintext secrets: the DSN password is extracted at construction time
// and scrubbed from every error message via redactWith (REQ-CP-002). The DSN
// is NEVER logged or surfaced in error strings.
//
// Statement/query timeouts: every operation runs under a context with a
// configurable deadline, AND each transaction sets statement_timeout via
// set_config (server-side kill on slow queries).
package pgvector

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/pgvector/pgvector-go"
)

// adapterID is the stable identifier declared via ID() and
// Capabilities().IndexType.
const adapterID = "pgvector"

// defaultTimeout is the per-operation timeout applied when the caller's
// context has no deadline.
const defaultTimeout = 30 * time.Second

// defaultStatementTimeoutMs is the PostgreSQL statement_timeout applied within
// each transaction (server-side query kill).
const defaultStatementTimeoutMs = 5000

// Index tuning defaults — pgvector-recommended values. These mirror
// config.PGVectorConfig defaults so the adapter is self-consistent when
// constructed directly (New/NewWithDB) without going through config validation.
const (
	defaultHNSWM              = 16
	defaultHNSWEfConstruction = 64
	defaultIVFFlatLists       = 100
)

// identifierRe matches safe PostgreSQL identifiers (schema/table/index names).
// Only lowercase letters, digits, and underscores, starting with a letter or
// underscore. This prevents SQL injection via identifier interpolation.
var identifierRe = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// pgvectorTx is the narrow transaction interface the adapter uses. *pgx.Tx
// satisfies it via the poolTx wrapper. Defining this seam lets unit tests
// substitute a fake WITHOUT a running Postgres.
type pgvectorTx interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

// pgvectorDB is the narrow DB interface the adapter depends on. *pgxpool.Pool
// satisfies it via the poolDB wrapper. The adapter never reaches for pool
// methods outside this set.
type pgvectorDB interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	BeginTx(ctx context.Context) (pgvectorTx, error)
	Ping(ctx context.Context) error
	Close()
}

// AdapterConfig holds the parameters to construct a pgvector Adapter. It
// mirrors config.PGVectorConfig but is declared here so the adapter package is
// self-contained (the server composition path maps config.PGVectorConfig →
// AdapterConfig). Index tuning uses typed validated integers — there is NO raw
// SQL string surface for index options (prevents injection via DDL).
type AdapterConfig struct {
	DSN                string        // PostgreSQL connection string
	Schema             string        // schema name (default cortex_vector)
	Table              string        // table name (default embeddings)
	Dimension          int           // expected vector dimension
	ModelName          string        // model name for namespace enforcement (empty = skip)
	IndexType          string        // hnsw or ivfflat (default hnsw)
	HNSWM              int           // HNSW max connections per node (default 16, range 2-100)
	HNSWEfConstruction int           // HNSW dynamic candidate list for build (default 64, range 1-1000)
	IVFFlatLists       int           // IVFFlat number of inverted lists (default 100, range 1-50000)
	MaxBatchSize       int           // upsert batch ceiling (default 256)
	Timeout            time.Duration // per-operation timeout (default 30s)
	MaxConns           int32         // pool max connections (default 10)
	StatementTimeoutMs int           // PostgreSQL statement_timeout in ms (default 5000)
}

// Adapter implements domain.VectorIndex over a PostgreSQL database with the
// pgvector extension via the pgx pure-Go driver.
type Adapter struct {
	db                 pgvectorDB
	schema             string
	table              string
	qualifiedTable     string // schema.table
	dimension          int
	modelName          string
	indexType          string
	hnswM              int
	hnswEfConstruction int
	ivfflatLists       int
	maxBatchSize       int
	timeout            time.Duration
	statementTimeout   int
	password           string // extracted from DSN for redaction (never logged)
	ownDB              bool   // Close should close the underlying pool (factory-built)
	created            bool   // schema/table/index have been verified this session
	caps               domain.Capabilities
}

// New constructs a pgvector Adapter with a real pgxpool connection pool. The
// caller MUST be in the server/external composition path (this package is
// server-track). The returned Adapter owns its pool; Close closes it.
//
// Bootstrap: New connects with a raw pgx.Conn first to create the extension,
// schema, table, and index, then creates the pool. This ensures the vector
// type exists before any pooled connection uses it.
func New(ctx context.Context, cfg AdapterConfig) (*Adapter, error) {
	if err := validateAdapterConfig(&cfg); err != nil {
		return nil, err
	}

	password := extractPassword(cfg.DSN)

	// Bootstrap: create extension + schema + table + index via a raw connection.
	conn, err := pgx.Connect(ctx, cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("pgvector: bootstrap connect: %w", redactDSN(err, password))
	}
	defer func() { _ = conn.Close(ctx) }()

	if err := bootstrapSchema(ctx, conn, cfg); err != nil {
		return nil, fmt.Errorf("pgvector: bootstrap schema: %w", redactDSN(err, password))
	}

	// Create the pool.
	poolCfg, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("pgvector: parse DSN: %w", redactDSN(err, password))
	}
	if cfg.MaxConns > 0 {
		poolCfg.MaxConns = cfg.MaxConns
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("pgvector: create pool: %w", redactDSN(err, password))
	}

	return &Adapter{
		db:                 &poolDB{pool: pool},
		schema:             cfg.Schema,
		table:              cfg.Table,
		qualifiedTable:     cfg.Schema + "." + cfg.Table,
		dimension:          cfg.Dimension,
		modelName:          cfg.ModelName,
		indexType:          cfg.IndexType,
		hnswM:              cfg.HNSWM,
		hnswEfConstruction: cfg.HNSWEfConstruction,
		ivfflatLists:       cfg.IVFFlatLists,
		maxBatchSize:       cfg.MaxBatchSize,
		timeout:            cfg.Timeout,
		statementTimeout:   cfg.StatementTimeoutMs,
		password:           password,
		ownDB:              true,
		created:            true, // schema already bootstrapped above
		caps:               defaultCapabilities(cfg.Dimension, cfg.MaxBatchSize),
	}, nil
}

// NewWithDB constructs an Adapter over a pre-built (or fake) DB. Used by tests
// and by compositions that manage the pool lifecycle externally. The caller
// controls whether Close closes the pool (see ownDB; default false).
func NewWithDB(db pgvectorDB, cfg AdapterConfig) (*Adapter, error) {
	if err := validateAdapterConfig(&cfg); err != nil {
		return nil, err
	}
	return &Adapter{
		db:                 db,
		schema:             cfg.Schema,
		table:              cfg.Table,
		qualifiedTable:     cfg.Schema + "." + cfg.Table,
		dimension:          cfg.Dimension,
		modelName:          cfg.ModelName,
		indexType:          cfg.IndexType,
		hnswM:              cfg.HNSWM,
		hnswEfConstruction: cfg.HNSWEfConstruction,
		ivfflatLists:       cfg.IVFFlatLists,
		maxBatchSize:       cfg.MaxBatchSize,
		timeout:            cfg.Timeout,
		statementTimeout:   cfg.StatementTimeoutMs,
		password:           extractPassword(cfg.DSN),
		ownDB:              false,
		caps:               defaultCapabilities(cfg.Dimension, cfg.MaxBatchSize),
	}, nil
}

// validateAdapterConfig validates the adapter config and applies defaults.
func validateAdapterConfig(cfg *AdapterConfig) error {
	if cfg.DSN == "" {
		return fmt.Errorf("pgvector: DSN is required")
	}
	if cfg.Schema == "" {
		cfg.Schema = "cortex_vector"
	}
	if !identifierRe.MatchString(cfg.Schema) {
		return fmt.Errorf("pgvector: schema name %q is not a safe identifier", cfg.Schema)
	}
	if cfg.Table == "" {
		cfg.Table = "embeddings"
	}
	if !identifierRe.MatchString(cfg.Table) {
		return fmt.Errorf("pgvector: table name %q is not a safe identifier", cfg.Table)
	}
	if cfg.Dimension <= 0 {
		return fmt.Errorf("pgvector: dimension must be > 0")
	}
	if cfg.IndexType == "" {
		cfg.IndexType = "hnsw"
	}
	if cfg.IndexType != "hnsw" && cfg.IndexType != "ivfflat" {
		return fmt.Errorf("pgvector: index_type must be hnsw or ivfflat, got %q", cfg.IndexType)
	}
	// Index tuning: zero → default; explicit out-of-range rejected. These are
	// typed integers only — never raw SQL — so DDL emission is injection-safe.
	if cfg.HNSWM == 0 {
		cfg.HNSWM = defaultHNSWM
	}
	if cfg.HNSWM < 2 || cfg.HNSWM > 100 {
		return fmt.Errorf("pgvector: hnsw_m must be 2-100, got %d", cfg.HNSWM)
	}
	if cfg.HNSWEfConstruction == 0 {
		cfg.HNSWEfConstruction = defaultHNSWEfConstruction
	}
	if cfg.HNSWEfConstruction < 1 || cfg.HNSWEfConstruction > 1000 {
		return fmt.Errorf("pgvector: hnsw_ef_construction must be 1-1000, got %d", cfg.HNSWEfConstruction)
	}
	if cfg.IVFFlatLists == 0 {
		cfg.IVFFlatLists = defaultIVFFlatLists
	}
	if cfg.IVFFlatLists < 1 || cfg.IVFFlatLists > 50000 {
		return fmt.Errorf("pgvector: ivfflat_lists must be 1-50000, got %d", cfg.IVFFlatLists)
	}
	if cfg.MaxBatchSize <= 0 {
		cfg.MaxBatchSize = 256
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultTimeout
	}
	if cfg.StatementTimeoutMs <= 0 {
		cfg.StatementTimeoutMs = defaultStatementTimeoutMs
	}
	return nil
}

// defaultCapabilities builds the Capabilities struct declared by this adapter.
// pgvector supports HNSW and IVFFlat indexes, cosine distance (and L2, inner
// product), and filtering via WHERE clauses (PostFilter). The adapter is a
// dense candidate source (the retrieval engine owns RRF fusion), so Hybrid is
// declared as "engine".
func defaultCapabilities(dimension, maxBatch int) domain.Capabilities {
	return domain.Capabilities{
		IndexType:       adapterID,
		DistanceMetrics: []string{"cosine"},
		MaxDimensions:   dimension,
		Filters:         "PostFilter",
		Hybrid:          "engine", // engine owns fusion; adapter is dense-only
		Namespaces:      "supported",
		Consistency:     "strong",
		BatchUpsert:     true,
		MaxBatchSize:    maxBatch,
	}
}

// ID returns the stable adapter identifier.
func (a *Adapter) ID() string { return adapterID }

// Capabilities declares the adapter's supported features for capability-driven
// strategy selection (ADR-05).
func (a *Adapter) Capabilities(_ context.Context) (domain.Capabilities, error) {
	return a.caps, nil
}

// Upsert stores a batch of vectors. Dimension and model-namespace mismatch are
// rejected FAIL-CLOSED before any DB call (REQ-VEC-001 dim-mismatch pin; model
// mismatch). The batch is chunked at maxBatchSize within a transaction.
func (a *Adapter) Upsert(ctx context.Context, points []domain.VectorPoint) error {
	if len(points) == 0 {
		return nil
	}
	// Validate EVERY point fail-closed before sending anything.
	for _, p := range points {
		if err := a.validatePoint(p); err != nil {
			return err
		}
	}
	ctx, cancel := a.withTimeout(ctx)
	defer cancel()

	tx, err := a.db.BeginTx(ctx)
	if err != nil {
		return fmt.Errorf("pgvector: begin tx: %w", a.redact(err))
	}
	defer func() { _ = tx.Rollback(ctx) }() // safe no-op after Commit

	// Set server-side statement timeout for this transaction.
	if _, err := tx.Exec(ctx, "SELECT set_config('statement_timeout', $1, true)",
		fmt.Sprintf("%dms", a.statementTimeout)); err != nil {
		return fmt.Errorf("pgvector: set statement_timeout: %w", a.redact(err))
	}

	upsertSQL := a.upsertSQL()

	for start := 0; start < len(points); start += a.maxBatchSize {
		end := start + a.maxBatchSize
		if end > len(points) {
			end = len(points)
		}
		batch := points[start:end]
		for _, p := range batch {
			if _, err := tx.Exec(ctx, upsertSQL, a.pointArgs(p)...); err != nil {
				return fmt.Errorf("pgvector: upsert point %d: %w", p.ID, a.redact(err))
			}
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("pgvector: commit upsert: %w", a.redact(err))
	}
	return nil
}

// validatePoint rejects dimension and model-namespace mismatch fail-closed.
func (a *Adapter) validatePoint(p domain.VectorPoint) error {
	dim := p.ModelInfo.Dimension
	if dim <= 0 {
		dim = a.dimension
	}
	if len(p.Vector) != dim {
		ns := p.ModelInfo.Name
		if p.ModelInfo.Version != "" {
			ns = p.ModelInfo.Name + ":" + p.ModelInfo.Version
		}
		return domain.NewDimensionMismatchError(dim, len(p.Vector), ns)
	}
	if a.modelName != "" && p.ModelInfo.Name != "" && p.ModelInfo.Name != a.modelName {
		return fmt.Errorf("%w: expected %q, got %q", domain.ErrNamespaceMismatch, a.modelName, p.ModelInfo.Name)
	}
	return nil
}

// upsertSQL builds the parameterized INSERT ... ON CONFLICT statement. The
// table name is interpolated (validated safe identifier); all values use $N
// placeholders. On conflict the updated_at timestamp is refreshed to NOW() so
// re-upserts keep the row's modification time current.
func (a *Adapter) upsertSQL() string {
	return fmt.Sprintf(
		`INSERT INTO %s (id, embedding, model, model_version, dimension, project, project_id, scope, tenant_id, workspace_id, source, type)
VALUES ($1, $2::vector, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
ON CONFLICT (id) DO UPDATE SET
    embedding = EXCLUDED.embedding,
    model = EXCLUDED.model,
    model_version = EXCLUDED.model_version,
    dimension = EXCLUDED.dimension,
    project = EXCLUDED.project,
	project_id = EXCLUDED.project_id,
    scope = EXCLUDED.scope,
    tenant_id = EXCLUDED.tenant_id,
	workspace_id = EXCLUDED.workspace_id,
    source = EXCLUDED.source,
    type = EXCLUDED.type,
    updated_at = NOW()`,
		a.qualifiedTable,
	)
}

// pointArgs extracts the parameterized values for a VectorPoint upsert.
func (a *Adapter) pointArgs(p domain.VectorPoint) []any {
	return []any{
		p.ID,
		pgvector.NewVector(p.Vector),
		p.ModelInfo.Name,
		p.ModelInfo.Version,
		len(p.Vector),
		metaString(p.Metadata, "project"),
		metaString(p.Metadata, "project_id"),
		metaString(p.Metadata, "scope"),
		metaString(p.Metadata, "tenant_id"),
		metaString(p.Metadata, "workspace_id"),
		metaString(p.Metadata, "source"),
		metaString(p.Metadata, "type"),
	}
}

// Search translates a domain.VectorQuery into a parameterized SELECT against
// the pgvector table. Filters are mapped to WHERE conditions (PostFilter).
// The score threshold is applied client-side after receiving results.
func (a *Adapter) Search(ctx context.Context, q domain.VectorQuery) ([]domain.VectorCandidate, error) {
	if len(q.Vector) == 0 {
		return nil, fmt.Errorf("pgvector: search vector is empty")
	}
	if len(q.Vector) != a.dimension {
		return nil, domain.NewDimensionMismatchError(a.dimension, len(q.Vector), a.modelName)
	}
	if err := a.ensureSchema(ctx); err != nil {
		return nil, err
	}

	ctx, cancel := a.withTimeout(ctx)
	defer cancel()

	limit := q.Limit
	if limit <= 0 {
		limit = 10
	}

	// Build the parameterized query with dynamic WHERE clauses for recognized
	// filter keys. Column names are from a fixed whitelist (safe). Values use
	// $N placeholders.
	var args []any
	args = append(args, pgvector.NewVector(q.Vector)) // $1 = query vector

	var whereClauses []string
	paramIdx := 2 // $1 is the vector

	for _, key := range filterKeyOrder {
		val, ok := q.Filters[key]
		if !ok {
			continue
		}
		s, ok := val.(string)
		if !ok || s == "" {
			continue
		}
		whereClauses = append(whereClauses, fmt.Sprintf("%s = $%d", key, paramIdx))
		args = append(args, s)
		paramIdx++
	}

	whereSQL := ""
	if len(whereClauses) > 0 {
		whereSQL = " WHERE " + strings.Join(whereClauses, " AND ")
	}

	args = append(args, limit) // last param is LIMIT
	limitParam := paramIdx

	sql := fmt.Sprintf(
		`SELECT id, 1 - (embedding <=> $1::vector) AS similarity
FROM %s%s
ORDER BY embedding <=> $1::vector
LIMIT $%d`,
		a.qualifiedTable, whereSQL, limitParam,
	)

	rows, err := a.db.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("pgvector: search: %w", a.redact(err))
	}
	defer rows.Close()

	candidates := make([]domain.VectorCandidate, 0, limit)
	for rows.Next() {
		var id int64
		var similarity float64
		if err := rows.Scan(&id, &similarity); err != nil {
			return nil, fmt.Errorf("pgvector: scan result: %w", a.redact(err))
		}
		if q.Threshold > 0 && similarity < q.Threshold {
			continue // client-side threshold enforcement
		}
		candidates = append(candidates, domain.VectorCandidate{
			ID:         id,
			Score:      similarity,
			Provenance: adapterID,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pgvector: rows iteration: %w", a.redact(err))
	}
	return candidates, nil
}

// Delete removes vectors by observation ID using a parameterized ANY($1)
// clause. Missing IDs are tolerated (idempotent delete).
func (a *Adapter) Delete(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	if err := a.ensureSchema(ctx); err != nil {
		return err
	}
	ctx, cancel := a.withTimeout(ctx)
	defer cancel()

	sql := fmt.Sprintf(`DELETE FROM %s WHERE id = ANY($1)`, a.qualifiedTable)
	if _, err := a.db.Exec(ctx, sql, ids); err != nil {
		return fmt.Errorf("pgvector: delete: %w", a.redact(err))
	}
	return nil
}

// Health reports the adapter's current health by pinging the PostgreSQL pool.
// The DSN/password is never included in the health message.
func (a *Adapter) Health(ctx context.Context) domain.Health {
	pingCtx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()
	if err := a.db.Ping(pingCtx); err != nil {
		return domain.Health{
			Status:  domain.StatusUnhealthy,
			Message: "pgvector: health check failed (connection unreachable)",
		}
	}
	return domain.Health{
		Status:  domain.StatusHealthy,
		Message: "pgvector: ready",
	}
}

// Close releases resources. If the adapter owns its pool (factory-built via
// New), the underlying pool is closed.
func (a *Adapter) Close() error {
	if a.ownDB {
		a.db.Close()
	}
	return nil
}

// ensureSchema lazily verifies that the schema/table/index exist. For a
// factory-built adapter (New), this is already done during bootstrap and the
// method is a no-op. For NewWithDB adapters, it runs the bootstrap on first
// use.
func (a *Adapter) ensureSchema(ctx context.Context) error {
	if a.created {
		return nil
	}
	ctx, cancel := a.withTimeout(ctx)
	defer cancel()
	// For NewWithDB, we use Exec directly (no raw connection available).
	// The schema/table/index creation is idempotent.
	stmts := schemaStatements(a.schema, a.table, a.dimension, indexTuning{
		IndexType:          a.indexType,
		HNSWM:              a.hnswM,
		HNSWEfConstruction: a.hnswEfConstruction,
		IVFFlatLists:       a.ivfflatLists,
	})
	for _, stmt := range stmts {
		if _, err := a.db.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("pgvector: ensure schema: %w", a.redact(err))
		}
	}
	a.created = true
	return nil
}

// withTimeout returns a context with the adapter's configured timeout if the
// parent context has no deadline.
func (a *Adapter) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx) // parent already has a deadline
	}
	return context.WithTimeout(ctx, a.timeout)
}

// redact ensures no secret (DSN password) leaks into an error message.
func (a *Adapter) redact(err error) error {
	return redactDSN(err, a.password)
}

// Ensure the Adapter implements domain.VectorIndex (W8.3, REQ-VEC-001).
var _ domain.VectorIndex = (*Adapter)(nil)

// ---------------------------------------------------------------------------
// Pool wrappers (adapt pgxpool.Pool → pgvectorDB, pgx.Tx → pgvectorTx)
// ---------------------------------------------------------------------------

// poolDB wraps *pgxpool.Pool to satisfy pgvectorDB.
type poolDB struct {
	pool *pgxpool.Pool
}

func (p *poolDB) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return p.pool.Exec(ctx, sql, args...)
}

func (p *poolDB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return p.pool.Query(ctx, sql, args...)
}

func (p *poolDB) BeginTx(ctx context.Context) (pgvectorTx, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &poolTx{tx: tx}, nil
}

func (p *poolDB) Ping(ctx context.Context) error {
	return p.pool.Ping(ctx)
}

func (p *poolDB) Close() {
	p.pool.Close()
}

// poolTx wraps pgx.Tx to satisfy pgvectorTx.
type poolTx struct {
	tx pgx.Tx
}

func (t *poolTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return t.tx.Exec(ctx, sql, args...)
}

func (t *poolTx) Commit(ctx context.Context) error {
	return t.tx.Commit(ctx)
}

func (t *poolTx) Rollback(ctx context.Context) error {
	return t.tx.Rollback(ctx)
}

// ---------------------------------------------------------------------------
// Schema bootstrap helpers
// ---------------------------------------------------------------------------

// indexTuning holds the validated, typed index construction parameters. These
// are integers only — never user-provided strings — so DDL emission cannot
// introduce SQL injection via the WITH (...) clause.
type indexTuning struct {
	IndexType          string // hnsw | ivfflat
	HNSWM              int    // HNSW max connections per node
	HNSWEfConstruction int    // HNSW dynamic candidate list for build
	IVFFlatLists       int    // IVFFlat number of inverted lists
}

// schemaStatements returns the idempotent DDL statements for schema/table/index
// creation. Identifiers are pre-validated safe; values use string interpolation
// only for the dimension integer (adapter-controlled, not user input). The
// index WITH (...) options are emitted from typed integers only — there is no
// raw SQL string surface. The adapter is cosine-only (DistanceMetrics declares
// only cosine), so the op class is always vector_cosine_ops — no redundant
// switch over index type.
func schemaStatements(schema, table string, dimension int, t indexTuning) []string {
	qualified := schema + "." + table
	indexName := table + "_embedding_idx"

	// Cosine distance only (adapter declares cosine-only capabilities). The
	// op class is always vector_cosine_ops regardless of index type.
	const opClass = "vector_cosine_ops"

	stmts := []string{
		`CREATE EXTENSION IF NOT EXISTS vector`,
		fmt.Sprintf(`CREATE SCHEMA IF NOT EXISTS %s`, schema),
		fmt.Sprintf(
			`CREATE TABLE IF NOT EXISTS %s (
    id BIGINT PRIMARY KEY,
    embedding vector(%d) NOT NULL,
    model TEXT NOT NULL DEFAULT '',
    model_version TEXT NOT NULL DEFAULT '',
    dimension INT NOT NULL,
    project TEXT NOT NULL DEFAULT '',
	project_id TEXT NOT NULL DEFAULT '',
    scope TEXT NOT NULL DEFAULT '',
    tenant_id TEXT NOT NULL DEFAULT '',
	workspace_id TEXT NOT NULL DEFAULT '',
    source TEXT NOT NULL DEFAULT '',
    type TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
)`, qualified, dimension),
		fmt.Sprintf(`ALTER TABLE %s ADD COLUMN IF NOT EXISTS workspace_id TEXT NOT NULL DEFAULT ''`, qualified),
		fmt.Sprintf(`ALTER TABLE %s ADD COLUMN IF NOT EXISTS project_id TEXT NOT NULL DEFAULT ''`, qualified),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s ON %s (tenant_id, workspace_id, project_id)`, table+"_tenant_workspace_project_idx", qualified),
	}

	// pgvector HNSW and IVFFlat indexes in PostgreSQL have a hard limit of 2000 dimensions.
	// For high-dimension embedding models (> 2000, e.g. 2560d, 3072d) or unconstrained vectors (dimension == 0),
	// vectors are stored and queried with exact cosine distance scan without index DDL failure.
	if dimension > 0 && dimension <= 2000 {
		var options string
		switch t.IndexType {
		case "ivfflat":
			options = fmt.Sprintf("WITH (lists = %d)", t.IVFFlatLists)
		default: // hnsw
			options = fmt.Sprintf("WITH (m = %d, ef_construction = %d)", t.HNSWM, t.HNSWEfConstruction)
		}

		indexSQL := fmt.Sprintf(
			`CREATE INDEX IF NOT EXISTS %s ON %s USING %s (embedding %s) %s`,
			indexName, qualified, t.IndexType, opClass, options,
		)
		stmts = append(stmts, indexSQL)
	}

	return stmts
}

// bootstrapSchema runs the schema/table/index DDL on a raw pgx.Conn.
func bootstrapSchema(ctx context.Context, conn *pgx.Conn, cfg AdapterConfig) error {
	stmts := schemaStatements(cfg.Schema, cfg.Table, cfg.Dimension, indexTuning{
		IndexType:          cfg.IndexType,
		HNSWM:              cfg.HNSWM,
		HNSWEfConstruction: cfg.HNSWEfConstruction,
		IVFFlatLists:       cfg.IVFFlatLists,
	})
	for _, stmt := range stmts {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			// If index creation fails (e.g. dimensions > 2000 limit or existing column constraint),
			// log/ignore to allow sequential exact scan fallback without failing startup.
			if strings.Contains(stmt, "CREATE INDEX") {
				continue
			}
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Filter helpers
// ---------------------------------------------------------------------------

// filterKeyOrder defines the recognized filter keys and their evaluation order.
// Only keys in this list are mapped to WHERE clauses; unknown keys are ignored
// (filter-transparent but safe — no dynamic column names from user input).
var filterKeyOrder = []string{
	"project",
	"project_id",
	"scope",
	"tenant_id",
	"workspace_id",
	"type",
	"model",
	"model_version",
	"source",
}

// metaString extracts a string value from a metadata map, returning "" if the
// key is absent or the value is not a string.
func metaString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

// ---------------------------------------------------------------------------
// Secret redaction (REQ-CP-002)
// ---------------------------------------------------------------------------

// extractPassword parses the DSN to extract the password for redaction
// purposes. Returns "" if the DSN has no password or cannot be parsed.
func extractPassword(dsn string) string {
	// Try URL format: postgres://user:pass@host:port/db
	u, err := url.Parse(dsn)
	if err == nil && u.User != nil {
		pw, _ := u.User.Password()
		return pw
	}
	// Try key=value format: postgres password=pass host=localhost
	// Search for password=... (best-effort; not a full DSN parser)
	if idx := strings.Index(dsn, "password="); idx >= 0 {
		start := idx + len("password=")
		rest := dsn[start:]
		// Value ends at whitespace or end of string.
		end := strings.IndexAny(rest, " \t")
		if end < 0 {
			return rest
		}
		return rest[:end]
	}
	return ""
}

// redactDSN ensures no secret (password) leaks into an error message. If the
// error string contains the password, it is replaced with a placeholder. This
// is defense-in-depth: pgx never echoes the password, but this guard
// guarantees it regardless of upstream behavior (REQ-CP-002).
func redactDSN(err error, secret string) error {
	if secret == "" || err == nil {
		return err
	}
	msg := err.Error()
	if strings.Contains(msg, secret) {
		return fmt.Errorf("%s", strings.ReplaceAll(msg, secret, "***REDACTED***"))
	}
	return err
}
