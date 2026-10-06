// Package locomo implements the LOCOMO benchmark runner for Cortex.
//
// LOCOMO evaluates memory systems on 1,986 questions across 10 long-term
// conversations with 5 category types (1-5).
//
// Dataset: https://github.com/snap-research/locomo
package locomo

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/lleontor705/cortex/v2/bench/common"
	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/embedding" //nolint:all
	"github.com/lleontor705/cortex/v2/internal/retrieval"
)

// categoryNames maps LOCOMO numeric categories to names.
var categoryNames = map[int]string{
	1: "single-hop",
	2: "multi-hop",
	3: "temporal",
	4: "open-domain",
	5: "adversarial",
}

// Conversation represents a LOCOMO conversation sample.
type Conversation struct {
	SampleID     string           `json:"sample_id"`
	Conversation ConversationData `json:"conversation"`
	QA           []QA             `json:"qa"`
	Observation  json.RawMessage  `json:"observation"` // Complex nested structure, parsed manually
}

// ConversationData holds the dialogue sessions and metadata.
type ConversationData struct {
	SpeakerA string            `json:"speaker_a"`
	SpeakerB string            `json:"speaker_b"`
	Sessions map[string][]Turn // Parsed from session_N keys
	Dates    map[string]string // Parsed from session_N_date_time keys
}

// Turn represents a single dialogue turn.
type Turn struct {
	Speaker string `json:"speaker"`
	DiaID   string `json:"dia_id"`
	Text    string `json:"text"`
}

// QA represents a question-answer pair.
type QA struct {
	Question string          `json:"question"`
	Answer   json.RawMessage `json:"answer"` // Can be string or number
	Evidence []string        `json:"evidence"`
	Category int             `json:"category"`
}

// AnswerString returns the answer as a string regardless of JSON type.
func (qa QA) AnswerString() string {
	var s string
	if err := json.Unmarshal(qa.Answer, &s); err == nil {
		return s
	}
	// Fallback: treat as raw value (number, bool, etc.)
	return strings.Trim(string(qa.Answer), "\"")
}

// UnmarshalJSON custom-parses the conversation dict with dynamic session keys.
func (cd *ConversationData) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	cd.Sessions = make(map[string][]Turn)
	cd.Dates = make(map[string]string)

	if v, ok := raw["speaker_a"]; ok {
		_ = json.Unmarshal(v, &cd.SpeakerA)
	}
	if v, ok := raw["speaker_b"]; ok {
		_ = json.Unmarshal(v, &cd.SpeakerB)
	}

	for key, val := range raw {
		if strings.HasPrefix(key, "session_") && strings.HasSuffix(key, "_date_time") {
			var dt string
			_ = json.Unmarshal(val, &dt)
			sessKey := strings.TrimSuffix(key, "_date_time")
			cd.Dates[sessKey] = dt
		} else if strings.HasPrefix(key, "session_") {
			var turns []Turn
			if err := json.Unmarshal(val, &turns); err == nil {
				cd.Sessions[key] = turns
			}
		}
	}

	return nil
}

// Config controls the benchmark run.
type Config struct {
	DataPath     string
	Limit        int // Max questions (0 = all)
	JudgeCfg     *common.JudgeConfig
	GraphBoost   bool
	EmbeddingCfg *embedding.Config // If set, enables vector search

	// Baseline records before-scores keyed by common.BaselineKey for the
	// per-task comparison; nil publishes after-scores with an unevaluated gate.
	Baseline common.EvalBaseline

	// MinDelta is the per-task regression gate applied to every comparison
	// row. Zero means a task may never fall below its recorded baseline.
	MinDelta float64

	// ReportPath writes the published evaluation JSON when non-empty.
	ReportPath string

	// LegacyPath keeps the original FTS + RRF fusion query instead of routing
	// through retrieval.ExecuteAdaptiveSearch, so a before/after row can be
	// reproduced with one flag flip and stays attributable to the adaptive
	// wiring alone.
	LegacyPath bool
}

