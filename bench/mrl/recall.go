// Package mrl implements the REQ-RET-103 MRL recall decision-gate spike.
//
// It measures recall@10 of halfvec(2048) subvector search over MRL-truncated
// 4096-dim embeddings (prefix truncation, arXiv 2205.13147 / 2506.05176)
// against the exact 4096-dim float32 cosine scan that pgvector '<=>' performs
// in production today. Three strategies run on a deterministic, fully offline
// synthetic corpus:
//
//	baseline: exact 4096-dim float32 cosine (the current production exact scan)
//	rerank:   halfvec(2048) subvector cosine top-K shortlist (K=50) followed by
//	          full-precision 4096-dim cosine re-rank (the designed production
//	          ANN pattern of design.md D3 / REQ-RET-103)
//	direct:   halfvec(2048) subvector cosine with no re-rank (diagnostic lower
//	          bound; never a production proposal)
//	control:  full 4096-dim binary16-quantized cosine (quantization-only
//	          control isolating halfvec storage noise from truncation loss)
//
// The harness replicates pgvector distance math in pure Go: '<=>' cosine on
// float4 operands for the baseline, and IEEE 754 binary16-quantized operands
// with wide accumulation for the halfvec paths (halfvec stores half precision
// and accumulates in float). The harness is READ-ONLY over production code: no
// production files are imported or mutated, no network, no model, no database.
// The Verdict field is a deterministic function of the recall measurements and
// the documented latency projection. The spike gates ret-202..205 planning; a
// NO-GO routes to ret-206 (RaBitQ / binary_quantize fallback decision record,
// REQ-RET-104).
package mrl

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
	"time"

	"github.com/lleontor705/cortex/v2/bench/common"
)

const (
	// SchemaVersion identifies the report contract of this harness.
	SchemaVersion = "1"

	// DefaultCorpusSize is the synthetic corpus size. The production store
	// currently holds 28 rows (exact scan trivially optimal there); the ANN
	// chain targets future scale, hence a production-like corpus.
	DefaultCorpusSize = 2000

	// DefaultRandomQueries is the number of random evaluation queries.
	DefaultRandomQueries = 100

	// DefaultHardQueries is the number of hard queries, each anchored on a
	// planted near-duplicate distractor cluster (tight-ranking stress).
	DefaultHardQueries = 50

	// DefaultDistractorsPerAnchor is the number of near-duplicate distractor
	// documents planted per hard-query anchor.
	DefaultDistractorsPerAnchor = 8

	// DefaultTopK is the evaluation cutoff k for recall (recall@10).
	DefaultTopK = 10

	// DefaultShortlistK is the ANN shortlist size fetched by the subvector
	// strategy before full-precision re-rank (the designed production K).
	DefaultShortlistK = 50

	// DefaultSeed makes the corpus fully reproducible across runs.
	DefaultSeed = int64(0x4D524C26)

	// DefaultDims is the full embedding dimension (qwen3-embedding 4096).
	DefaultDims = 4096

	// DefaultLatencyPasses is how many timed passes run over the query set.
	// Strategy latency is informational; the latency GO criterion uses the
	// documented production-scale projection, not harness wall-clock.
	DefaultLatencyPasses = 3

	// GoRecallThreshold is the GO criterion: recall@10 of the subvector+re-rank
	// strategy relative to the exact baseline must reach 0.95.
	GoRecallThreshold = 0.95

	// GoHardRecallFloor strengthens the GO criterion for the stress regime:
	// recall@10 on the near-duplicate distractor query set may not fall
	// below 0.90 (the combined gate alone could hide a stress collapse).
	GoHardRecallFloor = 0.90

	// GoLatencySpeedup is the GO criterion for the projected production
	// latency improvement of the ANN path vs the exact scan (>= 3x).
	GoLatencySpeedup = 3.0

	// ProjectionCorpusRows is the production-scale row count used for the
	// latency projection (exact scan is O(N); HNSW is sublinear).
	ProjectionCorpusRows = 200000

	// ProjectionEFSearch mirrors the planned hnsw.ef_search session setting.
	ProjectionEFSearch = 100

	// ProjectionGraphHopFactor approximates HNSW greedy traversal cost: each
	// of the ef_search visited candidates evaluates a small constant number of
	// distances during greedy descent. Documented approximation, not a graph
	// simulation; the margin against GoLatencySpeedup is orders of magnitude.
	ProjectionGraphHopFactor = 4.0

	// SuffixMirrorWeight and SuffixNoiseWeight shape the synthetic MRL-like
	// suffix: suffix = 0.6*mirror(prefix) + 0.8*independent noise. The suffix
	// therefore carries substantial full-precision signal the truncated prefix
	// view never sees — deliberately pessimistic vs real MRL embeddings.
	SuffixMirrorWeight = 0.6
	SuffixNoiseWeight  = 0.8

	// HardQueryNoise and distractor noise radii define the near-duplicate
	// stress geometry: the query sits at radius 0.05 from its anchor while
	// distractors sit at 0.02..0.048, bracketing the anchor tighter than the
	// query itself so the top-10 is a dense, contested neighborhood.
	HardQueryNoise     = 0.05
	HardDistractorBase = 0.02
	HardDistractorStep = 0.004

	// VerdictGO unblocks ret-202..205 planning (decision-gate review consumes the report).
	VerdictGO = "GO"

	// VerdictNoGo routes planning to ret-206 (RaBitQ fallback, REQ-RET-104).
	VerdictNoGo = "NO-GO"
)

