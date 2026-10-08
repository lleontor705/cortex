// Package fusion implements the REQ-RET-106 fusion A/B decision-gate harness.
//
// It compares the production position-only Reciprocal Rank Fusion (RRF, k=60 —
// the exact semantics mirrored from internal/retrieval/retrieval.go fuseInputs)
// against a calibrated convex combination of min-max normalized lexical and
// vector scores (arXiv 2210.11934) on a deterministic, fully offline synthetic
// corpus. The harness is READ-ONLY over retrieval inputs: it never mutates
// production fusion, never touches the network, and its recommendation field
// is advisory. REQ-RET-002 (score-as-rank) stays untouched here; any future
// production fusion flip requires an explicit decision-gate approval.
package fusion

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

	// DefaultRRFConstant mirrors the production RRF constant (k=60).
	DefaultRRFConstant = 60

	// DefaultTopK is the evaluation cutoff for recall/precision/nDCG.
	DefaultTopK = 10

	// DefaultSeed makes the corpus fully reproducible across runs.
	DefaultSeed = 0xF00D2026

	// DefaultTuningFraction of queries are reserved as the held-out split
	// used to calibrate the convex-combination weight. The remainder is the
	// evaluation split both strategies are scored on.
	DefaultTuningFraction = 0.5

	// DefaultAlphaGridStep is the calibration grid resolution in [0,1].
	DefaultAlphaGridStep = 0.05

	// RecommendationKeepRRF means the position-only production fusion stays.
	RecommendationKeepRRF = "keep-rrf-k60"

	// RecommendationConvex means the calibrated convex combination measured
	// strictly better on the evaluation split (advisory; still requires a
	// decision-gate approval before any production change).
	RecommendationConvex = "convex-combination"
)

// ContractNote is embedded in every report to make the REQ-RET-106 /
// REQ-RET-002 boundary explicit in the decision-gate artifact.
const ContractNote = "Advisory report only: production fusion remains position-only RRF k=60 " +
	"with the REQ-RET-002 score-as-rank contract intact. Adopting the convex combination " +
	"requires an explicit decision-gate approval and a reviewed contract change."

// Options tunes RRFFuse. Zero RRFConstant falls back to DefaultRRFConstant.
type Options struct {
	RRFConstant int
}

// Candidate is one retrieval candidate with its raw per-signal scores. The
// pre-ranked order of Lexical/Vector lists is what position-only RRF sees;
// the raw scores are what the convex combination sees.
type Candidate struct {
	ID           string
	LexicalScore float64
	VectorScore  float64
}

// QueryCase is one evaluation query: candidate lists plus graded relevance
// (0 = not relevant) for nDCG, and binary relevance derived for recall/MRR.
type QueryCase struct {
	ID       string
	Lexical  []Candidate
	Vector   []Candidate
	Relevant map[string]float64
}

// Config parameterizes the harness. Zero-value fields resolve to defaults.
type Config struct {
	CorpusSize     int
	QueryCount     int
	TopK           int
	Seed           int64
	TuningFraction float64
	AlphaGridStep  float64
}

func (c Config) normalized() Config {
	if c.CorpusSize <= 0 {
		c.CorpusSize = 200
	}
	if c.QueryCount <= 0 {
		c.QueryCount = 60
	}
	if c.TopK <= 0 {
		c.TopK = DefaultTopK
	}
	if c.Seed <= 0 {
		c.Seed = DefaultSeed
	}
	if c.TuningFraction <= 0 || c.TuningFraction >= 1 {
		c.TuningFraction = DefaultTuningFraction
	}
	if c.AlphaGridStep <= 0 || c.AlphaGridStep >= 1 {
		c.AlphaGridStep = DefaultAlphaGridStep
	}
	return c
}

// StrategyReport holds the per-strategy decision-gate metrics. Latency is the
// median wall-clock fusion time per query over the evaluation split; it is
// informational ONLY and is excluded from the recommendation decision so the
// recommendation field is stable across runs on identical inputs.
type StrategyReport struct {
	Name              string  `json:"name"`
	RecallAtK         float64 `json:"recall_at_k"`
	PrecisionAtK      float64 `json:"precision_at_k"`
	NDCGAtK           float64 `json:"ndcg_at_k"`
	MRR               float64 `json:"mrr"`
	LatencyNSPerQuery float64 `json:"latency_ns_per_query"`
}

// Report is the decision-gate artifact.
type Report struct {
	SchemaVersion   string           `json:"schema_version"`
	Config          Config           `json:"config"`
	TuningQueries   int              `json:"tuning_queries"`
	EvalQueries     int              `json:"eval_queries"`
	CalibratedAlpha float64          `json:"calibrated_alpha"`
	Strategies      []StrategyReport `json:"strategies"`
	Recommendation  string           `json:"recommendation"`
	Rationale       string           `json:"rationale"`
	ContractNote    string           `json:"contract_note"`
}

