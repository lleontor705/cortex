// Package sqlite_blob implements the always-available zero-CGO VectorIndex
// adapter (ADR-05, REQ-VEC-001).
//
// It wraps the SQLite BLOB vector store (internal/store/sqlite) behind the
// domain.VectorIndex port. This is the single highest-leverage change of
// the vector modernization: bundle.Stores.Vectors changes from the concrete
// *sqlite.VectorStore to domain.VectorIndex, unblocking every future adapter
// (qdrant, pgvector) without touching MCP/HTTP/CLI/TUI.
//
// Search is the REQ-VEC-SCAN-001 two-pass coarse-to-fine scan:
//
//  1. A Matryoshka prefix pass scores every candidate row over the leading
//     1-1/coarseDivisor of the stored dimensions and shortlists the strongest
//     max(limit*shortlistFactor, minShortlist) rows.
//  2. An int8 refinement pass rescores only that shortlist across the full
//     stored prefix.
//  3. Both passes shard disjoint row ranges across goroutines and the merge
//     orders by (score desc, observation id asc), so concurrency never changes
//     the returned sequence.
//
// Metadata-filtered searches (REQ-VEC-FILTER-001) switch plans on a
// deterministic cardinality probe: a highly selective filter refines every
// matching row (exact plan, full top-k guaranteed), a broad filter keeps the
// two-pass scan (constrained plan), and zero matches short-circuits to an
// empty result before any corpus row is read. The unfiltered path never runs
// the probe, so its scan behavior is unchanged.
//
// Storage uses the same scheme: Upsert writes a pre-normalized int8 payload
// holding the shortest leading prefix that retains energyRetain of the
// vector's L2 energy. Matryoshka embeddings concentrate that energy in the
// leading dimensions, which is where the storage reduction comes from; a
// fully unstructured vector degrades gracefully to the int8 floor instead of
// losing recall.
//
// Build-tag semantics are PRESERVED EXACTLY:
//   - Default build (cortex_vectors NOT set, zero-CGO): the underlying
//     sqlite.VectorStore is the stub that returns ErrVectorSearchDisabled.
//     The adapter reports degraded and passes the disabled error through
//     without issuing any SQL. No external service, no CGO.
//   - cortex_vectors build tag: the underlying sqlite.VectorStore is
//     available and the adapter runs the two-pass scan above.
//
// Dimension-mismatch corruption is FIXED (REQ-VEC-001 error scenario): the
// legacy cosine path logged a warning and scored mismatched vectors 0 (silent
// corruption). This adapter REJECTS any upsert whose vector dimension does not
// match the declared ModelInfo.Dimension with domain.ErrDimensionMismatch —
// the mismatched vector is never stored. At scan time a row whose stored
// dimension differs from the query still scores 0 with a warning, matching the
// legacy scan instead of silently comparing misaligned coordinates.
//
// A non-finite vector (NaN/Inf) keeps the legacy float32 blob so its scan
// semantics are unchanged: it still scores NaN and is filtered out, rather
// than being corrupted into an int8 code.
package sqlite_blob

import (
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"log"
	"math"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/lleontor705/cortex/v2/internal/domain"
	sqlitestore "github.com/lleontor705/cortex/v2/internal/store/sqlite"
)