// SyntheticDataCaveat is embedded in every report. The corpus is fully
// synthetic: the production pgvector store is not reachable from this offline
// harness, so no real embeddings are measured.
const SyntheticDataCaveat = "Fully synthetic corpus (deterministic seed; no production embeddings — " +
	"the production pgvector store is not accessible from this offline harness). The generator builds a " +
	"2048-dim unit prefix plus a suffix blended from the mirrored prefix and independent noise, so the " +
	"2048-dim subvector carries strong but deliberately INCOMPLETE signal: the full-precision ground truth " +
	"includes half of the signal the truncated view never sees. This is pessimistic vs real qwen3-embedding " +
	"MRL prefix truncation (arXiv 2205.13147, 2506.05176), where the prefix is explicitly trained to be " +
	"self-sufficient, so measured recall is a lower bound. A GO verdict must still be re-validated by " +
	"ret-205 against real stored embeddings before production reliance."

// DDLOutlook documents (informationally) the DDL that ret-203 would ship as a
// NEW migration version. This spike does NOT write migration files; shipped
// migration bytes and checksum pins are immutable (plan NG-2).
const DDLOutlook = "-- Migration 114 (ret-203 scope; NEW migration version executed through\n" +
	"-- PostgresServerMigration.Preflight/VerifyApplied — never an edit of shipped bytes):\n" +
	"CREATE INDEX CONCURRENTLY idx_embeddings_ann_mrl2048\n" +
	"  ON cortex_vector.embeddings\n" +
	"  USING hnsw ((subvector(embedding, 1, 2048)::halfvec(2048)) halfvec_cosine_ops)\n" +
	"  WITH (m = 16, ef_construction = 64);\n" +
	"-- Search session setup: SET LOCAL hnsw.ef_search = 100;\n" +
	"-- Operational implications:\n" +
	"--   * build time: trivial at current production scale (28 rows); CONCURRENTLY required on large corpora\n" +
	"--   * index storage: ~2048 dims x 2 bytes/row for the halfvec key plus HNSW graph links (m=16 => ~32-64 bytes/row)\n" +
	"--   * write amplification: every embedding upsert maintains the HNSW graph\n" +
	"--   * rollback: DROP INDEX CONCURRENTLY idx_embeddings_ann_mrl2048; dimension env back to 4096\n" +
	"--   * caller-visible scores remain full-precision 1 - cosine_distance after re-rank (REQ-RET-103)"

// ContractNote makes the decision-gate boundary explicit in the artifact.
const ContractNote = "Advisory decision-gate report (REQ-RET-103): this spike never mutates production " +
	"code paths. GO unblocks ret-202..205 planning; NO-GO opens the ret-206 RaBitQ/binary_quantize " +
	"fallback decision record (REQ-RET-104). Either way an explicit planning review consumes this report."