// Run executes the LOCOMO benchmark against Cortex. When a judge is
// configured the endpoint is probed first and any judge failure aborts the
// run with a common.BlockedError: scores are never fabricated by falling back
// to token overlap behind the judge's back.
func Run(cfg Config) (*common.BenchmarkResult, error) {
	if cfg.JudgeCfg != nil {
		if err := common.RequireJudge(context.Background(), cfg.JudgeCfg); err != nil {
			return nil, err
		}
	}

	data, err := os.ReadFile(cfg.DataPath)
	if err != nil {
		return nil, fmt.Errorf("locomo: read dataset: %w", err)
	}

	var conversations []Conversation
	if err := json.Unmarshal(data, &conversations); err != nil {
		return nil, fmt.Errorf("locomo: parse dataset: %w", err)
	}

	var stores *common.BenchStores
	if cfg.EmbeddingCfg != nil {
		stores, err = common.NewBenchStoresWithEmbeddings(*cfg.EmbeddingCfg)
	} else {
		stores, err = common.NewBenchStores()
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = stores.Close() }()

	ctx := context.Background()
	var results []common.QuestionResult

	for _, conv := range conversations {
		if err := ingestConversation(ctx, stores, conv); err != nil {
			return nil, fmt.Errorf("locomo: ingest %s: %w", conv.SampleID, err)
		}

		for _, qa := range conv.QA {
			if cfg.Limit > 0 && len(results) >= cfg.Limit {
				break
			}

			result, err := evaluateQuestion(ctx, stores, conv.SampleID, qa, cfg)
			if err != nil {
				return nil, err
			}
			results = append(results, result)
		}
	}

	agg := common.Aggregate(results)
	agg.Benchmark = "LOCOMO"
	return &agg, nil
}

// RunEval executes the published fixed-judge protocol: it fails BLOCKED when
// the judge endpoint is unreachable, scores every question with the fixed
// judge, publishes the per-task before/after comparison (including the
// vector-scan row when embeddings are enabled), and returns a
// *common.RegressionError when a task delta falls below Config.MinDelta.
// Callers map the outcome to a process exit with common.EvalExitCode.
func RunEval(cfg Config) (*common.EvalReport, error) {
	if cfg.JudgeCfg == nil {
		cfg.JudgeCfg = common.DefaultJudgeConfig()
	}
	result, err := Run(cfg)
	if err != nil {
		return nil, err
	}
	report, err := common.BuildEvalReport(
		common.EvalBenchmarkLOCOMO,
		*result,
		cfg.EmbeddingCfg != nil,
		common.DescribeJudgeProtocol(cfg.JudgeCfg),
		cfg.Baseline,
		cfg.MinDelta,
	)
	if err != nil {
		return nil, err
	}
	if cfg.ReportPath != "" {
		if err := common.PublishEvalReport(cfg.ReportPath, report); err != nil {
			return nil, fmt.Errorf("locomo: %w", err)
		}
	}
	return &report, report.RegressionError()
}

func ingestConversation(ctx context.Context, stores *common.BenchStores, conv Conversation) error {
	// Sort session keys for deterministic order
	var sessionKeys []string
	for k := range conv.Conversation.Sessions {
		sessionKeys = append(sessionKeys, k)
	}
	sort.Strings(sessionKeys)

	// Ingest dialogue sessions
	for _, sessKey := range sessionKeys {
		turns := conv.Conversation.Sessions[sessKey]
		sessionID := fmt.Sprintf("%s-%s", conv.SampleID, sessKey)

		var content strings.Builder
		date := conv.Conversation.Dates[sessKey]
		if date != "" {
			fmt.Fprintf(&content, "Date: %s\n", date)
		}
		for _, turn := range turns {
			fmt.Fprintf(&content, "%s: %s\n", turn.Speaker, turn.Text)
		}

		observations := []domain.Observation{{
			Title:   fmt.Sprintf("%s %s conversation on %s", conv.Conversation.SpeakerA, conv.Conversation.SpeakerB, date),
			Content: content.String(),
			Type:    "manual",
		}}

		if err := stores.IngestSession(ctx, sessionID, conv.SampleID, observations); err != nil {
			return err
		}
	}

	// Ingest extracted observations (higher signal)
	obsSlice := parseObservations(conv.Observation)
	if len(obsSlice) > 0 {
		obsSessionID := fmt.Sprintf("%s-observations", conv.SampleID)
		if err := stores.IngestSession(ctx, obsSessionID, conv.SampleID, obsSlice); err != nil {
			return err
		}
	}

	return nil
}