const (
	// adapterID is the stable identifier declared via ID() and Capabilities().IndexType.
	adapterID = "sqlite_blob"

	// coarseDivisor sets the shortlist pass to read 1-1/coarseDivisor of the
	// stored dimensions (75% at 4). A prefix cosine over a fixed FRACTION of
	// the embedding correlates ~0.87 with the full-score regardless of width,
	// which is what keeps shortlist recall inside tolerance for 64-dim and
	// 4096-dim models alike. A fixed dimension count would not scale.
	coarseDivisor = 4

	// shortlistFactor and minShortlist bound the refinement budget: the int8
	// pass scores at most max(limit*shortlistFactor, minShortlist) rows, which
	// is what keeps the fine pass off the bulk of a large corpus.
	shortlistFactor = 16
	minShortlist    = 512

	// exactSelectivityDivisor and exactCandidateCeiling bound the exact plan:
	// refine every matching row only when the filter keeps at most 1/8 of the
	// corpus AND the matching set stays small enough to score exhaustively
	// without giving up the bounded-work guarantee of the two-pass scan.
	exactSelectivityDivisor = 8
	exactCandidateCeiling   = 4096

	// energyRetain is the fraction of a vector's L2 energy its stored prefix
	// must keep. At 0.999 the truncation error stays well below the int8
	// quantization error, so recall is bounded by quantization alone.
	energyRetain = 0.999
)

// Adapter wraps the existing concrete *sqlite.VectorStore as a domain.VectorIndex.
// It is the zero-CGO default and is always available for wiring (operations
// return ErrVectorSearchDisabled when the cortex_vectors tag is not set).
type Adapter struct {
	db    *sql.DB
	store *sqlitestore.VectorStore
	caps  domain.Capabilities
}

// New creates a sqlite_blob adapter over the existing concrete VectorStore.
// The db may be nil for capability/health-only wiring (tests, capability
// negotiation before the database is open). A nil db produces a stub store
// that reports unavailable, matching the zero-CGO default.
func New(db *sql.DB) *Adapter {
	return &Adapter{
		db:    db,
		store: sqlitestore.NewVectorStore(db),
		caps: domain.Capabilities{
			IndexType:       adapterID,
			DistanceMetrics: []string{"cosine"},
			MaxDimensions:   sqlitestore.MaxEmbeddingDimension,
			Filters:         "PostFilter",
			Hybrid:          "disabled",
			Namespaces:      "supported",
			Consistency:     "strong",
			BatchUpsert:     true,
			MaxBatchSize:    0, // unbounded: upsert loops within the caller's tx
		},
	}
}

// ID returns the stable adapter identifier.
func (a *Adapter) ID() string { return adapterID }

// Upsert stores a batch of vectors. Each point's vector dimension MUST match
// its declared ModelInfo.Dimension; a mismatch is rejected with
// domain.ErrDimensionMismatch (REQ-VEC-001 dim-mismatch corruption pin). The
// mismatched point and every subsequent point in the batch are rejected — the
// caller treats the batch atomically.
//
// Model-version namespace: ModelInfo.Name identifies the embedding model so
// vectors stay namespaced by model, preventing cross-model corruption.
func (a *Adapter) Upsert(ctx context.Context, points []domain.VectorPoint) error {
	for _, p := range points {
		// Dimension enforcement BEFORE any write. The legacy path scored
		// mismatched vectors 0 (silent corruption); this rejects them.
		if p.ModelInfo.Dimension > 0 && len(p.Vector) != p.ModelInfo.Dimension {
			ns := p.ModelInfo.Name
			if p.ModelInfo.Version != "" {
				ns = p.ModelInfo.Name + ":" + p.ModelInfo.Version
			}
			return domain.NewDimensionMismatchError(p.ModelInfo.Dimension, len(p.Vector), ns)
		}
		if err := a.storeVector(ctx, p); err != nil {
			return fmt.Errorf("sqlite_blob: upsert point %d: %w", p.ID, err)
		}
	}
	return nil
}