// Config parameterizes the harness. Zero-value fields resolve to defaults,
// except that a config with no queries at all (both counts zero) is rejected
// by Run as degenerate rather than silently defaulting.
type Config struct {
	CorpusSize           int   `json:"corpus_size"`
	RandomQueries        int   `json:"random_queries"`
	HardQueries          int   `json:"hard_queries"`
	DistractorsPerAnchor int   `json:"distractors_per_anchor"`
	TopK                 int   `json:"top_k"`
	ShortlistK           int   `json:"shortlist_k"`
	Seed                 int64 `json:"seed"`
	LatencyPasses        int   `json:"latency_passes"`
}

func (c Config) normalized() Config {
	if c.CorpusSize <= 0 {
		c.CorpusSize = DefaultCorpusSize
	}
	if c.RandomQueries <= 0 {
		c.RandomQueries = DefaultRandomQueries
	}
	if c.HardQueries <= 0 {
		c.HardQueries = DefaultHardQueries
	}
	if c.DistractorsPerAnchor <= 0 {
		c.DistractorsPerAnchor = DefaultDistractorsPerAnchor
	}
	if c.TopK <= 0 {
		c.TopK = DefaultTopK
	}
	if c.ShortlistK <= 0 {
		c.ShortlistK = DefaultShortlistK
	}
	if c.Seed <= 0 {
		c.Seed = DefaultSeed
	}
	if c.LatencyPasses <= 0 {
		c.LatencyPasses = DefaultLatencyPasses
	}
	if min := c.HardQueries * (1 + c.DistractorsPerAnchor); c.CorpusSize < min {
		c.CorpusSize = min
	}
	if c.ShortlistK > c.CorpusSize {
		c.ShortlistK = c.CorpusSize
	}
	if c.TopK > c.CorpusSize {
		c.TopK = c.CorpusSize
	}
	return c
}

// StrategyReport holds the per-strategy gate metrics. Latency is the median
// wall-clock time per query over all timed passes; it is informational for the
// strategies themselves — the latency GO criterion uses the documented
// production-scale projection, not harness wall-clock (a linear Go scan cannot
// reproduce HNSW sublinearity).
type StrategyReport struct {
	Name                 string  `json:"name"`
	RecallAt10Random     float64 `json:"recall_at_10_random"`
	RecallAt10Hard       float64 `json:"recall_at_10_hard"`
	RecallAt10Combined   float64 `json:"recall_at_10_combined"`
	P50LatencyNSPerQuery float64 `json:"p50_latency_ns_per_query"`
}

// LatencyProjection documents the production-scale cost model anchored on the
// measured per-distance costs. It is an analytic projection, not a simulation
// of HNSW graph dynamics; the hop factor is a documented approximation.
type LatencyProjection struct {
	CorpusRows          int     `json:"corpus_rows"`
	EFSearch            int     `json:"ef_search"`
	GraphHopFactor      float64 `json:"graph_hop_factor"`
	ShortlistK          int     `json:"shortlist_k"`
	TExactDistanceNS    float64 `json:"t_exact_distance_ns"`
	THalfDistanceNS     float64 `json:"t_half_distance_ns"`
	ProjectedExactNS    float64 `json:"projected_exact_scan_ns"`
	ProjectedANNNS      float64 `json:"projected_ann_rerank_ns"`
	Speedup             float64 `json:"projected_speedup"`
	MeasuredScanSpeedup float64 `json:"measured_harness_scan_speedup"`
}

// Report is the REQ-RET-103 decision-gate artifact.
type Report struct {
	SchemaVersion       string            `json:"schema_version"`
	Config              Config            `json:"config"`
	Baseline            StrategyReport    `json:"baseline_exact_4096_f32"`
	SubvectorRerank     StrategyReport    `json:"subvector_2048_rerank"`
	SubvectorDirect     StrategyReport    `json:"subvector_2048_direct"`
	FullFP16Control     StrategyReport    `json:"full_4096_fp16_control"`
	Projection          LatencyProjection `json:"latency_projection"`
	RecallGatePass      bool              `json:"recall_gate_pass"`
	LatencyGatePass     bool              `json:"latency_gate_pass"`
	Verdict             string            `json:"verdict"`
	Rationale           string            `json:"rationale"`
	SyntheticDataCaveat string            `json:"synthetic_data_caveat"`
	DDLOutlook          string            `json:"ddl_outlook_migration_114"`
	ContractNote        string            `json:"contract_note"`
}

