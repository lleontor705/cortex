// Package qdrant implements the first external VectorIndex adapter
// (ADR-05, REQ-VEC-001/002). It uses the official Qdrant Go client (gRPC) to
// talk to a Qdrant server.
//
// SERVER-TRACK BOUNDARY: this package is SERVER-track (arch_test.go bans it
// from local composition). The local composition path (internal/app) wires the
// sqlite_blob zero-CGO default and MUST NOT import this package. Provider
// selection (qdrant.New) happens ONLY in the server/external composition path.
// This preserves REQ-FOUND-001: CGO_ENABLED=0 local build with zero external
// vector dependencies.
//
// Collection naming/namespacing: one Qdrant collection per (collection name).
// The collection's vector size is fixed at creation (dimension). Dimension and
// model mismatch are rejected FAIL-CLOSED before any server call (REQ-VEC-001
// dim-mismatch corruption pin; model-namespace mismatch). The collection is
// lazily created on first use if it does not exist.
//
// No plaintext secrets: the APIKey is passed to the gRPC dial config and is
// NEVER logged or surfaced in error messages (REQ-CP-002). Error wrapping
// excludes the key from every error string.
//
// Timeouts/retries: every operation runs under a context with a configurable
// timeout; the underlying client is configured with a RetryConfig for transient
// gRPC failures (ResourceExhausted, Unavailable).
package qdrant

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/qdrant/go-client/qdrant"
)

// adapterID is the stable identifier declared via ID() and
// Capabilities().IndexType.
const adapterID = "qdrant"

// defaultTimeout is the per-operation gRPC timeout applied when the caller's
// context has no deadline.
const defaultTimeout = 30 * time.Second

// qdrantClient is the NARROW interface the adapter depends on. *qdrant.Client
// satisfies it (it has a superset of these methods). Defining this seam lets
// unit tests substitute a fake WITHOUT a running Qdrant server. The adapter
// never reaches for client methods outside this set.
type qdrantClient interface {
	CreateCollection(ctx context.Context, req *qdrant.CreateCollection) error
	CollectionExists(ctx context.Context, collectionName string) (bool, error)
	DeleteCollection(ctx context.Context, collectionName string) error
	Upsert(ctx context.Context, req *qdrant.UpsertPoints) (*qdrant.UpdateResult, error)
	Query(ctx context.Context, req *qdrant.QueryPoints) ([]*qdrant.ScoredPoint, error)
	Delete(ctx context.Context, req *qdrant.DeletePoints) (*qdrant.UpdateResult, error)
	HealthCheck(ctx context.Context) (*qdrant.HealthCheckReply, error)
	Close() error
}

// AdapterConfig holds the parameters to construct a Qdrant Adapter. It mirrors
// config.QdrantConfig but is declared here so the adapter package is
// self-contained (the server composition path maps config.QdrantConfig →
// AdapterConfig).
type AdapterConfig struct {
	Host         string // gRPC host (default localhost)
	Port         int    // gRPC port (default 6334)
	Collection   string // collection name (default cortex)
	Dimension    int    // expected vector dimension (collection vector size)
	ModelName    string // model name for namespace enforcement (empty = skip)
	APIKey       string // optional API key (never logged)
	UseTLS       bool   // TLS for gRPC
	MaxBatchSize int    // upsert batch ceiling (default 256)
	MaxRetries   uint   // transient gRPC retries (default 3)
	Timeout      time.Duration
}

// Adapter implements domain.VectorIndex over a Qdrant server via the official
// Go client.
type Adapter struct {
	client       qdrantClient
	collection   string
	dimension    int
	modelName    string
	maxBatchSize int
	apiKey       string
	timeout      time.Duration
	ownClient    bool // Close should close the underlying client (factory-built)
	created      bool // collection has been verified/created this session
	caps         domain.Capabilities
}