// RRFFuse mirrors the production position-only RRF: for each signal list,
// rank r (0-based) contributes weight/(k+r+1); credits accumulate per ID;
// results sort score DESC with ID DESC as the deterministic tie-break.
// Weight is fixed at 1.0 per signal to match the default production weights.
func RRFFuse(lexical, vector []Candidate, opts Options) []Candidate {
	k := opts.RRFConstant
	if k <= 0 {
		k = DefaultRRFConstant
	}

	type scored struct {
		cand  Candidate
		score float64
	}
	scoreMap := make(map[string]*scored, len(lexical)+len(vector))

	accumulate := func(list []Candidate) {
		for rank, c := range list {
			credit := 1.0 / (float64(k) + float64(rank+1))
			if existing, ok := scoreMap[c.ID]; ok {
				existing.score += credit
			} else {
				scoreMap[c.ID] = &scored{cand: c, score: credit}
			}
		}
	}
	accumulate(lexical)
	accumulate(vector)

	out := make([]Candidate, 0, len(scoreMap))
	for _, s := range scoreMap {
		out = append(out, s.cand)
	}
	sort.SliceStable(out, func(i, j int) bool {
		si, sj := scoreMap[out[i].ID].score, scoreMap[out[j].ID].score
		if si != sj {
			return si > sj
		}
		return out[i].ID > out[j].ID
	})
	return out
}

// normalizeMinMax min-max normalizes scores into [0,1]. Degenerate lists
// (empty, or all-equal scores) normalize every candidate to 0.5 so a single
// signal never silently drops out of the convex combination.
func normalizeMinMax(list []Candidate, score func(Candidate) float64) []float64 {
	out := make([]float64, len(list))
	if len(list) == 0 {
		return out
	}
	min, max := math.Inf(1), math.Inf(-1)
	for _, c := range list {
		v := score(c)
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	if max <= min {
		for i := range out {
			out[i] = 0.5
		}
		return out
	}
	span := max - min
	for i, c := range list {
		out[i] = (score(c) - min) / span
	}
	return out
}

// ConvexFuse computes alpha*normalizedLexical + (1-alpha)*normalizedVector for
// every candidate of the union, sorted score DESC with ID DESC tie-break.
// alpha=1 is pure lexical, alpha=0 is pure vector.
func ConvexFuse(lexical, vector []Candidate, alpha float64) []Candidate {
	type entry struct {
		cand  Candidate
		score float64
	}
	entries := make(map[string]*entry, len(lexical)+len(vector))

	lexNorm := normalizeMinMax(lexical, func(c Candidate) float64 { return c.LexicalScore })
	vecNorm := normalizeMinMax(vector, func(c Candidate) float64 { return c.VectorScore })

	for i, c := range lexical {
		entries[c.ID] = &entry{cand: c, score: alpha * lexNorm[i]}
	}
	for i, c := range vector {
		term := (1 - alpha) * vecNorm[i]
		if existing, ok := entries[c.ID]; ok {
			existing.score += term
		} else {
			entries[c.ID] = &entry{cand: c, score: term}
		}
	}

	out := make([]Candidate, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.cand)
	}
	sort.SliceStable(out, func(i, j int) bool {
		si, sj := entries[out[i].ID].score, entries[out[j].ID].score
		if si != sj {
			return si > sj
		}
		return out[i].ID > out[j].ID
	})
	return out
}