func evaluateQuestion(ctx context.Context, stores *common.BenchStores, project string, qa QA, cfg Config) (common.QuestionResult, error) {
	// Hybrid search: FTS5 + optional vector RRF fusion, either directly
	// (LegacyPath) or as the prior stage feeding ExecuteAdaptiveSearch.
	searchResults, err := queryPath(ctx, stores, qa.Question, project, cfg)

	var got string
	if err == nil {
		var parts []string
		for i, r := range searchResults {
			if i >= 5 {
				break
			}
			parts = append(parts, r.Content)
		}
		got = strings.Join(parts, "\n")
	}

	answer := qa.AnswerString()
	f1 := common.F1Score(got, answer)

	correct := f1 >= 0.3
	if cfg.JudgeCfg != nil {
		judgeScore, judgeErr := common.JudgeAnswer(cfg.JudgeCfg, qa.Question, answer, got)
		if judgeErr != nil {
			return common.QuestionResult{}, common.NewJudgeBlockedError(cfg.JudgeCfg.Endpoint, cfg.JudgeCfg.Model, judgeErr)
		}
		correct = judgeScore > 0.5
	}

	catName := categoryNames[qa.Category]
	if catName == "" {
		catName = fmt.Sprintf("category-%d", qa.Category)
	}

	return common.QuestionResult{
		ID:       fmt.Sprintf("%s-q%d", project, qa.Category),
		Type:     catName,
		Query:    qa.Question,
		Expected: answer,
		Got:      truncate(got, 500),
		Score:    f1,
		Correct:  correct,
	}, nil
}

// hybridSearch performs FTS5 + optional vector search with RRF fusion.
func hybridSearch(ctx context.Context, stores *common.BenchStores, query, project string, cfg Config) ([]*domain.SearchResult, error) {
	// FTS5 search (always)
	ftsResults, err := stores.App.Stores.Search.Search(ctx, query, domain.SearchOptions{
		Limit:       10,
		Project:     project,
		GraphExpand: cfg.GraphBoost,
	})
	if err != nil {
		return nil, err
	}

	// Vector search (when embeddings are available). W8.1: stores.Vectors is a
	// domain.VectorIndex; availability is checked via Health.
	if stores.Embedder != nil && domain.IsVectorIndexHealthy(ctx, stores.App.Stores.Vectors) {
		queryVec := stores.EmbedQuery(ctx, query)
		if len(queryVec) > 0 {
			vecCandidates, vecErr := stores.App.Stores.Vectors.Search(ctx, domain.VectorQuery{
				Vector:    queryVec,
				Limit:     10,
				Threshold: 0.2,
				Filters: map[string]any{
					"project": project,
				},
			})
			if vecErr == nil && len(vecCandidates) > 0 {
				vecResults := retrieval.RevalidateCandidates(ctx, stores.App.Stores.Observations, vecCandidates)
				if len(vecResults) > 0 {
					ftsResults = retrieval.FuseResults(ftsResults, vecResults, 10)
				}
			}
		}
	}

	return ftsResults, nil
}

// queryPath dispatches to the legacy FTS + RRF fusion pipeline or to the
// adaptive pipeline, keeping both reachable so before/after deltas stay
// attributable to the routing change alone.
func queryPath(ctx context.Context, stores *common.BenchStores, query, project string, cfg Config) ([]*domain.SearchResult, error) {
	if cfg.LegacyPath {
		return hybridSearch(ctx, stores, query, project, cfg)
	}
	return adaptiveSearch(ctx, stores, query, project, cfg)
}

