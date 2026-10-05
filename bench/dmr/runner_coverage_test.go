package dmr

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/v2/bench/common"
)

func writeJSONL(t *testing.T, lines ...string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "data.jsonl")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return p
}

func convLine(t *testing.T, c MSCConversation) string {
	t.Helper()
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal conv: %v", err)
	}
	return string(b)
}

func TestRunSyntheticFixture(t *testing.T) {
	conv := MSCConversation{
		Personas:        [][]string{{"Alice"}, {"Bob"}},
		Dialog:          []DialogTurn{{Text: "Hello", ID: "alice"}, {Text: "Hi", ID: "bob"}},
		PreviousDialogs: []PreviousDialog{{Personas: [][]string{{"Alice"}}, Dialog: []DialogTurn{{Text: "Earlier talk", ID: "alice"}}}},
		SelfInstruct:    map[string]string{"B": "What did Alice say earlier?", "A": "Alice said hello earlier"},
		Summary1:        json.RawMessage(`"Alice is curious"`),
		Summary2:        json.RawMessage(`[["Bob","is","friendly"]]`),
	}
	result, err := Run(Config{DataPath: writeJSONL(t, convLine(t, conv)), Limit: 10})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Benchmark != "DMR" {
		t.Errorf("Benchmark = %q", result.Benchmark)
	}
	if result.Total != 1 {
		t.Errorf("Total = %d, want 1", result.Total)
	}
	if len(result.Details) != 1 {
		t.Fatalf("Details len = %d, want 1", len(result.Details))
	}
	d := result.Details[0]
	if d.Query != "What did Alice say earlier?" {
		t.Errorf("Query = %q", d.Query)
	}
	if d.Expected != "Alice said hello earlier" {
		t.Errorf("Expected = %q", d.Expected)
	}
}

func TestRunLimitAndParseErrors(t *testing.T) {
	good := MSCConversation{Dialog: []DialogTurn{{Text: "hi", ID: "a"}}, SelfInstruct: map[string]string{"B": "q1", "A": "a1"}}
	bad := "NOT VALID JSON"
	conv2 := MSCConversation{Dialog: []DialogTurn{{Text: "hi", ID: "a"}}, SelfInstruct: map[string]string{"B": "q2", "A": "a2"}}
	result, err := Run(Config{DataPath: writeJSONL(t, convLine(t, good), bad, convLine(t, conv2)), Limit: 1})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Total != 1 {
		t.Errorf("Total = %d, want 1 (limit=1)", result.Total)
	}
}

func TestRunNoSelfInstructAndEmpty(t *testing.T) {
	conv := MSCConversation{Dialog: []DialogTurn{{Text: "talk", ID: "x"}}, SelfInstruct: map[string]string{}}
	for _, tc := range []struct {
		name string
		path string
	}{
		{"no_self_instruct", writeJSONL(t, convLine(t, conv))},
		{"empty_file", writeJSONL(t)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := Run(Config{DataPath: tc.path})
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if result.Total != 0 {
				t.Errorf("Total = %d, want 0", result.Total)
			}
		})
	}
}

func TestRunFileNotFound(t *testing.T) {
	_, err := Run(Config{DataPath: filepath.Join(t.TempDir(), "nonexistent.jsonl")})
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestRunSummariesVariants(t *testing.T) {
	rawJSONL := []struct {
		name string
		line string
	}{
		{"both_empty", `{"personas":[],"dialog":[{"text":"hi","id":"a"}],"previous_dialogs":[],"self_instruct":{"B":"q","A":"a"},"summary_speaker_1":null,"summary_speaker_2":null}`},
		{"invalid_summary", `{"personas":[],"dialog":[{"text":"hi","id":"a"}],"previous_dialogs":[],"self_instruct":{"B":"q","A":"a"},"summary_speaker_1":123,"summary_speaker_2":[1,2,3]}`},
	}
	for _, tt := range rawJSONL {
		t.Run(tt.name, func(t *testing.T) {
			result, err := Run(Config{DataPath: writeJSONL(t, tt.line)})
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if result.Total != 1 {
				t.Errorf("Total = %d, want 1", result.Total)
			}
		})
	}
	structTests := []struct {
		name string
		sum1 json.RawMessage
		sum2 json.RawMessage
	}{
		{"both_strings", json.RawMessage(`"summary text"`), json.RawMessage(`"another summary"`)},
		{"both_nested_lists", json.RawMessage(`[["a","b"],["c"]]`), json.RawMessage(`[["d","e"]]`)},
	}
	for _, tt := range structTests {
		t.Run(tt.name, func(t *testing.T) {
			conv := MSCConversation{
				Dialog:          []DialogTurn{{Text: "hi", ID: "a"}},
				SelfInstruct:    map[string]string{"B": "q", "A": "a"},
				Summary1:        tt.sum1,
				Summary2:        tt.sum2,
				PreviousDialogs: []PreviousDialog{},
			}
			result, err := Run(Config{DataPath: writeJSONL(t, convLine(t, conv))})
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if result.Total != 1 {
				t.Errorf("Total = %d, want 1", result.Total)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	if s := truncate("abc", 10); s != "abc" {
		t.Errorf("short = %q", s)
	}
	if s := truncate("abcdefghij", 5); s != "abcde..." {
		t.Errorf("long = %q", s)
	}
	if s := truncate("abcde", 5); s != "abcde" {
		t.Errorf("exact = %q", s)
	}
}

func TestFlattenSummaryDirect(t *testing.T) {
	if got := flattenSummary(nil); got != "" {
		t.Errorf("nil = %q", got)
	}
	if got := flattenSummary(json.RawMessage(`"hello"`)); got != "hello" {
		t.Errorf("string = %q", got)
	}
	if got := flattenSummary(json.RawMessage(`[["a","b"],["c","d"]]`)); got != "a b\nc d" {
		t.Errorf("nested list = %q", got)
	}
	if got := flattenSummary(json.RawMessage(`{bad`)); got != "" {
		t.Errorf("invalid = %q", got)
	}
}

func TestEvaluateQuestionKeywordScoring(t *testing.T) {
	stores, err := common.NewBenchStores()
	if err != nil {
		t.Fatalf("NewBenchStores: %v", err)
	}
	defer func() { _ = stores.Close() }()
	conv := MSCConversation{
		Dialog:          []DialogTurn{{Text: "The answer is blue", ID: "a"}},
		PreviousDialogs: []PreviousDialog{{Dialog: []DialogTurn{{Text: "Favorite color: blue", ID: "a"}}}},
		SelfInstruct:    map[string]string{"B": "What color?", "A": "blue"},
	}
	ctx := context.Background()
	if err := ingestConversation(ctx, stores, "eval-0", conv); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	result := evaluateQuestion(ctx, stores, "eval-0", "What color?", "blue", Config{})
	if result.ID != "eval-0" {
		t.Errorf("ID = %q", result.ID)
	}
	if result.Type != "deep-retrieval" {
		t.Errorf("Type = %q", result.Type)
	}
	if result.Score < 0 || result.Score > 1 {
		t.Errorf("Score = %f, want [0,1]", result.Score)
	}
}

func TestRunGraphBoost(t *testing.T) {
	conv := MSCConversation{
		Dialog:          []DialogTurn{{Text: "data", ID: "a"}},
		SelfInstruct:    map[string]string{"B": "query", "A": "answer"},
		PreviousDialogs: []PreviousDialog{},
	}
	result, err := Run(Config{DataPath: writeJSONL(t, convLine(t, conv)), GraphBoost: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Total != 1 {
		t.Errorf("Total = %d, want 1", result.Total)
	}
}