// Float16bits rounds a float32 to IEEE 754 binary16 bits with
// round-to-nearest-even, mirroring how pgvector halfvec stores operands.
func Float16bits(f float32) uint16 {
	bits := math.Float32bits(f)
	sign := uint16((bits >> 16) & 0x8000)
	exp32 := int32((bits>>23)&0xFF) - 127
	mant := bits & 0x007FFFFF

	switch {
	case (bits>>23)&0xFF == 0xFF: // Inf / NaN
		if mant != 0 {
			return sign | 0x7E00
		}
		return sign | 0x7C00
	case exp32 > 15: // overflow to half infinity
		return sign | 0x7C00
	case exp32 >= -14: // half normal range
		exp := uint32(exp32+15) << 10
		rounded := mant + 0x00000FFF + ((mant >> 13) & 1)
		if rounded&0x00800000 != 0 { // mantissa rounded up into the exponent
			rounded = 0
			exp += 1 << 10
			if exp&0x7C00 == 0x7C00 { // exponent overflowed to infinity
				return sign | 0x7C00
			}
		}
		return sign | uint16(exp) | uint16(rounded>>13)
	case exp32 < -25: // underflows the half subnormal range to (signed) zero
		return sign
	default: // half subnormal range, round to nearest even
		full := mant | 0x00800000
		shift := uint32(-exp32 - 1)
		shifted := full >> shift
		rem := full & ((1 << shift) - 1)
		halfRem := uint32(1) << (shift - 1)
		if rem > halfRem || (rem == halfRem && shifted&1 == 1) {
			shifted++
		}
		if shifted > 0x3FF { // rounded up into the smallest half normal
			return sign | 1<<10
		}
		return sign | uint16(shifted)
	}
}

// HalfFloat converts IEEE 754 binary16 bits back to float32 exactly.
func HalfFloat(h uint16) float32 {
	exp := (h >> 10) & 0x1F
	mant := uint32(h & 0x03FF)
	var v float32
	switch exp {
	case 0:
		v = float32(math.Ldexp(float64(mant), -24))
	case 0x1F:
		if mant == 0 {
			v = float32(math.Inf(1))
		} else {
			v = float32(math.NaN())
		}
	default:
		v = float32(math.Ldexp(float64(mant|0x0400), int(exp)-25))
	}
	if h&0x8000 != 0 {
		return -v
	}
	return v
}

// quantizeHalf mirrors halfvec storage: every operand is rounded to binary16
// and read back, so distance math sees exactly the stored half values.
func quantizeHalf(v []float32) []float32 {
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = HalfFloat(Float16bits(x))
	}
	return out
}

// cosineDistance mirrors pgvector '<=>': 1 - dot/(|a||b|) with wide
// accumulation. The halfvec strategies pass binary16-roundtripped operands.
func cosineDistance(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		x, y := float64(a[i]), float64(b[i])
		dot += x * y
		na += x * x
		nb += y * y
	}
	if na == 0 || nb == 0 {
		return 1
	}
	return 1 - dot/math.Sqrt(na*nb)
}

// topK maintains the k smallest-distance candidates in ascending order.
type topK struct {
	k     int
	items []scoredIdx
}

type scoredIdx struct {
	idx  int
	dist float64
}

func newTopK(k int) *topK { return &topK{k: k} }

func (t *topK) add(idx int, dist float64) {
	if len(t.items) < t.k {
		pos := sort.Search(len(t.items), func(i int) bool { return t.items[i].dist > dist })
		t.items = append(t.items, scoredIdx{})
		copy(t.items[pos+1:], t.items[pos:])
		t.items[pos] = scoredIdx{idx: idx, dist: dist}
		return
	}
	if dist >= t.items[t.k-1].dist {
		return
	}
	pos := sort.Search(t.k, func(i int) bool { return t.items[i].dist > dist })
	copy(t.items[pos+1:], t.items[pos:])
	t.items[pos] = scoredIdx{idx: idx, dist: dist}
}

