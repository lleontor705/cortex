// Package longmemeval implements the LongMemEval benchmark runner for Cortex.
//
// LongMemEval tests 500 questions across 5 memory abilities:
// Information Extraction, Multi-Session Reasoning, Temporal Reasoning,
// Knowledge Updates, and Abstention.
//
// Reference: arXiv:2410.10813
package longmemeval

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/lleontor705/cortex/v2/bench/common"
	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/embedding"
	"github.com/lleontor705/cortex/v2/internal/retrieval"
)

// Dataset represents the LongMemEval dataset structure.
type Dataset struct {
	Questions []Question `json:"questions"`
}

// Question represents a single evaluation question.
type Question struct {
	ID          string     `json:"id"`
	Question    string     `json:"question"`
	Answer      string     `json:"answer"`
	Category    string     `json:"category"` // IE, MR, TR, KU, ABS
	ChatHistory []ChatTurn `json:"chat_history"`
}

// ChatTurn represents a single turn in the chat history.
type ChatTurn struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	SessionID int    `json:"session_id"`
	Timestamp string `json:"timestamp"`
}

// Config controls the benchmark run.
type Config struct {
	DataPath   string
	Limit      int
	JudgeCfg   *common.JudgeConfig
	GraphBoost bool

	// EmbeddingCfg embeds ingested observations when set; nil keeps the
	// lexical-only store. The query leg stays lexical either way (see
	// adaptiveRetrieve).
	EmbeddingCfg *embedding.Config

	// Baseline records before-scores keyed by common.BaselineKey for the
	// per-task comparison; nil publishes after-scores with an unevaluated gate.
	Baseline common.EvalBaseline

	// MinDelta is the per-task regression gate applied to every comparison
	// row. Zero means a task may never fall below its recorded baseline.
	MinDelta float64

	// ReportPath writes the published evaluation JSON when non-empty.
	ReportPath string

	// LegacyPath keeps the original direct FTS query instead of routing
	// through retrieval.ExecuteAdaptiveSearch, so a before/after row can be
	// reproduced with one flag flip and stays attributable to the adaptive
	// wiring alone.
	LegacyPath bool
}

// Run executes the LongMemEval benchmark against Cortex. When a judge is
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
		return nil, fmt.Errorf("longmemeval: read dataset: %w", err)
	}

	var dataset Dataset
	if err := json.Unmarshal(data, &dataset); err != nil {
		// Try alternative format: array of questions directly
		var questions []Question
		if err2 := json.Unmarshal(data, &questions); err2 != nil {
			return nil, fmt.Errorf("longmemeval: parse dataset: %w", err)
		}
		dataset.Questions = questions
	}

	if len(dataset.Questions) == 0 {
		return nil, fmt.Errorf("longmemeval: empty dataset")
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

	for _, q := range dataset.Questions {
		if cfg.Limit > 0 && len(results) >= cfg.Limit {
			break
		}

		// Ingest chat history as observations
		if err := ingestHistory(ctx, stores, q); err != nil {
			continue
		}

		result, err := evaluateQuestion(ctx, stores, q, cfg)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}

	agg := common.Aggregate(results)
	agg.Benchmark = "LongMemEval"
	return &agg, nil
}

// RunEval executes the published fixed-judge protocol: it fails BLOCKED when
// the judge endpoint is unreachable, scores every question with the fixed
// judge, publishes the per-task before/after comparison, and returns a
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
		common.EvalBenchmarkLongMemEval,
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
			return nil, fmt.Errorf("longmemeval: %w", err)
		}
	}
	return &report, report.RegressionError()
}

func ingestHistory(ctx context.Context, stores *common.BenchStores, q Question) error {
	// Group turns by session
	sessions := make(map[int][]ChatTurn)
	for _, turn := range q.ChatHistory {
		sessions[turn.SessionID] = append(sessions[turn.SessionID], turn)
	}

	for sessID, turns := range sessions {
		sessionID := fmt.Sprintf("%s-s%d", q.ID, sessID)

		var content strings.Builder
		for _, turn := range turns {
			fmt.Fprintf(&content, "%s: %s\n", turn.Role, turn.Content)
		}

		observations := []domain.Observation{
			{
				Title:   fmt.Sprintf("Chat session %d", sessID),
				Content: content.String(),
				Type:    "manual",
			},
		}

		if err := stores.IngestSession(ctx, sessionID, q.ID, observations); err != nil {
			return err
		}
	}

	return nil
}

func evaluateQuestion(ctx context.Context, stores *common.BenchStores, q Question, cfg Config) (common.QuestionResult, error) {
	searchResults, err := retrieve(ctx, stores, q.Question, cfg)

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

	f1 := common.F1Score(got, q.Answer)

	correct := f1 >= 0.4
	if cfg.JudgeCfg != nil {
		judgeScore, judgeErr := common.JudgeAnswer(cfg.JudgeCfg, q.Question, q.Answer, got)
		if judgeErr != nil {
			return common.QuestionResult{}, common.NewJudgeBlockedError(cfg.JudgeCfg.Endpoint, cfg.JudgeCfg.Model, judgeErr)
		}
		correct = judgeScore > 0.5
	}

	return common.QuestionResult{
		ID:       q.ID,
		Type:     q.Category,
		Query:    q.Question,
		Expected: q.Answer,
		Got:      truncate(got, 500),
		Score:    f1,
		Correct:  correct,
	}, nil
}

// retrieve runs the configured query path: the direct FTS search when
// Config.LegacyPath is set, otherwise the adaptive pipeline.
func retrieve(ctx context.Context, stores *common.BenchStores, question string, cfg Config) ([]*domain.SearchResult, error) {
	if cfg.LegacyPath {
		return stores.App.Stores.Search.Search(ctx, question, domain.SearchOptions{
			Limit:       10,
			GraphExpand: cfg.GraphBoost,
		})
	}
	return adaptiveRetrieve(ctx, stores, question, cfg)
}

// adaptiveRetrieve runs a prior FTS+RRF retrieval stage, feeds its fusion
// output scores into AdaptiveSearchOptions.FusionScores so SkewRoute routing
// (REQ-ROUTE-001) actually classifies the query, and returns the tier-selected
// results. The query leg carries no dense search, so the multi-hop tier seeds
// from lexical evidence only and never fails on a missing vector leg or an
// empty graph.
func adaptiveRetrieve(ctx context.Context, stores *common.BenchStores, question string, cfg Config) ([]*domain.SearchResult, error) {
	prior, err := stores.App.Stores.Search.Search(ctx, question, domain.SearchOptions{
		Limit:       10,
		GraphExpand: cfg.GraphBoost,
	})
	if err != nil {
		return nil, err
	}

	adaptive, err := retrieval.ExecuteAdaptiveSearch(ctx, question, retrieval.AdaptiveSearchOptions{
		Mode:         "auto",
		Limit:        10,
		FusionScores: fusionScores(prior),
	}, lexicalSearch(stores, cfg), nil)
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

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