// adaptiveSearch runs the legacy fusion as a prior retrieval stage, feeds its
// fusion output scores into AdaptiveSearchOptions.FusionScores so SkewRoute
// routing (REQ-ROUTE-001) classifies the query, and returns the tier-selected
// results. The query embedding is supplied only when embeddings are enabled;
// without it the multi-hop tier degrades to lexical seeding instead of
// failing, and an absent graph falls back to the legacy fusion path.
func adaptiveSearch(ctx context.Context, stores *common.BenchStores, query, project string, cfg Config) ([]*domain.SearchResult, error) {
	prior, err := hybridSearch(ctx, stores, query, project, cfg)
	if err != nil {
		return nil, err
	}

	opts := retrieval.AdaptiveSearchOptions{
		Mode:         "auto",
		Project:      project,
		Limit:        10,
		FusionScores: fusionScores(prior),
	}

	var vectorSearch func(context.Context, domain.VectorQuery) ([]*domain.VectorSearchResult, error)
	if stores.Embedder != nil && domain.IsVectorIndexHealthy(ctx, stores.App.Stores.Vectors) {
		opts.QueryVector = stores.EmbedQuery(ctx, query)
		vectorSearch = func(ctx context.Context, vq domain.VectorQuery) ([]*domain.VectorSearchResult, error) {
			return retrieval.SearchVectors(ctx, stores.App.Stores.Vectors, vq, stores.App.Stores.Observations)
		}
	}

	adaptive, err := retrieval.ExecuteAdaptiveSearch(ctx, query, opts, lexicalSearch(stores, cfg), vectorSearch)
	if err != nil {
		return nil, err
	}
	return adaptive.Results, nil
}

// lexicalSearch adapts the FTS store to the adaptive engine's lexical leg and
// re-applies GraphBoost, an option the engine's SearchOptions do not model.
func lexicalSearch(stores *common.BenchStores, cfg Config) func(context.Context, domain.SearchOptions) ([]*domain.SearchResult, error) {
	return func(ctx context.Context, opts domain.SearchOptions) ([]*domain.SearchResult, error) {
		opts.GraphExpand = cfg.GraphBoost
		return stores.App.Stores.Search.Search(ctx, opts.Query, opts)
	}
}

// fusionScores reads the prior stage's fused relevance scores (Rank on the
// fusion output) as the distribution ComputeFusionFeatures routes on; fewer
// than two scores degrades to the heuristic classifier, never to an error.
func fusionScores(results []*domain.SearchResult) []float64 {
	scores := make([]float64, 0, len(results))
	for _, r := range results {
		scores = append(scores, r.Rank)
	}
	return scores
}

// parseObservations extracts observation texts from the raw JSON structure.
// Structure: {"session_N_observation": {"SpeakerName": [["obs text", "evidence"], ...]}}
func parseObservations(raw json.RawMessage) []domain.Observation {
	if len(raw) == 0 {
		return nil
	}

	var sessions map[string]json.RawMessage
	if err := json.Unmarshal(raw, &sessions); err != nil {
		return nil
	}

	var result []domain.Observation
	for sessKey, speakersRaw := range sessions {
		var speakers map[string]json.RawMessage
		if err := json.Unmarshal(speakersRaw, &speakers); err != nil {
			continue
		}
		for speaker, itemsRaw := range speakers {
			var items [][]string
			if err := json.Unmarshal(itemsRaw, &items); err != nil {
				continue
			}
			for _, item := range items {
				if len(item) == 0 {
					continue
				}
				result = append(result, domain.Observation{
					Title:   fmt.Sprintf("%s observation from %s", speaker, sessKey),
					Content: item[0],
					Type:    "discovery",
				})
			}
		}
	}
	return result
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