func (t *topK) indices() []int {
	out := make([]int, len(t.items))
	for i, it := range t.items {
		out[i] = it.idx
	}
	return out
}

type queryCase struct {
	id       string
	vec      []float32
	hard     bool
	relevant []string // baseline exact-scan top-k IDs (ground truth for the gate)
}

// harness holds the precomputed corpus representations: float4 full vectors
// (production exact-scan representation), binary16-roundtripped 2048-dim
// subvectors (halfvec index representation), and binary16-roundtripped full
// 4096-dim vectors (quantization-only control).
type harness struct {
	cfg            Config
	corpusF32      [][]float32
	corpusHalfSub  [][]float32
	corpusFullHalf [][]float32
	queries        []queryCase
}

func docID(i int) string { return fmt.Sprintf("doc-%05d", i) }

func randn(rng *rand.Rand, n int) []float32 {
	v := make([]float32, n)
	for i := range v {
		v[i] = float32(rng.NormFloat64())
	}
	return v
}

func normalizeInPlace(v []float32) {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	norm := math.Sqrt(sum)
	if norm == 0 {
		return
	}
	inv := float32(1 / norm)
	for i := range v {
		v[i] *= inv
	}
}

// generateMRLVector builds one synthetic MRL-like 4096-dim vector: a 2048-dim
// unit prefix carrying primary signal plus a suffix blended from the mirrored
// prefix and independent noise. Both halves are unit norm so the concatenation
// is unit norm. See SyntheticDataCaveat for the honesty contract.
func generateMRLVector(rng *rand.Rand, dims int) []float32 {
	half := dims / 2
	prefix := randn(rng, half)
	suffix := randn(rng, half)
	for i := range suffix {
		suffix[i] = SuffixMirrorWeight*prefix[half-1-i] + SuffixNoiseWeight*suffix[i]
	}
	normalizeInPlace(prefix)
	normalizeInPlace(suffix)
	v := make([]float32, dims)
	copy(v, prefix)
	copy(v[half:], suffix)
	normalizeInPlace(v) // concat of two unit halves has norm sqrt(2); renormalize
	return v
}

// newHarness generates the deterministic corpus. Layout: hard-query anchors
// occupy doc slots [0, HardQueries), random documents fill the middle, and the
// trailing region holds one near-duplicate distractor cluster per anchor
// (anchor + DistractorsPerAnchor perturbed copies, radii 0.02..0.048).
func newHarness(cfg Config) *harness {
	cfg = cfg.normalized()
	rng := rand.New(rand.NewSource(cfg.Seed))
	half := DefaultDims / 2
	h := &harness{cfg: cfg}

	h.corpusF32 = make([][]float32, cfg.CorpusSize)
	for i := 0; i < cfg.CorpusSize; i++ {
		h.corpusF32[i] = generateMRLVector(rng, DefaultDims)
	}

	// Near-duplicate distractor clusters in the trailing region.
	regionStart := cfg.CorpusSize - cfg.HardQueries*(1+cfg.DistractorsPerAnchor)
	for anchor := 0; anchor < cfg.HardQueries; anchor++ {
		base := h.corpusF32[anchor]
		for d := 0; d < cfg.DistractorsPerAnchor; d++ {
			radius := HardDistractorBase + HardDistractorStep*float64(d)
			noisy := make([]float32, DefaultDims)
			pert := randn(rng, DefaultDims)
			for i := range noisy {
				noisy[i] = base[i] + float32(radius)*pert[i]
			}
			normalizeInPlace(noisy)
			h.corpusF32[regionStart+anchor*(1+cfg.DistractorsPerAnchor)+d] = noisy
		}
	}

	// halfvec index representation: binary16-roundtripped 2048-dim subvector.
	h.corpusHalfSub = make([][]float32, cfg.CorpusSize)
	for i, v := range h.corpusF32 {
		h.corpusHalfSub[i] = quantizeHalf(v[:half])
	}

	// quantization-only control: binary16-roundtripped full 4096-dim vector.
	h.corpusFullHalf = make([][]float32, cfg.CorpusSize)
	for i, v := range h.corpusF32 {
		h.corpusFullHalf[i] = quantizeHalf(v)
	}

	// Queries: random draws, then hard anchors perturbed at radius 0.05.
	for q := 0; q < cfg.RandomQueries; q++ {
		h.queries = append(h.queries, queryCase{
			id:   fmt.Sprintf("q-random-%03d", q),
			vec:  generateMRLVector(rng, DefaultDims),
			hard: false,
		})
	}
	for anchor := 0; anchor < cfg.HardQueries; anchor++ {
		noisy := make([]float32, DefaultDims)
		pert := randn(rng, DefaultDims)
		base := h.corpusF32[anchor]
		for i := range noisy {
			noisy[i] = base[i] + HardQueryNoise*pert[i]
		}
		normalizeInPlace(noisy)
		h.queries = append(h.queries, queryCase{
			id:   fmt.Sprintf("q-hard-%03d", anchor),
			vec:  noisy,
			hard: true,
		})
	}
	return h
}