// storeVector writes one point. When the vector backend is unavailable (stub
// build) it delegates so ErrVectorSearchDisabled propagates untouched; when it
// is available it writes the quantized Matryoshka payload.
func (a *Adapter) storeVector(ctx context.Context, p domain.VectorPoint) error {
	if !a.store.IsAvailable() || a.db == nil {
		return a.store.StoreEmbedding(ctx, p.ID, p.Vector, p.ModelInfo.Name)
	}
	dims := len(p.Vector)
	if dims < sqlitestore.MinEmbeddingDimension || dims > sqlitestore.MaxEmbeddingDimension {
		return &domain.ValidationError{
			Field:   "embedding",
			Message: fmt.Sprintf("embedding must have %d-%d dimensions, got %d", sqlitestore.MinEmbeddingDimension, sqlitestore.MaxEmbeddingDimension, dims),
		}
	}
	blob, ok := encodeQuantized(p.Vector)
	if !ok {
		blob = encodeFloat32(p.Vector)
	}
	return a.writeVector(ctx, p.ID, blob, dims, p.ModelInfo.Name)
}

// writeVector upserts the serialized payload, mirroring the concrete store's
// statement so row-affect and not-found semantics stay identical.
func (a *Adapter) writeVector(ctx context.Context, id int64, blob []byte, dims int, model string) error {
	res, err := a.db.ExecContext(ctx, `
		INSERT INTO observation_vectors (observation_id, embedding, embedding_model, dimensions, updated_at)
		VALUES (?, ?, ?, ?, datetime('now'))
		ON CONFLICT(observation_id) DO UPDATE SET
			embedding = excluded.embedding,
			embedding_model = excluded.embedding_model,
			dimensions = excluded.dimensions,
			updated_at = datetime('now')
	`, id, blob, model, dims)
	if err != nil {
		return fmt.Errorf("vector store: store embedding: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("vector store: get rows affected: %w", err)
	}
	if rows == 0 {
		return &domain.NotFoundError{Type: "embedding", ID: id}
	}
	return nil
}

// Search runs the two-pass coarse-to-fine scan. Filters map "project"/"scope"
// onto the legacy Project/Scope fields, preserving the concrete store's local
// filter behavior; "type" and "source" join them in the pushed-down WHERE and
// cardinality probe.
func (a *Adapter) Search(ctx context.Context, q domain.VectorQuery) ([]domain.VectorCandidate, error) {
	opts := domain.VectorSearchOptions{
		Embedding: q.Vector,
		Limit:     q.Limit,
		Threshold: q.Threshold,
	}
	if q.Filters != nil {
		opts = filtersToSearchOptionsWith(opts, q.Filters)
	}
	if !a.store.IsAvailable() || a.db == nil {
		return a.delegatedSearch(ctx, opts)
	}
	return a.scanTwoPass(ctx, opts, parseMetadataFilters(q.Filters))
}

// delegatedSearch preserves the pre-modernization path: it is the zero-CGO
// stub route (ErrVectorSearchDisabled) and the capability-only nil-db route.
func (a *Adapter) delegatedSearch(ctx context.Context, opts domain.VectorSearchOptions) ([]domain.VectorCandidate, error) {
	results, err := a.store.SearchByVector(ctx, opts)
	if err != nil {
		return nil, err
	}
	candidates := make([]domain.VectorCandidate, 0, len(results))
	for _, r := range results {
		candidates = append(candidates, domain.VectorCandidate{
			ID:         r.ID,
			Score:      r.Similarity,
			Provenance: adapterID,
		})
	}
	return candidates, nil
}

// scanTwoPass applies the filter-selectivity decision and executes the chosen
// plan: an exact refinement over every matching row, or the coarse prefix
// pass, the shortlist selection, the int8 refinement pass, and the
// deterministic merge.
func (a *Adapter) scanTwoPass(ctx context.Context, opts domain.VectorSearchOptions, filters metadataFilters) ([]domain.VectorCandidate, error) {
	dims := len(opts.Embedding)
	if dims < sqlitestore.MinEmbeddingDimension || dims > sqlitestore.MaxEmbeddingDimension {
		return nil, &domain.ValidationError{
			Field:   "embedding",
			Message: fmt.Sprintf("query embedding must have %d-%d dimensions, got %d", sqlitestore.MinEmbeddingDimension, sqlitestore.MaxEmbeddingDimension, dims),
		}
	}
	clampSearchOptions(&opts)

	plan := planConstrained
	if filters.active() {
		total, matching, err := a.probeCardinality(ctx, filters)
		if err != nil {
			return nil, err
		}
		plan = chooseScanPlan(total, matching)
		if plan == planNoCandidates {
			return []domain.VectorCandidate{}, nil
		}
	}

	queryNorm := normalizeUnit(opts.Embedding)
	corpus, err := a.readCorpus(ctx, filters)
	if err != nil {
		return nil, err
	}
	results := make([]domain.VectorCandidate, 0, opts.Limit)
	if len(corpus) == 0 {
		return results, nil
	}

	var shortlist []int
	if plan == planExact {
		shortlist = allRowIndices(len(corpus))
	} else {
		coarse, err := a.coarsePass(queryNorm, corpus)
		if err != nil {
			return nil, err
		}
		shortlist = selectShortlist(corpus, coarse, opts.Limit)
	}
	refined, err := a.refinePass(queryNorm, corpus, shortlist)
	if err != nil {
		return nil, err
	}

	for j, score := range refined {
		if math.IsNaN(score) || math.IsInf(score, 0) {
			continue
		}
		if score < opts.Threshold {
			continue
		}
		results = append(results, domain.VectorCandidate{
			ID:         corpus[shortlist[j]].id,
			Score:      score,
			Provenance: adapterID,
		})
	}
	sortCandidates(results)
	if len(results) > opts.Limit {
		results = results[:opts.Limit]
	}
	return results, nil
}

// readCorpus drains the join into memory so the scoring passes can shard row
// ranges across goroutines. Active metadata filters are pushed into the WHERE
// clause through the same condition builder the cardinality probe uses, so
// the probe's match count and the materialized corpus can never disagree.
// Only identity and payload are needed at the domain.VectorIndex boundary, so
// the observation body columns are no longer fetched.
func (a *Adapter) readCorpus(ctx context.Context, filters metadataFilters) ([]corpusRow, error) {
	query := `
		SELECT o.id, ov.embedding
		FROM observation_vectors ov
		JOIN observations o ON o.id = ov.observation_id
		WHERE o.deleted_at IS NULL
	`
	conditions, args := filters.filterConditions()
	for _, c := range conditions {
		query += " AND " + c
	}

	rows, err := a.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("vector store: search query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	corpus := make([]corpusRow, 0, 64)
	for rows.Next() {
		var r corpusRow
		if err := rows.Scan(&r.id, &r.blob); err != nil {
			return nil, fmt.Errorf("vector store: scan result: %w", err)
		}
		corpus = append(corpus, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("vector store: iterate results: %w", err)
	}
	return corpus, nil
}

// --- Filter selectivity (REQ-VEC-FILTER-001) -------------------------------

// scanPlan is the selectivity decision for one filtered search.
type scanPlan int

const (
	// planConstrained keeps the coarse-to-fine two-pass scan.
	planConstrained scanPlan = iota
	// planExact refines every matching row and skips the coarse pass, so a
	// selective filter returns the full requested top-k.
	planExact
	// planNoCandidates short-circuits before any corpus row is read.
	planNoCandidates
)

// metadataFilters holds the recognized string filter keys, mirroring the
// retrieval engine's in-engine post-filter set. An empty value (or a
// non-string value) means "not set"; scope is folded onto the stored
// normalization exactly as the corpus WHERE clause always applied it.
type metadataFilters struct {
	project string
	scope   string
	typ     string
	source  string
}

// parseMetadataFilters extracts the recognized keys from a VectorQuery filter
// map. Unknown keys are ignored (filter-transparent), matching retrieval.
func parseMetadataFilters(filters map[string]any) metadataFilters {
	parsed := metadataFilters{
		project: stringFilter(filters, "project"),
		typ:     stringFilter(filters, "type"),
		source:  stringFilter(filters, "source"),
	}
	if scope := stringFilter(filters, "scope"); scope != "" {
		parsed.scope = normalizeScope(scope)
	}
	return parsed
}

// stringFilter returns the filter's string value, or "" when the key is
// absent, empty, or not a string.
func stringFilter(filters map[string]any, key string) string {
	v, ok := filters[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

// active reports whether any recognized filter constrains the search.
func (f metadataFilters) active() bool {
	return f.project != "" || f.scope != "" || f.typ != "" || f.source != ""
}

// filterConditions returns the SQL predicates and ordered placeholder args
// for every active filter. Both the corpus WHERE clause and the cardinality
// probe's CASE expression consume this one builder so the probe can never
// disagree with the scan about which rows match.
func (f metadataFilters) filterConditions() ([]string, []any) {
	var conditions []string
	var args []any
	if f.project != "" {
		conditions = append(conditions, "o.project = ?")
		args = append(args, f.project)
	}
	if f.scope != "" {
		conditions = append(conditions, "o.scope = ?")
		args = append(args, f.scope)
	}
	if f.typ != "" {
		conditions = append(conditions, "o.type = ?")
		args = append(args, f.typ)
	}
	if f.source != "" {
		conditions = append(conditions, "o.source = ?")
		args = append(args, f.source)
	}
	return conditions, args
}

// probeCardinality counts the non-deleted vector join once and reports how
// many of those rows match the active filters. It runs only for active
// filters, so the unfiltered path pays nothing.
func (a *Adapter) probeCardinality(ctx context.Context, filters metadataFilters) (total, matching int, err error) {
	conditions, args := filters.filterConditions()
	query := `
		SELECT COUNT(*), COUNT(CASE WHEN ` + strings.Join(conditions, " AND ") + ` THEN 1 END)
		FROM observation_vectors ov
		JOIN observations o ON o.id = ov.observation_id
		WHERE o.deleted_at IS NULL
	`
	if err := a.db.QueryRowContext(ctx, query, args...).Scan(&total, &matching); err != nil {
		return 0, 0, fmt.Errorf("vector store: filter cardinality probe: %w", err)
	}
	return total, matching, nil
}

// chooseScanPlan applies the deterministic selectivity heuristic: zero
// matches short-circuit; an exact full refinement runs only when the filter
// keeps at most 1/exactSelectivityDivisor of the corpus within
// exactCandidateCeiling rows; everything else stays on the bounded two-pass
// scan. Pure so tests can pin every boundary.
func chooseScanPlan(total, matching int) scanPlan {
	if matching <= 0 {
		return planNoCandidates
	}
	if matching*exactSelectivityDivisor <= total && matching <= exactCandidateCeiling {
		return planExact
	}
	return planConstrained
}

// allRowIndices builds the identity shortlist the exact plan refines over.
func allRowIndices(n int) []int {
	indices := make([]int, n)
	for i := range indices {
		indices[i] = i
	}
	return indices
}

// scanPass distinguishes the cheap shortlist pass from the full refinement
// pass; the distinction is only how many stored dimensions are read.
type scanPass int

const (
	passCoarse scanPass = iota
	passRefine
)

// coarsePass scores every row over the leading 1-1/coarseDivisor of its stored
// dimensions. Each worker owns a disjoint index range of the shared output
// slices, so no synchronisation is needed and the result is independent of how
// the ranges are scheduled.
func (a *Adapter) coarsePass(queryNorm []float64, corpus []corpusRow) ([]float64, error) {
	scores := make([]float64, len(corpus))
	failures := make([]error, len(corpus))
	shardRange(len(corpus), func(lo, hi int) {
		for i := lo; i < hi; i++ {
			score, _, err := storedScore(queryNorm, corpus[i].blob, passCoarse)
			scores[i] = score
			failures[i] = err
		}
	})
	if err := firstFailure(failures); err != nil {
		return nil, fmt.Errorf("vector store: deserialize embedding: %w", err)
	}
	return scores, nil
}

// refinePass rescores the shortlisted rows across the full stored prefix.
func (a *Adapter) refinePass(queryNorm []float64, corpus []corpusRow, shortlist []int) ([]float64, error) {
	scores := make([]float64, len(shortlist))
	failures := make([]error, len(shortlist))
	shardRange(len(shortlist), func(lo, hi int) {
		for j := lo; j < hi; j++ {
			score, _, err := storedScore(queryNorm, corpus[shortlist[j]].blob, passRefine)
			scores[j] = score
			failures[j] = err
		}
	})
	if err := firstFailure(failures); err != nil {
		return nil, fmt.Errorf("vector store: deserialize embedding: %w", err)
	}
	return scores, nil
}

// selectShortlist picks the refinement candidates in deterministic
// (coarse score desc, id asc) order. Non-finite coarse scores rank last so a
// NaN row can never scramble the total order.
func selectShortlist(corpus []corpusRow, coarse []float64, limit int) []int {
	budget := limit * shortlistFactor
	if budget < minShortlist {
		budget = minShortlist
	}
	if budget > len(corpus) {
		budget = len(corpus)
	}
	order := make([]int, len(corpus))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(x, y int) bool {
		sx, sy := rankScore(coarse[order[x]]), rankScore(coarse[order[y]])
		if sx != sy {
			return sx > sy
		}
		return corpus[order[x]].id < corpus[order[y]].id
	})
	return order[:budget]
}

// Delete removes vectors by observation ID. The underlying store's
// DeleteEmbedding handles the not-found case; missing IDs in the batch are
// tolerated (idempotent delete).
func (a *Adapter) Delete(ctx context.Context, ids []int64) error {
	for _, id := range ids {
		if err := a.store.DeleteEmbedding(ctx, id); err != nil {
			// Tolerate not-found for batch idempotency.
			if domain.IsNotFoundError(err) {
				continue
			}
			return fmt.Errorf("sqlite_blob: delete point %d: %w", id, err)
		}
	}
	return nil
}

// Health reports the adapter's current health. When the underlying store is
// unavailable (zero-CGO stub), Health returns degraded with a diagnostic
// message. When the store is available (cortex_vectors enabled), Health
// returns healthy.
func (a *Adapter) Health(_ context.Context) domain.Health {
	if a.store == nil || !a.store.IsAvailable() {
		return domain.Health{
			Status:  domain.StatusDegraded,
			Message: "sqlite_blob: vector search disabled (rebuild with -tags cortex_vectors)",
		}
	}
	return domain.Health{Status: domain.StatusHealthy, Message: "sqlite_blob: ready"}
}

// Capabilities declares the sqlite_blob adapter's supported features for
// capability-driven strategy selection (ADR-05). sqlite_blob is a coarse-to-
// fine cosine scan with post-filtering, strong consistency (same SQLite tx),
// and batch upsert support.
func (a *Adapter) Capabilities(_ context.Context) (domain.Capabilities, error) {
	return a.caps, nil
}

// Close releases resources. sqlite_blob holds no resources beyond the shared
// *sql.DB (owned by the caller), so Close is a no-op.
func (a *Adapter) Close() error { return nil }

// Ensure the Adapter implements domain.VectorIndex (W8.1 adoption, REQ-VEC-001).
var _ domain.VectorIndex = (*Adapter)(nil)

// --- Payload encoding -------------------------------------------------------

// corpusRow is one scanned (identity, payload) pair.
type corpusRow struct {
	id   int64
	blob []byte
}

// encodeQuantized packs a vector into the pre-normalized int8 prefix payload.
// It reports false for non-finite input, which must keep the legacy float32
// blob so NaN/Inf scan semantics survive unchanged.
func encodeQuantized(v []float32) ([]byte, bool) {
	dims := len(v)
	if dims == 0 || dims > math.MaxUint16 {
		return nil, false
	}
	var energy float64
	for _, x := range v {
		f := float64(x)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, false
		}
		energy += f * f
	}
	inv := 0.0
	if energy > 0 {
		inv = 1 / math.Sqrt(energy)
	}
	prefix := 1
	if inv > 0 {
		prefix = dims
		var acc float64
		for i, x := range v {
			n := float64(x) * inv
			acc += n * n
			if acc >= energyRetain {
				prefix = i + 1
				break
			}
		}
	}
	var peak float64
	for i := 0; i < prefix; i++ {
		if a := math.Abs(float64(v[i]) * inv); a > peak {
			peak = a
		}
	}
	scale := float32(1)
	if peak > 0 {
		scale = float32(peak / 127)
	}
	if !(scale > 0) {
		scale = 1
	}

	out := make([]byte, sqlitestore.QuantHeaderBytes+prefix)
	copy(out[:4], sqlitestore.QuantMagic[:])
	binary.LittleEndian.PutUint16(out[4:6], uint16(dims))
	binary.LittleEndian.PutUint32(out[6:10], math.Float32bits(scale))
	for i := 0; i < prefix; i++ {
		q := math.Round(float64(v[i]) * inv / float64(scale))
		if q > 127 {
			q = 127
		} else if q < -127 {
			q = -127
		}
		out[sqlitestore.QuantHeaderBytes+i] = byte(int8(q))
	}
	return out, true
}

// encodeFloat32 is the legacy little-endian blob used for non-finite vectors.
func encodeFloat32(v []float32) []byte {
	buf := make([]byte, len(v)*4)
	for i, x := range v {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(x))
	}
	return buf
}

// storedScore computes the cosine between the unit-length query and a stored
// payload. passCoarse reads only the leading 1-1/coarseDivisor of the stored
// dimensions; passRefine reads all of them. The declared dimension is always
// returned so a stored/query dimension mismatch can be detected; such rows
// score 0 with a warning, exactly like the legacy scan, instead of comparing
// misaligned coordinates.
func storedScore(queryNorm []float64, data []byte, pass scanPass) (float64, int, error) {
	if dims, prefix, scale, ok := sqlitestore.DecodeQuantHeader(data); ok {
		if dims != len(queryNorm) {
			logDimensionMismatch(len(queryNorm), dims)
			return 0, dims, nil
		}
		return dotPrefix(queryNorm, data, sqlitestore.QuantHeaderBytes, prefix, scale, pass), dims, nil
	}

	dims := len(data) / 4
	if dims == 0 {
		return 0, 0, fmt.Errorf("empty embedding data")
	}
	if dims != len(queryNorm) {
		logDimensionMismatch(len(queryNorm), dims)
		return 0, dims, nil
	}
	return dotPrefix(queryNorm, data, 0, dims, 0, pass), dims, nil
}

// dotPrefix accumulates the cosine over the stored components selected by
// pass. A quantized payload (scale > 0) is dequantized from int8 first; a
// legacy payload is read as little-endian float32. A single division at the
// end replaces the legacy scan's per-element normalize pass.
func dotPrefix(queryNorm []float64, data []byte, offset, n int, scale float64, pass scanPass) float64 {
	if pass == passCoarse {
		n -= n / coarseDivisor
	}
	var dot, stored, query float64
	for i := 0; i < n; i++ {
		var x float64
		if scale > 0 {
			x = float64(int8(data[offset+i])) * scale
		} else {
			x = float64(math.Float32frombits(binary.LittleEndian.Uint32(data[offset+i*4:])))
		}
		q := queryNorm[i]
		dot += q * x
		stored += x * x
		query += q * q
	}
	// A NaN on either side must propagate as NaN (the legacy scan's "scored
	// NaN, filtered out" behavior); only a genuinely empty side scores 0.
	if math.IsNaN(dot) || math.IsNaN(stored) || math.IsNaN(query) {
		return math.NaN()
	}
	if !(stored > 0) || !(query > 0) {
		return 0
	}
	return dot / math.Sqrt(stored*query)
}

// --- Scan helpers -----------------------------------------------------------

// clampSearchOptions applies the concrete store's limit and threshold clamps.
func clampSearchOptions(opts *domain.VectorSearchOptions) {
	if opts.Limit <= 0 {
		opts.Limit = 10
	}
	if opts.Limit > 100 {
		opts.Limit = 100
	}
	if opts.Threshold < 0 {
		opts.Threshold = 0
	}
	if opts.Threshold > 1 {
		opts.Threshold = 1
	}
}

// normalizeUnit returns the L2-normalized query, or an all-zero slice for a
// zero-norm query (which scores 0 against everything).
func normalizeUnit(v []float32) []float64 {
	var sum float64
	for _, x := range v {
		f := float64(x)
		sum += f * f
	}
	norm := math.Sqrt(sum)
	out := make([]float64, len(v))
	if norm < 1e-10 {
		return out
	}
	for i, x := range v {
		out[i] = float64(x) / norm
	}
	return out
}

// normalizeScope folds a scope filter onto the stored normalization.
func normalizeScope(scope string) string {
	v := strings.TrimSpace(strings.ToLower(scope))
	if v == "personal" {
		return "personal"
	}
	return "project"
}

// shardRange runs fn over disjoint contiguous index ranges on GOMAXPROCS
// goroutines. Callers must not share mutable state between ranges.
func shardRange(n int, fn func(lo, hi int)) {
	if n <= 0 {
		return
	}
	workers := runtime.GOMAXPROCS(0)
	if workers > n {
		workers = n
	}
	if workers <= 1 {
		fn(0, n)
		return
	}
	var wg sync.WaitGroup
	for g := 0; g < workers; g++ {
		lo := g * n / workers
		hi := (g + 1) * n / workers
		if lo == hi {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			fn(lo, hi)
		}()
	}
	wg.Wait()
}

// firstFailure returns the earliest indexed failure so error reporting does
// not depend on goroutine scheduling.
func firstFailure(failures []error) error {
	for _, err := range failures {
		if err != nil {
			return err
		}
	}
	return nil
}

// rankScore maps a non-finite coarse score to the lowest possible rank so the
// shortlist sort always sees a total order.
func rankScore(s float64) float64 {
	if math.IsNaN(s) {
		return math.Inf(-1)
	}
	return s
}

// sortCandidates orders by similarity descending with observation id as the
// deterministic tie-break.
func sortCandidates(results []domain.VectorCandidate) {
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return results[i].ID < results[j].ID
	})
}

// logDimensionMismatch preserves the legacy scan's warning for rows whose
// stored dimension does not match the query.
func logDimensionMismatch(query, stored int) {
	log.Printf("warning: cosine similarity dimension mismatch: query=%d stored=%d", query, stored)
}

// filtersToSearchOptions builds a VectorSearchOptions from a filter map,
// mapping the "project" and "scope" keys onto the legacy Project/Scope fields.
// This preserves the exact local filter behavior the concrete store already
// implements.
func filtersToSearchOptions(filters map[string]any) domain.VectorSearchOptions {
	return filtersToSearchOptionsWith(domain.VectorSearchOptions{}, filters)
}

// filtersToSearchOptionsWith applies the filter map onto a base options struct.
func filtersToSearchOptionsWith(base domain.VectorSearchOptions, filters map[string]any) domain.VectorSearchOptions {
	if v, ok := filters["project"]; ok {
		if s, ok := v.(string); ok {
			base.Project = s
		}
	}
	if v, ok := filters["scope"]; ok {
		if s, ok := v.(string); ok {
			base.Scope = s
		}
	}
	return base
}