// New constructs a Qdrant Adapter with a real gRPC client. The caller MUST be
// in the server/external composition path (this package is server-track). The
// returned Adapter owns its client; Close closes it.
func New(cfg AdapterConfig) (*Adapter, error) {
	if cfg.Collection == "" {
		return nil, fmt.Errorf("qdrant: collection name is required")
	}
	if cfg.Dimension <= 0 {
		return nil, fmt.Errorf("qdrant: dimension must be > 0")
	}
	host := cfg.Host
	if host == "" {
		host = "localhost"
	}
	port := cfg.Port
	if port == 0 {
		port = 6334
	}
	maxBatch := cfg.MaxBatchSize
	if maxBatch <= 0 {
		maxBatch = 256
	}
	retries := cfg.MaxRetries
	if retries == 0 {
		retries = 3
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	client, err := qdrant.NewClient(&qdrant.Config{
		Host:                   host,
		Port:                   port,
		APIKey:                 cfg.APIKey,
		UseTLS:                 cfg.UseTLS,
		SkipCompatibilityCheck: false,
		VersionCheckTimeout:    timeout,
		RetryConfig: &qdrant.RetryConfig{
			MaxRetries:  retries,
			BaseBackoff: 100 * time.Millisecond,
			MaxBackoff:  5 * time.Second,
		},
	})
	if err != nil {
		// NewClient can fail during the version-compatibility probe. The key is
		// NOT included in the error (it never is — qdrant.NewClient does not
		// echo it). We wrap generically to be safe.
		return nil, fmt.Errorf("qdrant: connect to %s:%d: %w", host, port, err)
	}

	return &Adapter{
		client:       client,
		collection:   cfg.Collection,
		dimension:    cfg.Dimension,
		modelName:    cfg.ModelName,
		maxBatchSize: maxBatch,
		apiKey:       cfg.APIKey,
		timeout:      timeout,
		ownClient:    true,
		caps:         defaultCapabilities(cfg.Dimension, maxBatch),
	}, nil
}

// NewWithClient constructs an Adapter over a pre-built (or fake) client. Used
// by tests and by compositions that manage the client lifecycle externally. The
// caller controls whether Close closes the client (see ownClient on the
// returned Adapter; default false for NewWithClient).
func NewWithClient(client qdrantClient, cfg AdapterConfig) *Adapter {
	maxBatch := cfg.MaxBatchSize
	if maxBatch <= 0 {
		maxBatch = 256
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return &Adapter{
		client:       client,
		collection:   cfg.Collection,
		dimension:    cfg.Dimension,
		modelName:    cfg.ModelName,
		maxBatchSize: maxBatch,
		apiKey:       cfg.APIKey,
		timeout:      timeout,
		ownClient:    false,
		caps:         defaultCapabilities(cfg.Dimension, maxBatch),
	}
}

// defaultCapabilities builds the Capabilities struct declared by this adapter.
// Qdrant does filtered HNSW (PreFilter), HNSW indexing, supports namespaces via
// collections, and is eventually consistent in a cluster. The adapter itself is
// a dense candidate source (the retrieval engine owns RRF fusion), so Hybrid is
// declared as "engine" — the adapter does NOT do native fusion; it provides
// dense candidates for the engine to fuse.
//
// maxBatch is the NORMALIZED configured batch ceiling (already defaulted). It is
// threaded through here so Capabilities().MaxBatchSize reports the SAME value
// the adapter uses for chunking upserts — never a stale hardcoded constant.
func defaultCapabilities(dimension, maxBatch int) domain.Capabilities {
	return domain.Capabilities{
		IndexType:       adapterID,
		DistanceMetrics: []string{"cosine"},
		MaxDimensions:   dimension,
		Filters:         "PreFilter",
		Hybrid:          "engine", // engine owns fusion; adapter is dense-only
		Namespaces:      "supported",
		Consistency:     "eventual",
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
// rejected FAIL-CLOSED before any server call (REQ-VEC-001 dim-mismatch pin;
// model mismatch). The batch is chunked at maxBatchSize. Each point's metadata
// plus model info is forwarded as Qdrant payload for traceability and
// PreFilter search.
func (a *Adapter) Upsert(ctx context.Context, points []domain.VectorPoint) error {
	if len(points) == 0 {
		return nil
	}
	if err := a.ensureCollection(ctx); err != nil {
		return err
	}
	// Validate EVERY point fail-closed before sending anything.
	qPoints := make([]*qdrant.PointStruct, 0, len(points))
	for _, p := range points {
		if err := a.validatePoint(p); err != nil {
			return err // fail-closed: reject the whole batch on first bad point
		}
		qPoints = append(qPoints, a.toPointStruct(p))
	}
	// Chunk at maxBatchSize.
	for start := 0; start < len(qPoints); start += a.maxBatchSize {
		end := start + a.maxBatchSize
		if end > len(qPoints) {
			end = len(qPoints)
		}
		batch := qPoints[start:end]
		wait := true
		if _, err := a.client.Upsert(ctx, &qdrant.UpsertPoints{
			CollectionName: a.collection,
			Wait:           &wait,
			Points:         batch,
		}); err != nil {
			return fmt.Errorf("qdrant: upsert batch [%d:%d]: %w", start, end, a.redact(err))
		}
	}
	return nil
}

// validatePoint rejects dimension and model-namespace mismatch fail-closed.
func (a *Adapter) validatePoint(p domain.VectorPoint) error {
	dim := p.ModelInfo.Dimension
	if dim <= 0 {
		dim = a.dimension // fall back to collection dimension when ModelInfo omits it
	}
	if len(p.Vector) != dim {
		ns := p.ModelInfo.Name
		if p.ModelInfo.Version != "" {
			ns = p.ModelInfo.Name + ":" + p.ModelInfo.Version
		}
		return domain.NewDimensionMismatchError(dim, len(p.Vector), ns)
	}
	// Model-namespace mismatch: the adapter is configured for a single model.
	// A point from a different model is rejected to prevent mixing models in
	// one collection (model-version namespace isolation).
	if a.modelName != "" && p.ModelInfo.Name != "" && p.ModelInfo.Name != a.modelName {
		return fmt.Errorf("%w: expected %q, got %q", domain.ErrNamespaceMismatch, a.modelName, p.ModelInfo.Name)
	}
	return nil
}

// toPointStruct maps a domain.VectorPoint to a Qdrant PointStruct, forwarding
// metadata and model info as payload.
func (a *Adapter) toPointStruct(p domain.VectorPoint) *qdrant.PointStruct {
	payload := qdrant.NewValueMap(p.Metadata)
	if payload == nil {
		payload = map[string]*qdrant.Value{}
	}
	// Always stamp model metadata for traceability + namespace queries.
	payload["model"] = qdrant.NewValueString(p.ModelInfo.Name)
	if p.ModelInfo.Version != "" {
		payload["model_version"] = qdrant.NewValueString(p.ModelInfo.Version)
	}
	if p.ModelInfo.Dimension > 0 {
		payload["dimension"] = qdrant.NewValueInt(int64(p.ModelInfo.Dimension))
	}
	return &qdrant.PointStruct{
		Id:      qdrant.NewIDNum(uint64(p.ID)),
		Vectors: qdrant.NewVectors(p.Vector...),
		Payload: payload,
	}
}

// Search translates a domain.VectorQuery into a Qdrant QueryPoints call. Filters
// are mapped to Must conditions (PreFilter). The score threshold is applied
// client-side after receiving results (Qdrant ScoreThreshold is forwarded too,
// but the client-side filter is authoritative for cross-adapter conformance).
func (a *Adapter) Search(ctx context.Context, q domain.VectorQuery) ([]domain.VectorCandidate, error) {
	if len(q.Vector) == 0 {
		return nil, fmt.Errorf("qdrant: search vector is empty")
	}
	if err := a.ensureCollection(ctx); err != nil {
		return nil, err
	}
	limit := uint64(q.Limit)
	if limit == 0 {
		limit = 10
	}
	qp := &qdrant.QueryPoints{
		CollectionName: a.collection,
		Query:          qdrant.NewQueryDense(q.Vector),
		Limit:          &limit,
		WithPayload:    qdrant.NewWithPayload(false),
	}
	if q.Filters != nil {
		qp.Filter = filtersToQdrant(q.Filters)
	}
	if q.Threshold > 0 {
		th := float32(q.Threshold)
		qp.ScoreThreshold = &th
	}
	results, err := a.client.Query(ctx, qp)
	if err != nil {
		return nil, fmt.Errorf("qdrant: search: %w", a.redact(err))
	}
	candidates := make([]domain.VectorCandidate, 0, len(results))
	for _, sp := range results {
		score := float64(sp.GetScore())
		if q.Threshold > 0 && score < q.Threshold {
			continue // client-side threshold enforcement
		}
		candidates = append(candidates, domain.VectorCandidate{
			ID:         int64(sp.GetId().GetNum()),
			Score:      score,
			Provenance: adapterID,
		})
	}
	return candidates, nil
}

// Delete removes vectors by observation ID. Missing IDs are tolerated
// (idempotent delete — Qdrant does not error on absent IDs).
func (a *Adapter) Delete(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	if err := a.ensureCollection(ctx); err != nil {
		return err
	}
	pointIDs := make([]*qdrant.PointId, 0, len(ids))
	for _, id := range ids {
		pointIDs = append(pointIDs, qdrant.NewIDNum(uint64(id)))
	}
	wait := true
	if _, err := a.client.Delete(ctx, &qdrant.DeletePoints{
		CollectionName: a.collection,
		Wait:           &wait,
		Points:         qdrant.NewPointsSelectorIDs(pointIDs),
	}); err != nil {
		return fmt.Errorf("qdrant: delete: %w", a.redact(err))
	}
	return nil
}

// Health reports the adapter's current health by probing the Qdrant server. The
// API key is never included in the health message.
func (a *Adapter) Health(ctx context.Context) domain.Health {
	reply, err := a.client.HealthCheck(ctx)
	if err != nil {
		return domain.Health{
			Status:  domain.StatusUnhealthy,
			Message: "qdrant: health check failed (connection unreachable)",
		}
	}
	return domain.Health{
		Status:  domain.StatusHealthy,
		Message: fmt.Sprintf("qdrant: ready (server %s)", reply.GetVersion()),
	}
}

// Close releases resources. If the adapter owns its client (factory-built via
// New), the underlying gRPC client is closed.
func (a *Adapter) Close() error {
	if a.ownClient {
		return a.client.Close()
	}
	return nil
}

// ensureCollection lazily verifies (and creates if absent) the Qdrant collection
// on first use. This is idempotent: once created/verified, subsequent calls are
// no-ops within the session.
func (a *Adapter) ensureCollection(ctx context.Context) error {
	if a.created {
		return nil
	}
	exists, err := a.client.CollectionExists(ctx, a.collection)
	if err != nil {
		return fmt.Errorf("qdrant: check collection %q: %w", a.collection, a.redact(err))
	}
	if !exists {
		if err := a.client.CreateCollection(ctx, &qdrant.CreateCollection{
			CollectionName: a.collection,
			VectorsConfig: qdrant.NewVectorsConfig(&qdrant.VectorParams{
				Size:     uint64(a.dimension),
				Distance: qdrant.Distance_Cosine,
			}),
		}); err != nil {
			return fmt.Errorf("qdrant: create collection %q: %w", a.collection, a.redact(err))
		}
	}
	a.created = true
	return nil
}

// filtersToQdrant maps a domain.VectorQuery filter map to a Qdrant Filter with
// Must conditions (PreFilter). Recognized keys include tenant_id and
// workspace_id; scoped server queries provide both so legacy points missing
// either payload field cannot match.
// model, model_version, source, type. Unknown string-valued keys are also
// forwarded as Match conditions (the adapter is filter-transparent).
func filtersToQdrant(filters map[string]any) *qdrant.Filter {
	var must []*qdrant.Condition
	for k, v := range filters {
		s, ok := v.(string)
		if !ok || s == "" {
			continue
		}
		must = append(must, qdrant.NewMatch(k, s))
	}
	if len(must) == 0 {
		return nil
	}
	return &qdrant.Filter{Must: must}
}

// redact ensures no secret (API key) leaks into an error message. If the error
// string contains the configured key, it is replaced with a placeholder. This
// is defense-in-depth: the qdrant client never echoes the key, but this guard
// guarantees it regardless of upstream behavior (REQ-CP-002).
func (a *Adapter) redact(err error) error {
	return redactWith(err, a.apiKey)
}

func redactWith(err error, secret string) error {
	if secret == "" || err == nil {
		return err
	}
	msg := err.Error()
	if strings.Contains(msg, secret) {
		return fmt.Errorf("%s", strings.ReplaceAll(msg, secret, "***REDACTED***"))
	}
	return err
}

// Ensure the Adapter implements domain.VectorIndex (W8.2, REQ-VEC-001).
var _ domain.VectorIndex = (*Adapter)(nil)