// searchExact is strategy (a): full-precision float32 4096-dim cosine over the
// whole corpus — the current production exact scan ('<=>' on float4 vectors).
func (h *harness) searchExact(q []float32, k int) []int {
	t := newTopK(k)
	for i, doc := range h.corpusF32 {
		t.add(i, cosineDistance(q, doc))
	}
	return t.indices()
}

// searchSubvectorHalf is the halfvec shortlist fetch: binary16-quantized
// 2048-dim prefix cosine (subvector(v,1,2048)::halfvec(2048) '<=>'), the
// representation the planned HNSW expression index would hold.
func (h *harness) searchSubvectorHalf(qSub []float32, k int) []int {
	t := newTopK(k)
	for i, doc := range h.corpusHalfSub {
		t.add(i, cosineDistance(qSub, doc))
	}
	return t.indices()
}

// Strategy functions. Each returns retrieved IDs (ascending distance); the
// latency measurement wraps the full strategy call identically for all three.
func (h *harness) runBaseline(q queryCase) []string {
	ids := h.searchExact(q.vec, h.cfg.TopK)
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = docID(id)
	}
	return out
}

func (h *harness) runRerank(q queryCase) []string {
	half := DefaultDims / 2
	qSub := quantizeHalf(q.vec[:half])
	shortlist := h.searchSubvectorHalf(qSub, h.cfg.ShortlistK)
	t := newTopK(h.cfg.TopK)
	for _, idx := range shortlist {
		t.add(idx, cosineDistance(q.vec, h.corpusF32[idx]))
	}
	ids := t.indices()
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = docID(id)
	}
	return out
}

func (h *harness) runDirect(q queryCase) []string {
	half := DefaultDims / 2
	qSub := quantizeHalf(q.vec[:half])
	ids := h.searchSubvectorHalf(qSub, h.cfg.TopK)
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = docID(id)
	}
	return out
}

// runFullFP16Control is the quantization-only control: full 4096-dim cosine
// over binary16-roundtripped operands. Any recall it loses vs the float32
// baseline is halfvec storage noise, NOT MRL truncation — the gap between
// this control and the subvector strategies is the truncation signal.
func (h *harness) runFullFP16Control(q queryCase) []string {
	qHalf := quantizeHalf(q.vec)
	t := newTopK(h.cfg.TopK)
	for i, doc := range h.corpusFullHalf {
		t.add(i, cosineDistance(qHalf, doc))
	}
	ids := t.indices()
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = docID(id)
	}
	return out
}

// evaluate runs one strategy over all timed passes, collects per-query latency
// samples, and scores recall@k per query set against the baseline relevant
// sets. Every strategy (including the baseline itself, which scores 1.0 by
// construction) is measured identically.
func (h *harness) evaluate(name string, strategy func(queryCase) []string) StrategyReport {
	cfg := h.cfg
	rep := StrategyReport{Name: name}

	randomRecall, hardRecall := 0.0, 0.0
	var randomN, hardN int
	latencies := make([]float64, 0, len(h.queries)*cfg.LatencyPasses)

	for pass := 0; pass < cfg.LatencyPasses; pass++ {
		for _, q := range h.queries {
			start := time.Now()
			retrieved := strategy(q)
			latencies = append(latencies, float64(time.Since(start).Nanoseconds()))
			recall := common.RecallAtK(retrieved, q.relevant, cfg.TopK)
			if q.hard {
				hardRecall += recall
				hardN++
			} else {
				randomRecall += recall
				randomN++
			}
		}
	}
	rep.RecallAt10Hard = hardRecall / float64(max(hardN, 1))
	rep.RecallAt10Random = randomRecall / float64(max(randomN, 1))
	rep.RecallAt10Combined = (hardRecall + randomRecall) / float64(max(hardN+randomN, 1))
	sort.Float64s(latencies)
	if len(latencies) > 0 {
		rep.P50LatencyNSPerQuery = latencies[len(latencies)/2]
	}
	return rep
}