// GenerateCorpus deterministically builds a synthetic offline corpus with
// planted relevance and controlled lexical/vector disagreement so the A/B has
// signal: a third of the queries is lexical-strong, a third vector-strong,
// and a third balanced.
func GenerateCorpus(cfg Config) []QueryCase {
	cfg = cfg.normalized()
	rng := rand.New(rand.NewSource(cfg.Seed))

	queries := make([]QueryCase, 0, cfg.QueryCount)
	for q := 0; q < cfg.QueryCount; q++ {
		profile := q % 3 // 0 lexical-strong, 1 vector-strong, 2 balanced
		candidates := make([]Candidate, cfg.CorpusSize)
		for i := range candidates {
			candidates[i] = Candidate{ID: fmt.Sprintf("doc-%04d", i)}
		}

		// Planted relevant set: 6 docs per query with graded relevance.
		relevant := make(map[string]float64, 6)
		for g := 0; g < 6; g++ {
			id := candidates[rng.Intn(cfg.CorpusSize)].ID
			relevant[id] = 3 - float64(g%2) // grades 3 and 2
		}

		for i := range candidates {
			c := &candidates[i]
			lexBase, vecBase := 0.4*rng.Float64(), 0.4*rng.Float64()
			var lexBoost, vecBoost float64
			switch profile {
			case 0: // lexical signal carries relevance
				if relevant[c.ID] > 0 {
					lexBoost = 0.55
				}
			case 1: // vector signal carries relevance
				if relevant[c.ID] > 0 {
					vecBoost = 0.55
				}
			default: // balanced
				if relevant[c.ID] > 0 {
					lexBoost, vecBoost = 0.35, 0.35
				}
			}
			c.LexicalScore = clamp01(lexBase + lexBoost)
			c.VectorScore = clamp01(vecBase + vecBoost)
		}

		// Pre-ranked lists: sort by the respective raw score (deterministic
		// score DESC then ID DESC, matching the fusion tie-break).
		lexical := make([]Candidate, len(candidates))
		vector := make([]Candidate, len(candidates))
		copy(lexical, candidates)
		copy(vector, candidates)
		sort.SliceStable(lexical, func(i, j int) bool {
			if lexical[i].LexicalScore != lexical[j].LexicalScore {
				return lexical[i].LexicalScore > lexical[j].LexicalScore
			}
			return lexical[i].ID > lexical[j].ID
		})
		sort.SliceStable(vector, func(i, j int) bool {
			if vector[i].VectorScore != vector[j].VectorScore {
				return vector[i].VectorScore > vector[j].VectorScore
			}
			return vector[i].ID > vector[j].ID
		})

		queries = append(queries, QueryCase{
			ID:       fmt.Sprintf("query-%03d", q),
			Lexical:  lexical,
			Vector:   vector,
			Relevant: relevant,
		})
	}
	return queries
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// split holds out the leading TuningFraction of queries for alpha calibration
// and evaluates on the remaining tail. Deterministic and offline.
func split(queries []QueryCase, tuningFraction float64) (tuning, eval []QueryCase) {
	cut := int(float64(len(queries)) * tuningFraction)
	if cut < 1 {
		cut = 1
	}
	if cut >= len(queries) {
		cut = len(queries) - 1
	}
	if cut < 0 {
		return queries[:0], queries
	}
	return queries[:cut], queries[cut:]
}

// decisionEpsilon absorbs float noise so metric ties stay ties across runs.
const decisionEpsilon = 1e-9

// CalibrateAlpha grid-searches the convex weight maximizing mean nDCG@k on the
// tuning split. Ties resolve deterministically: prefer the alpha closest to
// 0.5 (least committed to one signal), then the lower alpha.
func CalibrateAlpha(tuning []QueryCase, topK int, gridStep float64) float64 {
	if gridStep <= 0 || gridStep >= 1 {
		gridStep = DefaultAlphaGridStep
	}
	n := int(1.0/gridStep + 0.5)
	if n < 1 {
		n = 20
	}
	bestScore, bestDist, bestAlpha := math.Inf(-1), math.Inf(1), 0.5
	for i := 0; i <= n; i++ {
		alpha := float64(i) / float64(n)
		score := meanNDCG(tuning, alpha, topK)
		dist := math.Abs(alpha - 0.5)
		improves := score > bestScore+decisionEpsilon
		tiesAndCloser := math.Abs(score-bestScore) <= decisionEpsilon &&
			(dist < bestDist-1e-12 ||
				(math.Abs(dist-bestDist) <= 1e-12 && alpha < bestAlpha))
		if improves || tiesAndCloser {
			bestScore, bestDist, bestAlpha = score, dist, alpha
		}
	}
	return math.Round(bestAlpha*100) / 100
}

func meanNDCG(queries []QueryCase, alpha float64, topK int) float64 {
	if len(queries) == 0 {
		return 0
	}
	sum := 0.0
	for _, q := range queries {
		fused := ConvexFuse(q.Lexical, q.Vector, alpha)
		sum += common.NDCGAtK(ids(fused), q.Relevant, topK)
	}
	return sum / float64(len(queries))
}

func ids(cands []Candidate) []string {
	out := make([]string, len(cands))
	for i, c := range cands {
		out[i] = c.ID
	}
	return out
}

// evaluate scores one strategy over the eval split and measures the median
// per-query fusion latency (informational only — excluded from the decision).
func evaluate(name string, queries []QueryCase, topK int, fuse func(QueryCase) []Candidate) StrategyReport {
	rep := StrategyReport{Name: name}
	if len(queries) == 0 {
		return rep
	}
	latencies := make([]float64, 0, len(queries))
	for _, q := range queries {
		start := time.Now()
		fused := fuse(q)
		latencies = append(latencies, float64(time.Since(start).Nanoseconds()))

		retrieved := ids(fused)
		binary := make([]string, 0, len(q.Relevant))
		relevantSet := make(map[string]struct{}, len(q.Relevant))
		for id, grade := range q.Relevant {
			if grade > 0 {
				binary = append(binary, id)
				relevantSet[id] = struct{}{}
			}
		}
		rep.RecallAtK += common.RecallAtK(retrieved, binary, topK)
		rep.NDCGAtK += common.NDCGAtK(retrieved, q.Relevant, topK)
		rep.MRR += common.MRR(retrieved, binary)

		limit := topK
		if limit > len(retrieved) {
			limit = len(retrieved)
		}
		hits := 0
		for _, id := range retrieved[:limit] {
			if _, ok := relevantSet[id]; ok {
				hits++
			}
		}
		rep.PrecisionAtK += float64(hits) / float64(topK)
	}
	n := float64(len(queries))
	rep.RecallAtK /= n
	rep.PrecisionAtK /= n
	rep.NDCGAtK /= n
	rep.MRR /= n
	sort.Float64s(latencies)
	rep.LatencyNSPerQuery = latencies[len(latencies)/2]
	return rep
}

// Run executes the full harness and returns the decision-gate report. The
// recommendation is a deterministic function of the eval-split metrics only
// (latency excluded), so repeated runs on the same inputs emit a stable
// recommendation.
func Run(cfg Config) (Report, error) {
	cfg = cfg.normalized()
	queries := GenerateCorpus(cfg)
	tuning, eval := split(queries, cfg.TuningFraction)
	if len(eval) == 0 {
		return Report{}, fmt.Errorf("fusion harness: empty evaluation split (queries=%d)", cfg.QueryCount)
	}

	alpha := CalibrateAlpha(tuning, cfg.TopK, cfg.AlphaGridStep)

	rrf := evaluate("rrf-k60", eval, cfg.TopK, func(q QueryCase) []Candidate {
		return RRFFuse(q.Lexical, q.Vector, Options{RRFConstant: DefaultRRFConstant})
	})
	convex := evaluate("convex-combination", eval, cfg.TopK, func(q QueryCase) []Candidate {
		return ConvexFuse(q.Lexical, q.Vector, alpha)
	})

	rep := Report{
		SchemaVersion:   SchemaVersion,
		Config:          cfg,
		TuningQueries:   len(tuning),
		EvalQueries:     len(eval),
		CalibratedAlpha: alpha,
		Strategies:      []StrategyReport{rrf, convex},
		ContractNote:    ContractNote,
	}
	rep.Recommendation, rep.Rationale = decide(rrf, convex, alpha)
	return rep, nil
}

// decide applies the deterministic dominance rule: the convex combination is
// only recommended when it strictly improves recall@k without degrading
// nDCG@k, or strictly improves nDCG@k without degrading recall@k. Latency is
// deliberately excluded so the recommendation is run-stable.
func decide(rrf, convex StrategyReport, alpha float64) (string, string) {
	recallUp := convex.RecallAtK > rrf.RecallAtK+decisionEpsilon
	ndcgUp := convex.NDCGAtK > rrf.NDCGAtK+decisionEpsilon
	recallNotDown := convex.RecallAtK >= rrf.RecallAtK-decisionEpsilon
	ndcgNotDown := convex.NDCGAtK >= rrf.NDCGAtK-decisionEpsilon

	if recallUp && ndcgNotDown {
		return RecommendationConvex, fmt.Sprintf(
			"convex combination (alpha=%.2f) improves recall@k by %.4f with nDCG@k not worse (%.4f vs %.4f) on the eval split; decision-gate approval still required before any production flip",
			alpha, convex.RecallAtK-rrf.RecallAtK, convex.NDCGAtK, rrf.NDCGAtK)
	}
	if ndcgUp && recallNotDown {
		return RecommendationConvex, fmt.Sprintf(
			"convex combination (alpha=%.2f) improves nDCG@k by %.4f with recall@k not worse (%.4f vs %.4f) on the eval split; decision-gate approval still required before any production flip",
			alpha, convex.NDCGAtK-rrf.NDCGAtK, convex.RecallAtK, rrf.RecallAtK)
	}
	return RecommendationKeepRRF, fmt.Sprintf(
		"position-only RRF k=60 holds: recall@k %.4f vs %.4f, nDCG@k %.4f vs %.4f (convex alpha=%.2f); no production change",
		rrf.RecallAtK, convex.RecallAtK, rrf.NDCGAtK, convex.NDCGAtK, alpha)
}
