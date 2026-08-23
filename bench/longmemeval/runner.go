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
}

// Run executes the LongMemEval benchmark against Cortex.
func Run(cfg Config) (*common.BenchmarkResult, error) {
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

	stores, err := common.NewBenchStores()
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

		result := evaluateQuestion(ctx, stores, q, cfg)
		results = append(results, result)
	}

	agg := common.Aggregate(results)
	agg.Benchmark = "LongMemEval"
	return &agg, nil
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

func evaluateQuestion(ctx context.Context, stores *common.BenchStores, q Question, cfg Config) common.QuestionResult {
	searchResults, err := stores.App.Stores.Search.Search(ctx, q.Question, domain.SearchOptions{
		Limit:       10,
		GraphExpand: cfg.GraphBoost,
	})

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
		if judgeErr == nil && judgeScore >= 0 {
			correct = judgeScore > 0.5
		}
	}

	return common.QuestionResult{
		ID:       q.ID,
		Type:     q.Category,
		Query:    q.Question,
		Expected: q.Answer,
		Got:      truncate(got, 500),
		Score:    f1,
		Correct:  correct,
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