// Run executes the full harness and returns the decision-gate report. Recall
// fields and Verdict are deterministic functions of the seed; latency and the
// projection vary within noise but the latency gate margin is orders of
// magnitude, so the Verdict is run-stable.
func Run(cfg Config) (Report, error) {
	// Query counts are validated before normalization: zero defaults apply to
	// individual unset fields, but a config with NO queries is degenerate.
	if cfg.RandomQueries <= 0 && cfg.HardQueries <= 0 {
		return Report{}, fmt.Errorf("mrl harness: empty query set (random=%d hard=%d)", cfg.RandomQueries, cfg.HardQueries)
	}
	cfg = cfg.normalized()
	if cfg.TopK > cfg.ShortlistK {
		return Report{}, fmt.Errorf("mrl harness: top-k %d exceeds shortlist %d; re-rank could never recover the cutoff", cfg.TopK, cfg.ShortlistK)
	}

	h := newHarness(cfg)

	// Ground truth: exact-scan top-k per query (the production baseline result
	// set every strategy is measured against).
	for i := range h.queries {
		ids := h.searchExact(h.queries[i].vec, cfg.TopK)
		relevant := make([]string, 0, len(ids))
		for _, id := range ids {
			relevant = append(relevant, docID(id))
		}
		h.queries[i].relevant = relevant
	}

	// All three strategies are then measured identically against that ground
	// truth; the baseline scores 1.0 by construction and the subvector
	// strategies report their relative recall. The quantization-only control
	// separates halfvec storage noise from truncation loss.
	baseline := h.evaluate("baseline-exact-4096-f32", h.runBaseline)
	rerank := h.evaluate("subvector-2048-rerank", h.runRerank)
	direct := h.evaluate("subvector-2048-direct", h.runDirect)
	control := h.evaluate("full-4096-fp16-control", h.runFullFP16Control)

	rep := Report{
		SchemaVersion:       SchemaVersion,
		Config:              cfg,
		Baseline:            baseline,
		SubvectorRerank:     rerank,
		SubvectorDirect:     direct,
		FullFP16Control:     control,
		SyntheticDataCaveat: SyntheticDataCaveat,
		DDLOutlook:          DDLOutlook,
		ContractNote:        ContractNote,
	}

	// Production-scale latency projection anchored on measured costs:
	//   exact scan at N rows        ~= N * tFull
	//   ANN shortlist + re-rank     ~= ef_search * hopFactor * tHalf + K * tFull
	// where tFull/tHalf are the measured per-element scan costs (strategy
	// p50 divided by corpus size). The hop factor is a documented HNSW
	// approximation; the re-rank is K full-precision distances.
	tFull := baseline.P50LatencyNSPerQuery / float64(cfg.CorpusSize)
	tHalf := direct.P50LatencyNSPerQuery / float64(cfg.CorpusSize)
	projExact := float64(ProjectionCorpusRows) * tFull
	projANN := ProjectionEFSearch*ProjectionGraphHopFactor*tHalf + float64(cfg.ShortlistK)*tFull
	speedup := 0.0
	if projANN > 0 {
		speedup = projExact / projANN
	}
	rep.Projection = LatencyProjection{
		CorpusRows:          ProjectionCorpusRows,
		EFSearch:            ProjectionEFSearch,
		GraphHopFactor:      ProjectionGraphHopFactor,
		ShortlistK:          cfg.ShortlistK,
		TExactDistanceNS:    tFull,
		THalfDistanceNS:     tHalf,
		ProjectedExactNS:    projExact,
		ProjectedANNNS:      projANN,
		Speedup:             speedup,
		MeasuredScanSpeedup: measuredSpeedup(baseline, rerank),
	}

	rep.RecallGatePass, rep.LatencyGatePass, rep.Verdict, rep.Rationale = decide(rep.Baseline, rep.SubvectorRerank, rep.SubvectorDirect, rep.FullFP16Control, rep.Projection)
	return rep, nil
}

