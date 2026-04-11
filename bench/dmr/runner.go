// Package dmr implements the Deep Memory Retrieval benchmark runner for Cortex.
//
// DMR uses the MSC-Self-Instruct dataset (500 conversations).
// Each conversation has multiple dialog sessions and self-instruct Q&A pairs
// that test retrieval from earlier sessions.
//
// Reference: arXiv:2310.08560 (MemGPT)
package dmr

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/lleontor705/cortex/bench/common"
	"github.com/lleontor705/cortex/internal/domain"
)

// MSCConversation represents a single MSC-Self-Instruct conversation.
type MSCConversation struct {
	Personas        [][]string        `json:"personas"`
	Dialog          []DialogTurn      `json:"dialog"`
	PreviousDialogs []PreviousDialog  `json:"previous_dialogs"`
	SelfInstruct    map[string]string `json:"self_instruct"` // {"B": question, "A": answer}
	Summary1        json.RawMessage   `json:"summary_speaker_1"`
	Summary2        json.RawMessage   `json:"summary_speaker_2"`
}

// DialogTurn represents a single turn.
type DialogTurn struct {
	Text string `json:"text"`
	ID   string `json:"id"`
}

// PreviousDialog represents a previous conversation session.
type PreviousDialog struct {
	Personas [][]string   `json:"personas"`
	Dialog   []DialogTurn `json:"dialog"`
}

// Config controls the benchmark run.
type Config struct {
	DataPath   string
	Limit      int
	JudgeCfg   *common.JudgeConfig
	GraphBoost bool
}

// Run executes the DMR benchmark against Cortex.
func Run(cfg Config) (*common.BenchmarkResult, error) {
	file, err := os.Open(cfg.DataPath)
	if err != nil {
		return nil, fmt.Errorf("dmr: open dataset: %w", err)
	}
	defer func() { _ = file.Close() }()

	stores, err := common.NewBenchStores()
	if err != nil {
		return nil, err
	}
	defer func() { _ = stores.Close() }()

	ctx := context.Background()
	var results []common.QuestionResult

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 2*1024*1024), 2*1024*1024)

	convIdx := 0
	var parseErrors int
	var ingestErrors int
	for scanner.Scan() {
		if cfg.Limit > 0 && len(results) >= cfg.Limit {
			break
		}

		var conv MSCConversation
		if err := json.Unmarshal(scanner.Bytes(), &conv); err != nil {
			parseErrors++
			convIdx++
			continue
		}

		project := fmt.Sprintf("conv-%d", convIdx)

		// Ingest all dialog sessions
		if err := ingestConversation(ctx, stores, project, conv); err != nil {
			ingestErrors++
			convIdx++
			continue
		}

		// Self-instruct: B is the question (referencing past sessions), A is the expected answer
		question := conv.SelfInstruct["B"]
		answer := conv.SelfInstruct["A"]
		if question != "" && answer != "" {
			if cfg.Limit > 0 && len(results) >= cfg.Limit {
				break
			}
			result := evaluateQuestion(ctx, stores, project, question, answer, cfg)
			results = append(results, result)
		}

		convIdx++
	}

	if parseErrors > 0 || ingestErrors > 0 {
		fmt.Fprintf(os.Stderr, "dmr: %d parse errors, %d ingest errors out of %d conversations\n", parseErrors, ingestErrors, convIdx)
	}

	agg := common.Aggregate(results)
	agg.Benchmark = "DMR"
	return &agg, nil
}

func ingestConversation(ctx context.Context, stores *common.BenchStores, project string, conv MSCConversation) error {
	// Ingest previous dialog sessions
	for i, pd := range conv.PreviousDialogs {
		sessionID := fmt.Sprintf("%s-prev-%d", project, i)
		var content strings.Builder
		for _, turn := range pd.Dialog {
			fmt.Fprintf(&content, "%s: %s\n", turn.ID, turn.Text)
		}

		observations := []domain.Observation{{
			Title:   fmt.Sprintf("Previous conversation session %d", i+1),
			Content: content.String(),
			Type:    "manual",
		}}

		if err := stores.IngestSession(ctx, sessionID, project, observations); err != nil {
			return err
		}
	}

	// Ingest current dialog
	currentSessionID := fmt.Sprintf("%s-current", project)
	var content strings.Builder
	for _, turn := range conv.Dialog {
		fmt.Fprintf(&content, "%s: %s\n", turn.ID, turn.Text)
	}

	observations := []domain.Observation{{
		Title:   "Current conversation session",
		Content: content.String(),
		Type:    "manual",
	}}

	// Ingest persona summaries as observations (flatten list of lists to text)
	if s := flattenSummary(conv.Summary1); s != "" {
		observations = append(observations, domain.Observation{
			Title:   "Speaker 1 persona summary",
			Content: s,
			Type:    "discovery",
		})
	}
	if s := flattenSummary(conv.Summary2); s != "" {
		observations = append(observations, domain.Observation{
			Title:   "Speaker 2 persona summary",
			Content: s,
			Type:    "discovery",
		})
	}

	return stores.IngestSession(ctx, currentSessionID, project, observations)
}

func evaluateQuestion(ctx context.Context, stores *common.BenchStores, project, question, expectedAnswer string, cfg Config) common.QuestionResult {
	searchResults, err := stores.App.Stores.Search.Search(ctx, question, domain.SearchOptions{
		Limit:       10,
		Project:     project,
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

	f1 := common.F1Score(got, expectedAnswer)
	rougeL := common.RougeL(got, expectedAnswer)
	score := (f1 + rougeL) / 2

	correct := score >= 0.3
	if cfg.JudgeCfg != nil {
		judgeScore, judgeErr := common.JudgeAnswer(cfg.JudgeCfg, question, expectedAnswer, got)
		if judgeErr == nil && judgeScore >= 0 {
			correct = judgeScore > 0.5
		}
	}

	return common.QuestionResult{
		ID:       project,
		Type:     "deep-retrieval",
		Query:    question,
		Expected: truncate(expectedAnswer, 200),
		Got:      truncate(got, 500),
		Score:    score,
		Correct:  correct,
	}
}

func flattenSummary(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	// Try as string first
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	// Try as list of list of strings
	var lists [][]string
	if err := json.Unmarshal(raw, &lists); err == nil {
		var parts []string
		for _, list := range lists {
			parts = append(parts, strings.Join(list, " "))
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