// measuredSpeedup is the informational harness-scale ratio (a half-dimension
// linear scan cannot exceed ~2x; HNSW sublinearity is captured by the
// projection instead).
func measuredSpeedup(baseline, rerank StrategyReport) float64 {
	if rerank.P50LatencyNSPerQuery <= 0 {
		return 0
	}
	return baseline.P50LatencyNSPerQuery / rerank.P50LatencyNSPerQuery
}

// decide applies the GO criteria:
//  1. recall gate: combined recall@10 of subvector+re-rank >= 0.95 relative to
//     the exact baseline AND >= 0.90 on the hard near-duplicate stress set;
//  2. latency gate: projected production speedup >= 3x.
func decide(baseline, rerank, direct, control StrategyReport, proj LatencyProjection) (recallPass, latencyPass bool, verdict, rationale string) {
	recallPass = rerank.RecallAt10Combined >= GoRecallThreshold && rerank.RecallAt10Hard >= GoHardRecallFloor
	latencyPass = proj.Speedup >= GoLatencySpeedup

	switch {
	case recallPass && latencyPass:
		verdict = VerdictGO
		rationale = fmt.Sprintf(
			"GO: subvector+re-rank recall@10 %.4f (random %.4f, hard %.4f) vs exact baseline, meeting the >= %.2f combined and >= %.2f hard gates; direct no-re-rank lower bound %.4f; quantization-only control %.4f; projected production speedup %.0fx at %d rows (ef_search=%d, hop factor %.0f, K=%d), meeting the >= %.0fx latency gate. Measured harness scan speedup %.2fx is bounded by the half-dimension linear scan; HNSW sublinearity is captured by the projection. Unblocks ret-202..205 planning.",
			rerank.RecallAt10Combined, rerank.RecallAt10Random, rerank.RecallAt10Hard,
			GoRecallThreshold, GoHardRecallFloor, direct.RecallAt10Combined, control.RecallAt10Combined,
			proj.Speedup, proj.CorpusRows, proj.EFSearch, proj.GraphHopFactor, proj.ShortlistK,
			GoLatencySpeedup, proj.MeasuredScanSpeedup)
	case recallPass && !latencyPass:
		verdict = VerdictNoGo
		rationale = fmt.Sprintf(
			"NO-GO (latency): recall@10 %.4f passes the recall gate but projected speedup %.2fx is below %.0fx at %d rows; the ANN chain does not pay for itself at the projected operating point. Route to ret-206 (RaBitQ fallback, REQ-RET-104).",
			rerank.RecallAt10Combined, proj.Speedup, GoLatencySpeedup, proj.CorpusRows)
	case !recallPass && latencyPass:
		verdict = VerdictNoGo
		rationale = fmt.Sprintf(
			"NO-GO (recall): subvector+re-rank recall@10 %.4f combined / %.4f hard vs the >= %.2f combined and >= %.2f hard gates (direct no-re-rank lower bound %.4f; quantization-only control %.4f — the gap control-vs-rerank is truncation loss, the gap baseline-vs-control is halfvec storage noise). MRL 2048 truncation loses too much ranking signal on this corpus; route to ret-206 (RaBitQ fallback, REQ-RET-104).",
			rerank.RecallAt10Combined, rerank.RecallAt10Hard, GoRecallThreshold, GoHardRecallFloor, direct.RecallAt10Combined, control.RecallAt10Combined)
	default:
		verdict = VerdictNoGo
		rationale = fmt.Sprintf(
			"NO-GO (both gates): subvector+re-rank recall@10 %.4f combined / %.4f hard fails the recall gate and projected speedup %.2fx fails the >= %.0fx latency gate. Route to ret-206 (RaBitQ fallback, REQ-RET-104).",
			rerank.RecallAt10Combined, rerank.RecallAt10Hard, proj.Speedup, GoLatencySpeedup)
	}
	return recallPass, latencyPass, verdict, rationale
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
